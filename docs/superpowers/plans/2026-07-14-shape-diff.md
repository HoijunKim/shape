# Shape Plan 3: `shape diff` - breaking-change diff with a CI contract

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `shape diff <old> <new> [--fail-on breaking|any|none] [--json] [--format]` that profiles both inputs, classifies each per-path change as breaking or safe (from the perspective of an old-data consumer meeting new data), reports it, and sets the process exit code (exit 1 reserved for a failing gate).

**Architecture:** A new pure package `internal/diff` compares two `profile.ProfileResult`s into a `DiffResult` (per-path `Change`s with `Detail`s and breaking flags), and renders it. The command profiles both files via the shared `profileSource` helper and, after printing, returns a sentinel `failErr` (implementing `ExitCode() int -> 1`) when the `--fail-on` gate trips. `main.go`'s existing `exitCode(err)` hook turns that into exit 1.

**Tech Stack:** Go, `spf13/cobra`, `encoding/json`, `text/tabwriter`, standard `testing`.

## Global Constraints

- Module `github.com/hoijun-kim/shape`. Go floor `go 1.23`.
- New package `internal/diff` may import `internal/profile` only. It MUST NOT import `internal/readers`, `internal/render`, `internal/schema`, or `internal/cmd`.
- Plain ASCII hyphen `-` only in code and docs. Never em/en dash or middle dot.
- Commits: Conventional Commits, NO co-author trailer.
- TDD: failing test first, then minimal implementation, then green, then commit.
- Breaking direction (v1, the only direction): "would a consumer built for `<old>` break when fed `<new>`?" Do NOT print the bare words "backward"/"forward" (they mean the opposite in Avro/Confluent); describe it as consumers of the old data.
- Breaking categories (high-confidence, structural only): field removed when it was always-present in old; new value type in new that old lacked (null introduced is a breaking sub-case); always-present becoming optional; enum member added (only when BOTH sides prove a complete small value set). Everything else is non-breaking. `int` and `float` fold to one `number` token, so int/float jitter is never a change.
- Exit codes: 0 = ran, no failing change under the gate. 1 = failing change under `--fail-on` (returned as `failErr` with `ExitCode() int` = 1, RESERVED for this). 2 = read/usage error (plain errors, no `ExitCode` method). Print the full report BEFORE returning the gate error.
- Determinism: union path set sorted; type/value set diffs sorted; output stable.

---

### Task 1: Diff types and per-dimension classifiers

**Files:**
- Create: `internal/diff/diff.go`
- Test: `internal/diff/diff_test.go`

**Interfaces:**
- Consumes: `profile.FieldProfile`, `profile.JSONKind` consts.
- Produces:
  - `type ChangeKind string` (`Added`/`Removed`/`Changed`), `type Reason string` (`ReasonPresence`/`ReasonType`/`ReasonEnum`).
  - `type Detail struct { Reason Reason; Breaking bool; Message, Old, New string }` (with json tags).
  - `type Change struct { Path string; Kind ChangeKind; Breaking bool; Details []Detail }` (json tags).
  - `type DiffResult struct { Old, New string; Compared, Added, Removed, Changed, Breaking int; Caveats []string; Changes []Change }` (json tags) + `func (DiffResult) HasBreaking() bool`.
  - helpers: `typeSet`, `setDiff`, `joinSet`, `valueSet`, `completeEnum`, `guaranteed`, `pct`, and the three classifiers `typeChange`, `presenceChange`, `enumChange` (each `(profile.FieldProfile, profile.FieldProfile) (Detail, bool)`).

- [ ] **Step 1: Write the failing test**

`internal/diff/diff_test.go`:

