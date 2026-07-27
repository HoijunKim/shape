# P1: Profiler Streaming Histogram Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a bounded streaming numeric histogram (with approximate quantiles) to the profiler core so numeric fields carry distribution data for the visual dashboard, without breaking the single-pass / bounded-memory identity or the existing CLI output.

**Architecture:** A new `numHistogram` sketch in `internal/profile` (same family as the existing `hll` and `spaceSaving` sketches) keeps at most `histMaxBins` (centroid, count) bins per numeric field, merging the closest adjacent pair when the cap is exceeded (Ben-Haim & Tom-Tov). It exposes bins and interpolated quantiles. `fieldAccumulator` feeds every numeric observation into it and surfaces `Histogram`, `Median`, and `P95` on the exported `FieldProfile`. Consumers (`internal/visual`, GUI) read those fields in later phases.

**Tech Stack:** Go, standard library only (`sort`, `math`).

## Global Constraints

- Single streaming pass, bounded memory - never buffer the whole input; per-field histogram memory is capped at `histMaxBins` bins. (Copied from spec §2/§3.)
- cgo-free; Go standard library only for sketches - no third-party deps. (Spec §1.)
- Deterministic output - no per-process seed, no reliance on Go map-iteration order in results. Tie-breaks are explicit. (Matches existing `hll`/`spaceSaving` conventions.)
- CLI output stays exactly as it is - do NOT modify `internal/render/json.go` or `internal/render/table.go`. Those use their own view structs, so adding fields to `FieldProfile` does not change CLI output. (Spec §1: "CLI stays exactly as it is.")
- Follow existing package conventions: unexported sketch struct, `newXxx` constructor, package `profile`, table-with-tolerance tests using `t.Errorf`/`t.Fatalf`.

---

### Task 1: `numHistogram` sketch - add, bins, bounded merge

**Files:**
- Create: `internal/profile/histogram.go`
- Test: `internal/profile/histogram_test.go`

**Interfaces:**
- Consumes: nothing (leaf sketch).
- Produces:
  - `type HistBin struct { Value float64 \`json:"value"\`; Count int \`json:"count"\` }`
  - `const histMaxBins = 64`
  - `func newNumHistogram(maxBins int) *numHistogram`
  - `func (h *numHistogram) add(x float64)`
  - `func (h *numHistogram) snapshot() []HistBin`  (copy, sorted by Value asc)
  - field `total int` on `numHistogram` (observation count)

- [ ] **Step 1: Write the failing tests**

Create `internal/profile/histogram_test.go`:

```go
package profile

import "testing"

func TestHistogramExactBelowCap(t *testing.T) {
	h := newNumHistogram(64)
	for _, v := range []float64{30, 10, 20, 10} {
		h.add(v)
	}
	bins := h.snapshot()
	// 3 distinct values -> 3 bins, no merge, sorted ascending.
	if len(bins) != 3 {
		t.Fatalf("bins = %d, want 3", len(bins))
	}
	if bins[0].Value != 10 || bins[0].Count != 2 {
		t.Errorf("bins[0] = %+v, want {10, 2}", bins[0])
	}
	if bins[1].Value != 20 || bins[1].Count != 1 {
		t.Errorf("bins[1] = %+v, want {20, 1}", bins[1])
	}
	if bins[2].Value != 30 || bins[2].Count != 1 {
		t.Errorf("bins[2] = %+v, want {30, 1}", bins[2])
	}
	if h.total != 4 {
		t.Errorf("total = %d, want 4", h.total)
	}
}

func TestHistogramBoundedBins(t *testing.T) {
	h := newNumHistogram(64)
	for i := 0; i < 10000; i++ {
		h.add(float64(i))
	}
	bins := h.snapshot()
	if len(bins) > 64 {
		t.Fatalf("bins = %d, want <= 64 (bounded memory)", len(bins))
	}
	if h.total != 10000 {
		t.Errorf("total = %d, want 10000", h.total)
	}
	// total count across bins must equal observations (merging preserves mass).
	sum := 0
	for _, b := range bins {
		sum += b.Count
	}
	if sum != 10000 {
		t.Errorf("sum of bin counts = %d, want 10000", sum)
	}
	// bins stay sorted ascending.
	for i := 1; i < len(bins); i++ {
		if bins[i].Value < bins[i-1].Value {
			t.Fatalf("bins not sorted at %d: %v", i, bins)
		}
	}
}

func TestHistogramSnapshotIsCopy(t *testing.T) {
	h := newNumHistogram(64)
	h.add(1)
	snap := h.snapshot()
	snap[0].Count = 999
	if h.snapshot()[0].Count != 1 {
		t.Error("snapshot must return a copy, not the live slice")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/profile/ -run TestHistogram -v`
