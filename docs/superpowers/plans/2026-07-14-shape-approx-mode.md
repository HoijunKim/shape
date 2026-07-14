# Shape Plan 4: bounded-memory approximate mode (HyperLogLog + Space-Saving)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-field exact-capped value map with a two-phase accumulator - exact up to `exactCap`, then promoted to a HyperLogLog (distinct-count estimate) plus a Space-Saving sketch (top-K heavy hitters) - so shape profiles arbitrarily high-cardinality fields in bounded memory while keeping small/medium fields exact and sound.

**Architecture:** Two new deterministic sketches live in package `internal/profile`: `hll` (HyperLogLog, p=12, fixed-seed FNV-1a+fmix64 hash) and `spaceSaving` (512 counters). `fieldAccumulator` tracks values in an exact map until a new key would exceed `exactCap` (16384), then promotes: it seeds both sketches from the map (HLL order-independently, Space-Saving in sorted order) and frees the map. `Result()` reports `DistinctExact=false` + the HLL estimate (clamped `>= len(TopValues)`) + Space-Saving top-10 for promoted fields; exact behavior is byte-identical for non-promoted fields. Presence, type distribution, null rate, numeric min/max, and string-length min/max stay exact at all cardinalities.

**Tech Stack:** Go stdlib only (`hash/fnv`, `math`, `math/bits`, `sort`), standard `testing`.

## Global Constraints

- Module `github.com/hoijun-kim/shape`. Go floor `go 1.23`.
- Sketches live in package `internal/profile` (alongside the accumulator). No new external dependency. NEVER use `hash/maphash` (per-process random seed breaks determinism); the hash is fixed-seed FNV-1a + a murmur3 fmix64 finalizer.
- Plain ASCII hyphen `-` only. Never em/en dash or middle dot.
- Commits: Conventional Commits, NO co-author trailer.
- TDD: failing test first, then minimal implementation, then green, then commit.
- Invariant that MUST hold: `DefaultExactCap (16384) >= 2.5 * 2^hllPrecision (10240)` so HLL only estimates cardinalities in its unbiased regime (no small-range correction), and `DefaultExactCap >> 10` so no small enum (<=10 distinct) is ever promoted.
- MUST stay exact/unchanged for every field with distinct cardinality `<= exactCap`: DistinctCount, DistinctExact=true, TopValues (count desc, value asc), presence, TypeDist, NullRate, numeric min/max, string-length min/max, and deterministic sorted output. Existing profile/schema/diff tests must pass unchanged.
- Output MUST be deterministic (byte-identical) for a given input across runs and machines. Space-Saving is order-sensitive in general; determinism holds because shape reads a fixed file in fixed order and seeds Space-Saving in sorted order.

---

### Task 1: HyperLogLog sketch

**Files:**
- Create: `internal/profile/hll.go`
- Test: `internal/profile/hll_test.go`

**Interfaces produced:** `func hash64(s string) uint64`; `type hll struct{...}`; `func newHLL(p uint) *hll`; `(*hll).add(key string)`; `(*hll).estimate() int`.

- [ ] **Step 1: Write the failing test**

`internal/profile/hll_test.go`:

```go
package profile

import (
	"fmt"
	"math"
	"testing"
)

func TestHash64Deterministic(t *testing.T) {
	if hash64("shape") != hash64("shape") {
		t.Error("hash64 must be deterministic for the same input")
	}
	if hash64("a") == hash64("b") {
		t.Error("hash64 must distinguish different inputs")
	}
}

func TestHLLAccuracy(t *testing.T) {
	// n well above 2.5*2^12 (10240) so the raw estimator is in its unbiased range.
	for _, n := range []int{20000, 200000} {
		h := newHLL(12)
		for i := 0; i < n; i++ {
			h.add(fmt.Sprintf("value-%d", i))
		}
		est := h.estimate()
		rel := math.Abs(float64(est-n)) / float64(n)
		if rel > 0.05 { // ~3x the 1.6% standard error, generous
			t.Errorf("n=%d: estimate=%d rel-error=%.4f > 0.05", n, est, rel)
		}
	}
}

func TestHLLSameInputSameRegisters(t *testing.T) {
	build := func() *hll {
		h := newHLL(12)
		for i := 0; i < 5000; i++ {
			h.add(fmt.Sprintf("k-%d", i))
		}
		return h
	}
	a, b := build(), build()
	for i := range a.regs {
		if a.regs[i] != b.regs[i] {
			t.Fatalf("register %d differs: %d vs %d (non-deterministic)", i, a.regs[i], b.regs[i])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/profile/ -run 'TestHash64|TestHLL' -v`