```go
package diff

import (
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

func fp(path string, mut func(*profile.FieldProfile)) profile.FieldProfile {
	f := profile.FieldProfile{Path: path, PresenceRate: 1.0, TypeDist: map[profile.JSONKind]float64{}}
	if mut != nil {
		mut(&f)
	}
	return f
}

func TestTypeChangeAddedIsBreaking(t *testing.T) {
	a := fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 })
	b := fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 0.5; f.TypeDist[profile.KindString] = 0.5 })
	d, ok := typeChange(a, b)
	if !ok || !d.Breaking {
		t.Fatalf("adding a string type must be breaking, got %+v ok=%v", d, ok)
	}
}

func TestTypeChangeDroppedIsSafe(t *testing.T) {
	a := fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 0.5; f.TypeDist[profile.KindString] = 0.5 })
	b := fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 })
	d, ok := typeChange(a, b)
	if !ok || d.Breaking {
		t.Fatalf("dropping a type must be non-breaking, got %+v ok=%v", d, ok)
	}
}

func TestTypeChangeIntFloatSame(t *testing.T) {
	a := fp("n", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 })
	b := fp("n", func(f *profile.FieldProfile) { f.TypeDist[profile.KindFloat] = 1 })
	if _, ok := typeChange(a, b); ok {
		t.Fatal("int vs float must not register as a type change (both are number)")
	}
}

func TestPresenceBecameOptionalIsBreaking(t *testing.T) {
	a := fp("x", func(f *profile.FieldProfile) { f.PresenceRate = 1.0 })
	b := fp("x", func(f *profile.FieldProfile) { f.PresenceRate = 0.5 })
	d, ok := presenceChange(a, b)
	if !ok || !d.Breaking {
		t.Fatalf("always-present -> optional must be breaking, got %+v ok=%v", d, ok)
	}
}

func TestPresenceBecameRequiredIsSafe(t *testing.T) {
	a := fp("x", func(f *profile.FieldProfile) { f.PresenceRate = 0.5 })
	b := fp("x", func(f *profile.FieldProfile) { f.PresenceRate = 1.0 })
	d, ok := presenceChange(a, b)
	if !ok || d.Breaking {
		t.Fatalf("optional -> always-present must be non-breaking, got %+v ok=%v", d, ok)
	}
}

func closedString(path string, vals ...string) profile.FieldProfile {
	return fp(path, func(f *profile.FieldProfile) {
		f.TypeDist[profile.KindString] = 1
		f.DistinctExact = true
		f.DistinctCount = len(vals)
		for _, v := range vals {
			f.TopValues = append(f.TopValues, profile.ValueCount{Value: v, Count: 1})
		}
	})
}

func TestEnumGainedMemberIsBreaking(t *testing.T) {
	a := closedString("s", "open", "closed")
	b := closedString("s", "open", "pending")
	d, ok := enumChange(a, b)
	if !ok || !d.Breaking {
		t.Fatalf("enum gaining a member must be breaking, got %+v ok=%v", d, ok)
	}
}

func TestEnumLostMemberIsSafe(t *testing.T) {
	a := closedString("s", "open", "closed", "pending")
	b := closedString("s", "open", "closed")
	d, ok := enumChange(a, b)
	if !ok || d.Breaking {
		t.Fatalf("enum losing a member must be non-breaking, got %+v ok=%v", d, ok)
	}
}

func TestEnumSuppressedWhenIncomplete(t *testing.T) {
	// DistinctExact false on one side -> no enum verdict at all.
	a := closedString("s", "open", "closed")
	b := closedString("s", "open", "pending")
	b.DistinctExact = false
	if _, ok := enumChange(a, b); ok {
		t.Fatal("enum change must be suppressed when a side is not a proven complete set")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/diff/ -v`
Expected: FAIL (build error: undefined symbols).

- [ ] **Step 3: Write minimal implementation**

`internal/diff/diff.go`:

