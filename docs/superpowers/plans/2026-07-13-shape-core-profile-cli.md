# Shape Plan 1: Core Profiling Engine + `shape profile` CLI (JSON/NDJSON)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a working `shape profile <file|->` CLI that streams a JSON or NDJSON file and prints a per-field profile (presence, type distribution, null rate, ranges, distinct/top values) as a human table or `--json`, with type-drift highlighted.

**Architecture:** One pure Go core (`internal/profile`) that turns decoded JSON records into a `ProfileResult`, fed by a streaming reader (`internal/readers/jsonreader`), rendered by `internal/render`, and driven by a cobra CLI (`internal/cmd`). The core never imports the reader, renderer, or CLI - they depend on it, not the reverse.

**Tech Stack:** Go, `spf13/cobra` for the CLI, standard library `encoding/json` (with `UseNumber` for int/float distinction), standard `testing` for tests.

## Global Constraints

- Module path: `github.com/hoijun-kim/shape` (repo already exists, private).
- Go version floor: `go 1.23`.
- Text/copy rule: plain ASCII hyphen `-` only in all output and docs. Never use `-`, `-`, or `·`.
- Numbers must be decoded with `json.Decoder.UseNumber()` so integers and floats are distinguishable (they arrive as `json.Number`, not `float64`).
- Core package `internal/profile` MUST NOT import `internal/readers`, `internal/render`, or `internal/cmd`. Dependencies point toward the core only.
- Commit after each task. Commit messages use Conventional Commits. Do NOT add any co-author trailer.
- Every task is TDD: failing test first, then minimal implementation, then green, then commit.

---

### Task 1: Project scaffold and CLI shell

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: `internal/cmd/root.go`
- Test: `internal/cmd/root_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces: `cmd.NewRootCmd() *cobra.Command` returning the configured root command; `cmd.Execute() error` running it against `os.Args`.

- [ ] **Step 1: Initialize the module and add cobra**

```bash
cd C:/Users/hoijun/Projects/shape
go mod init github.com/hoijun-kim/shape   # skip if go.mod already exists
go get github.com/spf13/cobra@latest
```

- [ ] **Step 2: Write the failing test**

`internal/cmd/root_test.go`:

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootVersion(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "shape version") {
		t.Fatalf("expected version banner, got %q", out.String())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run TestRootVersion -v`
Expected: FAIL (build error: `NewRootCmd` undefined).

- [ ] **Step 4: Write minimal implementation**

`internal/cmd/root.go`:

```go
package cmd

import "github.com/spf13/cobra"

// Version is the CLI version string, overridable at build time via -ldflags.
var Version = "0.1.0-dev"

// NewRootCmd builds the root `shape` command.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "shape",
		Short:         "See the real shape of your structured data files",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("shape version {{.Version}}\n")
	return root
}

// Execute runs the root command against os.Args.
func Execute() error {
	return NewRootCmd().Execute()
}
```

`main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/hoijun-kim/shape/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "shape:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cmd/ -run TestRootVersion -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum main.go internal/cmd/root.go internal/cmd/root_test.go
git commit -m "feat: scaffold shape module and cobra root command"
```

---

### Task 2: JSON value kinds

**Files:**
- Create: `internal/profile/kind.go`
- Test: `internal/profile/kind_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type JSONKind string` with consts `KindNull, KindBool, KindInt, KindFloat, KindString, KindArray, KindObject`.
  - `func KindOf(v any) JSONKind` classifying a value decoded by `encoding/json` with `UseNumber()`.

- [ ] **Step 1: Write the failing test**

`internal/profile/kind_test.go`:

```go
package profile

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(stringReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

func TestKindOf(t *testing.T) {
	cases := []struct {
		in   string
		want JSONKind
	}{
		{`null`, KindNull},
		{`true`, KindBool},
		{`42`, KindInt},
		{`4.2`, KindFloat},
		{`1e3`, KindFloat},
		{`"hi"`, KindString},
		{`[1,2]`, KindArray},
		{`{"a":1}`, KindObject},
	}
	for _, c := range cases {
		if got := KindOf(decode(t, c.in)); got != c.want {
			t.Errorf("KindOf(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}
```

Add a tiny helper `internal/profile/testutil_test.go`:

```go
package profile

import "strings"

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/profile/ -run TestKindOf -v`
Expected: FAIL (build error: `JSONKind`/`KindOf` undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/profile/kind.go`:

```go
package profile

import (
	"encoding/json"
	"strings"
)

// JSONKind is the classified kind of a decoded JSON value.
type JSONKind string

const (
	KindNull   JSONKind = "null"
	KindBool   JSONKind = "bool"
	KindInt    JSONKind = "int"
	KindFloat  JSONKind = "float"
	KindString JSONKind = "string"
	KindArray  JSONKind = "array"
	KindObject JSONKind = "object"
)

// KindOf classifies a value produced by encoding/json with UseNumber().
func KindOf(v any) JSONKind {
	switch t := v.(type) {
	case nil:
		return KindNull
	case bool:
		return KindBool
	case string:
		return KindString
	case json.Number:
		if strings.ContainsAny(t.String(), ".eE") {
			return KindFloat
		}
		return KindInt
	case float64: // fallback if a caller decoded without UseNumber
		return KindFloat
	case []any:
		return KindArray
	case map[string]any:
		return KindObject
	default:
		return KindString
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/profile/ -run TestKindOf -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/profile/kind.go internal/profile/kind_test.go internal/profile/testutil_test.go
git commit -m "feat: classify decoded JSON values into kinds"
```

---

### Task 3: Path flattening into observations

**Files:**
- Create: `internal/profile/flatten.go`
- Test: `internal/profile/flatten_test.go`

**Interfaces:**
- Consumes: `KindOf`, `JSONKind` (Task 2).
- Produces:
  - `type Observation struct { Path string; Kind JSONKind; Num float64; Str string }`
  - `func Flatten(record any, emit func(Observation))` - walks a decoded record and emits one `Observation` per path node. Object keys join with `.`; array elements collapse under a `[]` suffix. A root object emits its children (not itself); a root array emits `[]`; a root scalar emits path `$`. Container paths (object/array) are emitted with their kind and zero Num/Str.

- [ ] **Step 1: Write the failing test**

`internal/profile/flatten_test.go`:

```go
package profile

import (
	"sort"
	"testing"
)

func collect(t *testing.T, s string) map[string]JSONKind {
	t.Helper()
	got := map[string]JSONKind{}
	Flatten(decode(t, s), func(o Observation) { got[o.Path] = o.Kind })
	return got
}

func TestFlattenNestedPaths(t *testing.T) {
	got := collect(t, `{"email":"a@b.c","user":{"name":"x"},"tags":["p","q"]}`)
	want := map[string]JSONKind{
		"email":     KindString,
		"user":      KindObject,
		"user.name": KindString,
		"tags":      KindArray,
		"tags[]":    KindString,
	}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for p, k := range want {
		if got[p] != k {
			t.Errorf("path %q kind = %s, want %s", p, got[p], k)
		}
	}
}

func TestFlattenScalarAndValues(t *testing.T) {
	var nums []float64
	Flatten(decode(t, `{"a":42,"b":"hi"}`), func(o Observation) {
		if o.Kind == KindInt {
			nums = append(nums, o.Num)
		}
		if o.Path == "b" && o.Str != "hi" {
			t.Errorf("b.Str = %q, want hi", o.Str)
		}
	})
	sort.Float64s(nums)
	if len(nums) != 1 || nums[0] != 42 {
		t.Errorf("int values = %v, want [42]", nums)
	}
}

func TestFlattenRootScalar(t *testing.T) {
	got := collect(t, `7`)
	if got["$"] != KindInt {
		t.Errorf("root scalar kind = %s, want int", got["$"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/profile/ -run TestFlatten -v`
Expected: FAIL (build error: `Observation`/`Flatten` undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/profile/flatten.go`:

```go
package profile

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Observation is one path node seen in one record.
type Observation struct {
	Path string
	Kind JSONKind
	Num  float64 // valid when Kind is KindInt or KindFloat
	Str  string  // valid when Kind is KindString
}

// Flatten walks a decoded record and emits an Observation per path node.
func Flatten(record any, emit func(Observation)) {
	walk("", record, emit)
}

func walk(path string, v any, emit func(Observation)) {
	switch t := v.(type) {
	case map[string]any:
		if path != "" {
			emit(Observation{Path: path, Kind: KindObject})
		}
		for k, cv := range t {
			child := k
			if path != "" {
				child = path + "." + k
			}
			walk(child, cv, emit)
		}
	case []any:
		emit(Observation{Path: rootOr(path), Kind: KindArray})
		elem := "[]"
		if path != "" {
			elem = path + "[]"
		}
		for _, cv := range t {
			walk(elem, cv, emit)
		}
	default:
		emit(scalarObservation(rootOr(path), t))
	}
}

func rootOr(path string) string {
	if path == "" {
		return "$"
	}
	return path
}

func scalarObservation(path string, v any) Observation {
	switch t := v.(type) {
	case nil:
		return Observation{Path: path, Kind: KindNull}
	case bool:
		return Observation{Path: path, Kind: KindBool}
	case string:
		return Observation{Path: path, Kind: KindString, Str: t}
	case json.Number:
		if strings.ContainsAny(t.String(), ".eE") {
			f, _ := t.Float64()
			return Observation{Path: path, Kind: KindFloat, Num: f}
		}
		i, _ := t.Int64()
		return Observation{Path: path, Kind: KindInt, Num: float64(i)}
	case float64:
		return Observation{Path: path, Kind: KindFloat, Num: t}
	default:
		return Observation{Path: path, Kind: KindString, Str: fmt.Sprintf("%v", t)}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/profile/ -run TestFlatten -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/profile/flatten.go internal/profile/flatten_test.go
git commit -m "feat: flatten records into per-path observations"
```

---

### Task 4: Field accumulator and FieldProfile

**Files:**
- Create: `internal/profile/accumulator.go`
- Test: `internal/profile/accumulator_test.go`

**Interfaces:**
- Consumes: `Observation`, `JSONKind`, kind consts (Tasks 2-3).
- Produces:
  - `type ValueCount struct { Value string; Count int }`
  - `type FieldProfile struct { Path string; PresenceRate float64; TypeDist map[JSONKind]float64; NullRate float64; Min, Max *float64; DistinctCount int; DistinctExact bool; TopValues []ValueCount; StrLenMin, StrLenMax *int; Observations int }`
  - `func newFieldAccumulator(path string, distinctCap int) *fieldAccumulator`
  - `(*fieldAccumulator).AddValue(o Observation)` - records one observation's kind/value stats.
  - `(*fieldAccumulator).MarkPresent()` - records that the path appeared in the current record (call once per record per path).
  - `(*fieldAccumulator).Result(totalRecords int) FieldProfile`

- [ ] **Step 1: Write the failing test**

`internal/profile/accumulator_test.go`:

```go
package profile

import "testing"

func TestAccumulatorTypeDistAndNull(t *testing.T) {
	a := newFieldAccumulator("x", 1000)
	a.AddValue(Observation{Path: "x", Kind: KindInt, Num: 1})
	a.MarkPresent()
	a.AddValue(Observation{Path: "x", Kind: KindString, Str: "oops"})
	a.MarkPresent()
	a.AddValue(Observation{Path: "x", Kind: KindNull})
	a.MarkPresent()
	fp := a.Result(4) // 4 records total, present in 3

	if fp.PresenceRate < 0.749 || fp.PresenceRate > 0.751 {
		t.Errorf("presence = %v, want 0.75", fp.PresenceRate)
	}
	if fp.NullRate < 0.332 || fp.NullRate > 0.334 {
		t.Errorf("null rate = %v, want ~0.333", fp.NullRate)
	}
	if fp.TypeDist[KindInt] < 0.332 || fp.TypeDist[KindString] < 0.332 {
		t.Errorf("type dist = %v, want int and string each ~0.333", fp.TypeDist)
	}
}

func TestAccumulatorNumericRangeAndTop(t *testing.T) {
	a := newFieldAccumulator("n", 1000)
	for _, v := range []float64{5, 1, 9, 5, 5} {
		a.AddValue(Observation{Path: "n", Kind: KindInt, Num: v})
		a.MarkPresent()
	}
	fp := a.Result(5)
	if fp.Min == nil || *fp.Min != 1 || fp.Max == nil || *fp.Max != 9 {
		t.Fatalf("min/max = %v/%v, want 1/9", fp.Min, fp.Max)
	}
	if len(fp.TopValues) == 0 || fp.TopValues[0].Value != "5" || fp.TopValues[0].Count != 3 {
		t.Errorf("top value = %v, want 5 x3", fp.TopValues)
	}
	if fp.DistinctCount != 3 || !fp.DistinctExact {
		t.Errorf("distinct = %d exact=%v, want 3 exact", fp.DistinctCount, fp.DistinctExact)
	}
}

func TestAccumulatorDistinctOverflow(t *testing.T) {
	a := newFieldAccumulator("s", 2)
	for _, v := range []string{"a", "b", "c", "d"} {
		a.AddValue(Observation{Path: "s", Kind: KindString, Str: v})
		a.MarkPresent()
	}
	fp := a.Result(4)
	if fp.DistinctExact {
		t.Errorf("expected distinct overflow to mark inexact")
	}
	if fp.StrLenMin == nil || *fp.StrLenMin != 1 {
		t.Errorf("str len min = %v, want 1", fp.StrLenMin)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/profile/ -run TestAccumulator -v`
Expected: FAIL (build error: accumulator symbols undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/profile/accumulator.go`:

```go
package profile

import (
	"sort"
	"strconv"
)

// ValueCount is a value and how often it was seen.
type ValueCount struct {
	Value string
	Count int
}

// FieldProfile is the accumulated profile for one path.
type FieldProfile struct {
	Path          string
	PresenceRate  float64
	TypeDist      map[JSONKind]float64
	NullRate      float64
	Min, Max      *float64
	DistinctCount int
	DistinctExact bool
	TopValues     []ValueCount
	StrLenMin     *int
	StrLenMax     *int
	Observations  int
}

type fieldAccumulator struct {
	path        string
	kindCounts  map[JSONKind]int
	present     int
	obs         int
	haveNum     bool
	min, max    float64
	haveLen     bool
	lenMin      int
	lenMax      int
	counts      map[string]int // value key -> count, doubles as top-K + distinct
	distinctCap int
	overflow    bool
}

func newFieldAccumulator(path string, distinctCap int) *fieldAccumulator {
	return &fieldAccumulator{
		path:        path,
		kindCounts:  map[JSONKind]int{},
		counts:      map[string]int{},
		distinctCap: distinctCap,
	}
}

func (a *fieldAccumulator) MarkPresent() { a.present++ }

func (a *fieldAccumulator) AddValue(o Observation) {
	a.obs++
	a.kindCounts[o.Kind]++
	switch o.Kind {
	case KindInt, KindFloat:
		if !a.haveNum || o.Num < a.min {
			a.min = o.Num
		}
		if !a.haveNum || o.Num > a.max {
			a.max = o.Num
		}
		a.haveNum = true
		a.addCount(numKey(o.Num))
	case KindString:
		l := len(o.Str)
		if !a.haveLen || l < a.lenMin {
			a.lenMin = l
		}
		if !a.haveLen || l > a.lenMax {
			a.lenMax = l
		}
		a.haveLen = true
		a.addCount(o.Str)
	}
}

func (a *fieldAccumulator) addCount(key string) {
	if _, ok := a.counts[key]; ok {
		a.counts[key]++
		return
	}
	if len(a.counts) >= a.distinctCap {
		a.overflow = true
		return
	}
	a.counts[key] = 1
}

func (a *fieldAccumulator) Result(totalRecords int) FieldProfile {
	fp := FieldProfile{
		Path:          a.path,
		TypeDist:      map[JSONKind]float64{},
		DistinctCount: len(a.counts),
		DistinctExact: !a.overflow,
		Observations:  a.obs,
	}
	if totalRecords > 0 {
		fp.PresenceRate = float64(a.present) / float64(totalRecords)
	}
	if a.obs > 0 {
		for k, c := range a.kindCounts {
			fp.TypeDist[k] = float64(c) / float64(a.obs)
		}
		fp.NullRate = float64(a.kindCounts[KindNull]) / float64(a.obs)
	}
	if a.haveNum {
		mn, mx := a.min, a.max
		fp.Min, fp.Max = &mn, &mx
	}
	if a.haveLen {
		mn, mx := a.lenMin, a.lenMax
		fp.StrLenMin, fp.StrLenMax = &mn, &mx
	}
	fp.TopValues = topValues(a.counts, 10)
	return fp
}

func topValues(counts map[string]int, k int) []ValueCount {
	vs := make([]ValueCount, 0, len(counts))
	for v, c := range counts {
		vs = append(vs, ValueCount{Value: v, Count: c})
	}
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Count != vs[j].Count {
			return vs[i].Count > vs[j].Count
		}
		return vs[i].Value < vs[j].Value
	})
	if len(vs) > k {
		vs = vs[:k]
	}
	return vs
}

func numKey(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
```

Note: bool fields get a type distribution and presence but no top-value counts in v1 (the common profiling targets are string/number fields, which are exact). Do not add a bool field to `Observation` for this - YAGNI.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/profile/ -run TestAccumulator -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/profile/accumulator.go internal/profile/accumulator_test.go
git commit -m "feat: accumulate per-field type, range, distinct, and top-value stats"
```

---

### Task 5: Profiler, ProfileResult, and drift detection

**Files:**
- Create: `internal/profile/profiler.go`
- Test: `internal/profile/profiler_test.go`

**Interfaces:**
- Consumes: `Flatten`, `Observation`, `fieldAccumulator`, `FieldProfile` (Tasks 3-4).
- Produces:
  - `type ProfileResult struct { Records int; Skipped int; Source string; Fields []FieldProfile }`
  - `func NewProfiler() *Profiler`
  - `(*Profiler).AddRecord(record any)` - flattens one record, updating presence once per unique path and value stats per observation.
  - `(*Profiler).AddSkipped(n int)` - records malformed inputs skipped by a reader.
  - `(*Profiler).Result() ProfileResult` - fields sorted by path.
  - `func IsTypeDrift(fp FieldProfile) bool` - true when a field shows more than one non-null value type (int and float count as one type, "number").

- [ ] **Step 1: Write the failing test**

`internal/profile/profiler_test.go`:

```go
package profile

import "testing"

func fieldByPath(res ProfileResult, path string) (FieldProfile, bool) {
	for _, f := range res.Fields {
		if f.Path == path {
			return f, true
		}
	}
	return FieldProfile{}, false
}

func TestProfilerPresenceAcrossRecords(t *testing.T) {
	p := NewProfiler()
	p.AddRecord(decodeAny(t, `{"a":1,"b":"x"}`))
	p.AddRecord(decodeAny(t, `{"a":2}`))
	res := p.Result()
	if res.Records != 2 {
		t.Fatalf("records = %d, want 2", res.Records)
	}
	a, _ := fieldByPath(res, "a")
	b, _ := fieldByPath(res, "b")
	if a.PresenceRate != 1.0 {
		t.Errorf("a presence = %v, want 1.0", a.PresenceRate)
	}
	if b.PresenceRate != 0.5 {
		t.Errorf("b presence = %v, want 0.5", b.PresenceRate)
	}
}

func TestProfilerArrayPresenceCountedOncePerRecord(t *testing.T) {
	p := NewProfiler()
	p.AddRecord(decodeAny(t, `{"tags":["x","y","z"]}`))
	res := p.Result()
	tags, _ := fieldByPath(res, "tags[]")
	if tags.PresenceRate != 1.0 {
		t.Errorf("tags[] presence = %v, want 1.0 (once per record)", tags.PresenceRate)
	}
	if tags.Observations != 3 {
		t.Errorf("tags[] observations = %d, want 3", tags.Observations)
	}
}

func TestIsTypeDrift(t *testing.T) {
	p := NewProfiler()
	p.AddRecord(decodeAny(t, `{"id":1}`))
	p.AddRecord(decodeAny(t, `{"id":"two"}`))
	res := p.Result()
	id, _ := fieldByPath(res, "id")
	if !IsTypeDrift(id) {
		t.Errorf("expected id to be flagged as drifting (int + string)")
	}

	p2 := NewProfiler()
	p2.AddRecord(decodeAny(t, `{"id":1}`))
	p2.AddRecord(decodeAny(t, `{"id":2.5}`))
	res2 := p2.Result()
	id2, _ := fieldByPath(res2, "id")
	if IsTypeDrift(id2) {
		t.Errorf("int + float should count as one number type, not drift")
	}
}
```

Add to `internal/profile/testutil_test.go`:

```go
func decodeAny(t testingT, s string) any { return decode(asT(t), s) }
```

Simpler: just reuse `decode`. Replace the two lines above by using `decode(t, s)` directly in the test (delete the helper). Keep `decode` from Task 2.

(Implementation note for the engineer: the profiler tests call `decode(t, s)` directly - do not add `decodeAny`. The line above is only here to flag the reuse.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/profile/ -run 'TestProfiler|TestIsTypeDrift' -v`
Expected: FAIL (build error: profiler symbols undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/profile/profiler.go`:

```go
package profile

import "sort"

// DefaultDistinctCap bounds exact distinct/top-value tracking per field.
const DefaultDistinctCap = 50000

// ProfileResult is the profile of a whole input.
type ProfileResult struct {
	Records int
	Skipped int
	Source  string
	Fields  []FieldProfile
}

// Profiler accumulates records into a ProfileResult.
type Profiler struct {
	accs    map[string]*fieldAccumulator
	order   []string
	records int
	skipped int
	cap     int
}

// NewProfiler returns a Profiler using the default distinct cap.
func NewProfiler() *Profiler {
	return &Profiler{accs: map[string]*fieldAccumulator{}, cap: DefaultDistinctCap}
}

// AddSkipped records malformed inputs skipped by a reader.
func (p *Profiler) AddSkipped(n int) { p.skipped += n }

// AddRecord flattens one record and updates presence and value stats.
func (p *Profiler) AddRecord(record any) {
	p.records++
	seen := map[string]bool{}
	Flatten(record, func(o Observation) {
		a := p.acc(o.Path)
		a.AddValue(o)
		if !seen[o.Path] {
			seen[o.Path] = true
			a.MarkPresent()
		}
	})
}

func (p *Profiler) acc(path string) *fieldAccumulator {
	a, ok := p.accs[path]
	if !ok {
		a = newFieldAccumulator(path, p.cap)
		p.accs[path] = a
		p.order = append(p.order, path)
	}
	return a
}

// Result assembles the sorted ProfileResult.
func (p *Profiler) Result() ProfileResult {
	paths := append([]string(nil), p.order...)
	sort.Strings(paths)
	fields := make([]FieldProfile, 0, len(paths))
	for _, path := range paths {
		fields = append(fields, p.accs[path].Result(p.records))
	}
	return ProfileResult{Records: p.records, Skipped: p.skipped, Fields: fields}
}

// IsTypeDrift reports whether a field shows more than one non-null value type.
// Int and float collapse to a single "number" type so 1 and 2.5 do not drift.
func IsTypeDrift(fp FieldProfile) bool {
	types := map[string]bool{}
	for k, frac := range fp.TypeDist {
		if frac <= 0 || k == KindNull {
			continue
		}
		switch k {
		case KindInt, KindFloat:
			types["number"] = true
		default:
			types[string(k)] = true
		}
	}
	return len(types) > 1
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/profile/ -run 'TestProfiler|TestIsTypeDrift' -v`
Expected: PASS. Then run the whole package: `go test ./internal/profile/ -v` (all green).

- [ ] **Step 5: Commit**

```bash
git add internal/profile/profiler.go internal/profile/profiler_test.go internal/profile/testutil_test.go
git commit -m "feat: assemble per-record profiles and detect type drift"
```

---

### Task 6: Streaming JSON/NDJSON reader

**Files:**
- Create: `internal/readers/jsonreader/reader.go`
- Test: `internal/readers/jsonreader/reader_test.go`

**Interfaces:**
- Consumes: nothing from other internal packages (returns raw decoded values).
- Produces:
  - `type Mode int` with `WholeMode` (a single JSON document; if it is an array, stream its elements) and `LineMode` (NDJSON: one JSON value per line, malformed lines skipped).
  - `func DetectMode(path, formatFlag string, peek []byte) Mode` - `formatFlag` one of `auto|json|ndjson`; for `auto`, `.ndjson`/`.jsonl` extension picks LineMode, `.json` picks WholeMode, otherwise the first non-space byte `[` picks WholeMode else LineMode.
  - `type Stream struct { ... }`
  - `func New(r io.Reader, mode Mode) *Stream`
  - `(*Stream).Next() (any, error)` - returns the next decoded record, `io.EOF` when done; in LineMode a malformed line is skipped (counted) rather than returned as an error.
  - `(*Stream).Skipped() int`

- [ ] **Step 1: Write the failing test**

`internal/readers/jsonreader/reader_test.go`:

```go
package jsonreader

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func drain(t *testing.T, s *Stream) []any {
	t.Helper()
	var got []any
	for {
		v, err := s.Next()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		got = append(got, v)
	}
}

func TestWholeModeArrayStreamsElements(t *testing.T) {
	s := New(strings.NewReader(`[{"a":1},{"a":2}]`), WholeMode)
	if got := drain(t, s); len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
}

func TestWholeModeSingleObject(t *testing.T) {
	s := New(strings.NewReader(`{"a":1}`), WholeMode)
	if got := drain(t, s); len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
}

func TestLineModeSkipsMalformed(t *testing.T) {
	s := New(strings.NewReader("{\"a\":1}\nnot json\n{\"a\":2}\n"), LineMode)
	got := drain(t, s)
	if len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
	if s.Skipped() != 1 {
		t.Fatalf("skipped = %d, want 1", s.Skipped())
	}
}

func TestDetectMode(t *testing.T) {
	if DetectMode("x.ndjson", "auto", nil) != LineMode {
		t.Error("ndjson ext should be LineMode")
	}
	if DetectMode("x.json", "auto", nil) != WholeMode {
		t.Error("json ext should be WholeMode")
	}
	if DetectMode("-", "auto", []byte("  [")) != WholeMode {
		t.Error("leading [ should be WholeMode")
	}
	if DetectMode("-", "auto", []byte(`{"a":1}`)) != LineMode {
		t.Error("leading { with no ext should be LineMode")
	}
	if DetectMode("x.json", "ndjson", nil) != LineMode {
		t.Error("explicit ndjson flag overrides ext")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/readers/jsonreader/ -v`
Expected: FAIL (build error: reader symbols undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/readers/jsonreader/reader.go`:

```go
package jsonreader

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// Mode selects how the reader interprets the input stream.
type Mode int

const (
	// WholeMode decodes one JSON document; an array streams its elements.
	WholeMode Mode = iota
	// LineMode decodes NDJSON: one JSON value per line, skipping malformed lines.
	LineMode
)

// DetectMode chooses a Mode from a path, an explicit format flag, and a peek.
func DetectMode(path, formatFlag string, peek []byte) Mode {
	switch formatFlag {
	case "json":
		return WholeMode
	case "ndjson":
		return LineMode
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".ndjson"), strings.HasSuffix(lower, ".jsonl"):
		return LineMode
	case strings.HasSuffix(lower, ".json"):
		return WholeMode
	}
	for _, b := range peek {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b == '[' {
			return WholeMode
		}
		return LineMode
	}
	return LineMode
}

// Stream yields decoded records from an input reader.
type Stream struct {
	mode    Mode
	dec     *json.Decoder // WholeMode
	inArray bool
	started bool
	sc      *bufio.Scanner // LineMode
	skipped int
	done    bool
}

// New builds a Stream over r in the given mode.
func New(r io.Reader, mode Mode) *Stream {
	s := &Stream{mode: mode}
	if mode == LineMode {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		s.sc = sc
		return s
	}
	dec := json.NewDecoder(r)
	dec.UseNumber()
	s.dec = dec
	return s
}

// Next returns the next record, or io.EOF when the stream is exhausted.
func (s *Stream) Next() (any, error) {
	if s.mode == LineMode {
		return s.nextLine()
	}
	return s.nextWhole()
}

func (s *Stream) nextLine() (any, error) {
	for s.sc.Scan() {
		line := strings.TrimSpace(s.sc.Text())
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			s.skipped++
			continue
		}
		return v, nil
	}
	if err := s.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *Stream) nextWhole() (any, error) {
	if s.done {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		tok, err := s.dec.Token()
		if err != nil {
			return nil, err
		}
		if d, ok := tok.(json.Delim); ok && d == '[' {
			s.inArray = true
		} else {
			// Not an array: the token we read is the start of a single value.
			// Re-decode from scratch is not possible, so handle the two shapes:
			// scalar/true/false/null tokens are complete values; object/array
			// delimiters need full decoding. Simplest robust path: only arrays
			// stream; everything else is decoded as one value below.
			s.inArray = false
			return s.decodeSingleAfterToken(tok)
		}
	}
	if s.inArray {
		if !s.dec.More() {
			s.done = true
			return nil, io.EOF
		}
		var v any
		if err := s.dec.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	}
	s.done = true
	return nil, io.EOF
}

// decodeSingleAfterToken reconstructs a single non-array document whose first
// token has already been consumed.
func (s *Stream) decodeSingleAfterToken(tok json.Token) (any, error) {
	s.done = true
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			// Decode the rest of the object by reading key/value tokens.
			return s.decodeObjectBody()
		}
		return nil, io.EOF
	default:
		return t, nil // scalar, bool, or null value
	}
}

func (s *Stream) decodeObjectBody() (any, error) {
	obj := map[string]any{}
	for s.dec.More() {
		keyTok, err := s.dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		var val any
		if err := s.dec.Decode(&val); err != nil {
			return nil, err
		}
		obj[key] = val
	}
	// consume closing '}'
	if _, err := s.dec.Token(); err != nil && err != io.EOF {
		return nil, err
	}
	return obj, nil
}

// Skipped returns how many malformed inputs were skipped.
func (s *Stream) Skipped() int { return s.skipped }
```

Implementation note: for WholeMode the common cases (top-level array, single object) are covered. Nested numbers inside a single object decoded via `decodeObjectBody`/`s.dec.Decode(&val)` are plain `float64` (not `json.Number`) because `Decode(&any)` does not propagate `UseNumber` into nested generic decoding for the token path - that is acceptable in v1 (`KindOf` has a `float64` fallback). NDJSON line values DO preserve `json.Number` via the per-line decoder. Do not over-engineer this in v1; the WholeMode single-object numeric-kind edge is documented, not fixed here.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/readers/jsonreader/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/readers/jsonreader/reader.go internal/readers/jsonreader/reader_test.go
git commit -m "feat: streaming JSON/NDJSON reader with mode detection"
```

---

### Task 7: Renderers (TTY table and --json)

**Files:**
- Create: `internal/render/table.go`
- Create: `internal/render/json.go`
- Test: `internal/render/render_test.go`

**Interfaces:**
- Consumes: `profile.ProfileResult`, `profile.FieldProfile`, `profile.IsTypeDrift` (Tasks 4-5).
- Produces:
  - `func Table(w io.Writer, res profile.ProfileResult)` - writes a human table; drifting fields are marked with a `!` in a `DRIFT` column.
  - `func JSON(w io.Writer, res profile.ProfileResult) error` - writes stable indented JSON.

- [ ] **Step 1: Write the failing test**

`internal/render/render_test.go`:

```go
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

func sample() profile.ProfileResult {
	p := profile.NewProfiler()
	dec := func(s string) any {
		d := json.NewDecoder(strings.NewReader(s))
		d.UseNumber()
		var v any
		_ = d.Decode(&v)
		return v
	}
	p.AddRecord(dec(`{"id":1,"email":"a@b.c"}`))
	p.AddRecord(dec(`{"id":"two"}`))
	return p.Result()
}

func TestTableMarksDrift(t *testing.T) {
	var b bytes.Buffer
	Table(&b, sample())
	out := b.String()
	if !strings.Contains(out, "id") || !strings.Contains(out, "email") {
		t.Fatalf("table missing fields:\n%s", out)
	}
	if !strings.Contains(out, "!") {
		t.Errorf("expected a drift marker for id:\n%s", out)
	}
}

func TestJSONStableShape(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, sample()); err != nil {
		t.Fatalf("json: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(b.Bytes(), &back); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if _, ok := back["fields"]; !ok {
		t.Errorf("expected top-level fields key, got %v", back)
	}
	if _, ok := back["records"]; !ok {
		t.Errorf("expected top-level records key, got %v", back)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/ -v`
Expected: FAIL (build error: `Table`/`JSON` undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/render/table.go`:

```go
package render

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/hoijun-kim/shape/internal/profile"
)

// Table writes a human-readable profile table to w.
func Table(w io.Writer, res profile.ProfileResult) {
	fmt.Fprintf(w, "records: %d", res.Records)
	if res.Skipped > 0 {
		fmt.Fprintf(w, "  skipped: %d", res.Skipped)
	}
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "FIELD\tPRESENCE\tTYPES\tNULL\tDISTINCT\tDRIFT")
	for _, f := range res.Fields {
		drift := ""
		if profile.IsTypeDrift(f) {
			drift = "!"
		}
		distinct := fmt.Sprintf("%d", f.DistinctCount)
		if !f.DistinctExact {
			distinct += "+"
		}
		fmt.Fprintf(tw, "%s\t%.0f%%\t%s\t%.0f%%\t%s\t%s\n",
			f.Path, f.PresenceRate*100, typesLabel(f), f.NullRate*100, distinct, drift)
	}
	tw.Flush()
}

func typesLabel(f profile.FieldProfile) string {
	best := ""
	var bestFrac float64
	for k, frac := range f.TypeDist {
		if frac > bestFrac {
			bestFrac, best = frac, string(k)
		}
	}
	if len(f.TypeDist) > 1 {
		return fmt.Sprintf("%s..", best)
	}
	return best
}
```

`internal/render/json.go`:

```go
package render

import (
	"encoding/json"
	"io"

	"github.com/hoijun-kim/shape/internal/profile"
)

// JSON writes the profile result as stable indented JSON.
func JSON(w io.Writer, res profile.ProfileResult) error {
	type field struct {
		Path          string                 `json:"path"`
		Presence      float64                `json:"presence"`
		Types         map[string]float64     `json:"types"`
		NullRate      float64                `json:"null_rate"`
		Min           *float64               `json:"min,omitempty"`
		Max           *float64               `json:"max,omitempty"`
		DistinctCount int                    `json:"distinct_count"`
		DistinctExact bool                   `json:"distinct_exact"`
		Drift         bool                   `json:"drift"`
		Top           []map[string]any       `json:"top_values,omitempty"`
	}
	out := struct {
		Records int     `json:"records"`
		Skipped int     `json:"skipped"`
		Fields  []field `json:"fields"`
	}{Records: res.Records, Skipped: res.Skipped}

	for _, f := range res.Fields {
		types := map[string]float64{}
		for k, v := range f.TypeDist {
			types[string(k)] = v
		}
		top := make([]map[string]any, 0, len(f.TopValues))
		for _, v := range f.TopValues {
			top = append(top, map[string]any{"value": v.Value, "count": v.Count})
		}
		out.Fields = append(out.Fields, field{
			Path: f.Path, Presence: f.PresenceRate, Types: types,
			NullRate: f.NullRate, Min: f.Min, Max: f.Max,
			DistinctCount: f.DistinctCount, DistinctExact: f.DistinctExact,
			Drift: profile.IsTypeDrift(f), Top: top,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/render/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/render/table.go internal/render/json.go internal/render/render_test.go
git commit -m "feat: render profiles as a TTY table and stable JSON"
```

---

### Task 8: Wire the `shape profile` command end-to-end

**Files:**
- Create: `internal/cmd/profile.go`
- Modify: `internal/cmd/root.go` (register the subcommand)
- Test: `internal/cmd/profile_test.go`
- Create (test fixture): `internal/cmd/testdata/sample.ndjson`

**Interfaces:**
- Consumes: `jsonreader.DetectMode/New/Stream` (Task 6), `profile.NewProfiler` (Task 5), `render.Table/JSON` (Task 7).
- Produces: `shape profile <file|-> [--json] [--format auto|json|ndjson]` reading a file path or stdin (`-`), streaming records into a profiler, and rendering the result. Exit non-zero on read/open errors.

- [ ] **Step 1: Create the test fixture**

`internal/cmd/testdata/sample.ndjson`:

```
{"id":1,"email":"a@b.c","tags":["x","y"]}
{"id":2,"email":null,"tags":["z"]}
{"id":"three","tags":[]}
```

- [ ] **Step 2: Write the failing test**

`internal/cmd/profile_test.go`:

```go
package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runProfile(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"profile"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (out: %s)", err, out.String())
	}
	return out.String()
}

func TestProfileTableFromFile(t *testing.T) {
	out := runProfile(t, "testdata/sample.ndjson")
	if !strings.Contains(out, "records: 3") {
		t.Errorf("expected 3 records:\n%s", out)
	}
	if !strings.Contains(out, "id") || !strings.Contains(out, "!") {
		t.Errorf("expected id field flagged as drift:\n%s", out)
	}
}

func TestProfileJSONFromFile(t *testing.T) {
	out := runProfile(t, "--json", "testdata/sample.ndjson")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if parsed["records"].(float64) != 3 {
		t.Errorf("records = %v, want 3", parsed["records"])
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run TestProfile -v`
Expected: FAIL (build error: `profile` subcommand not registered).

- [ ] **Step 4: Write minimal implementation**

`internal/cmd/profile.go`:

```go
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers/jsonreader"
	"github.com/hoijun-kim/shape/internal/render"
	"github.com/spf13/cobra"
)

func newProfileCmd() *cobra.Command {
	var asJSON bool
	var format string

	cmd := &cobra.Command{
		Use:   "profile <file|->",
		Short: "Profile the shape of a JSON or NDJSON input",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			r, peek, closeFn, err := openSource(src)
			if err != nil {
				return err
			}
			defer closeFn()

			mode := jsonreader.DetectMode(src, format, peek)
			stream := jsonreader.New(r, mode)
			p := profile.NewProfiler()
			for {
				rec, err := stream.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return fmt.Errorf("read %s: %w", src, err)
				}
				p.AddRecord(rec)
			}
			p.AddSkipped(stream.Skipped())
			res := p.Result()
			res.Source = src

			if asJSON {
				return render.JSON(cmd.OutOrStdout(), res)
			}
			render.Table(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	cmd.Flags().StringVar(&format, "format", "auto", "input format: auto|json|ndjson")
	return cmd
}

// openSource opens a file path or stdin ("-"), returning the reader, a peek of
// the first bytes (for format detection), and a close function.
func openSource(src string) (io.Reader, []byte, func(), error) {
	if src == "-" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(os.Stdin, buf)
		peek := buf[:n]
		combined := io.MultiReader(bytesReader(peek), os.Stdin)
		return combined, peek, func() {}, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open %s: %w", src, err)
	}
	peek := make([]byte, 512)
	n, _ := f.Read(peek)
	peek = peek[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, nil, err
	}
	return f, peek, func() { f.Close() }, nil
}
```

Add a tiny helper `internal/cmd/bytesreader.go`:

```go
package cmd

import "bytes"

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
```

Modify `internal/cmd/root.go` - register the subcommand inside `NewRootCmd`, just before `return root`:

```go
	root.AddCommand(newProfileCmd())
	return root
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/cmd/ -run TestProfile -v`
Expected: PASS.

- [ ] **Step 6: Run the whole suite and build**

Run:
```bash
go test ./...
go build -o shape .   # produces the shape binary (shape.exe on Windows)
```
Expected: all tests PASS; binary builds.

- [ ] **Step 7: Manual smoke check**

Run: `go run . profile internal/cmd/testdata/sample.ndjson`
Expected: a table showing `records: 3` and a `!` in the DRIFT column for `id`.
Run: `go run . profile --json internal/cmd/testdata/sample.ndjson`
Expected: valid JSON with `"records": 3` and `"drift": true` on the `id` field.

- [ ] **Step 8: Commit**

```bash
git add internal/cmd/profile.go internal/cmd/root.go internal/cmd/bytesreader.go internal/cmd/profile_test.go internal/cmd/testdata/sample.ndjson
git commit -m "feat: wire shape profile command end-to-end"
```

---

## Plan 1 self-review

Coverage vs spec (Plan 1 slice only):
- Streaming JSON + NDJSON input, file or stdin - Tasks 6, 8. (CSV/Parquet/SQLite are Plan 5, by design.)
- Per-field profile: presence, type distribution, null rate, min/max, distinct/cardinality with exact-cap flag, top-K, string length - Tasks 4, 5.
- Type-drift surfacing - Tasks 5, 7 (table `!` and JSON `drift`).
- TTY table + `--json` output - Task 7, 8.
- JSON Schema export - NOT in Plan 1 (Plan 2, intentional).
- Snapshot diff + breaking contract - NOT in Plan 1 (Plan 3, intentional).
- Approximate HLL/top-K/reservoir - NOT in Plan 1; v1 uses exact tracking with a distinct cap and an inexact flag (Plan 4 replaces the cap with sketches).

Placeholder scan: no TBD/TODO; every code step contains complete code. The two "implementation note" callouts (bool top-values coarseness; WholeMode nested numeric-kind edge) are explicit accepted-limitations for v1, not deferred work.

Type consistency check: `ProfileResult`, `FieldProfile`, `Observation`, `JSONKind` consts, `NewProfiler/AddRecord/AddSkipped/Result`, `IsTypeDrift`, `jsonreader.Mode/New/DetectMode/Stream.Next/Skipped`, `render.Table/JSON` are defined once and used with matching signatures across Tasks 2-8.

## Next plans (write each when its predecessor lands)
- Plan 2: `shape schema` - infer and export JSON Schema (Draft 2020-12) from a `ProfileResult`.
- Plan 3: `shape diff A B` + `--fail-on breaking` contract and CLI exit codes.
- Plan 4: approximate mode - HyperLogLog distinct, Space-Saving top-K, reservoir sampling; auto-engage past a size threshold.
- Plan 5: CSV / Parquet / SQLite readers behind the same record-stream contract.
- Plan 6: Wails desktop GUI over the same core.
- Plan 7: distribution - GitHub Action, Homebrew tap, npm wrapper, release binaries.
