# P2: internal/visual VisualModel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `internal/visual` package that turns the profiler's `ProfileResult` (and the differ's `DiffResult`) into pure, deterministic, render-ready chart geometry (a "VisualModel") for both the Svelte GUI (P3) and the Go HTML report (P6).

**Architecture:** All chart math lives once in Go as pure data. Two entry points, `FromProfile(res, opts) VisualModel` and `FromDiff(d) DiffVisualModel`. Each `FieldCard` is a pure function of one `FieldProfile` (plus a read-only field index for array containers). No rendering, no color, stdlib only. Golden-tested.

**Tech Stack:** Go, standard library only.

**AUTHORITATIVE SPEC:** `docs/superpowers/specs/2026-07-16-shape-p2-visualmodel-design.md`. Every task implements a named section of that doc verbatim — exact type definitions, thresholds, algorithms, and formulas. Each task below names its section; the design doc is the single source of exact values. Where a task shows code, it is the deliverable; where it names a design-doc section, transcribe that section exactly.

## Global Constraints

- Package `visual` at `internal/visual`; imports only `internal/profile`, `internal/diff`, and Go stdlib. No third-party deps. (Design "Package API".)
- Pure data + deterministic: NO Go map is ever iterated into output; project `TypeDist` through the fixed `kindOrder` slice; `TopValues` are consumed in their existing `(count desc, value asc)` order. (Design "Determinism contract".)
- All rounding/formatting happens in this package via the §7 helpers; geometry is emitted as fractions in 0..1. Views do `fraction × extent` only.
- NaN/Inf are never emitted in any output field (skip them; `fmtNum` maps them to "—"). (Design §5, §7.)
- Constant values are named consts exactly as in design §3 and §5.1 — no magic numbers inline.
- Existing CLI/GUI behavior is untouched by P2 (this package is new and not yet wired into any command; wiring is P3/P6).

---

### Task 1: Types, constants, and formatting helpers

**Files:**
- Create: `internal/visual/types.go`
- Create: `internal/visual/format.go`
- Test: `internal/visual/format_test.go`

**Interfaces:**
- Produces (consumed by every later task): all types and consts from design §1, §2, §3 (const block), §5.1 (`fieldPenalty`, `SkipPenaltyMax`), §6; and helpers `fmtPct(float64) string`, `fmtInt(int) string`, `fmtNum(float64) string`, `fmtDistinct(int, bool) string`, `safeDiv(float64, float64) float64`, `trim1(float64) string`, and `deriveFormat(name string) string`.

- [ ] **Step 1: Write the failing tests** for the formatting helpers.

Create `internal/visual/format_test.go`:

```go
package visual

import "testing"

func TestFmtPct(t *testing.T) {
	cases := map[float64]string{0: "0%", 0.5: "50%", 0.976: "98%", 1: "100%", 0.024: "2%"}
	for in, want := range cases {
		if got := fmtPct(in); got != want {
			t.Errorf("fmtPct(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFmtInt(t *testing.T) {
	cases := map[int]string{0: "0", 5: "5", 1000: "1,000", 12480: "12,480", -12480: "-12,480", 1000000: "1,000,000"}
	for in, want := range cases {
		if got := fmtInt(in); got != want {
			t.Errorf("fmtInt(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFmtNum(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"}, {42, "42"}, {3.14159, "3.14"}, {3.5, "3.5"}, {1500, "1,500"},
		{12000, "12K"}, {1500000, "1.5M"}, {2000000000, "2B"}, {3.5e12, "3.5T"},
	}
	for _, c := range cases {
		if got := fmtNum(c.in); got != c.want {
			t.Errorf("fmtNum(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFmtNumNonFinite(t *testing.T) {
	for _, v := range []float64{nan(), inf(1), inf(-1)} {
		if got := fmtNum(v); got != "—" {
			t.Errorf("fmtNum(%v) = %q, want em-dash", v, got)
		}
	}
}

func TestFmtDistinct(t *testing.T) {
	if got := fmtDistinct(48210, true); got != "48,210" {
		t.Errorf("exact = %q, want 48,210", got)
	}
	if got := fmtDistinct(48210, false); got != "~48,210" {
		t.Errorf("approx = %q, want ~48,210", got)
	}
}

func TestSafeDiv(t *testing.T) {
	if got := safeDiv(1, 0); got != 0 {
		t.Errorf("safeDiv(1,0) = %v, want 0", got)
	}
	if got := safeDiv(3, 6); got != 0.5 {
		t.Errorf("safeDiv(3,6) = %v, want 0.5", got)
	}
}

func TestDeriveFormat(t *testing.T) {
	cases := map[string]string{
		"a.csv": "CSV", "b.tsv": "TSV", "c.parquet": "Parquet", "d.sqlite": "SQLite",
		"e.ndjson": "NDJSON", "f.jsonl": "NDJSON", "g.json": "JSON", "": "—",
	}
	for in, want := range cases {
		if got := deriveFormat(in); got != want {
			t.Errorf("deriveFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

// nan/inf/half-up helpers local to the test (avoid importing math at top just for these).
func nan() float64   { var z float64; return z / z }
func inf(s int) float64 { z := 0.0; if s < 0 { return -1 / z }; return 1 / z }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/visual/ -run 'TestFmt|TestSafeDiv|TestDeriveFormat' -v`
Expected: FAIL — package/functions do not exist yet (build error).