Expected: FAIL - `undefined: newNumHistogram` (build error).

- [ ] **Step 3: Write minimal implementation**

Create `internal/profile/histogram.go`:

```go
package profile

import "sort"

// histMaxBins bounds the streaming histogram's bin count per numeric field.
// Like hll and spaceSaving, this keeps a numeric field's memory bounded no
// matter how many distinct values stream through.
const histMaxBins = 64

// HistBin is one histogram bin: a centroid value and how many observations
// merged into it. Bins are always kept sorted by Value ascending.
type HistBin struct {
	Value float64 `json:"value"`
	Count int     `json:"count"`
}

// numHistogram is a bounded streaming histogram (Ben-Haim & Tom-Tov, "A
// Streaming Parallel Decision Tree Algorithm"). It keeps at most maxBins
// (centroid, count) bins sorted by centroid; when an insertion would exceed the
// cap it merges the adjacent pair with the smallest gap into their
// count-weighted mean. Single pass, bounded memory - the same family as hll and
// spaceSaving.
type numHistogram struct {
	maxBins int
	bins    []HistBin // sorted by Value asc
	total   int
}

func newNumHistogram(maxBins int) *numHistogram {
	return &numHistogram{maxBins: maxBins, bins: make([]HistBin, 0, maxBins+1)}
}

// add records one numeric observation.
func (h *numHistogram) add(x float64) {
	h.total++
	i := sort.Search(len(h.bins), func(j int) bool { return h.bins[j].Value >= x })
	if i < len(h.bins) && h.bins[i].Value == x {
		h.bins[i].Count++
		return
	}
	h.bins = append(h.bins, HistBin{})
	copy(h.bins[i+1:], h.bins[i:])
	h.bins[i] = HistBin{Value: x, Count: 1}
	if len(h.bins) > h.maxBins {
		h.mergeClosest()
	}
}

// mergeClosest merges the adjacent bin pair with the smallest centroid gap,
// breaking ties toward the leftmost pair so merging is deterministic.
func (h *numHistogram) mergeClosest() {
	best := 0
	bestGap := h.bins[1].Value - h.bins[0].Value
	for i := 1; i < len(h.bins)-1; i++ {
		if gap := h.bins[i+1].Value - h.bins[i].Value; gap < bestGap {
			bestGap = gap
			best = i
		}
	}
	a, b := h.bins[best], h.bins[best+1]
	c := a.Count + b.Count
	h.bins[best] = HistBin{
		Value: (a.Value*float64(a.Count) + b.Value*float64(b.Count)) / float64(c),
		Count: c,
	}
	h.bins = append(h.bins[:best+1], h.bins[best+2:]...)
}

// snapshot returns a copy of the current bins (sorted by Value asc).
func (h *numHistogram) snapshot() []HistBin {
	out := make([]HistBin, len(h.bins))
	copy(out, h.bins)
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/profile/ -run TestHistogram -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/profile/histogram.go internal/profile/histogram_test.go
git commit -m "feat(profile): bounded streaming numeric histogram sketch"
```

---

### Task 2: Approximate quantiles on `numHistogram`

**Files:**
- Modify: `internal/profile/histogram.go` (add `math` import + `quantile` method)
- Test: `internal/profile/histogram_test.go` (add quantile tests)

**Interfaces:**
- Consumes: `numHistogram` from Task 1.
- Produces: `func (h *numHistogram) quantile(q float64) float64` - returns the approximate q-quantile (q in 0..1); `math.NaN()` when empty; clamps q<=0 to the min centroid and q>=1 to the max centroid.

- [ ] **Step 1: Write the failing tests**

Append to `internal/profile/histogram_test.go`:

```go
import "math" // add to the existing import block at the top of the file

func TestHistogramQuantileEmpty(t *testing.T) {
	h := newNumHistogram(64)
	if q := h.quantile(0.5); !math.IsNaN(q) {
		t.Errorf("quantile of empty = %v, want NaN", q)
	}
}

func TestHistogramQuantileSingleValue(t *testing.T) {
	h := newNumHistogram(64)
	for i := 0; i < 5; i++ {
		h.add(42)
	}
	if q := h.quantile(0.5); q != 42 {
		t.Errorf("median = %v, want 42", q)
	}
	if q := h.quantile(0.95); q != 42 {
		t.Errorf("p95 = %v, want 42", q)
	}
}

func TestHistogramQuantileClamp(t *testing.T) {
	h := newNumHistogram(64)
	for _, v := range []float64{10, 20, 30} {
		h.add(v)
	}
	if q := h.quantile(0); q != 10 {
		t.Errorf("q(0) = %v, want 10 (min)", q)
	}
	if q := h.quantile(1); q != 30 {
		t.Errorf("q(1) = %v, want 30 (max)", q)
	}
}

func TestHistogramQuantileUniformAccuracy(t *testing.T) {
	h := newNumHistogram(64)
	for i := 0; i < 10000; i++ {
		h.add(float64(i)) // uniform 0..9999
	}
	med := h.quantile(0.5)
	if med < 4900 || med > 5100 { // within ~1% of true median 4999.5
		t.Errorf("median = %v, want ~4999.5 (+/-100)", med)
	}
	p95 := h.quantile(0.95)
	if p95 < 9400 || p95 > 9600 { // within ~1% of true p95 ~9499
		t.Errorf("p95 = %v, want ~9499 (+/-100)", p95)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/profile/ -run TestHistogramQuantile -v`
Expected: FAIL - `h.quantile undefined` (build error).

- [ ] **Step 3: Write minimal implementation**

In `internal/profile/histogram.go`, change the import line

```go
import "sort"
```

to

```go
import (
	"math"
	"sort"
)
```

and append this method to the file:

```go
// quantile returns an approximate q-quantile (q in 0..1) of the observed
// values, interpolating linearly across the cumulative counts positioned at bin
// centroids (each bin's mass is treated as centered on its centroid). Returns
// NaN when empty; clamps q<=0 to the smallest centroid and q>=1 to the largest.
func (h *numHistogram) quantile(q float64) float64 {
	if len(h.bins) == 0 {
		return math.NaN()
	}
	if len(h.bins) == 1 || q <= 0 {
		return h.bins[0].Value
	}
	if q >= 1 {
		return h.bins[len(h.bins)-1].Value
	}
	target := q * float64(h.total)
	cum := 0.0       // running count strictly before the current bin
	prevCenter := 0.0
	prevVal := h.bins[0].Value
	for i, b := range h.bins {
		center := cum + float64(b.Count)/2
		if center >= target {
			if i == 0 {
				return b.Value
			}
			frac := (target - prevCenter) / (center - prevCenter)
			return prevVal + frac*(b.Value-prevVal)
		}
		prevCenter = center
		prevVal = b.Value
		cum += float64(b.Count)
	}
	return h.bins[len(h.bins)-1].Value
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/profile/ -run TestHistogram -v`
Expected: PASS (all histogram tests, including the 4 new quantile tests).

- [ ] **Step 5: Commit**

```bash
git add internal/profile/histogram.go internal/profile/histogram_test.go
git commit -m "feat(profile): approximate quantiles from streaming histogram"
```

---

### Task 3: Wire histogram into the accumulator and `FieldProfile`

**Files:**
- Modify: `internal/profile/accumulator.go` (struct field, `AddValue`, `Result`, `FieldProfile`)
- Test: `internal/profile/accumulator_test.go` (add integration test)

**Interfaces:**
- Consumes: `numHistogram`, `newNumHistogram`, `histMaxBins`, `HistBin` (Tasks 1–2).
- Produces (on the exported `FieldProfile`):
  - `Histogram []HistBin` - bins for numeric fields (nil for non-numeric).
  - `Median *float64` - approximate 0.5 quantile (nil for non-numeric).
  - `P95 *float64` - approximate 0.95 quantile (nil for non-numeric).

- [ ] **Step 1: Write the failing test**

Append to `internal/profile/accumulator_test.go`:

```go
func TestAccumulatorNumericHistogram(t *testing.T) {
	a := newFieldAccumulator("n", DefaultExactCap)
	for i := 0; i < 1000; i++ {
		a.AddValue(Observation{Path: "n", Kind: KindInt, Num: float64(i)})
		a.MarkPresent()
	}
	fp := a.Result(1000)

	if len(fp.Histogram) == 0 {
		t.Fatal("Histogram is empty for a numeric field")
	}
	if len(fp.Histogram) > histMaxBins {
		t.Errorf("Histogram has %d bins, want <= %d", len(fp.Histogram), histMaxBins)
	}
	if fp.Median == nil || *fp.Median < 450 || *fp.Median > 550 {
		t.Errorf("Median = %v, want ~499.5 (+/-50)", fp.Median)
	}
	if fp.P95 == nil || *fp.P95 < 900 || *fp.P95 > 990 {
		t.Errorf("P95 = %v, want ~949 (+/-50)", fp.P95)
	}
}

func TestAccumulatorNonNumericHasNoHistogram(t *testing.T) {
	a := newFieldAccumulator("s", DefaultExactCap)
	a.AddValue(Observation{Path: "s", Kind: KindString, Str: "x"})
	a.MarkPresent()
	fp := a.Result(1)
	if fp.Histogram != nil || fp.Median != nil || fp.P95 != nil {
		t.Errorf("string field got histogram data: bins=%v median=%v p95=%v",
			fp.Histogram, fp.Median, fp.P95)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/profile/ -run TestAccumulatorNumericHistogram -v`
Expected: FAIL - `fp.Histogram undefined` (build error).

- [ ] **Step 3: Write minimal implementation**

In `internal/profile/accumulator.go`:

**3a.** Add three fields to `FieldProfile` (place them right after the `Max` line):

```go
	Min, Max      *float64
	Histogram     []HistBin
	Median        *float64
	P95           *float64
```

**3b.** Add the `hist` field to `fieldAccumulator` (place it after the `min, max float64` line):

```go
	haveNum    bool
	min, max   float64
	hist       *numHistogram
```

**3c.** In `AddValue`, extend the `KindInt, KindFloat` case. Replace:

```go
		a.haveNum = true
		a.addCount(numKey(o.Num))
```

with:

```go
		a.haveNum = true
		if a.hist == nil {
			a.hist = newNumHistogram(histMaxBins)
		}
		a.hist.add(o.Num)
		a.addCount(numKey(o.Num))
```

**3d.** In `Result`, populate the new fields. Insert this block right after the existing `if a.haveNum { ... }` block (which sets `fp.Min`/`fp.Max`):

```go
	if a.hist != nil && a.hist.total > 0 {
		fp.Histogram = a.hist.snapshot()
		med := a.hist.quantile(0.5)
		p95 := a.hist.quantile(0.95)
		fp.Median, fp.P95 = &med, &p95
	}
```

- [ ] **Step 4: Run the full package test suite to verify pass + no regressions**

Run: `go test ./internal/profile/ -v`
Expected: PASS - the two new accumulator tests pass and every pre-existing profile test still passes.

- [ ] **Step 5: Verify the CLI output is unchanged**

Run: `go build -o shape.exe . && ./shape.exe profile --json internal/cmd/testdata/sample.ndjson`
Expected: identical JSON to before this plan - no `histogram`, `median`, or `p95` keys (the `--json` view struct in `internal/render/json.go` was not touched).

- [ ] **Step 6: Commit**

```bash
git add internal/profile/accumulator.go internal/profile/accumulator_test.go
git commit -m "feat(profile): surface histogram, median, p95 on FieldProfile"
```

---

## Self-Review

**Spec coverage (P1 scope only):** Spec §3 "streaming numeric histogram sketch" → Tasks 1–2. Spec §3 "surfaced on FieldProfile … alongside existing numeric min/max" → Task 3. Spec §11 "histogram accuracy against known distributions, bounded memory, single-pass golden tests" → `TestHistogramExactBelowCap`, `TestHistogramBoundedBins`, `TestHistogramQuantileUniformAccuracy`, `TestAccumulatorNumericHistogram`. Spec §1 "CLI stays exactly as it is" → Task 3 Step 5 verification + Global Constraints (render package untouched). Consumers (`internal/visual`, GUI) are P2/P3, out of this plan's scope.

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every run step shows the exact command and expected result.

**Type consistency:** `HistBin{Value float64, Count int}`, `newNumHistogram(int) *numHistogram`, `add(float64)`, `snapshot() []HistBin`, `quantile(float64) float64`, `total int`, and `histMaxBins` are named identically across Tasks 1–3. `FieldProfile` new fields `Histogram []HistBin`, `Median *float64`, `P95 *float64` match their reads in Task 3's tests.

**Note on determinism:** streaming-histogram merges are order-sensitive in general, so accuracy tests use tolerance bands; the exact-value assertions (`TestHistogramExactBelowCap`, `TestHistogramQuantileClamp`) only use inputs whose distinct count stays under the bin cap, where no merging occurs and results are exact.