```go
package diff

import (
	"sort"
	"strconv"
	"strings"

	"github.com/hoijun-kim/shape/internal/profile"
)

// ChangeKind categorizes a path-level change.
type ChangeKind string

const (
	Added   ChangeKind = "added"
	Removed ChangeKind = "removed"
	Changed ChangeKind = "changed"
)

// Reason categorizes one dimension of a change.
type Reason string

const (
	ReasonPresence Reason = "presence"
	ReasonType     Reason = "type"
	ReasonEnum     Reason = "enum"
)

// Detail is one differing dimension of a path.
type Detail struct {
	Reason   Reason `json:"reason"`
	Breaking bool   `json:"breaking"`
	Message  string `json:"message"`
	Old      string `json:"old,omitempty"`
	New      string `json:"new,omitempty"`
}

// Change is the aggregate change for one path.
type Change struct {
	Path     string     `json:"path"`
	Kind     ChangeKind `json:"kind"`
	Breaking bool       `json:"breaking"`
	Details  []Detail   `json:"details"`
}

// DiffResult is the full comparison of two profiles.
type DiffResult struct {
	Old      string   `json:"old"`
	New      string   `json:"new"`
	Compared int      `json:"compared"`
	Added    int      `json:"added"`
	Removed  int      `json:"removed"`
	Changed  int      `json:"changed"`
	Breaking int      `json:"breaking"`
	Caveats  []string `json:"caveats,omitempty"`
	Changes  []Change `json:"changes"`
}

// HasBreaking reports whether any breaking change was found.
func (d DiffResult) HasBreaking() bool { return d.Breaking > 0 }

const presenceEps = 1e-9

func guaranteed(fp profile.FieldProfile) bool { return fp.PresenceRate >= 1.0-presenceEps }

func pct(f float64) string { return strconv.Itoa(int(f*100+0.5)) + "%" }

// typeSet returns the JSON Schema type tokens a field shows, folding int/float
// to "number" and keeping "null" (null introduced in new data is a real break).
func typeSet(fp profile.FieldProfile) map[string]bool {
	set := map[string]bool{}
	for k, frac := range fp.TypeDist {
		if frac <= 0 {
			continue
		}
		switch k {
		case profile.KindInt, profile.KindFloat:
			set["number"] = true
		case profile.KindNull:
			set["null"] = true
		case profile.KindBool:
			set["boolean"] = true
		case profile.KindString:
			set["string"] = true
		case profile.KindArray:
			set["array"] = true
		case profile.KindObject:
			set["object"] = true
		}
	}
	return set
}

// setDiff returns the sorted members present in from but not in to.
func setDiff(from, to map[string]bool) []string {
	var out []string
	for k := range from {
		if !to[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func joinSet(s map[string]bool) string {
	ks := make([]string, 0, len(s))
	for k := range s {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func typeChange(a, b profile.FieldProfile) (Detail, bool) {
	sa, sb := typeSet(a), typeSet(b)
	added := setDiff(sb, sa)
	dropped := setDiff(sa, sb)
	if len(added) == 0 && len(dropped) == 0 {
		return Detail{}, false
	}
	parts := make([]string, 0, len(added)+len(dropped))
	for _, t := range added {
		parts = append(parts, "+"+t)
	}
	for _, t := range dropped {
		parts = append(parts, "-"+t)
	}
	return Detail{
		Reason: ReasonType, Breaking: len(added) > 0,
		Message: "type " + strings.Join(parts, " "),
		Old:     joinSet(sa), New: joinSet(sb),
	}, true
}

func presenceChange(a, b profile.FieldProfile) (Detail, bool) {
	ga, gb := guaranteed(a), guaranteed(b)
	switch {
	case ga && !gb:
		return Detail{Reason: ReasonPresence, Breaking: true, Message: "always-present -> optional", Old: pct(a.PresenceRate), New: pct(b.PresenceRate)}, true
	case !ga && gb:
		return Detail{Reason: ReasonPresence, Breaking: false, Message: "optional -> always-present", Old: pct(a.PresenceRate), New: pct(b.PresenceRate)}, true
	default:
		return Detail{}, false
	}
}

// completeEnum reports whether the profiler proved a complete small (>=2) value set.
func completeEnum(fp profile.FieldProfile) bool {
	return fp.DistinctExact && fp.DistinctCount >= 2 && fp.DistinctCount == len(fp.TopValues)
}

func valueSet(fp profile.FieldProfile) map[string]bool {
	s := map[string]bool{}
	for _, v := range fp.TopValues {
		s[v.Value] = true
	}
	return s
}

func enumChange(a, b profile.FieldProfile) (Detail, bool) {
	if !(completeEnum(a) && completeEnum(b)) {
		return Detail{}, false
	}
	sa, sb := valueSet(a), valueSet(b)
	added := setDiff(sb, sa)
	lost := setDiff(sa, sb)
	if len(added) == 0 && len(lost) == 0 {
		return Detail{}, false
	}
	parts := make([]string, 0, len(added)+len(lost))
	for _, v := range added {
		parts = append(parts, "+"+strconv.Quote(v))
	}
	for _, v := range lost {
		parts = append(parts, "-"+strconv.Quote(v))
	}
	return Detail{
		Reason: ReasonEnum, Breaking: len(added) > 0,
		Message: "enum " + strings.Join(parts, " "),
		Old:     joinSet(sa), New: joinSet(sb),
	}, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/diff/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diff/diff.go internal/diff/diff_test.go
git commit -m "feat: diff types and per-dimension breaking classifiers"
```