Expected: FAIL (build error: undefined symbols).

- [ ] **Step 3: Write minimal implementation**

`internal/profile/hll.go`:

```go
package profile

import (
	"hash/fnv"
	"math"
	"math/bits"
)

// hash64 is a fixed-seed deterministic 64-bit hash: FNV-1a plus a murmur3
// fmix64 finalizer that fixes FNV's weak low-bit distribution. It uses no
// per-process seed (hash/maphash is deliberately avoided) so output is
// reproducible across runs and machines.
func hash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	x := h.Sum64()
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

// hll is a HyperLogLog cardinality estimator with dense registers. It is only
// used for cardinalities above the accumulator's exactCap (>= 2.5*2^p), so no
// small-range/linear-counting correction is needed.
type hll struct {
	p    uint
	regs []uint8
}

func newHLL(p uint) *hll {
	return &hll{p: p, regs: make([]uint8, 1<<p)}
}

func (h *hll) add(key string) {
	x := hash64(key)
	idx := x >> (64 - h.p)             // top p bits -> register index
	w := (x << h.p) | (1 << (h.p - 1)) // remaining bits; guard bit bounds the rank
	rank := uint8(bits.LeadingZeros64(w)) + 1
	if rank > h.regs[idx] {
		h.regs[idx] = rank
	}
}

func hllAlpha(m float64) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/m)
	}
}

func (h *hll) estimate() int {
	m := float64(len(h.regs))
	sum := 0.0
	for _, r := range h.regs {
		sum += math.Ldexp(1, -int(r)) // 2^-r
	}
	return int(hllAlpha(m)*m*m/sum + 0.5)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/profile/ -run 'TestHash64|TestHLL' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/profile/hll.go internal/profile/hll_test.go
git commit -m "feat: deterministic HyperLogLog cardinality sketch"
```

---

### Task 2: Space-Saving top-K sketch

**Files:**
- Create: `internal/profile/spacesaving.go`
- Test: `internal/profile/spacesaving_test.go`

**Interfaces produced:** `type ssCounter struct{...}`; `type spaceSaving struct{...}`; `func newSpaceSaving(cap int) *spaceSaving`; `(*spaceSaving).seed(value string, count int)`; `(*spaceSaving).add(key string)`; `(*spaceSaving).min() *ssCounter`; `(*spaceSaving).top(k int) []ValueCount`. Consumes `ValueCount` (existing in accumulator.go).

- [ ] **Step 1: Write the failing test**

`internal/profile/spacesaving_test.go`:

```go
package profile

import (
	"fmt"
	"testing"
)

func TestSpaceSavingHeavyHitters(t *testing.T) {
	s := newSpaceSaving(64)
	for i := 0; i < 1000; i++ {
		s.add("heavy-a")
		s.add("heavy-b")
	}
	for i := 0; i < 500; i++ {
		s.add("heavy-c")
	}
	for i := 0; i < 5000; i++ { // long unique tail
		s.add(fmt.Sprintf("noise-%d", i))
	}
	got := map[string]bool{}
	for _, v := range s.top(3) {
		got[v.Value] = true
	}
	for _, h := range []string{"heavy-a", "heavy-b", "heavy-c"} {
		if !got[h] {
			t.Errorf("heavy hitter %q missing from top-3: %v", h, s.top(3))
		}
	}
}

func TestSpaceSavingDeterministicEviction(t *testing.T) {
	s := newSpaceSaving(2)
	s.add("b") // count 1
	s.add("a") // count 1, full; a and b both count 1
	s.add("c") // must evict min by (count asc, value asc) -> "a"
	if _, ok := s.counters["a"]; ok {
		t.Error("min counter (smallest value at min count) 'a' should have been evicted")
	}
	if _, ok := s.counters["c"]; !ok {
		t.Error("new key 'c' should be present after eviction")
	}
}

func TestSpaceSavingTopOrdering(t *testing.T) {
	s := newSpaceSaving(8)
	s.add("x")
	s.add("x")
	s.add("y")
	top := s.top(2)
	if top[0].Value != "x" || top[0].Count != 2 || top[1].Value != "y" {
		t.Errorf("top ordering wrong: %v", top)
	}
}

func TestSpaceSavingSeed(t *testing.T) {
	s := newSpaceSaving(8)
	s.seed("v", 42)
	if s.counters["v"].count != 42 || s.counters["v"].err != 0 {
		t.Errorf("seed should set exact count and zero error, got %+v", s.counters["v"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/profile/ -run TestSpaceSaving -v`