- [ ] **Step 3: Transcribe the types** into `internal/visual/types.go`.

Transcribe, verbatim, all Go type/const/var blocks from design doc sections: §1 (Severity, severityRank, severityIcon, kindOrder, Badge, Meter, TypeSegment, Stat), §2 (VisualModel, Summary, KPITile, FieldCard, ChartForm+consts, Histogram, HistBar, Categorical, CategoryBar, HighCardString, StrLenBar, ArrayBreakdown, SparkPoint), §3 const block (DisplayBins … NullSeriousBand), §5.1 (fieldPenalty, SkipPenaltyMax), and §6 (DiffVisualModel, DiffGroup, DiffRow, DiffDetail). Add the package clause and the `Options`, `FromProfile`, `FromDiff` signatures (bodies as `panic("not implemented")` stubs to be filled by later tasks — Task 7 fills `FromProfile`, Task 8 fills `FromDiff`). Do not add any field or const not in the design doc.

- [ ] **Step 4: Write the formatting helpers** into `internal/visual/format.go`.

Implement per design §7:

```go
package visual

import (
	"math"
	"strconv"
	"strings"
)

func fmtPct(f float64) string { return strconv.Itoa(int(f*100+0.5)) + "%" }

func fmtInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func fmtDistinct(n int, exact bool) string {
	if exact {
		return fmtInt(n)
	}
	return "~" + fmtInt(n)
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// trim1 formats x to one decimal and drops a trailing ".0".
func trim1(x float64) string {
	s := strconv.FormatFloat(x, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// fmtNum formats a numeric value for display. Non-finite -> em-dash. Compact
// SI-ish suffix at/above 1e4; integer-valued prints grouped; else 2 decimals
// with trailing zeros trimmed.
func fmtNum(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "—"
	}
	abs := math.Abs(f)
	switch {
	case abs >= 1e12:
		return trim1(f/1e12) + "T"
	case abs >= 1e9:
		return trim1(f/1e9) + "B"
	case abs >= 1e6:
		return trim1(f/1e6) + "M"
	case abs >= 1e4:
		return trim1(f/1e3) + "K"
	}
	if f == math.Trunc(f) {
		return fmtInt(int(f))
	}
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// deriveFormat maps a filename/label extension to a format display label.
func deriveFormat(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".csv"):
		return "CSV"
	case strings.HasSuffix(l, ".tsv"):
		return "TSV"
	case strings.HasSuffix(l, ".parquet"), strings.HasSuffix(l, ".pqt"):
		return "Parquet"
	case strings.HasSuffix(l, ".sqlite"), strings.HasSuffix(l, ".sqlite3"), strings.HasSuffix(l, ".db"):
		return "SQLite"
	case strings.HasSuffix(l, ".ndjson"), strings.HasSuffix(l, ".jsonl"):
		return "NDJSON"
	case strings.HasSuffix(l, ".json"):
		return "JSON"
	case name == "":
		return "—"
	default:
		if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
			return strings.ToUpper(name[i+1:])
		}
		return "—"
	}
}
```

- [ ] **Step 5: Run tests + build to verify pass**