---

### Task 2: Path classification and Diff aggregation

**Files:**
- Modify: `internal/diff/diff.go` (add `index`, `classify`, `Diff`, `caveats`)
- Test: `internal/diff/diff_test.go` (append cases)

**Interfaces:**
- Consumes: Task 1 symbols, `profile.ProfileResult`.
- Produces: `func Diff(old, new profile.ProfileResult) DiffResult`; internal `index`, `classify`, `caveats`.

- [ ] **Step 1: Add failing tests**

Append to `internal/diff/diff_test.go`:

```go
func result(src string, fields ...profile.FieldProfile) profile.ProfileResult {
	return profile.ProfileResult{Source: src, Records: 10, Fields: fields}
}

func changeFor(d DiffResult, path string) (Change, bool) {
	for _, c := range d.Changes {
		if c.Path == path {
			return c, true
		}
	}
	return Change{}, false
}

func TestDiffRemovedAddedChanged(t *testing.T) {
	old := result("old",
		fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 }),
		fp("email", func(f *profile.FieldProfile) { f.TypeDist[profile.KindString] = 1 }),
	)
	new := result("new",
		fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 0.5; f.TypeDist[profile.KindString] = 0.5 }),
		fp("nickname", func(f *profile.FieldProfile) { f.TypeDist[profile.KindString] = 1 }),
	)
	d := Diff(old, new)
	if d.Removed != 1 || d.Added != 1 || d.Changed != 1 {
		t.Fatalf("counts: removed=%d added=%d changed=%d, want 1/1/1", d.Removed, d.Added, d.Changed)
	}
	email, _ := changeFor(d, "email")
	if email.Kind != Removed || !email.Breaking {
		t.Errorf("email = %+v, want removed+breaking (was always-present)", email)
	}
	nick, _ := changeFor(d, "nickname")
	if nick.Kind != Added || nick.Breaking {
		t.Errorf("nickname = %+v, want added+safe", nick)
	}
	id, _ := changeFor(d, "id")
	if id.Kind != Changed || !id.Breaking {
		t.Errorf("id = %+v, want changed+breaking (type added)", id)
	}
	if d.Breaking != 2 {
		t.Errorf("breaking = %d, want 2 (email + id)", d.Breaking)
	}
}

func TestDiffOptionalFieldRemovalIsSafe(t *testing.T) {
	old := result("old", fp("opt", func(f *profile.FieldProfile) { f.PresenceRate = 0.4; f.TypeDist[profile.KindString] = 1 }))
	new := result("new", fp("keep", func(f *profile.FieldProfile) { f.TypeDist[profile.KindString] = 1 }))
	d := Diff(old, new)
	opt, _ := changeFor(d, "opt")
	if opt.Kind != Removed || opt.Breaking {
		t.Errorf("optional removal must be non-breaking, got %+v", opt)
	}
}

func TestDiffNoChangeOnIdenticalProfiles(t *testing.T) {
	p := result("same", fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 }))
	d := Diff(p, p)
	if len(d.Changes) != 0 || d.Breaking != 0 {
		t.Errorf("identical profiles must diff clean, got %+v", d)
	}
}

func TestDiffCaveats(t *testing.T) {
	old := profile.ProfileResult{Source: "old", Records: 5, Skipped: 3}
	new := profile.ProfileResult{Source: "new", Records: 5000}
	d := Diff(old, new)
	if len(d.Caveats) < 2 {
		t.Errorf("expected skipped + count-mismatch caveats, got %v", d.Caveats)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/diff/ -run TestDiff -v`