Expected: FAIL (build error: undefined symbols).

- [ ] **Step 3: Write minimal implementation**

`internal/profile/spacesaving.go`:

```go
package profile

import "sort"

// ssCounter is one monitored value. err is the maximum overestimate of count.
type ssCounter struct {
	value string
	count int
	err   int
}

// spaceSaving is a bounded top-K heavy-hitter sketch (Metwally et al.). Any
// value whose true frequency exceeds N/cap is guaranteed to be retained.
type spaceSaving struct {
	cap      int
	counters map[string]*ssCounter
}

func newSpaceSaving(cap int) *spaceSaving {
	return &spaceSaving{cap: cap, counters: make(map[string]*ssCounter, cap)}
}

// seed inserts a value with a known exact count (used at promotion, err=0).
func (s *spaceSaving) seed(value string, count int) {
	s.counters[value] = &ssCounter{value: value, count: count}
}

func (s *spaceSaving) add(key string) {
	if c, ok := s.counters[key]; ok {
		c.count++
		return
	}
	if len(s.counters) < s.cap {
		s.counters[key] = &ssCounter{value: key, count: 1}
		return
	}
	m := s.min()
	delete(s.counters, m.value)
	m.value, m.err, m.count = key, m.count, m.count+1
	s.counters[key] = m
}

// min returns the counter with the smallest count, breaking ties by smallest
// value string so eviction is a deterministic pure function of counter state.
func (s *spaceSaving) min() *ssCounter {
	var m *ssCounter
	for _, c := range s.counters {
		if m == nil || c.count < m.count || (c.count == m.count && c.value < m.value) {
			m = c
		}
	}
	return m
}

// top returns the k highest-count values, sorted count desc then value asc.
func (s *spaceSaving) top(k int) []ValueCount {
	vs := make([]ValueCount, 0, len(s.counters))
	for _, c := range s.counters {
		vs = append(vs, ValueCount{Value: c.value, Count: c.count})
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/profile/ -run TestSpaceSaving -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/profile/spacesaving.go internal/profile/spacesaving_test.go
git commit -m "feat: deterministic Space-Saving top-K sketch"
```

---

### Task 3: Two-phase accumulator + profiler wiring

**Files:**
- Modify: `internal/profile/accumulator.go` (fields, `newFieldAccumulator`, `addCount`, add `promote`, `Result`; add sketch-param consts)
- Modify: `internal/profile/profiler.go` (`DefaultDistinctCap` -> `DefaultExactCap = 16384`)
- Test: `internal/profile/accumulator_test.go` (replace the overflow test; add promotion tests)
- Test: `internal/profile/profiler_test.go` (add a high-cardinality promotion test)

**Interfaces:** `fieldAccumulator` gains `exactCap int; promoted bool; hll *hll; ss *spaceSaving` and drops `distinctCap`/`overflow`. Consts `hllPrecision = 12`, `spaceSavingCap = 512` (in accumulator.go). `DefaultExactCap = 16384` (in profiler.go). `newFieldAccumulator(path string, exactCap int) *fieldAccumulator` (same signature, param renamed).

- [ ] **Step 1: Replace the overflow test and add promotion tests**

In `internal/profile/accumulator_test.go`, DELETE `TestAccumulatorDistinctOverflow` and add (append `"fmt"` to the test file imports):