Run: `go build ./internal/visual/ && go test ./internal/visual/ -run 'TestFmt|TestSafeDiv|TestDeriveFormat' -v`
Expected: PASS (package builds with stubbed FromProfile/FromDiff; all helper tests pass).

- [ ] **Step 6: gofmt + commit**

```bash
gofmt -w internal/visual/types.go internal/visual/format.go internal/visual/format_test.go
git add internal/visual/types.go internal/visual/format.go internal/visual/format_test.go
git commit -m "feat(visual): package types, constants, formatting helpers"
```

---

### Task 2: Chart-form selection + EnumLike

**Files:**
- Create: `internal/visual/form.go`
- Test: `internal/visual/form_test.go`

**Interfaces:**
- Consumes: types from Task 1; `profile.FieldProfile`, `profile.IsTypeDrift`.
- Produces: `selectForm(fp profile.FieldProfile) (ChartForm, string)` returning the form and the resolved `Kind` string; `enumLike(fp profile.FieldProfile) bool`; helper `dominantKind(fp profile.FieldProfile) string` (the single non-null folded kind, `""` if none/all-null).

- [ ] **Step 1: Write the failing tests** — boundary cases from design §8.

Create `internal/visual/form_test.go` with a table-driven test constructing `profile.FieldProfile` values and asserting `(form, kind)`. Cover exactly: empty (`Observations==0`), all-null (`NullRate>=1`), drift (two non-null kinds) -> `FormTypeMix`/"mixed", discrete numeric distinct 12 -> categorical, distinct 13 -> histogram, string distinct 25 -> categorical, distinct 26 -> highCard, promoted string (`DistinctExact=false`) -> highCard, numeric with empty `Histogram` -> meter, array -> array, bool -> meter. Plus `enumLike` true for a string with `DistinctExact`, distinct 3, `len(TopValues)==3`; false when distinct 11 or numeric.

Write full assertions (build each `profile.FieldProfile` literal with the fields the rule reads: `Observations`, `NullRate`, `TypeDist`, `DistinctExact`, `DistinctCount`, `TopValues`, `Histogram`, `Min`). Example rows:

```go
package visual

import (
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

func num(kinds ...profile.JSONKind) map[profile.JSONKind]float64 {
	m := map[profile.JSONKind]float64{}
	for _, k := range kinds {
		m[k] = 1.0 / float64(len(kinds))
	}
	return m
}

func TestSelectFormBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		fp       profile.FieldProfile
		wantForm ChartForm
		wantKind string
	}{
		{"empty", profile.FieldProfile{Observations: 0}, FormEmpty, "empty"},
		{"all-null", profile.FieldProfile{Observations: 5, NullRate: 1, TypeDist: num(profile.KindNull)}, FormEmpty, "empty"},
		{"drift", profile.FieldProfile{Observations: 5, TypeDist: num(profile.KindInt, profile.KindString), DistinctExact: true, DistinctCount: 5}, FormTypeMix, "mixed"},
		{"discrete-num-12", profile.FieldProfile{Observations: 100, TypeDist: num(profile.KindInt), DistinctExact: true, DistinctCount: 12, TopValues: make([]profile.ValueCount, 12)}, FormCategorical, "number"},
		{"num-13-hist", profile.FieldProfile{Observations: 100, TypeDist: num(profile.KindInt), DistinctExact: true, DistinctCount: 13, Histogram: []profile.HistBin{{Value: 1, Count: 100}}, Min: fptr(1), Max: fptr(9)}, FormHistogram, "number"},
		{"str-25-cat", profile.FieldProfile{Observations: 100, TypeDist: num(profile.KindString), DistinctExact: true, DistinctCount: 25}, FormCategorical, "string"},
		{"str-26-highcard", profile.FieldProfile{Observations: 100, TypeDist: num(profile.KindString), DistinctExact: true, DistinctCount: 26}, FormHighCard, "string"},
		{"promoted-str", profile.FieldProfile{Observations: 100000, TypeDist: num(profile.KindString), DistinctExact: false, DistinctCount: 90000}, FormHighCard, "string"},
		{"num-no-hist-meter", profile.FieldProfile{Observations: 5, TypeDist: num(profile.KindFloat), DistinctExact: true, DistinctCount: 30}, FormMeter, "number"},
		{"array", profile.FieldProfile{Observations: 5, TypeDist: num(profile.KindArray), DistinctExact: true, DistinctCount: 1}, FormArray, "array"},
		{"bool", profile.FieldProfile{Observations: 5, TypeDist: num(profile.KindBool), DistinctExact: true, DistinctCount: 2}, FormMeter, "bool"},
	}
	for _, tt := range tests {
		gotForm, gotKind := selectForm(tt.fp)
		if gotForm != tt.wantForm || gotKind != tt.wantKind {
			t.Errorf("%s: selectForm = (%s,%s), want (%s,%s)", tt.name, gotForm, gotKind, tt.wantForm, tt.wantKind)
		}
	}
}

func fptr(f float64) *float64 { return &f }
```