Expected: FAIL (build error: `Diff` undefined).

- [ ] **Step 3: Implement**

Add to `internal/diff/diff.go` (append; `fmt` must be added to the import block):

```go
func index(p profile.ProfileResult) map[string]profile.FieldProfile {
	m := make(map[string]profile.FieldProfile, len(p.Fields))
	for _, f := range p.Fields {
		m[f.Path] = f
	}
	return m
}

// classify produces zero or one Change for a path. a/b are nil when the path is
// absent from that side.
func classify(path string, a, b *profile.FieldProfile) (Change, bool) {
	switch {
	case a != nil && b == nil:
		br := guaranteed(*a)
		msg := "removed (was optional)"
		if br {
			msg = "removed (was always-present)"
		}
		return Change{Path: path, Kind: Removed, Breaking: br,
			Details: []Detail{{Reason: ReasonPresence, Breaking: br, Message: msg, Old: pct(a.PresenceRate), New: "-"}}}, true
	case a == nil && b != nil:
		return Change{Path: path, Kind: Added, Breaking: false,
			Details: []Detail{{Reason: ReasonPresence, Breaking: false, Message: "new field", Old: "-", New: pct(b.PresenceRate)}}}, true
	default:
		var details []Detail
		if d, ok := typeChange(*a, *b); ok {
			details = append(details, d)
		}
		if d, ok := presenceChange(*a, *b); ok {
			details = append(details, d)
		}
		if d, ok := enumChange(*a, *b); ok {
			details = append(details, d)
		}
		if len(details) == 0 {
			return Change{}, false
		}
		br := false
		for _, d := range details {
			if d.Breaking {
				br = true
			}
		}
		return Change{Path: path, Kind: Changed, Breaking: br, Details: details}, true
	}
}

// Diff compares two profiles into a DiffResult. Breaking is judged from the
// perspective of a consumer built for old data meeting new data.
func Diff(old, new profile.ProfileResult) DiffResult {
	ia, ib := index(old), index(new)
	seen := map[string]bool{}
	var paths []string
	for p := range ia {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for p := range ib {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	res := DiffResult{Old: old.Source, New: new.Source, Compared: len(paths)}
	for _, p := range paths {
		var a, b *profile.FieldProfile
		if f, ok := ia[p]; ok {
			a = &f
		}
		if f, ok := ib[p]; ok {
			b = &f
		}
		ch, ok := classify(p, a, b)
		if !ok {
			continue
		}
		res.Changes = append(res.Changes, ch)
		switch ch.Kind {
		case Added:
			res.Added++
		case Removed:
			res.Removed++
		case Changed:
			res.Changed++
		}
		if ch.Breaking {
			res.Breaking++
		}
	}
	res.Caveats = caveats(old, new)
	return res
}

// caveats warns when the two profiles may not be soundly comparable.
func caveats(old, new profile.ProfileResult) []string {
	var cs []string
	if old.Skipped > 0 || new.Skipped > 0 {
		cs = append(cs, fmt.Sprintf("skipped lines (old=%d, new=%d): removed/dropped-type signals may be parse artifacts", old.Skipped, new.Skipped))
	}
	lo, hi := old.Records, new.Records
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo > 0 && hi >= lo*100 {
		cs = append(cs, fmt.Sprintf("record counts differ widely (old=%d, new=%d): set differences may reflect sample size, not a real change", old.Records, new.Records))
	}
	return cs
}
```

Change the import block of `internal/diff/diff.go` to add `"fmt"`:

```go
import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hoijun-kim/shape/internal/profile"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/diff/ -count=1`
Expected: PASS (all diff tests).