```go
func TestAccumulatorPromotionBoundary(t *testing.T) {
	a := newFieldAccumulator("s", 8) // exact up to 8 distinct
	for i := 0; i < 8; i++ {
		a.AddValue(Observation{Path: "s", Kind: KindString, Str: fmt.Sprintf("v%d", i)})
		a.MarkPresent()
	}
	fp := a.Result(8)
	if !fp.DistinctExact || fp.DistinctCount != 8 {
		t.Fatalf("at cap: exact=%v count=%d, want true/8", fp.DistinctExact, fp.DistinctCount)
	}
	if fp.StrLenMin == nil || *fp.StrLenMin != 2 { // "v0".."v7" all length 2
		t.Errorf("string length must stay exact, got %v", fp.StrLenMin)
	}
	// a 9th distinct value exceeds the cap -> promote.
	a.AddValue(Observation{Path: "s", Kind: KindString, Str: "v8"})
	a.MarkPresent()
	if a.counts != nil {
		t.Error("exact map must be freed after promotion")
	}
	fp2 := a.Result(9)
	if fp2.DistinctExact {
		t.Error("after exceeding cap the field must be approximate (DistinctExact=false)")
	}
	if fp2.StrLenMin == nil || *fp2.StrLenMin != 2 {
		t.Errorf("string length must stay exact after promotion, got %v", fp2.StrLenMin)
	}
}

func TestAccumulatorPromotedKeepsHeavyHitter(t *testing.T) {
	a := newFieldAccumulator("s", 16)
	for i := 0; i < 100; i++ {
		a.AddValue(Observation{Path: "s", Kind: KindString, Str: "HEAVY"})
		a.MarkPresent()
	}
	for i := 0; i < 200; i++ { // unique tail forces promotion
		a.AddValue(Observation{Path: "s", Kind: KindString, Str: fmt.Sprintf("tail-%d", i)})
		a.MarkPresent()
	}
	fp := a.Result(300)
	if fp.DistinctExact {
		t.Fatal("should have promoted")
	}
	if len(fp.TopValues) == 0 || fp.TopValues[0].Value != "HEAVY" {
		t.Errorf("promoted top value should be HEAVY, got %v", fp.TopValues)
	}
	if fp.DistinctCount < len(fp.TopValues) {
		t.Errorf("DistinctCount %d must not be below len(TopValues) %d", fp.DistinctCount, len(fp.TopValues))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/profile/ -run 'TestAccumulatorPromot' -v`
Expected: FAIL (build error: `newFieldAccumulator` still uses old fields / promotion not implemented). It will not compile until Step 3.

- [ ] **Step 3: Rewrite the accumulator**

In `internal/profile/accumulator.go`:

(a) Add the sketch-param consts near the top (after the imports):

```go
const (
	// hllPrecision sets the HyperLogLog register count (2^12 = 4096 registers,
	// ~1.6% standard error). DefaultExactCap must stay >= 2.5*2^hllPrecision.
	hllPrecision = 12
	// spaceSavingCap bounds the top-K heavy-hitter counters per promoted field.
	spaceSavingCap = 512
)
```

(b) Replace the `distinctCap`/`overflow` fields in the `fieldAccumulator` struct. The struct's value-tracking fields become:

```go
	counts   map[string]int // value key -> count; nil once promoted
	exactCap int
	promoted bool
	hll      *hll
	ss       *spaceSaving
```

(remove the old `distinctCap int` and `overflow bool` lines).

(c) Update `newFieldAccumulator`:

```go
func newFieldAccumulator(path string, exactCap int) *fieldAccumulator {
	return &fieldAccumulator{
		path:       path,
		kindCounts: map[JSONKind]int{},
		counts:     map[string]int{},
		exactCap:   exactCap,
	}
}
```

(d) Replace `addCount` and add `promote`:

```go
func (a *fieldAccumulator) addCount(key string) {
	if a.promoted {
		a.hll.add(key)
		a.ss.add(key)
		return
	}
	if _, ok := a.counts[key]; ok {
		a.counts[key]++
		return
	}
	if len(a.counts) >= a.exactCap { // a new key would exceed the exact cap
		a.promote()
		a.hll.add(key)
		a.ss.add(key)
		return
	}
	a.counts[key] = 1
}

// promote switches from the exact map to bounded sketches, seeded from the map,
// then frees the map. HLL seeding is order-independent (register max); the
// Space-Saving seed uses the sorted (count desc, value asc) entry order so it is
// deterministic despite Go's randomized map iteration.
func (a *fieldAccumulator) promote() {
	a.hll = newHLL(hllPrecision)
	a.ss = newSpaceSaving(spaceSavingCap)
	entries := topValues(a.counts, len(a.counts)) // all entries, sorted
	for _, e := range entries {
		a.hll.add(e.Value)
	}
	for i, e := range entries {
		if i >= spaceSavingCap {
			break
		}
		a.ss.seed(e.Value, e.Count)
	}
	a.promoted = true
	a.counts = nil
}
```