(Note: `num()` here splits share evenly; for the "drift" row two non-null kinds each get 0.5 so `IsTypeDrift` is true. For single-kind rows one kind gets 1.0. The "num-no-hist-meter" row has empty `Histogram` and distinct 30 > DiscreteNumericMax, so it is neither categorical nor histogram -> meter.)

Add `TestEnumLike` asserting the four-clause rule.

- [ ] **Step 2: Run to verify fail.** `go test ./internal/visual/ -run 'TestSelectForm|TestEnumLike' -v` -> FAIL (undefined).

- [ ] **Step 3: Implement** `internal/visual/form.go` per design §3 (the `selectForm` pseudocode, `dominantKind`, and the `enumLike` four-clause rule). `dominantKind` folds `KindInt`/`KindFloat` to `"number"`, ignores `KindNull`, and returns `""` if zero or more-than-one non-null kind has a positive share (drift is handled before `dominantKind` matters, but return `""` on ambiguity for safety). Use `profile.IsTypeDrift(fp)` for the drift check.

- [ ] **Step 4: Run to verify pass.** `go test ./internal/visual/ -run 'TestSelectForm|TestEnumLike' -v` -> PASS.

- [ ] **Step 5: gofmt + commit**

```bash
gofmt -w internal/visual/form.go internal/visual/form_test.go
git add internal/visual/form.go internal/visual/form_test.go
git commit -m "feat(visual): chart-form selection and enum detection"
```

---

### Task 3: Histogram display re-binning

**Files:**
- Create: `internal/visual/histogram.go`
- Test: `internal/visual/histogram_test.go`

**Interfaces:**
- Consumes: `profile.FieldProfile`, `profile.HistBin`, types from Task 1, `fmtNum`/`safeDiv` from Task 1.
- Produces: `displayHistogram(fp profile.FieldProfile) Histogram`.

- [ ] **Step 1: Write the failing tests** per design §8 (`displayHistogram`):

```go
package visual

import "testing"

func hbins(pairs ...float64) []profile.HistBin { // value,count,value,count,...
	var out []profile.HistBin
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, profile.HistBin{Value: pairs[i], Count: int(pairs[i+1])})
	}
	return out
}

func TestDisplayHistogramMassPreserved(t *testing.T) {
	fp := profile.FieldProfile{Min: fptr(0), Max: fptr(100), Histogram: hbins(1, 5, 25, 10, 50, 20, 99, 5)}
	h := displayHistogram(fp)
	if len(h.Bins) != DisplayBins {
		t.Fatalf("bins = %d, want %d", len(h.Bins), DisplayBins)
	}
	sum := 0
	for _, b := range h.Bins {
		sum += b.Count
	}
	if sum != 40 || h.Total != 40 {
		t.Errorf("sum=%d total=%d, want 40/40", sum, h.Total)
	}
}

func TestDisplayHistogramDegenerate(t *testing.T) {
	fp := profile.FieldProfile{Min: fptr(7), Max: fptr(7), Histogram: hbins(7, 9)}
	h := displayHistogram(fp)
	if len(h.Bins) != 1 || h.Bins[0].Count != 9 || h.BinWidth != 0 || h.Bins[0].Frac != 1 {
		t.Errorf("degenerate = %+v, want single full bar", h)
	}
}

func TestDisplayHistogramMaxClampsToLastBin(t *testing.T) {
	fp := profile.FieldProfile{Min: fptr(0), Max: fptr(20), Histogram: hbins(20, 3)} // value == max
	h := displayHistogram(fp)
	if h.Bins[DisplayBins-1].Count != 3 {
		t.Errorf("value==max landed in bin != last: %+v", h.Bins[DisplayBins-1])
	}
}
```

