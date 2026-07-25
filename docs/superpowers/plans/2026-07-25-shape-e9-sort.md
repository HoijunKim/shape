# shape E9 — Column Sort Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Click a column header to sort the table by that column (none → asc → desc → none), exact over the whole result on every tier (memory / SQLite / Parquet / rescan streaming), composing with filter + search + transform.

**Architecture:** Sort is a query-key dimension, NOT a renumbering — `Row.Index` stays the absolute scan ordinal, so E6 getCell / E7 edit overlay / E8 stats need zero change. A single shared comparator (`compareValues`) defines the cross-tier total order; it compiles into `CompiledPlan.Sort` (built once in `Engine.QueryRows`, like `CompiledFilter`), and each backend serves the sorted `[offset,limit)` window (mem: index permutation; sql: ORDER BY pushdown or Go fallback; parquet: keys + SeekToRow; rescan: a keys-only sorted-ordinal index). Threading is via `QueryRequest.Sort` + `CompiledPlan.Sort` — NOT `Window`, so the `Backend.Query` signature and its test doubles are untouched.

**Tech Stack:** Go 1.25 (`internal/query`), Wails v2.12.0 bindings, Svelte 3 + TypeScript, Vitest, `go test`, real jq 1.7.1 + modernc SQLite for codegen equivalence.

## Global Constraints