- [ ] **Step 5: Commit**

```bash
git add internal/diff/diff.go internal/diff/diff_test.go
git commit -m "feat: align paths and aggregate the profile diff"
```

---

### Task 3: Text renderer

**Files:**
- Create: `internal/diff/render.go`
- Test: `internal/diff/render_test.go`

**Interfaces:**
- Consumes: `DiffResult`, `Change`, `Detail`.
- Produces: `func RenderText(w io.Writer, d DiffResult)` - header + counts + caveat lines (`! ...`) + one line per change (breaking first, then by path) with a `BREAK`/`ok` marker; `no changes` when empty.

- [ ] **Step 1: Write the failing test**

`internal/diff/render_test.go`:

```go
package diff

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderTextMarksBreaking(t *testing.T) {
	d := DiffResult{
		Old: "old", New: "new", Compared: 3, Removed: 1, Changed: 1, Breaking: 2,
		Caveats: []string{"small sample"},
		Changes: []Change{
			{Path: "tags[]", Kind: Changed, Breaking: false, Details: []Detail{{Reason: ReasonEnum, Message: `enum -"beta"`}}},
			{Path: "email", Kind: Removed, Breaking: true, Details: []Detail{{Reason: ReasonPresence, Breaking: true, Message: "removed (was always-present)"}}},
		},
	}
	var b bytes.Buffer
	RenderText(&b, d)
	out := b.String()
	if !strings.Contains(out, "2 breaking") {
		t.Errorf("missing breaking count:\n%s", out)
	}
	if !strings.Contains(out, "! small sample") {
		t.Errorf("missing caveat line:\n%s", out)
	}
	if !strings.Contains(out, "BREAK") || !strings.Contains(out, "email") {
		t.Errorf("missing breaking marker for email:\n%s", out)
	}
	// email (breaking) must be listed before tags[] (non-breaking).
	if strings.Index(out, "email") > strings.Index(out, "tags[]") {
		t.Errorf("breaking change should sort first:\n%s", out)
	}
}

func TestRenderTextNoChanges(t *testing.T) {
	var b bytes.Buffer
	RenderText(&b, DiffResult{Old: "a", New: "b", Compared: 5})
	if !strings.Contains(b.String(), "no changes") {
		t.Errorf("expected 'no changes', got:\n%s", b.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/diff/ -run TestRender -v`
Expected: FAIL (build error: `RenderText` undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/diff/render.go`:

```go
package diff

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// RenderText writes a human-readable diff summary to w.
func RenderText(w io.Writer, d DiffResult) {
	fmt.Fprintf(w, "diff %s -> %s\n", srcOr(d.Old), srcOr(d.New))
	fmt.Fprintf(w, "  %d paths compared - %d added, %d removed, %d changed (%d breaking)\n",
		d.Compared, d.Added, d.Removed, d.Changed, d.Breaking)
	for _, c := range d.Caveats {
		fmt.Fprintf(w, "  ! %s\n", c)
	}
	if len(d.Changes) == 0 {
		fmt.Fprintln(w, "  no changes")
		return
	}
	sorted := append([]Change(nil), d.Changes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Breaking != sorted[j].Breaking {
			return sorted[i].Breaking
		}
		return sorted[i].Path < sorted[j].Path
	})
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, c := range sorted {
		marker := "ok"
		if c.Breaking {
			marker = "BREAK"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", marker, c.Kind, c.Path, messages(c.Details))
	}
	tw.Flush()
}

func messages(ds []Detail) string {
	ms := make([]string, 0, len(ds))
	for _, d := range ds {
		ms = append(ms, d.Message)
	}
	return strings.Join(ms, "; ")
}

func srcOr(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/diff/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diff/render.go internal/diff/render_test.go
git commit -m "feat: render the diff as a human summary"
```

---

### Task 4: Wire the `shape diff` command with the exit-code contract

**Files:**
- Create: `internal/cmd/diff.go`
- Modify: `internal/cmd/root.go` (register `newDiffCmd`)
- Create (fixtures): `internal/cmd/testdata/diff_old.ndjson`, `internal/cmd/testdata/diff_new.ndjson`
- Test: `internal/cmd/diff_test.go`

**Interfaces:**
- Consumes: `profileSource` (Plan 2), `diff.Diff`, `diff.RenderText`.
- Produces: `func newDiffCmd() *cobra.Command`; internal `type failErr struct{ msg string }` with `Error()` and `ExitCode() int` = 1.

- [ ] **Step 1: Create the fixtures**

`internal/cmd/testdata/diff_old.ndjson` (exactly two lines):

```
{"id":1,"email":"a@b.c","status":"open"}
{"id":2,"email":"d@e.f","status":"closed"}
```

`internal/cmd/testdata/diff_new.ndjson` (exactly two lines):

```
{"id":1,"status":"open"}
{"id":"two","status":"pending"}
```

(old->new: `email` removed [breaking], `id` gains a string type [breaking], `status` enum gains "pending" and loses "closed" [breaking] -> 3 breaking.)

- [ ] **Step 2: Write the failing test**

`internal/cmd/diff_test.go`:

```go
package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func runDiff(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"diff"}, args...))
	return out.String(), root.Execute()
}

