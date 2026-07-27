# shape E9 - Column Sort Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Click a column header to sort the table by that column (none → asc → desc → none), exact over the whole result on every tier (memory / SQLite / Parquet / rescan streaming), composing with filter + search + transform.

**Architecture:** Sort is a query-key dimension, NOT a renumbering - `Row.Index` stays the absolute scan ordinal, so E6 getCell / E7 edit overlay / E8 stats need zero change. A single shared comparator (`compareValues`) defines the cross-tier total order; it compiles into `CompiledPlan.Sort` (built once in `Engine.QueryRows`, like `CompiledFilter`), and each backend serves the sorted `[offset,limit)` window (mem: index permutation; sql: ORDER BY pushdown or Go fallback; parquet: keys + SeekToRow; rescan: a keys-only sorted-ordinal index). Threading is via `QueryRequest.Sort` + `CompiledPlan.Sort` - NOT `Window`, so the `Backend.Query` signature and its test doubles are untouched.

**Tech Stack:** Go 1.25 (`internal/query`), Wails v2.12.0 bindings, Svelte 3 + TypeScript, Vitest, `go test`, real jq 1.7.1 + modernc SQLite for codegen equivalence.

## Global Constraints

- cgo-free (`CGO_ENABLED=0 go build` stays green). No DuckDB. No new runtime deps (go.mod/go.sum unchanged; no `dependencies` growth in gui/frontend/package.json).
- Conventional Commits, lowercase imperative subject, **NO** `Co-Authored-By` trailer.
- Every test carries a mutation proof: break the logic, watch the specific test fail, restore.
- **Row.Index stays absolute** - never stamp the sorted rank into it. This is the load-bearing invariant.
- **Cross-tier parity**: the same fixture on memory / rescan (BudgetMB=1) / SQLite / Parquet must return byte-identical sorted windows. The rescan tier is forced with BudgetMB=1 (a fixture must exceed it - E4/E5 use ~20000 records; `openExportFixture(t, maps, 1)` FAILS LOUDLY if the tier isn't "rescan").
- Windows: gofmt via `tr -d '\r' | gofmt -l`; after `wails build` never `git add -A` (deletes gui/frontend/dist/.gitkeep); revert build churn (go.mod, dist/.gitkeep, wailsjs/runtime/*). `wails generate module` output committed with the Go change; final generate diff empty.
- The user performs/authorizes the `--no-ff` merge. Branch: `feat/e9-sort` off current master.
- `Sort.Path == ""` MUST be byte-for-byte today's behavior on every tier (the no-op baseline). Assert it.

---

### Task 1: The comparator + `CompiledSort` (the parity heart)

**Files:**
- Create: `internal/query/sort.go`
- Create: `internal/query/sort_test.go`

**Interfaces:**
- Consumes: the profiler's scalar value set (`nil`, `bool`, `json.Number`, **`float64`**, `string`) + the `Missing`/`IsMissing` sentinel (transform.go:275/279). `resolveSegs(path, cm)` (filter.go:241) to parse a sort path to `[]Seg`; the real value resolver `resolve(record, segs) []any` (columns.go:114, returns an empty slice for an absent path); `Missing`. (float64 is real: readers.go:99,121 pass it through; Parquet DOUBLE / SQLite REAL store it.)
- Produces: `type SortSpec struct { Path string; Desc bool }`, `type CompiledSort struct { segs []Seg; desc bool }`, `func CompileSort(spec SortSpec, cm *ColumnModel) (*CompiledSort, error)` (nil when `spec.Path == ""`), `func compareValues(a, b any) int`, and `func (cs *CompiledSort) Less(recA any, ordA int64, recB any, ordB int64) bool` (resolves each record's key, compares, ties break on ordinal ascending).

- [ ] **Step 1: Write the failing comparator tests**

Create `internal/query/sort_test.go`:

```go
package query

import (
	"encoding/json"
	"testing"
)

func n(s string) json.Number { return json.Number(s) }

func TestCompareValues_TotalOrderAcrossKinds(t *testing.T) {
	// Missing < null < bool < number < string, then within-kind.
	ordered := []any{
		Missing, nil,
		false, true,
		n("-5"), n("0"), n("2.5"), n("10"),
		"a", "b",
	}
	for i := 0; i < len(ordered); i++ {
		for j := 0; j < len(ordered); j++ {
			got := compareValues(ordered[i], ordered[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if sign(got) != want {
				t.Fatalf("compareValues(%v, %v) sign = %d, want %d", ordered[i], ordered[j], sign(got), want)
			}
		}
	}
}

func sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	default:
		return 0
	}
}

func TestCompareValues_BigIntExactNotFloat(t *testing.T) {
	// 9007199254740993 and 9007199254740992 are indistinguishable as float64
	// but must order strictly. Mutation: compare via Float64() -> equal -> fails.
	if compareValues(n("9007199254740993"), n("9007199254740992")) <= 0 {
		t.Fatalf("9007199254740993 must sort AFTER 9007199254740992 (float64 collapses them)")
	}
	if compareValues(n("9007199254740992"), n("9007199254740993")) >= 0 {
		t.Fatalf("ordering must be strict and antisymmetric at 2^53")
	}
}

func TestCompareValues_IntVsFraction(t *testing.T) {
	if compareValues(n("2"), n("10")) >= 0 {
		t.Fatalf("2 < 10 numerically (not lexically)")
	}
	if compareValues(n("2.5"), n("2.49")) <= 0 {
		t.Fatalf("2.5 > 2.49")
	}
}

func TestCompareValues_Float64AndJSONNumberUnify(t *testing.T) {
	// The memory tier holds json.Number; Parquet DOUBLE / SQLite REAL hold
	// float64. The SAME logical value must compare equal across tiers, and a
	// float64 must order numerically against a json.Number (mutation: omit the
	// float64 case -> a float64 ranks as "unexpected" (rank 5, after strings)
	// and two floats compare mutually equal -> both assertions fail).
	if compareValues(float64(2.5), n("2.5")) != 0 {
		t.Fatalf("float64(2.5) must equal json.Number(\"2.5\") cross-tier")
	}
	if compareValues(float64(2.5), n("10")) >= 0 {
		t.Fatalf("float64(2.5) < json.Number(10) numerically")
	}
	if compareValues(float64(1.0), float64(2.0)) >= 0 {
		t.Fatalf("float64 values must order numerically among themselves")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/query/ -run TestCompareValues -count=1`
Expected: FAIL - `compareValues` undefined (does not compile).

- [ ] **Step 3: Implement `sort.go`**

Create `internal/query/sort.go`:

```go
package query

import (
	"encoding/json"
	"math/big"
)

// SortSpec is the DTO-level sort request: a single column path and a direction.
// Path == "" means "no sort" (source-record order, the pre-E9 behavior).
type SortSpec struct {
	Path string `json:"path"`
	Desc bool   `json:"desc"`
}

// CompiledSort holds the resolved sort path segments + direction. nil == no sort.
type CompiledSort struct {
	segs []Seg
	desc bool
}

// CompileSort resolves the sort path once (like CompiledFilter). Returns nil for
// an empty path so callers can treat "no sort" as a cheap nil check.
func CompileSort(spec SortSpec, cm *ColumnModel) (*CompiledSort, error) {
	if spec.Path == "" {
		return nil, nil
	}
	segs, err := resolveSegs(spec.Path, cm)
	if err != nil {
		return nil, err
	}
	return &CompiledSort{segs: segs, desc: spec.Desc}, nil
}

// resolveValue is the first-value scalar for a []Seg against a record: the
// caller-side rule Project/ProjectValues already apply (transform.go:255-260 /
// :321-326) over the real resolver `resolve(record, segs) []any` (columns.go:114),
// which returns an EMPTY slice (NOT Missing) for an absent path and applies the
// first-array-element rule via Elem segments. This is NOT a duplicate resolver -
// `resolve` is the primitive; this is the 3-line first-value+Missing adapter that
// both the comparator and the keys-only tiers (T4/T6) share.
func resolveValue(rec any, segs []Seg) any {
	vs := resolve(rec, segs)
	if len(vs) == 0 {
		return Missing
	}
	return vs[0]
}

// valueKindRank orders values across scalar kinds: Missing < null < bool <
// number < string. Total + deterministic even for mixed-type columns. NAME NOTE:
// there is already a package-level `var kindRank` (columns.go:769, ranks
// profile.JSONKind for dominantKind) - this MUST be a different identifier.
// The number rank covers BOTH json.Number (memory tier) and float64 (Parquet
// DOUBLE / SQLite REAL / readers.ToProfileValue passthrough, readers.go:99,121).
func valueKindRank(v any) int {
	switch v.(type) {
	case nil:
		return 1
	case bool:
		return 2
	case json.Number, float64:
		return 3
	case string:
		return 4
	}
	if IsMissing(v) {
		return 0
	}
	return 5 // any unexpected type sorts last, deterministically
}

// compareValues is the cross-tier total order over profiler scalar values.
// Returns <0, 0, >0. Numbers (json.Number AND float64) compare by EXACT value,
// so json.Number("2.5") == float64(2.5) cross-tier and 9007199254740993 !=
// ...992 (which float64 would collapse - but we only reach float64 for values
// SQLite/Parquet actually store as REAL/DOUBLE).
func compareValues(a, b any) int {
	ra, rb := valueKindRank(a), valueKindRank(b)
	if ra != rb {
		return ra - rb
	}
	switch av := a.(type) {
	case bool:
		bv := b.(bool)
		switch {
		case av == bv:
			return 0
		case !av:
			return -1
		default:
			return 1
		}
	case json.Number, float64:
		return compareNumeric(a, b) // both operands are rank-3 numbers here
	case string:
		bv := b.(string)
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		default:
			return 0
		}
	}
	return 0 // equal within nil/Missing/unexpected kind
}

// numericRat converts a rank-3 numeric (json.Number or float64) to an exact
// big.Rat, or (nil,false) for a non-finite float64 (NaN/±Inf: SetFloat64
// returns nil) or an unparseable literal.
func numericRat(v any) (*big.Rat, bool) {
	switch x := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(string(x))
		return r, ok
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		return new(big.Rat).SetFloat64(x), true
	}
	return nil, false
}

// compareNumeric compares two rank-3 numbers by exact rational value; a
// non-finite/unparseable operand sorts AFTER a finite one (deterministic), and
// two such compare equal - matching how the profiler already treats non-finite
// as excluded-but-present.
func compareNumeric(a, b any) int {
	ra, oka := numericRat(a)
	rb, okb := numericRat(b)
	switch {
	case oka && okb:
		return ra.Cmp(rb)
	case oka:
		return -1
	case okb:
		return 1
	default:
		return 0
	}
}

// LessKeys orders two PRE-RESOLVED keys, ties broken on the absolute ordinal
// ASCENDING. The keys-only tiers (rescan T4, parquet T6) hold (key, ordinal)
// pairs and MUST use this - CompiledSort.Less resolves records first, which
// those tiers cannot do (they discarded the records).
func (cs *CompiledSort) LessKeys(keyA any, ordA int64, keyB any, ordB int64) bool {
	c := compareValues(keyA, keyB)
	if cs.desc {
		c = -c
	}
	if c != 0 {
		return c < 0
	}
	return ordA < ordB
}

// Less orders two RECORDS by resolving each to its sort key, then delegating to
// LessKeys (so record-holding tiers and key-holding tiers share one ordering).
func (cs *CompiledSort) Less(recA any, ordA int64, recB any, ordB int64) bool {
	return cs.LessKeys(resolveValue(recA, cs.segs), ordA, resolveValue(recB, cs.segs), ordB)
}
```

Imports for sort.go: `encoding/json`, `math`, `math/big`.

Prerequisite to confirm before coding (settled by the plan review): `resolve(record any, segs []Seg) []any` exists at columns.go:114; `Missing`/`IsMissing` are exported (transform.go:275/279); there is NO existing `resolveValue` (create it) and `kindRank` is taken (hence `valueKindRank`).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/query/ -run TestCompareValues -count=1`
Expected: PASS (3 tests).

- [ ] **Step 5: Prove the mutations (two)**

Mutation A (exact numbers) - in `numericRat`, for the `json.Number` case return `new(big.Rat).SetFloat64(f)` where `f, _ := x.Float64()`. Run `go test ./internal/query/ -run TestCompareValues_BigIntExactNotFloat` → FAIL (the two 2^53-neighbours collapse to equal). Restore.

Mutation B (float64 kind) - remove `float64` from `valueKindRank`'s rank-3 case (so it falls through to rank 5). Run `go test ./internal/query/ -run TestCompareValues_Float64AndJSONNumberUnify` → FAIL (float64 now ranks after strings and two floats compare equal). Restore.

- [ ] **Step 6: gofmt + commit**

Run: `gofmt -l internal/query/sort.go internal/query/sort_test.go` (LF-normalized; expect empty).
```bash
git add internal/query/sort.go internal/query/sort_test.go
git commit -m "feat(query): exact cross-tier value comparator + CompiledSort"
```

---

### Task 2: Thread `Sort` through the DTO + `CompiledPlan` (no behavior change yet)

**Files:**
- Modify: `internal/query/engine.go` (QueryRequest:250 + wherever `CompiledPlan` is built in `QueryRows` ~:438-444)
- Modify: `internal/query/transform.go` (CompiledPlan struct:335)
- Modify: `internal/query/engine_test.go` (a plumbing test)
- Regenerate: `gui/frontend/wailsjs/go/models.ts` (QueryRequest + SortSpec)

**Interfaces:**
- Consumes: `CompileSort` (Task 1).
- Produces: `QueryRequest.Sort SortSpec`; `CompiledPlan.Sort *CompiledSort`; `CompiledPlan` is built with the compiled sort. Backends do NOT yet read `p.Sort` (behavior unchanged this task).

- [ ] **Step 1: Write the failing test**

In `engine_test.go`, add a test that a sorted `QueryRequest` compiles and (because no backend reads `p.Sort` yet) returns the SAME rows as an unsorted one - proving the plumbing added a field without breaking the pipeline:

```go
func TestQueryRows_SortPlumbingIsInert_UntilBackendsRead(t *testing.T) {
	eng, handle := openMemFixtureForSort(t) // small helper: open a few-row NDJSON with a numeric col "n"
	plain, err := eng.QueryRows(context.Background(), QueryRequest{Handle: handle, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	sorted, err := eng.QueryRows(context.Background(), QueryRequest{Handle: handle, Limit: 100, Sort: SortSpec{Path: "n", Desc: true}})
	if err != nil {
		t.Fatalf("a sorted QueryRequest must compile + run: %v", err)
	}
	// This task does NOT wire the backend, so order is unchanged. (Task 3 makes
	// memBackend actually sort; that task's test asserts the reorder.)
	if len(sorted.Rows) != len(plain.Rows) {
		t.Fatalf("plumbing changed the row COUNT: %d vs %d", len(sorted.Rows), len(plain.Rows))
	}
}
```
(Add a small `openMemFixtureForSort` helper writing e.g. `{"n":3},{"n":1},{"n":2}` via the existing `writeNDJSONFile` + `eng.OpenSource`, mirroring `openExportFixture`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/query/ -run TestQueryRows_SortPlumbing -count=1`
Expected: FAIL - `QueryRequest` has no field `Sort` (does not compile).

- [ ] **Step 3: Add the fields + compile the sort**

In `engine.go` QueryRequest, add `Sort SortSpec \`json:"sort"\``. In `transform.go` CompiledPlan, add `Sort *CompiledSort`. In `Engine.QueryRows`, after building the plan, compile the sort and attach it:
```go
	cs, err := CompileSort(req.Sort, plan.Columns)
	if err != nil {
		return RowSet{}, fmt.Errorf("query: sort: %w", err)
	}
	plan.Sort = cs
```
(Place this beside the existing filter/search compilation. Every backend receives `p.Sort`; none reads it yet.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/query/ -run TestQueryRows_SortPlumbing -count=1`
Expected: PASS.

- [ ] **Step 5: Prove the mutation**

Make `CompileSort` ignore errors: `segs, _ := resolveSegs(...)`. Add an assertion to the test that `QueryRows(..., Sort: SortSpec{Path: "does.not[.parse"})` returns a non-nil error; with the mutation it does not. (A malformed path must surface as an error, not a silent no-sort.) Restore.

- [ ] **Step 6: Regenerate bindings + commit**

Run: `cd gui && wails generate module` - `models.ts` gains `SortSpec` + `QueryRequest.sort`. Revert `wailsjs/runtime/*` churn.
Run: `go test ./internal/query/ -count=1`; `cd gui/frontend && npm run check`.
```bash
git add internal/query/engine.go internal/query/transform.go internal/query/engine_test.go gui/frontend/wailsjs/go/models.ts
git commit -m "feat(query): thread SortSpec through QueryRequest and CompiledPlan"
```

---

### Task 3: memBackend sorted window + permutation cache

**Files:**
- Modify: `internal/query/memstore.go` (the Query skip/take loop ~:156-172; add a permutation cache field + helper mirroring `matchBitsetFor` :301-332)
- Modify: `internal/query/memstore_test.go`

**Interfaces:**
- Consumes: `p.Sort *CompiledSort` (Task 2); the match bitset (`matchBitsetFor`).
- Produces: memBackend serving the sorted `[offset,limit)` window with `Row.Index == absolute ordinal`.

- [ ] **Step 1: Write the failing test**

```go
func TestMemBackend_SortsByColumnKeepingAbsoluteIndex(t *testing.T) {
	// records: n = [3,1,2] at absolute indices [0,1,2].
	eng, handle := openMemFixtureForSort(t) // {"n":3},{"n":1},{"n":2}
	rs, err := eng.QueryRows(context.Background(), QueryRequest{Handle: handle, Limit: 100, Sort: SortSpec{Path: "n"}})
	if err != nil {
		t.Fatal(err)
	}
	// Ascending by n -> display order is the records with n=1,2,3 -> absolute
	// indices 1,2,0. The Row.Index MUST be the absolute ordinal, NOT 0,1,2.
	gotIdx := []int64{rs.Rows[0].Index, rs.Rows[1].Index, rs.Rows[2].Index}
	if gotIdx[0] != 1 || gotIdx[1] != 2 || gotIdx[2] != 0 {
		t.Fatalf("sorted Row.Index = %v, want [1 2 0] (absolute ordinals in sorted order)", gotIdx)
	}
}
```

- [ ] **Step 2: Run - FAIL** (memBackend ignores `p.Sort`; rows come back in source order [0,1,2]).
Run: `go test ./internal/query/ -run TestMemBackend_Sorts -count=1`

- [ ] **Step 3: Implement**

In `memBackend.Query`, when `p.Sort != nil`: build the matching absolute-index slice from the bitset, sort it with `sort.SliceStable(idx, func(a,b) bool { return p.Sort.Less(m.records[idx[a]], int64(idx[a]), m.records[idx[b]], int64(idx[b])) })` (mem HOLDS the records, so `Less` - which resolves each record to its key - is the right call here; ordinals are the indices themselves). Then **clamp** the window: `lo := offset; if lo > int64(len(idx)) { lo = int64(len(idx)) }; hi := offset + int64(limit); if hi > int64(len(idx)) { hi = int64(len(idx)) }`, take `idx[lo:hi]`, and emit `p.Transform.Project(m.records[j], int64(j))` for each `j` (the absolute ordinal, NOT the display position). Guard the whole sorted path behind `if p.Sort != nil`; the existing ascending loop stays the `p.Sort == nil` path verbatim. Add a `sortPermCache` keyed by `(filterKey, sortPath, desc)` - copy the double-checked LRU shape of `matchBitsetFor` (memstore.go:301-332) - storing the sorted `[]int` so re-windowing does not re-sort.

- [ ] **Step 4: Run - PASS.**

- [ ] **Step 5: Prove the mutation**

Change the emit to `Project(m.records[j], int64(sortedPosition))` (stamp the display rank instead of the absolute ordinal). Run - the test fails (`Row.Index` becomes `[0 1 2]`). This is the load-bearing invariant. Restore.

- [ ] **Step 6: Assert the no-op baseline + the last-page clamp + commit**

Add `TestMemBackend_NoSortIsSourceOrder` asserting `Sort{Path:""}` returns indices `[0,1,2]` (unchanged). Add `TestMemBackend_SortOffsetPastEndIsEmpty` querying `Offset: 1000, Limit: 10, Sort: {Path:"n"}` on the 3-row fixture and asserting an empty, non-panicking window (guards the clamp - without it, `idx[1000:1010]` panics). Run the mem package. gofmt. Commit `feat(query): memBackend serves sorted windows with a permutation cache`.

---

### Task 4: rescanBackend keys-only sorted-ordinal index (the hard tier)

**Files:**
- Modify: `internal/query/rescan.go` (Query ~:202-263; add a keys-only index builder + cache)
- Modify: `internal/query/rescan_test.go`

**Interfaces:**
- Consumes: `p.Sort`; the forward `scan()` (rescan.go:146-174) and `resolveValue`.
- Produces: rescan serving exact sorted windows via a cached `[]ordinal` permutation, memory bounded by keys (not records).

- [ ] **Step 1: Create the shared fixture helper, then write the failing cross-tier parity test**

First add `manyNumberedRecords` to the test file (it does NOT exist; the real `manyRecords` has no numeric column and is pre-sorted). It must carry a numeric `n` (json.Number on re-read) whose sorted order DIFFERS from source order (so parity + the Row.Index mutation are non-vacuous) AND a float `f` column (so the float64 comparator path - plan-review Critical #1 - is exercised on the Parquet/SQLite tiers in T10). Deterministic shuffle (no `Math.random`; use a fixed seed). ~20000 records exceed BudgetMB=1 → rescan tier (engine_test.go:122-127 confirms this threshold):

```go
func manyNumberedRecords(nrec int) []map[string]any {
	recs := make([]map[string]any, nrec)
	r := rand.New(rand.NewSource(1)) // deterministic
	perm := r.Perm(nrec)
	for i := 0; i < nrec; i++ {
		recs[i] = map[string]any{"n": perm[i], "f": float64(perm[i]) + 0.5}
	}
	return recs
}
```
(Import `math/rand`. `perm[i]` shuffles the sort key so index order != sorted order.)

```go
func TestRescanBackend_SortMatchesMemoryTier(t *testing.T) {
	maps := manyNumberedRecords(20000) // n = shuffled ints, > BudgetMB=1 -> rescan tier
	engMem, hMem, _ := openExportFixture(t, maps, 0) // memory tier
	engRe, hRe, _ := openExportFixture(t, maps, 1)   // rescan tier (fixture FAILS if not "rescan")
	req := func(h string) QueryRequest {
		return QueryRequest{Handle: h, Offset: 50, Limit: 30, Sort: SortSpec{Path: "n", Desc: true}}
	}
	rsMem, err := engMem.QueryRows(context.Background(), req(hMem))
	if err != nil { t.Fatal(err) }
	rsRe, err := engRe.QueryRows(context.Background(), req(hRe))
	if err != nil { t.Fatal(err) }
	if len(rsMem.Rows) != len(rsRe.Rows) {
		t.Fatalf("window sizes differ: mem %d vs rescan %d", len(rsMem.Rows), len(rsRe.Rows))
	}
	for i := range rsMem.Rows {
		if rsMem.Rows[i].Index != rsRe.Rows[i].Index {
			t.Fatalf("row %d: mem Index %d != rescan Index %d (sorted windows must be byte-identical across tiers)", i, rsMem.Rows[i].Index, rsRe.Rows[i].Index)
		}
	}
}
```

- [ ] **Step 2: Run - FAIL** (rescan ignores `p.Sort`, returns source-order window; mem returns sorted → indices differ).

- [ ] **Step 3: Implement the keys-only index**

When `p.Sort != nil`: build (once, cached per `(filterKey, sortPath, desc)`) a `[]int64` of absolute ordinals sorted by key: one forward `scan()` collecting `(resolveValue(rec, p.Sort.segs), idx)` for every MATCHING record into a `[]struct{key any; ord int64}`, `sort.SliceStable` by **`p.Sort.LessKeys(pairs[a].key, pairs[a].ord, pairs[b].key, pairs[b].ord)`** (the keys-only comparator - these pairs hold pre-resolved keys, so `Less`, which re-resolves records, is NOT usable here), keep only the `[]int64` ordinals. Serve the window: **clamp** `lo := min(offset, int64(len(perm)))`, `hi := min(offset+int64(limit), int64(len(perm)))` (a last/over-range page must yield an empty window, never panic), take `perm[lo:hi]`, put those ordinals in a set, and one forward `scan()` collecting the records at those ordinals, then emit them **in perm order** as `Project(rec, ord)`. Cache the `[]int64` on the backend (guard the total set size; no silent cap - if one is ever needed, surface it). `wantTotal` → `len(perm)` exactly. The early-stop (rescan.go:233-235) applies only to the `p.Sort == nil` path. (`p.Sort.segs` is unexported but same-package, so the backend reads it directly.)

- [ ] **Step 4: Run - PASS** (the parity test now matches mem).

- [ ] **Step 5: Prove the mutation**

Emit `Project(rec, int64(displayPos))` instead of the true ordinal → the cross-tier index comparison fails. Restore. Also add a mutation on the cache: return the sorted window WITHOUT the ordinal tiebreak in `Less` and assert a second, different fixture with duplicate keys still matches mem (proves determinism). Restore.

- [ ] **Step 6: gofmt + commit** `feat(query): rescan sorts via a bounded keys-only ordinal index`.

---

### Task 5: sqlBackend ORDER BY pushdown + Go fallback

**Files:**
- Modify: `internal/query/sqlbackend.go` (selectSQL / window / recordAt ORDER BY `_rowid_`), `internal/query/sqlpushdown.go` (a `sortPushable` gate mirroring the filter's exact-or-nothing bar)
- Modify: `internal/query/sqlbackend_test.go`

**Interfaces:**
- Consumes: `p.Sort`; the taint/collation machinery (`collated()` sqlpushdown.go:203-216, taint probe :342-349, decltype :429-471, exact-or-nothing :36-55).
- Produces: exact sorted windows on SQLite - via pushed `ORDER BY <col> COLLATE BINARY, _rowid_` when pushable, else the shared Go comparator over the cursor scan.

- [ ] **Step 1: Write the failing tests** (two): (a) a pushable single-type untainted column sorts exactly and equals the memory tier on the same data; (b) a NOCASE-declared column falls back to Go-sort and STILL byte-matches the memory tier (mutation target: push it with a bare `ORDER BY <col>` → NOCASE diverges). Build both against a real modernc SQLite fixture (the file already has SQLite executability tests - mirror them).

- [ ] **Step 2: Run - FAIL** (sqlBackend ignores `p.Sort`).

- [ ] **Step 3: Implement**

Add `sortPushable(cs, cols) (string, bool)` in sqlpushdown.go: returns the `ORDER BY` fragment `"quoted_col" COLLATE BINARY [DESC], _rowid_` iff the column is single-storage-class, untainted (no DATE/DATETIME/TIMESTAMP decltype, no BLOB/time.Time), and non-Elem/non-dotted (same gates as the filter pushdown), **AND `s.hasRowID && s.denseRowIDs`** - because a pushed ORDER BY must still stamp the ABSOLUTE ordinal into Row.Index, and the only way to recover it from a reordered result is `_rowid_ - 1` (dense rowids), exactly the gate `queryPushed` already uses (E5). **CRITICAL (plan-review #4/#17): never let the pushed-sort window stamp `offset+i`** - `queryWindowSQL`/`queryUnfiltered` set `Row.Index = offset+i` (sqlbackend.go queryUnfiltered:597-605), which under a reorder is the sorted RANK, silently breaking getCell/edit/stats. So when pushing sort, use a query shaped like `pushedWindow` that SELECTs `_rowid_` alongside the columns and emits `Project(rec, rowid-1)` (NOT offset+i), for BOTH the filtered and the unfiltered-with-sort case (add a WHERE-less rowid-selecting windowed query for the latter). When `!sortPushable` (tainted / mixed class / no dense rowid) → Go-fallback: fetch the matched rows via the existing `_rowid_`-ordered cursor scan (which yields `idx == absolute ordinal`), collect `(idx, rec)`, sort by `p.Sort.Less`, **clamp** the window, emit `Project(rec, idx)` - idx stays the absolute ordinal. Cache the fallback permutation per `(filterKey, sortPath, desc)`. Add an assertion that the sorted rows' `Row.Index` values are absolute ordinals (a permutation), NOT `0..limit` - the same invariant mutation as T3.

- [ ] **Step 4: Run - PASS.**

- [ ] **Step 5: Prove the mutations** - drop `COLLATE BINARY` → the NOCASE test diverges from mem; push a tainted DATE column → its sorted order diverges. Restore each.

- [ ] **Step 6: gofmt + commit** `feat(query): sqlBackend pushes ORDER BY when exact, else Go-sorts`.

---

### Task 6: parquetBackend keys + SeekToRow

**Files:**
- Modify: `internal/query/parquetbackend.go` (queryUnfiltered :374-400 / readWindow :271-312 / filtered scan :424)
- Modify: `internal/query/parquetbackend_test.go`

**Interfaces:**
- Consumes: `p.Sort`; `SeekToRow`; the sort-key leaf reader.
- Produces: exact sorted windows via a keys-only physical-ordinal permutation + per-window `SeekToRow`, keeping the bounded-memory contract (:38-49).

- [ ] **Step 1: Write the failing cross-tier parity test** - a parquet fixture sorted on a numeric column equals the memory tier's sorted window (offset+limit into the middle). Every parquet test re-opens through shape's own engine; register `t.Cleanup(CloseSource)` (Windows file-handle rule).

- [ ] **Step 2: Run - FAIL** (parquet ignores `p.Sort`).

- [ ] **Step 3: Implement** - when `p.Sort != nil`: one O(N) scan collecting `(resolveValue(rec, p.Sort.segs), physicalOrdinal)` for matching rows (piggyback the filtered scan :424 when filtered; else a dedicated key scan replacing the SeekToRow fast path), `sort.SliceStable` by **`p.Sort.LessKeys`** (parquet holds the pre-resolved keys, not the records - `Less` would re-resolve and is wrong here), keep the `[]ordinal` permutation (cached per key), **clamp** the window `[lo,hi)`, then serve it by `SeekToRow(ordinal)` per row, emitting `Project(rec, ordinal)` (the absolute physical ordinal, NOT display pos). 

- [ ] **Step 4: Run - PASS.**

- [ ] **Step 5: Prove the mutation** - stamp display position into Row.Index → parity index comparison fails. Restore.

- [ ] **Step 6: gofmt + commit** `feat(query): parquetBackend sorts via keys + SeekToRow`.

---

### Task 7: App/store `setSort` seam

**Files:**
- Modify: `gui/frontend/src/lib/explorer/store.ts` (a `currentSort` module var next to `currentSearch` :149; thread into the QueryRows payload :297-298; a `setSort(spec)` calling `requery({})` mirroring `setSearch` :500-503; export it)
- Modify: `gui/frontend/src/lib/explorer/types.ts` (re-export `SortSpec`)
- Modify: `gui/frontend/src/lib/explorer/store.test.ts`

**Interfaces:**
- Consumes: the generated `query.SortSpec` (Task 2 bindings); `requery`.
- Produces: `explorer.setSort(spec: SortSpec)`; a `sort: SortSpec` field on `ExplorerState` so the header ▲/▼ indicator (T8) can read the active sort; the sort threaded into every `QueryRows` payload.
- NOTE: `SortSpec` is a member of the already **value-imported** `query` namespace in `types.ts:7` (used for `CellKind`), so re-export it as `export type SortSpec = query.SortSpec;` - NOT via the `visual` type-only import (that was the E8 FieldCard case).

- [ ] **Step 1: Write the failing test** - `setSort({path:"n",desc:true})`: (a) sends `sort` in the next `QueryRows` payload; (b) clears the page cache and bumps `resetToken` (scroll-to-top), mirroring `setSearch`'s `requery({})`. **A pure sort does NOT reset `total` to -1 or recount** - `requery` only sets `total=-1`+recount when a filter/search is active (`anyActive`), and a sort changes neither (plan-review #20/#22: the original `total=-1` mutation was vacuous). Mutation A: **skip `cache.clear()` in the sort path** → a stale pre-sort page is served for row 0 (assert the served rows change after a sort). Also assert **identity is preserved**: an edit set (via `setEdit`) before a sort is still addressable by the same `row.index` after (getCell/overlay unchanged).

- [ ] **Step 2–5:** run FAIL → implement `currentSort: SortSpec` (module var next to `currentSearch`) + `setSort(spec)` = set `currentSort` + `requery({ sort: spec })` (so `$explorer.sort` updates for the indicator) + thread `currentSort` into the `QueryRows` payload + the `ExplorerState.sort` field + the `types.ts` re-export → run PASS → prove Mutation A (skip `cache.clear`). 

- [ ] **Step 6: check + commit** `feat(gui): store.setSort threads sort through the query like search`.

---

### Task 8: DataTable header sort affordance + indicator

**Files:**
- Modify: `gui/frontend/src/lib/explorer/DataTable.svelte` (header cell: a sort click-region distinct from the focus/scroll-to-column click, cycling none→asc→desc→none, ▲/▼ indicator reflecting `$explorer` sort)
- Modify: `gui/frontend/src/lib/explorer/DataTable.*.test.ts` (a focused header-sort test file, mocked store)

**Interfaces:**
- Consumes: `explorer.setSort`, `$explorer` active sort.
- Produces: clicking a column's sort control cycles the direction and calls `setSort`; the header shows ▲ (asc) / ▼ (desc) / none.

- [ ] **Step 1: Write the failing test** - clicking the sort control on column "n" calls `setSort({path:"n",desc:false})`; clicking again `{path:"n",desc:true}`; a third time `{path:"",...}` (clear). Mutation: the sort click also fires `focus` (scroll-to-column) → assert no `focus` event on a sort click. Follow DataTable.edit.test.ts's mocked-store harness.

- [ ] **Step 2–5:** FAIL → implement the header control (a small caret button with `on:click|stopPropagation`, cycling logic reading `$explorer` sort) → PASS → prove the no-focus + cycle mutations.

- [ ] **Step 6: full suite + check + commit** `feat(gui): click a column header to sort, with a direction indicator`.

---

### Task 9: Codegen ORDER BY / sort_by

**Files:**
- Modify: `internal/query/codegensql.go` (append `ORDER BY <col> [DESC]`), `internal/query/codegenjq.go` (append `| sort_by(.<path>)` + `| reverse` for desc), `internal/query/codegen.go` (thread `Sort` into `CodegenRequest`/`Context`), `gui/app.go`/store (thread sort into the Codegen call), regenerate bindings
- Modify: the codegen test files (real jq + real SQLite equivalence for a single-column sort)

**Interfaces:**
- Consumes: `SortSpec`; the existing codegen path encoders (E5).
- Produces: the Code panel reflects the active sort in both jq and SQL.

- [ ] **Steps:** TDD each.
  - **SQL**: append `ORDER BY <col> [DESC]` under a sort, omit it without one (mutation: always emit → an unsorted query shows a spurious ORDER BY).
  - **jq (plan-review #15 - the streaming form ERRORS)**: `sort_by` needs an ARRAY, but the generated jq is a per-record streaming pipeline (`.[] | select(...) | {proj}`), so appending `| sort_by(.path)` runs `sort_by` against a single object and jq dies at runtime. Emit the **aggregating** form: wrap the record stream in `[ ... ]`, then `| sort_by(.<path>)`, then `| reverse` for descending, then `| .[]` to re-stream. Verify the emitted program actually RUNS under real jq 1.7.1 (not just "parses").
  - **Divergence disclosure (plan-review #19/#21)**: jq's `sort_by`/`reverse` does NOT match `compareValues` on (a) descending ties (`reverse` flips equal-key order), (b) missing-vs-null (a missing key sorts as null in jq), (c) big-integer precision. Emit a sort-specific caveat alongside the existing `warnCaseInsensitive`/type-guard notes, and DROP the spec's false "sort adds no new class of divergence" line (correct spec §7 too). Restrict the real-jq equivalence test to a fixture where the tiers provably agree: single-type, all-distinct keys, no missing/null in the sort column, ascending (or descending without ties).
  - Thread `Sort` through `CodegenRequest`/`Context` + the store's `refreshCodegen` (so a `setSort` refreshes the panel). Commit `feat(query): codegen reflects the active sort (ORDER BY / sort_by)`.

---

### Task 10: Cross-tier parity sweep + verification

**Files:**
- Create: `internal/query/sort_parity_test.go`

- [ ] **Step 1: Write the parity sweep** - ONE shuffled fixture (`manyNumberedRecords`, which carries a shuffled int `n`, a **float `f`**, and add a string `s` column), opened on all four tiers (memory, rescan via BudgetMB=1, SQLite, Parquet - choosing column types so values round-trip identically: int→INTEGER/INT64→json.Number, float→REAL/DOUBLE→**float64**, string→TEXT). Assert the sorted window `[offset,limit)` (both asc and desc) is byte-identical (same `Row.Index` sequence) across all four, **on the int column, the float column (exercises the float64 comparator path - plan-review Critical #1 - where mem holds json.Number but Parquet/SQLite hold float64), and the string column**. This is the single strongest E9 guarantee. Mutation: any one backend's comparator/ordinal handling diverging fails here; in particular the float column fails if `float64` was omitted from the comparator.

- [ ] **Step 2: Run - PASS** (all prior tasks make it green; if not, the diverging tier is a real bug to fix before proceeding).

- [ ] **Step 3: Full gate run** - `go test ./...`; `CGO_ENABLED=0 go build`; `npm run check`; `npx vitest run`; `git diff --stat go.mod go.sum` empty; `wails build` (+revert churn); `wails generate module` empty diff.

- [ ] **Step 4: Commit** `test(query): cross-tier sort parity sweep`.

---

### Task 11: Docs

**Files:** Modify `README.md` (Sort bullet in the Desktop GUI list) + `gui/README.md` (a Sort section: header click cycles none/asc/desc; exact over the whole result on every tier via the shared comparator; the gutter shows true non-contiguous source row numbers under a sort; go-to-row scrolls to the sorted position; export stays in source order for v1; single column).

- [ ] Add both bullets (concrete copy, honest about the gutter/go-to-row/export-order semantics), then commit `docs: document column sort`.

---

## Self-review (against the spec)

**Spec coverage:** comparator/CompiledSort (T1); SortSpec threading + Row.Index invariant (T2, asserted in T3/T4/T6 mutations); mem (T3); rescan keys-only index (T4); sql pushdown+fallback (T5); parquet keys+seek (T6); store setSort seam + identity preservation (T7); header ▲/▼ + no-focus (T8); codegen ORDER BY/sort_by (T9); cross-tier parity (T10, the top obligation); docs incl. gutter/go-to-row/export-order (T11). Export-source-order (non-goal) is respected by never threading sort into the export path. ✓

**Placeholder scan:** every task has concrete code or an exact file:line pattern to mirror. Post-review hardening: `resolveValue` is now DEFINED in T1 (a 3-line adapter over the real `resolve()[]any`), not assumed; `valueKindRank` renamed off the `kindRank` collision; `float64` folded into the numeric comparator; `LessKeys` added for the keys-only tiers; `manyNumberedRecords`/`openMemFixtureForSort` defined; window slices clamped; SQL pushed-sort gated on dense rowids (stamps `rowid-1`, never the rank); jq codegen aggregates (`[…]|sort_by|reverse?|.[]`) with a divergence caveat. No TODO/TBD. ✓

**Type consistency:** `SortSpec{Path,Desc}` / `CompiledSort` / `compareValues` / `CompiledSort.Less(rec,ord,rec,ord)` used identically T1→T2→T3→T4→T5→T6; `CompiledPlan.Sort *CompiledSort` (T2) read by every backend; `explorer.setSort(SortSpec)` (T7) consumed by the header (T8); `Row.Index == absolute ordinal` asserted by a mutation in T3/T4/T6 and end-to-end in T7. ✓

**Note (deviation from the spec, an improvement):** the spec floated adding `Sort` to `Window`; this plan puts the compiled sort on `CompiledPlan.Sort` instead (compiled once in QueryRows, like `CompiledFilter`), so the `Backend.Query` signature and its two test doubles are untouched.