(e) In `Result`, replace the distinct/top-values assembly. The current code builds `FieldProfile{... DistinctCount: len(a.counts), DistinctExact: !a.overflow ...}` and later `fp.TopValues = topValues(a.counts, 10)`. Replace BOTH with a promoted/exact branch. The `FieldProfile` literal becomes (drop `DistinctCount`/`DistinctExact` from the literal):

```go
	fp := FieldProfile{
		Path:         a.path,
		TypeDist:     map[JSONKind]float64{},
		Observations: a.obs,
	}
```

and just before `return fp`, replace the old `fp.TopValues = topValues(a.counts, 10)` line with:

```go
	if a.promoted {
		top := a.ss.top(10)
		est := a.hll.estimate()
		if est < len(top) {
			est = len(top) // never report fewer distinct than the top values shown
		}
		fp.DistinctCount = est
		fp.DistinctExact = false
		fp.TopValues = top
	} else {
		fp.DistinctCount = len(a.counts)
		fp.DistinctExact = true
		fp.TopValues = topValues(a.counts, 10)
	}
```

(Leave the presence, TypeDist, NullRate, numeric min/max, and string-length blocks exactly as they are - they are computed from independent exact fields and must not change.)

(f) In `internal/profile/profiler.go`, rename the cap constant and its use:

```go
// DefaultExactCap is the number of distinct values a field tracks exactly before
// promoting to bounded sketches. It stays above 2.5*2^hllPrecision (10240) so the
// HLL only estimates cardinalities in its unbiased regime, and far above the
// top-K cap of 10 so small enums are never promoted.
const DefaultExactCap = 16384
```

and change `NewProfiler` to use `cap: DefaultExactCap` (replacing the old `DefaultDistinctCap`).

- [ ] **Step 4: Add the profiler promotion test**

In `internal/profile/profiler_test.go`, add (append `"fmt"` and `"math"` to its imports):

```go
func TestProfilerPromotesHighCardinality(t *testing.T) {
	p := NewProfiler()
	const n = 20000 // > DefaultExactCap (16384)
	for i := 0; i < n; i++ {
		p.AddRecord(map[string]any{"id": fmt.Sprintf("id-%d", i), "kind": "x"})
	}
	res := p.Result()
	id, _ := fieldByPath(res, "id")
	if id.DistinctExact {
		t.Errorf("id (%d distinct > exactCap) should be approximate", n)
	}
	rel := math.Abs(float64(id.DistinctCount-n)) / float64(n)
	if rel > 0.05 {
		t.Errorf("id distinct estimate %d for %d, rel-error %.4f > 0.05", id.DistinctCount, n, rel)
	}
	kind, _ := fieldByPath(res, "kind")
	if !kind.DistinctExact || kind.DistinctCount != 1 {
		t.Errorf("low-cardinality kind must stay exact, got exact=%v count=%d", kind.DistinctExact, kind.DistinctCount)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/profile/ -count=1`
Expected: PASS (all profile tests, including the unchanged exact-behavior tests and the new promotion tests).

- [ ] **Step 6: Verify the whole suite still passes**

Run: `go test ./... -count=1`
Expected: PASS. (schema/diff/cmd tests use small fixtures that never promote, so they are byte-identical.)

- [ ] **Step 7: Commit**

```bash
git add internal/profile/accumulator.go internal/profile/profiler.go internal/profile/accumulator_test.go internal/profile/profiler_test.go
git commit -m "feat: promote high-cardinality fields to bounded sketches"
```

---

### Task 4: Approximate marker in the table + end-to-end verification

**Files:**
- Modify: `internal/render/table.go` (distinct marker `+` -> `~` for approximate fields)
- Test: `internal/render/render_test.go` (add a case)

**Rationale:** the current table appends `+` to an inexact distinct count, but a HyperLogLog estimate can be above OR below the true value, so `+` (which reads as "at least N") is a false claim. Use a `~` prefix to mean "approximately".

**Interfaces:** none changed; render output for a field with `DistinctExact=false` now shows `~<n>` instead of `<n>+`.

- [ ] **Step 1: Add the failing test**

Append to `internal/render/render_test.go`:

```go
func TestTableApproximateDistinctMarker(t *testing.T) {
	res := profile.ProfileResult{
		Records: 100000,
		Fields: []profile.FieldProfile{
			{Path: "id", PresenceRate: 1, TypeDist: map[profile.JSONKind]float64{profile.KindString: 1}, DistinctCount: 98765, DistinctExact: false},
		},
	}
	var b bytes.Buffer
	Table(&b, res)
	out := b.String()
	if !strings.Contains(out, "~98765") {
		t.Errorf("approximate distinct should render as ~98765, got:\n%s", out)
	}
	if strings.Contains(out, "98765+") {
		t.Errorf("must not use the misleading '+' suffix for an estimate:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/ -run TestTableApproximate -v`
Expected: FAIL (output still uses `98765+`).

- [ ] **Step 3: Implement**

In `internal/render/table.go`, in `Table`, change the distinct-marker block. It currently reads:

```go
		distinct := fmt.Sprintf("%d", f.DistinctCount)
		if !f.DistinctExact {
			distinct += "+"
		}
```

Replace with:

```go
		distinct := fmt.Sprintf("%d", f.DistinctCount)
		if !f.DistinctExact {
			distinct = "~" + distinct // approximate estimate (HyperLogLog)
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/render/ -count=1`
Expected: PASS.

- [ ] **Step 5: Whole suite and build**

Run:
```bash
go test ./... -count=1
go build -o shape.exe .
```
Expected: all PASS; binary builds.

- [ ] **Step 6: Manual end-to-end smoke check**

Generate a high-cardinality NDJSON and profile it:
```bash
awk 'BEGIN{ for(i=0;i<20000;i++) printf "{\"id\":\"id-%d\",\"kind\":\"x\"}\n", i }' > /tmp/hicard.ndjson
./shape.exe profile /tmp/hicard.ndjson
./shape.exe profile --json /tmp/hicard.ndjson
```
Expected: the `id` field shows a `~<estimate>` distinct near 20000 (DistinctExact false in JSON), while `kind` shows exact `1`. Memory stays bounded regardless of cardinality (the exact map is freed on promotion).

- [ ] **Step 7: Commit**

```bash
git add internal/render/table.go internal/render/render_test.go
git commit -m "feat: render approximate distinct counts with a ~ prefix"
```

---

## Plan 4 self-review

Coverage: HLL sketch (Task 1), Space-Saving sketch (Task 2), two-phase accumulator + promotion + profiler wiring + Result mapping (Task 3), approximate marker + e2e (Task 4). Determinism: fixed-seed FNV-1a+fmix64 hash (no maphash), order-independent HLL seeding, sorted Space-Saving seeding, deterministic min/tie-breaks (Tasks 1-3, tested by `TestHLLSameInputSameRegisters`, `TestSpaceSavingDeterministicEviction`). Soundness: enum safety preserved because exactCap (16384) >> 10 so enums never promote, and promoted => DistinctExact=false (schema.enumOK / diff.completeEnum short-circuit); `DistinctCount >= len(TopValues)` clamp; presence/TypeDist/NullRate/min-max/strlen stay exact (untouched blocks in Result). Byte-identical exact behavior for <=exactCap fields keeps existing profile/schema/diff tests green (Task 3 Step 6).

Placeholder scan: none; every code step is complete.

Type consistency: `hll`/`hash64` (Task 1) and `spaceSaving`/`ssCounter`/`ValueCount` (Task 2) are consumed by the accumulator's `promote`/`Result` (Task 3); `hllPrecision`/`spaceSavingCap`/`DefaultExactCap` consts are defined once and used at their call sites; `newFieldAccumulator(path, exactCap)` keeps its two-arg shape.

Behavior-change note (documented, intentional): lowering the exact cap from 50000 to 16384 means fields with 16385..49999 distinct values now report an ESTIMATE (DistinctExact=false) instead of an exact count. Enum inference (<=10 distinct) and all exact metrics are unaffected; the estimate is within ~1.6% and is clearly marked `~`.

Out of scope (later): richer estimate metadata in JSON (an explicit accuracy band); a `--exact` flag to force full-exact profiling; reservoir-sampled example/outlier values; HLL merge across shards/files (the `add`/register-max design already supports it if a multi-file mode is added).

## Next plans
- Plan 5: CSV / Parquet / SQLite readers behind the same record-stream contract.
- Plans 6-7: Wails desktop GUI; distribution (GitHub Action / Homebrew / npm).