(Requires `fptr` from Task 2's test file — both are package `visual` test files, so `fptr` is shared. If Task 3 runs before Task 2 in a fresh checkout it is not — but this plan runs tasks in order and both are in the same test package.)

- [ ] **Step 2: Run to verify fail.** -> FAIL (undefined `displayHistogram`).

- [ ] **Step 3: Implement** `internal/visual/histogram.go` per design §4 pseudocode exactly (point-mass binning, degenerate `hi<=lo` branch, floor index with clamp, `Frac=safeDiv(count,maxCount)`, labels via `fmtNum`).

- [ ] **Step 4: Run to verify pass.** -> PASS.

- [ ] **Step 5: gofmt + commit**

```bash
gofmt -w internal/visual/histogram.go internal/visual/histogram_test.go
git add internal/visual/histogram.go internal/visual/histogram_test.go
git commit -m "feat(visual): equal-width histogram display re-binning"
```

---

### Task 4: Base field geometry — type-mix, meter, sparkline, stats

**Files:**
- Create: `internal/visual/geometry.go`
- Test: `internal/visual/geometry_test.go`

**Interfaces:**
- Consumes: Task 1 types + helpers; `profile.FieldProfile`, `profile.IsTypeDrift`, `kindOrder`.
- Produces: `typeMix(fp) []TypeSegment`; `meter(fp) Meter`; `numericStats(fp) []Stat`; `stringStats(fp) []Stat`; `otherStats(fp) []Stat`; `sparkFromBins([]HistBar) []SparkPoint`; `sparkFromBars([]CategoryBar) []SparkPoint`; `nullStatus(rate float64) Severity`.

- [ ] **Step 1: Write the failing tests** covering: type-mix on a clean single-type field (one segment `Frac≈1`, `Offset 0`), a drift field (segments in `kindOrder` order, `Offset` cumulative, fracs sum to ~1); meter texts + `nullStatus` bands at 0.19/0.20/0.50/1.0; numericStats key order `min,mean,median,p95,max,distinct` with `mean` = centroid-weighted and `Approx` flags; sparkline point count and normalized X. Write complete assertions (use `fptr`, `hbins`, `num` helpers from earlier test files).

Example (type-mix + nullStatus):

```go
func TestTypeMixClean(t *testing.T) {
	fp := profile.FieldProfile{Observations: 10, NullRate: 0, TypeDist: num(profile.KindString)}
	segs := typeMix(fp)
	if len(segs) != 1 || segs[0].Kind != "string" || segs[0].Offset != 0 {
		t.Fatalf("clean typeMix = %+v", segs)
	}
	if segs[0].Frac < 0.999 {
		t.Errorf("frac = %v, want ~1", segs[0].Frac)
	}
}

func TestNullStatusBands(t *testing.T) {
	cases := map[float64]Severity{0.19: SevNone, 0.20: SevWarning, 0.49: SevWarning, 0.50: SevSerious, 1.0: SevCritical}
	for r, want := range cases {
		if got := nullStatus(r); got != want {
			t.Errorf("nullStatus(%v) = %q, want %q", r, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify fail.** -> FAIL.

- [ ] **Step 3: Implement** `internal/visual/geometry.go` per design §5 (type-mix folding through `kindOrder` over non-null observations with precomputed `Offset`; meter with `fmtPct` texts and `nullStatus`; numeric/string/other stats with the fixed key order and `Approx` flags, `mean` computed from centroids, NaN/Inf skipped; sparkline builders with `X:(i+0.5)/n`).

- [ ] **Step 4: Run to verify pass.** -> PASS.

- [ ] **Step 5: gofmt + commit**

```bash
gofmt -w internal/visual/geometry.go internal/visual/geometry_test.go
git add internal/visual/geometry.go internal/visual/geometry_test.go
git commit -m "feat(visual): type-mix, meter, stats, sparkline geometry"
```

---

### Task 5: Hero payload builders — categorical, high-card, array

**Files:**
- Create: `internal/visual/heroes.go`
- Test: `internal/visual/heroes_test.go`

**Interfaces:**
- Consumes: Task 1 types + helpers; `profile.FieldProfile`, `profile.ValueCount`; a field index `map[string]profile.FieldProfile`.
- Produces: `categorical(fp) *Categorical`; `highCard(fp) *HighCardString`; `arrayBreakdown(fp profile.FieldProfile, index map[string]profile.FieldProfile) *ArrayBreakdown`.

- [ ] **Step 1: Write the failing tests**: categorical bars from `TopValues` (bar `Frac=Count/MaxCount`, `Percent` vs total, `Truncated` when `DistinctCount>len(bars)`, `Other` aggregate present when truncated); highCard `DistinctText` exact vs `~` approx, `Sample` = first `CardinalitySample` values, `StrLen` present when `StrLenMin/Max` set; arrayBreakdown reads the `path+"[]"` sibling from the index (`Present`, `ElementCount`, `ElementTypes` via type-mix) and `Present=false` when the element path is absent (empty arrays). Full assertions.

- [ ] **Step 2: Run to verify fail.** -> FAIL.

- [ ] **Step 3: Implement** `internal/visual/heroes.go` per design §2.2 + §3 (TopK, CardinalitySample) + §5 (array uses `typeMix` of the element field).

- [ ] **Step 4: Run to verify pass.** -> PASS.

- [ ] **Step 5: gofmt + commit**

```bash
gofmt -w internal/visual/heroes.go internal/visual/heroes_test.go
git add internal/visual/heroes.go internal/visual/heroes_test.go
git commit -m "feat(visual): categorical, high-card, and array hero payloads"
```

---

### Task 6: Badges and health score

**Files:**
- Create: `internal/visual/health.go`
- Test: `internal/visual/health_test.go`

**Interfaces:**
- Consumes: Task 1 types (`Badge`, `Severity`, `severityRank`, `severityIcon`, `fieldPenalty`, `SkipPenaltyMax`); Task 2 `selectForm`; `profile` types + `IsTypeDrift`.
- Produces: `fieldBadges(fp profile.FieldProfile, form ChartForm) []Badge` (sorted, never empty — Clean fallback); `fileBadges(res profile.ProfileResult) []Badge`; `worstSeverity([]Badge) Severity`; `healthScore(cards []FieldCard, records, skipped int) (score int, grade string, sev Severity)`.

- [ ] **Step 1: Write the failing tests** per design §5.1 + §8: each field trigger fires with the right severity (all_null/critical, type_drift/serious, null bands serious+warning, high_cardinality/warning via form, constant/warning, clean/good fallback); badge sort order; `healthScore` all-clean=100, all-critical=0, and a mixed case with a skip ratio asserting the exact integer score + grade band boundary (>=90 Excellent, 75/50/25 boundaries). Full assertions.

- [ ] **Step 2: Run to verify fail.** -> FAIL.

- [ ] **Step 3: Implement** `internal/visual/health.go` per design §5.1 exactly (trigger table in order, mutually-exclusive null bands, file-level badges, `fieldPenalty` average with bounded `SkipPenaltyMax` skip penalty, grade bands).

- [ ] **Step 4: Run to verify pass.** -> PASS.

- [ ] **Step 5: gofmt + commit**

```bash
gofmt -w internal/visual/health.go internal/visual/health_test.go
git add internal/visual/health.go internal/visual/health_test.go
git commit -m "feat(visual): health badges and score"
```

---

### Task 7: FromProfile assembly + golden tests

**Files:**
- Create: `internal/visual/visual.go` (replace the `FromProfile` stub from Task 1)
- Test: `internal/visual/visual_test.go`
- Test fixtures: `internal/visual/testdata/*.golden` (created by the test on first run via a `-update` flag, then committed)

**Interfaces:**
- Consumes: all prior tasks (`selectForm`, `displayHistogram`, `typeMix`, `meter`, stats, `sparkFrom*`, `categorical`, `highCard`, `arrayBreakdown`, `fieldBadges`, `fileBadges`, `healthScore`, formatting helpers).
- Produces: `func FromProfile(res profile.ProfileResult, opts Options) VisualModel` (fills the stub); internal `buildCard(fp, index) FieldCard`, `buildSummary`, `buildKPIs`, `displayName(path string) string`.

- [ ] **Step 1: Write the failing golden test** with a `-update` flag pattern (standard Go golden test): build a `profile.ProfileResult` fixture in-code covering the field kinds from design §8 (numeric-continuous, discrete-numeric, enum string, high-card string, mixed, all-null, bool, array container + `[]` element), run `FromProfile`, `json.MarshalIndent` the result, and compare to `testdata/fromprofile.golden`. Also a dual-consumer assertion test: every `FieldCard` has non-nil `TypeMix`, non-zero-value `Meter`, non-nil `Stats`, non-empty `Badges`.

```go
package visual

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

var update = flag.Bool("update", false, "update golden files")

func goldenCheck(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run -update first): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch for %s; run: go test ./internal/visual -run %s -update", name, t.Name())
	}
}

func TestFromProfileGolden(t *testing.T) {
	res := sampleProfile() // build the multi-kind fixture (defined in this file)
	vm := FromProfile(res, Options{Name: "sample.ndjson"})
	b, err := json.MarshalIndent(vm, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	goldenCheck(t, "fromprofile.golden", b)
}

func TestFromProfileDualConsumer(t *testing.T) {
	vm := FromProfile(sampleProfile(), Options{Name: "sample.ndjson"})
	for _, c := range vm.Fields {
		if c.TypeMix == nil || c.Stats == nil || len(c.Badges) == 0 {
			t.Errorf("card %q missing base payload: typeMix=%v stats=%v badges=%d", c.Path, c.TypeMix, c.Stats, len(c.Badges))
		}
	}
}
```

Write `sampleProfile()` in the test file building the fixture fields explicitly.

- [ ] **Step 2: Run to verify fail.** `go test ./internal/visual/ -run TestFromProfile -v` -> FAIL (`FromProfile` panics "not implemented" / golden missing).

- [ ] **Step 3: Implement** `internal/visual/visual.go`: `FromProfile` builds the field index (`map[string]profile.FieldProfile` over `res.Fields`), then one `buildCard` per field (in order), then `buildSummary` + `buildKPIs` + collects+sorts all badges. `buildCard` calls `selectForm`, always sets `TypeMix/Meter/Stats/Badges/Status`, and sets exactly the one hero payload for the form (`Histogram` via `displayHistogram`, `Categorical` via `categorical`, `HighCard` via `highCard`, `Array` via `arrayBreakdown`, plus `Sparkline` for histogram/categorical). `displayName` per design §2. Resolve `Summary.Format` from `opts.Format` or `deriveFormat(opts.Name | res.Source)`.

- [ ] **Step 4: Generate goldens + run to verify pass.**

Run: `go test ./internal/visual/ -run TestFromProfile -update` then `go test ./internal/visual/ -run TestFromProfile -v`
Expected: second run PASS. Manually inspect `testdata/fromprofile.golden` for sanity (correct forms per field, fracs in 0..1, health score plausible) before committing.

- [ ] **Step 5: gofmt + commit**

```bash
gofmt -w internal/visual/visual.go internal/visual/visual_test.go
git add internal/visual/visual.go internal/visual/visual_test.go internal/visual/testdata/fromprofile.golden
git commit -m "feat(visual): FromProfile assembly with golden coverage"
```

---

### Task 8: FromDiff + diff-derived badges + golden tests

**Files:**
- Create: `internal/visual/diff.go` (replace the `FromDiff` stub from Task 1)
- Test: `internal/visual/diff_test.go`
- Test fixtures: `internal/visual/testdata/fromdiff*.golden`

**Interfaces:**
- Consumes: Task 1 types + helpers; `diff.DiffResult`, `diff.Change`, `diff.Detail`, `diff.ChangeKind` (`Added`/`Removed`/`Changed`), `diff.Reason` (`ReasonType`).
- Produces: `func FromDiff(d diff.DiffResult) DiffVisualModel` (fills the stub); internal `diffBadges(d diff.DiffResult) []Badge`.

- [ ] **Step 1: Write the failing tests** per design §6 + §8: a golden test over a `diff.DiffResult` fixture exercising added/removed/changed groups (fixed order, empty groups omitted), a breaking change -> `SevCritical` row, verdict selection, KPI tiles, caveats passthrough; and targeted tests for `diffBadges`: `field_removed` (Kind==Removed && Breaking) and `type_narrowing` (a `Detail{Reason:ReasonType,Breaking}` with `New ⊊ Old` by token-set) both produce critical badges; a widening type change (`Old ⊊ New`) produces NO `type_narrowing` badge.

```go
func TestDiffBadgesTypeNarrowing(t *testing.T) {
	d := diff.DiffResult{Changes: []diff.Change{{
		Path: "id", Kind: diff.Changed, Breaking: true,
		Details: []diff.Detail{{Reason: diff.ReasonType, Breaking: true, Old: "number,string", New: "number"}},
	}}}
	bs := diffBadges(d)
	found := false
	for _, b := range bs {
		if b.Code == "type_narrowing" && b.Severity == SevCritical {
			found = true
		}
	}
	if !found {
		t.Errorf("expected type_narrowing critical badge, got %+v", bs)
	}
}

func TestDiffBadgesWideningNoBadge(t *testing.T) {
	d := diff.DiffResult{Changes: []diff.Change{{
		Path: "id", Kind: diff.Changed, Breaking: true,
		Details: []diff.Detail{{Reason: diff.ReasonType, Breaking: true, Old: "number", New: "number,string"}},
	}}}
	for _, b := range diffBadges(d) {
		if b.Code == "type_narrowing" {
			t.Errorf("widening must not yield type_narrowing: %+v", b)
		}
	}
}
```

Plus `TestFromDiffGolden` using the same `goldenCheck`/`-update` helper (shared from Task 7's test file, same package).

- [ ] **Step 2: Run to verify fail.** -> FAIL.

- [ ] **Step 3: Implement** `internal/visual/diff.go` per design §6 + §6.1: KPI tiles, verdict, group partition (fixed order, omit empty), row+detail mapping (`"—"` for empty old/new, severity rules), and `diffBadges` (`field_removed`; `type_narrowing` via strict-subset token-set comparison of `Detail.Old`/`Detail.New` split on `,`). Sort badges by (severity desc, path asc, code asc).

- [ ] **Step 4: Generate goldens + run to verify pass + full package suite.**

Run: `go test ./internal/visual/ -run TestFromDiff -update` then `go test ./internal/visual/ -v`
Expected: full `internal/visual` suite PASS. Inspect `testdata/fromdiff*.golden`.

- [ ] **Step 5: gofmt + commit**

```bash
gofmt -w internal/visual/diff.go internal/visual/diff_test.go
git add internal/visual/diff.go internal/visual/diff_test.go internal/visual/testdata/fromdiff.golden
git commit -m "feat(visual): FromDiff mapping with diff-derived critical badges"
```

---

## Self-Review

**Spec coverage:** Design §1/§2/§3-consts/§5.1/§6 types -> Task 1. §3 selection + enum -> Task 2. §4 histogram -> Task 3. §5 type-mix/meter/stats/sparkline -> Task 4. §2.2 hero payloads -> Task 5. §5.1 badges+health -> Task 6. §2 assembly (Summary/KPIs/FieldCard) + §8 goldens -> Task 7. §6 diff -> Task 8. §7 helpers -> Task 1. §9 flags: (1) `Options.Format` in Task 1/7; (2) bool->meter in Task 2; (3) `StrLenBar` range in Task 5; (4) `mean` from centroids in Task 4; (5) array link in Task 5/7.

**Placeholder scan:** The one intentional deferral is the stubbed `FromProfile`/`FromDiff` bodies created in Task 1 (`panic("not implemented")`), explicitly filled in Tasks 7/8 — this is a compile-ordering device, not a placeholder gap. All test bodies carry concrete assertions. No TBDs.

**Type consistency:** `selectForm` returns `(ChartForm, string)` in Task 2 and is consumed that way in Tasks 6/7. `displayHistogram(fp) Histogram` (Task 3) matches its call in Task 7. Hero builders return pointers (`*Categorical` etc., Task 5) matching the optional `FieldCard` fields (Task 1). `healthScore(cards, records, skipped)` (Task 6) matches its call in Task 7's `buildSummary`. `fptr`/`num`/`hbins`/`goldenCheck` test helpers are defined once (Tasks 2/2/3/7) and reused across the shared `visual` test package.

**Ordering note:** shared test helpers (`fptr` in Task 2, `hbins` in Task 3, `num` in Task 2, `goldenCheck`/`update` in Task 7) live in the single `package visual` test binary; tasks run in order so each helper exists before later tasks reference it. A reviewer running one task's tests in isolation on a partial tree may need earlier tasks present — expected under sequential execution.