- cgo-free (`CGO_ENABLED=0 go build` stays green). No DuckDB. No new runtime deps (go.mod/go.sum unchanged; no `dependencies` growth in gui/frontend/package.json).
- Conventional Commits, lowercase imperative subject, **NO** `Co-Authored-By` trailer.
- Every test carries a mutation proof: break the logic, watch the specific test fail, restore.
- **Row.Index stays absolute** — never stamp the sorted rank into it. This is the load-bearing invariant.
- **Cross-tier parity**: the same fixture on memory / rescan (BudgetMB=1) / SQLite / Parquet must return byte-identical sorted windows. The rescan tier is forced with BudgetMB=1 (a fixture must exceed it — E4/E5 use ~20000 records; `openExportFixture(t, maps, 1)` FAILS LOUDLY if the tier isn't "rescan").
- Windows: gofmt via `tr -d '\r' | gofmt -l`; after `wails build` never `git add -A` (deletes gui/frontend/dist/.gitkeep); revert build churn (go.mod, dist/.gitkeep, wailsjs/runtime/*). `wails generate module` output committed with the Go change; final generate diff empty.
- The user performs/authorizes the `--no-ff` merge. Branch: `feat/e9-sort` off current master.
- `Sort.Path == ""` MUST be byte-for-byte today's behavior on every tier (the no-op baseline). Assert it.

---

### Task 1: The comparator + `CompiledSort` (the parity heart)

**Files:**
- Create: `internal/query/sort.go`
- Create: `internal/query/sort_test.go`

**Interfaces:**
- Consumes: the profiler's scalar value set (`nil`, `bool`, `json.Number`, `string`) + the `Missing`/`IsMissing` sentinel (already in this package, from E4). `resolveSegs(path, cm)` (filter.go:241) to parse a sort path to `[]Seg`; the transform first-value resolve rule (transform.go:249-263).
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/query/ -run TestCompareValues -count=1`
Expected: FAIL — `compareValues` undefined (does not compile).

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

// kindRank orders values across JSON scalar kinds: Missing < null < bool <
// number < string. A total, deterministic order even for mixed-type columns.
func kindRank(v any) int {
	switch v.(type) {
	case nil:
		return 1
	case bool:
		return 2
	case json.Number:
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
// Returns <0, 0, >0. Numbers compare by EXACT value (big.Rat), never float64.
func compareValues(a, b any) int {
	ra, rb := kindRank(a), kindRank(b)
	if ra != rb {
		return ra - rb
	}
	switch av := a.(type) {
	case bool:
		bv := b.(bool)
		if av == bv {
			return 0
		}
		if !av {
			return -1
		}
		return 1
	case json.Number:
		return compareNumbers(av, b.(json.Number))
	case string:
		bv := b.(string)
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
		return 0
	}
	return 0 // equal within nil/Missing/unexpected kind
}

// compareNumbers compares two json.Number literals by exact rational value, so
// 9007199254740993 != 9007199254740992 (which float64 would collapse).
func compareNumbers(a, b json.Number) int {
	ra, oka := new(big.Rat).SetString(string(a))
	rb, okb := new(big.Rat).SetString(string(b))
	if !oka || !okb {
		// Non-finite or unparseable (NaN/Inf never reach here — the profiler
		// excludes them from numeric stats); fall back to string compare for a
		// deterministic order.
		if string(a) < string(b) {
			return -1
		}
		if string(a) > string(b) {
			return 1
		}
		return 0
	}
	return ra.Cmp(rb)
}

// Less orders two records by the sort key, ties broken on the absolute ordinal
// ASCENDING (so descending is not merely the reversed slice — ties and nulls
// stay deterministic and cross-tier identical).
func (cs *CompiledSort) Less(recA any, ordA int64, recB any, ordB int64) bool {
	ka := resolveValue(recA, cs.segs)
	kb := resolveValue(recB, cs.segs)
	c := compareValues(ka, kb)
	if cs.desc {
		c = -c
	}
	if c != 0 {
		return c < 0
	}
	return ordA < ordB
}
```

Note: `resolveValue(rec, segs) any` is the first-value scalar resolver already used by the transform/filter path (transform.go:249-282 resolves a `[]Seg` against a record, applying the first-array-element rule and returning `Missing` for an absent path). If its exported name differs, use the existing helper — do NOT write a second resolver. Confirm the name by reading transform.go before implementing; the plan assumes `resolveValue`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/query/ -run TestCompareValues -count=1`
Expected: PASS (3 tests).

- [ ] **Step 5: Prove the mutation**

In `compareNumbers`, replace the big.Rat compare with float64: `af, _ := a.Float64(); bf, _ := b.Float64(); return cmpF(af, bf)` (add a tiny float cmp). Run `go test ./internal/query/ -run TestCompareValues_BigIntExactNotFloat` → FAIL (the two 2^53-neighbours collapse to equal). Restore.

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

In `engine_test.go`, add a test that a sorted `QueryRequest` compiles and (because no backend reads `p.Sort` yet) returns the SAME rows as an unsorted one — proving the plumbing added a field without breaking the pipeline:

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
Expected: FAIL — `QueryRequest` has no field `Sort` (does not compile).

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

Run: `cd gui && wails generate module` — `models.ts` gains `SortSpec` + `QueryRequest.sort`. Revert `wailsjs/runtime/*` churn.
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

- [ ] **Step 2: Run — FAIL** (memBackend ignores `p.Sort`; rows come back in source order [0,1,2]).
Run: `go test ./internal/query/ -run TestMemBackend_Sorts -count=1`

- [ ] **Step 3: Implement**

In `memBackend.Query`, when `p.Sort != nil`: build the matching absolute-index slice from the bitset, sort it with `sort.SliceStable(idx, func(a,b) bool { return p.Sort.Less(m.records[idx[a]], idx[a], m.records[idx[b]], idx[b]) })` (ordinals are the indices themselves), then window `idx[offset:offset+limit]` and emit `p.Transform.Project(m.records[j], int64(j))` for each `j`. Guard the whole sorted path behind `if p.Sort != nil`; the existing ascending loop stays the `p.Sort == nil` path verbatim. Add a `sortPermCache` keyed by `(filterKey, sortPath, desc)` — copy the double-checked LRU shape of `matchBitsetFor` (memstore.go:301-332) — storing the sorted `[]int` so re-windowing does not re-sort.

- [ ] **Step 4: Run — PASS.**

- [ ] **Step 5: Prove the mutation**

Change the emit to `Project(m.records[j], int64(sortedPosition))` (stamp the display rank instead of the absolute ordinal). Run — the test fails (`Row.Index` becomes `[0 1 2]`). This is the load-bearing invariant. Restore.

- [ ] **Step 6: Assert the no-op baseline + commit**

Add `TestMemBackend_NoSortIsSourceOrder` asserting `Sort{Path:""}` returns indices `[0,1,2]` (unchanged). Run the mem package. gofmt. Commit `feat(query): memBackend serves sorted windows with a permutation cache`.

---

### Task 4: rescanBackend keys-only sorted-ordinal index (the hard tier)

**Files:**
- Modify: `internal/query/rescan.go` (Query ~:202-263; add a keys-only index builder + cache)
- Modify: `internal/query/rescan_test.go`

**Interfaces:**
- Consumes: `p.Sort`; the forward `scan()` (rescan.go:146-174) and `resolveValue`.
- Produces: rescan serving exact sorted windows via a cached `[]ordinal` permutation, memory bounded by keys (not records).

- [ ] **Step 1: Write the failing cross-tier parity test**

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

- [ ] **Step 2: Run — FAIL** (rescan ignores `p.Sort`, returns source-order window; mem returns sorted → indices differ).

- [ ] **Step 3: Implement the keys-only index**

When `p.Sort != nil`: build (once, cached per `(filterKey, sortPath, desc)`) a `[]int64` of absolute ordinals sorted by key: one forward `scan()` collecting `(resolveValue(rec, sort.segs), idx)` for every MATCHING record into a `[]struct{key any; ord int64}`, `sort.SliceStable` by `p.Sort.Less` (key + ordinal tiebreak), keep only the `[]int64` ordinals. Serve the window: take `perm[offset:offset+limit]`, put those ordinals in a set, and one forward `scan()` collecting the records at those ordinals, then emit them **in perm order** as `Project(rec, ord)`. Cache the `[]int64` on the backend (guard the total set size; no silent cap — if one is ever needed, surface it). `wantTotal` → `len(perm)` exactly. The early-stop (rescan.go:233-235) applies only to the `p.Sort == nil` path.

- [ ] **Step 4: Run — PASS** (the parity test now matches mem).

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
- Produces: exact sorted windows on SQLite — via pushed `ORDER BY <col> COLLATE BINARY, _rowid_` when pushable, else the shared Go comparator over the cursor scan.

- [ ] **Step 1: Write the failing tests** (two): (a) a pushable single-type untainted column sorts exactly and equals the memory tier on the same data; (b) a NOCASE-declared column falls back to Go-sort and STILL byte-matches the memory tier (mutation target: push it with a bare `ORDER BY <col>` → NOCASE diverges). Build both against a real modernc SQLite fixture (the file already has SQLite executability tests — mirror them).

- [ ] **Step 2: Run — FAIL** (sqlBackend ignores `p.Sort`).

- [ ] **Step 3: Implement**

Add `sortPushable(cs, cols) (string, bool)` in sqlpushdown.go: returns the `ORDER BY` fragment `"quoted_col" COLLATE BINARY [DESC], _rowid_` iff the column is single-storage-class, untainted (no DATE/DATETIME/TIMESTAMP decltype, no BLOB/time.Time), and non-Elem/non-dotted (same gates as the filter pushdown). In sqlBackend.Query: when `p.Sort != nil` and `sortPushable` → swap the `ORDER BY _rowid_` in the window SQL for the fragment (keeping `_rowid_` as the final tiebreaker). Else → fetch the matched rows via the existing cursor path and apply the Go comparator (reuse the memBackend permutation approach over the returned rows; the SQL result set is bounded by the window only if pushed, so the fallback must materialize the matched set — acceptable, same as the non-pushable filter). Cache the fallback permutation per `(filterKey, sortPath, desc)`.

- [ ] **Step 4: Run — PASS.**

- [ ] **Step 5: Prove the mutations** — drop `COLLATE BINARY` → the NOCASE test diverges from mem; push a tainted DATE column → its sorted order diverges. Restore each.

- [ ] **Step 6: gofmt + commit** `feat(query): sqlBackend pushes ORDER BY when exact, else Go-sorts`.

---

### Task 6: parquetBackend keys + SeekToRow

**Files:**
- Modify: `internal/query/parquetbackend.go` (queryUnfiltered :374-400 / readWindow :271-312 / filtered scan :424)
- Modify: `internal/query/parquetbackend_test.go`

**Interfaces:**
- Consumes: `p.Sort`; `SeekToRow`; the sort-key leaf reader.
- Produces: exact sorted windows via a keys-only physical-ordinal permutation + per-window `SeekToRow`, keeping the bounded-memory contract (:38-49).

- [ ] **Step 1: Write the failing cross-tier parity test** — a parquet fixture sorted on a numeric column equals the memory tier's sorted window (offset+limit into the middle). Every parquet test re-opens through shape's own engine; register `t.Cleanup(CloseSource)` (Windows file-handle rule).

- [ ] **Step 2: Run — FAIL** (parquet ignores `p.Sort`).

- [ ] **Step 3: Implement** — when `p.Sort != nil`: one O(N) scan collecting `(resolveValue(rec, segs), physicalOrdinal)` for matching rows (piggyback the filtered scan :424 when filtered; else a dedicated key scan replacing the SeekToRow fast path), `sort.SliceStable` by `p.Sort.Less`, keep the `[]ordinal` permutation (cached per key), then serve the window by `SeekToRow(ordinal)` per row, emitting `Project(rec, ordinal)` (absolute ordinal, not display pos). 

- [ ] **Step 4: Run — PASS.**

- [ ] **Step 5: Prove the mutation** — stamp display position into Row.Index → parity index comparison fails. Restore.

- [ ] **Step 6: gofmt + commit** `feat(query): parquetBackend sorts via keys + SeekToRow`.

---

### Task 7: App/store `setSort` seam

**Files:**
- Modify: `gui/frontend/src/lib/explorer/store.ts` (a `currentSort` module var next to `currentSearch` :149; thread into the QueryRows payload :297-298; a `setSort(spec)` calling `requery({})` mirroring `setSearch` :500-503; export it)
- Modify: `gui/frontend/src/lib/explorer/types.ts` (re-export `SortSpec`)
- Modify: `gui/frontend/src/lib/explorer/store.test.ts`

**Interfaces:**
- Consumes: the generated `query.SortSpec` (Task 2 bindings); `requery`.
- Produces: `explorer.setSort(spec: SortSpec)`, `$explorer` exposing the active sort for the header indicator; the sort threaded into every `QueryRows`.

- [ ] **Step 1: Write the failing test** — `setSort({path:"n",desc:true})` (a) sends `sort` in the next `QueryRows` payload, (b) supersedes + resets like `setSearch` (total → -1, cache cleared, resetToken bumped). Mutation: skip the `total=-1` reset in the sort path → a stale count survives. Also: `getCell`/the edit overlay still key on `row.index` after a sort (identity unchanged — assert an edit set before a sort is still addressable by the same index).

- [ ] **Step 2–5:** run FAIL → implement `currentSort` + `setSort` (mirror `setSearch` exactly, including the `onDestroy`-safe supersede) + payload threading + `types.ts` re-export → run PASS → prove the reset mutation. 

- [ ] **Step 6: check + commit** `feat(gui): store.setSort threads sort through the query like search`.

---

### Task 8: DataTable header sort affordance + indicator

**Files:**
- Modify: `gui/frontend/src/lib/explorer/DataTable.svelte` (header cell: a sort click-region distinct from the focus/scroll-to-column click, cycling none→asc→desc→none, ▲/▼ indicator reflecting `$explorer` sort)
- Modify: `gui/frontend/src/lib/explorer/DataTable.*.test.ts` (a focused header-sort test file, mocked store)

**Interfaces:**
- Consumes: `explorer.setSort`, `$explorer` active sort.
- Produces: clicking a column's sort control cycles the direction and calls `setSort`; the header shows ▲ (asc) / ▼ (desc) / none.

- [ ] **Step 1: Write the failing test** — clicking the sort control on column "n" calls `setSort({path:"n",desc:false})`; clicking again `{path:"n",desc:true}`; a third time `{path:"",...}` (clear). Mutation: the sort click also fires `focus` (scroll-to-column) → assert no `focus` event on a sort click. Follow DataTable.edit.test.ts's mocked-store harness.

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

- [ ] **Steps:** TDD each: SQL gains `ORDER BY` under a sort and omits it without one (mutation: always emit → an unsorted query shows a spurious ORDER BY); jq gains `sort_by`/`reverse` (real-jq equivalence: the jq output order equals the engine's sorted order on a fixture where they agree). Thread `Sort` through `CodegenRequest` + the store's `refreshCodegen` (so a `setSort` refreshes the panel). Commit `feat(query): codegen reflects the active sort (ORDER BY / sort_by)`.

---

### Task 10: Cross-tier parity sweep + verification

**Files:**
- Create: `internal/query/sort_parity_test.go`

- [ ] **Step 1: Write the parity sweep** — ONE shuffled fixture, opened on all four tiers (memory, rescan via BudgetMB=1, SQLite, Parquet), asserting the sorted window `[offset,limit)` (both asc and desc, on a numeric AND a string column) is byte-identical (same `Row.Index` sequence) across all four. This is the single strongest E9 guarantee. Mutation: any one backend's comparator/ordinal handling diverging fails here.

- [ ] **Step 2: Run — PASS** (all prior tasks make it green; if not, the diverging tier is a real bug to fix before proceeding).

- [ ] **Step 3: Full gate run** — `go test ./...`; `CGO_ENABLED=0 go build`; `npm run check`; `npx vitest run`; `git diff --stat go.mod go.sum` empty; `wails build` (+revert churn); `wails generate module` empty diff.

- [ ] **Step 4: Commit** `test(query): cross-tier sort parity sweep`.

---

### Task 11: Docs

**Files:** Modify `README.md` (Sort bullet in the Desktop GUI list) + `gui/README.md` (a Sort section: header click cycles none/asc/desc; exact over the whole result on every tier via the shared comparator; the gutter shows true non-contiguous source row numbers under a sort; go-to-row scrolls to the sorted position; export stays in source order for v1; single column).

- [ ] Add both bullets (concrete copy, honest about the gutter/go-to-row/export-order semantics), then commit `docs: document column sort`.

---

## Self-review (against the spec)

**Spec coverage:** comparator/CompiledSort (T1); SortSpec threading + Row.Index invariant (T2, asserted in T3/T4/T6 mutations); mem (T3); rescan keys-only index (T4); sql pushdown+fallback (T5); parquet keys+seek (T6); store setSort seam + identity preservation (T7); header ▲/▼ + no-focus (T8); codegen ORDER BY/sort_by (T9); cross-tier parity (T10, the top obligation); docs incl. gutter/go-to-row/export-order (T11). Export-source-order (non-goal) is respected by never threading sort into the export path. ✓

**Placeholder scan:** every task has concrete code or an exact file:line pattern to mirror; the one open name (`resolveValue`) is flagged in T1 with an instruction to confirm against transform.go before implementing. No TODO/TBD. ✓

**Type consistency:** `SortSpec{Path,Desc}` / `CompiledSort` / `compareValues` / `CompiledSort.Less(rec,ord,rec,ord)` used identically T1→T2→T3→T4→T5→T6; `CompiledPlan.Sort *CompiledSort` (T2) read by every backend; `explorer.setSort(SortSpec)` (T7) consumed by the header (T8); `Row.Index == absolute ordinal` asserted by a mutation in T3/T4/T6 and end-to-end in T7. ✓

**Note (deviation from the spec, an improvement):** the spec floated adding `Sort` to `Window`; this plan puts the compiled sort on `CompiledPlan.Sort` instead (compiled once in QueryRows, like `CompiledFilter`), so the `Backend.Query` signature and its two test doubles are untouched.