func exitCodeOf(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

func TestDiffJSONReportsBreaking(t *testing.T) {
	out, err := runDiff(t, "--json", "--fail-on", "none", "testdata/diff_old.ndjson", "testdata/diff_new.ndjson")
	if err != nil {
		t.Fatalf("fail-on none must not error: %v", err)
	}
	var d map[string]any
	if e := json.Unmarshal([]byte(out), &d); e != nil {
		t.Fatalf("not JSON: %v\n%s", e, out)
	}
	if d["breaking"].(float64) != 3 {
		t.Errorf("breaking = %v, want 3", d["breaking"])
	}
}

func TestDiffFailOnBreakingExits1(t *testing.T) {
	_, err := runDiff(t, "testdata/diff_old.ndjson", "testdata/diff_new.ndjson")
	if err == nil {
		t.Fatal("default --fail-on breaking must return an error on breaking changes")
	}
	if got := exitCodeOf(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

func TestDiffFailOnNoneExits0(t *testing.T) {
	_, err := runDiff(t, "--fail-on", "none", "testdata/diff_old.ndjson", "testdata/diff_new.ndjson")
	if err != nil {
		t.Errorf("fail-on none must return nil, got %v", err)
	}
}

func TestDiffInvalidFailOn(t *testing.T) {
	_, err := runDiff(t, "--fail-on", "bogus", "testdata/diff_old.ndjson", "testdata/diff_new.ndjson")
	if err == nil {
		t.Fatal("invalid --fail-on must error")
	}
	if exitCodeOf(err) == 1 {
		t.Error("invalid --fail-on is a usage error (exit 2), not a gate failure (exit 1)")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run TestDiff -v`
Expected: FAIL (build error: `diff` subcommand not registered).

- [ ] **Step 4: Implement the diff command**

`internal/cmd/diff.go`:

```go
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/hoijun-kim/shape/internal/diff"
	"github.com/spf13/cobra"
)

// failErr signals the --fail-on gate tripped; exit code 1 (reserved for a
// failing diff), routed through main.go's ExitCode() hook.
type failErr struct{ msg string }

func (e failErr) Error() string { return e.msg }
func (e failErr) ExitCode() int { return 1 }

func newDiffCmd() *cobra.Command {
	var failOn, format string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "diff <old> <new>",
		Short: "Diff two snapshots and flag changes that break consumers of the old data",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch failOn {
			case "breaking", "any", "none":
			default:
				return fmt.Errorf("invalid --fail-on %q (want breaking|any|none)", failOn)
			}

			a, err := profileSource(args[0], format)
			if err != nil {
				return err
			}
			b, err := profileSource(args[1], format)
			if err != nil {
				return err
			}
			d := diff.Diff(a, b)

			if asJSON {
				out, err := json.MarshalIndent(d, "", "  ")
				if err != nil {
					return err
				}
				out = append(out, '\n')
				if _, err := cmd.OutOrStdout().Write(out); err != nil {
					return err
				}
			} else {
				diff.RenderText(cmd.OutOrStdout(), d)
			}

			switch failOn {
			case "any":
				if len(d.Changes) > 0 {
					return failErr{fmt.Sprintf("%d change(s)", len(d.Changes))}
				}
			case "breaking":
				if d.Breaking > 0 {
					return failErr{fmt.Sprintf("%d breaking change(s)", d.Breaking)}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&failOn, "fail-on", "breaking", "exit 1 on: breaking|any|none")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	cmd.Flags().StringVar(&format, "format", "auto", "input format: auto|json|ndjson")
	return cmd
}
```

In `internal/cmd/root.go`, register the command. The relevant tail becomes:

```go
	root.AddCommand(newProfileCmd())
	root.AddCommand(newSchemaCmd())
	root.AddCommand(newDiffCmd())
	return root
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cmd/ -run TestDiff -v`
Expected: PASS.

- [ ] **Step 6: Run the whole suite and build**

Run:
```bash
go test ./... -count=1
go build -o shape.exe .
```
Expected: all tests PASS; binary builds.

- [ ] **Step 7: Manual smoke check**

Run: `go run . diff internal/cmd/testdata/diff_old.ndjson internal/cmd/testdata/diff_new.ndjson; echo "exit=$?"`
Expected: a report showing `3 breaking`, `BREAK` lines for `email` (removed), `id` (type +string), `status` (enum +"pending"), and `exit=1`.
Run: `go run . diff --fail-on none internal/cmd/testdata/diff_old.ndjson internal/cmd/testdata/diff_new.ndjson; echo "exit=$?"`
Expected: same report, `exit=0`.

(On this Windows/Git-Bash setup `go run` may mangle child exit codes; if `exit=` looks wrong, build first with `go build -o shape.exe .` and run `./shape.exe diff ...` to observe the real exit code.)

- [ ] **Step 8: Commit**

```bash
git add internal/cmd/diff.go internal/cmd/root.go internal/cmd/testdata/diff_old.ndjson internal/cmd/testdata/diff_new.ndjson internal/cmd/diff_test.go
git commit -m "feat: wire shape diff command with the fail-on exit-code contract"
```

---

## Plan 3 self-review

Coverage: per-dimension classifiers (Task 1), path alignment + aggregation + caveats (Task 2), text render + `--json` via struct tags (Tasks 3-4), the `shape diff` command + `--fail-on` exit contract (Task 4). Breaking semantics (field removed when always-present, type added incl. null, became-optional, enum-added guarded; everything else safe; int/float folded) are enforced in Task 1 and covered by tests. Soundness guardrails: enum verdicts only on proven-complete sets on BOTH sides (`TestEnumSuppressedWhenIncomplete`), optional-field removal non-breaking (`TestDiffOptionalFieldRemovalIsSafe`), comparability caveats (`TestDiffCaveats`), no-change on identical profiles (`TestDiffNoChangeOnIdenticalProfiles`).

Placeholder scan: none; every code step is complete.

Type consistency: `DiffResult`/`Change`/`Detail`/`ChangeKind`/`Reason` and helpers defined in Task 1 are consumed unchanged by `Diff`/`classify` (Task 2), `RenderText` (Task 3), and `newDiffCmd` (Task 4); `failErr.ExitCode()` matches the `interface{ ExitCode() int }` hook `main.go` already checks; `profileSource` signature matches Plan 2.

Out of scope (later): `--direction forward|full` (swap/union runs); `--strict` (promote ambiguous to breaking); range/length/null-rate informational details; confidence tiers / presence tolerance / N floors (v1 profiles are exact over the whole file, so intra-file sampling error does not apply; cross-snapshot small-sample noise is surfaced via caveats, not silently gated).

## Next plans
- Plan 4: approximate mode (HyperLogLog / Space-Saving / reservoir) for GB-scale inputs.
- Plans 5-7 as listed in the Plan 1 roadmap (CSV/Parquet/SQLite readers; Wails GUI; distribution).
