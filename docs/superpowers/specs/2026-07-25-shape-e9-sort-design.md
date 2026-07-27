# shape E9 - column sort in the explorer

Status: design approved 2026-07-25. Next: implementation plan (writing-plans).

Grounded in the E9 feasibility map (5 parallel backend/frontend readers, workflow wwz23jp4n).

## Goal

Click a column header to sort the table by that column - none → ascending → descending → none. Sort is **exact over the whole result on every storage tier** (memory, SQLite, Parquet, and the >512 MiB streaming rescan tier), not just the loaded window. It composes with the filter, the search, and the transform.

## The one invariant that makes this safe

`Row.Index` is the **absolute scan ordinal** and stays that way. Sort is modeled as another **query-key dimension** (like the E6 search), NOT as a renumbering of rows. A backend serves the `[offset, offset+limit)` slice of the *sorted* sequence, but every emitted `Row` still carries its true absolute index (`Project(rec, absoluteOrdinal)`, memstore.go:171). Therefore:

- `getCell(index)` (E6, store.ts:763), the edit overlay keyed by index (E7, store.ts:664 / DataTable.svelte:515-559), and `getColumnStats` (E8) **need zero change** - they already key on `row.index`, and that index is still absolute under a sort.
- The row-number gutter (DataTable.svelte:516 renders `row.index`) shows the **true source row numbers**, which under a sort are non-contiguous down the screen ("row 5, row 900, row 12"). This is correct and honest, not a bug.
- **Hard rule (a named footgun):** never stamp the sorted rank into `Row.Index`. Doing so would silently break every `getCell`/`setEdit`/stats call that keys on it.

## Non-goals (locked)

- **Single column only.** Multi-column / tiebreak-by-second-column is a follow-up. (A deterministic tiebreak on the absolute ordinal is applied internally for stability, but the user picks one column.)
- **Export stays in source order for v1.** Sort is an interactive-view concern; a full sorted export would re-sort the entire file. The filter and search still narrow the export (E4/E6); sort does not reorder it. Documented as a deliberate limitation.
- No sort on container (object/array-as-a-whole) columns beyond the first-scalar rule below.

## Architecture

### The Sort spec + threading

```go
type SortSpec struct {
    Path string `json:"path"` // "" = no sort (source order, today's behavior)
    Desc bool   `json:"desc"`
}
```

- Add `Sort SortSpec` to `QueryRequest` (engine.go:250) and to `Window` (backend.go:13). Regenerate the Wails bindings (`models.ts` QueryRequest, ~:665).
- `Engine.QueryRows` (engine.go:438-444) compiles the plan once; the sort compiles into a shared `CompiledSort` (comparator + path segs), carried on the `CompiledPlan` next to `CompiledFilter`, so every backend shares ONE ordering definition.
- Each backend's inline skip/take loop (e.g. memstore.go:156-172) is extended to serve the sorted window. `Sort.Path == ""` is the exact current code path (byte-for-byte no-op), so unsorted queries are unaffected.

### The comparator (the parity heart - the largest correctness surface)

A single `compareValues(a, b any) int` defining a **total order** over the profiler's scalar value set (`nil`, `bool`, `json.Number`, **`float64`**, `string`, plus the `Missing` sentinel), used by every Go-side sort so all tiers agree. **`float64` is not optional**: `readers.ToProfileValue` passes it through and Parquet DOUBLE / SQLite REAL columns are stored as `float64`, while the memory tier holds the same number as `json.Number` - so cross-tier parity REQUIRES treating `json.Number` and `float64` as one numeric kind, compared by exact value (both normalized to `big.Rat`). Rules:

1. **Kind order** (for mixed-type columns): `Missing` < `null` < `bool` < `number` < `string`. (A fixed, documented order; the column is usually single-type, but mixed columns must still be total and deterministic.)
2. **Numbers**: compare `json.Number` by **exact numeric value**, never via `float64` (columns.go:216-222 flags the precision loss). Parse each to a sign + integer/fraction decomposition (or `big.Rat`) and compare exactly, so a 64-bit id and a high-precision decimal order correctly.
3. **Strings**: byte-wise (Go `<`), matching `COLLATE BINARY` on the SQL side.
4. **Bools**: `false` < `true`.
5. **Elem paths** (a sort column whose path crosses a `[]`): sort by the **first array element's** scalar (transform.go:249-263 first-value rule), documented.
6. **Stability**: ties break on the absolute ordinal (ascending), so paginated windows are repeatable and cross-tier identical.
7. `Desc` reverses the comparison but keeps the ordinal tiebreak ascending (so descending is not merely the reverse array - nulls/ties stay deterministic).

This comparator is unit-tested exhaustively against golden orderings AND asserted equal to SQLite's ORDER BY output for the *pushable* subset (see SQL tier).

### Per-tier serving

**memory (memstore.go) - easy, exact.** Walk the match bitset to materialize the matching absolute-index list, resolve the sort key per record, sort the `[]int` permutation by the comparator, emit `permutation[offset:offset+limit]` as `Project(m.records[j], int64(j))`. The early `break`-at-limit (memstore.go:168) is gone under sort (all matches must be examined). A **permutation cache** keyed by `(FilterKey, sortPath, desc)` - mirroring `matchBitsetFor`'s double-checked LRU (memstore.go:301-332) - so scrolling a sorted view does not re-sort every window.

**SQLite (sqlbackend.go / sqlpushdown.go) - medium, exact via pushdown-or-fallback.** Replace `_rowid_` in the ORDER BY of the window/pushed/export SQL with `<col> COLLATE BINARY, _rowid_` (the `_rowid_` tiebreaker makes ties deterministic and cross-tier identical). Push ORDER BY **only** when the column clears the same exact-or-nothing bar the filter pushdown uses (sqlpushdown.go:36-55): single storage class, untainted (no DATE/DATETIME/TIMESTAMP decltype :429-471, no BLOB/time.Time :342-349), and `COLLATE BINARY` forced (:203-216, table_info hides a NOCASE/RTRIM collation). Otherwise **fall back to the Go comparator** over a full cursor scan (the same path the non-pushable filter takes), so the result is always exact and matches the other tiers. A column with mixed storage classes (SQLite sorts by class NULL<INT/REAL<TEXT<BLOB, which the Go comparator's kind-order must mirror OR the column is refused for pushdown) → refuse pushdown, Go-sort.

**Parquet (parquetbackend.go) - hard, exact via keys + seek.** Parquet has no value-ordered access, only physical position (`SeekToRow`). Under a sort: one O(N) scan of the sort-key leaf column collecting `(key, physicalOrdinal)` pairs (under a filter, piggyback on the existing full scan :424; unfiltered, this replaces the `SeekToRow` fast path :374-400), sort the permutation by the comparator, then serve each window row by `SeekToRow(ordinal)`. Memory stays bounded (the permutation, not the rows - preserving the :38-49 contract). Cache the sorted permutation per `(FilterKey, sortPath, desc)`.

**rescan (rescan.go) - hard, exact via a keys-only ordinal index (the scoped choice).** The streaming tier has no seek and its records were dropped because they exceeded 512 MiB (source.go:194-208), so a full in-memory row sort is impossible. Instead build a **keys-only sorted-ordinal index**: on the first sorted query, one forward pass collects `(sortKey, absoluteOrdinal)` for every matching record (keys of one column + an int64 ordinal - far smaller than the full records, so it fits where the records did not), sort by the comparator, and **cache** the resulting `[]ordinal` per `(FilterKey, sortPath, desc)`. Serving a window `[offset, offset+limit)` = take `perm[offset:offset+limit]` (the target absolute ordinals) and one forward pass fetching those records (rescan has no seek, so it re-scans, collecting the target ordinals; bounded by the max ordinal in the window). The early-stop optimization (rescan.go:233-235) is necessarily dead under sort (every sorted page examines all matches to build/consult the index), documented as the cost of exact huge-file sort. `wantTotal` is a free byproduct once the index exists (its length is the exact match count).

### Frontend (store + DataTable)

- **Store seam = the search seam, exactly.** A module-level `currentSort: SortSpec` (store.ts:149, next to `currentSearch`), threaded into the `QueryRows` payload (store.ts:297-298), and a `setSort(spec)` that calls `requery({})` - which already does the whole dance (`++gen` supersede, cancel + clear in-flight, `cache.clear()`, `total = -1`, `resetToken++` to scroll to the top, `refreshCodegen`) (store.ts:457-489, mirroring `setSearch` :500-503).
- **Header click** cycles `none → asc → desc → none` for that column (clicking a different column starts it at `asc`), with a ▲/▼ indicator in the header. The existing header click already dispatches `focus` (scroll-to-column); sort is a distinct affordance (a small sort caret/click-region) so it does not fight the focus dispatch - resolved the way TreeNode separates the caret from the row body.
- **Huge-file scaled scroll (V1)** is unaffected: it maps `scrollTop → row range → fetch sorted [offset,limit]`; the backend serves the sorted window. `go-to-row` under a sort scrolls to the **sorted position N** (not absolute index N) - documented, since row numbers are non-contiguous under a sort.

### Codegen (E5)

The Code panel reflects the sort so the copied query still means the same thing: SQL gains `ORDER BY <col> [DESC]` (with the same identifier quoting E5 uses). jq needs the **aggregating** form - `sort_by` requires an array, so the per-record streaming pipeline is wrapped `[ … ] | sort_by(.<path>) | reverse? | .[]` (appending `| sort_by` to the record stream errors at runtime). Single column. Sort DOES introduce new disclosed divergences (a sort-specific caveat is emitted, like the existing case-insensitive/type-guard notes): jq's `sort_by`/`reverse` orders **descending ties** and **missing-vs-null** differently from `compareValues`, and loses **big-integer** precision. The real-jq equivalence test is restricted to a fixture where the tiers provably agree (single-type, all-distinct, present keys).

## Edge cases & error handling

- `Sort.Path == ""` → today's exact source-order path (the no-op baseline every backend keeps).
- A sort column that is **absent** from a record → its key is `Missing` (orders first, per the kind-order).
- **null vs Missing** → distinct positions (Missing < null).
- **Approximate/huge**: the rescan keys-only index is bounded by (N × key-size + N × 8 bytes); for a pathological all-distinct-huge-string column this could be large but is still far under the full-record size that forced the tier. No silent cap - if a hard cap is ever needed it must be surfaced, not silent.
- **Sort + filter + search compose**: the comparator sorts the matched set; the match set is exactly what filter+search already produce. The permutation cache key includes the `FilterKey` (which already folds in search, E6), so a search change invalidates the sorted index correctly.
- **Cross-tier parity is the top test obligation**: the same fixture opened on memory vs rescan (via BudgetMB=1) vs SQLite vs Parquet must return byte-identical sorted windows.

## Testing (TDD + mutation proof, per project rule)

- **Comparator**: golden total-order tests over mixed types (Missing/null/bool/number/string), exact big-int ordering (a mutation using `float64` reorders `9007199254740993` vs `...992`), string byte order, Elem first-value, the descending + ordinal-tiebreak determinism. This is the densest test surface.
- **Per backend**: a sorted window equals the comparator's ordering of the matched set; the permutation/keys-only cache returns identical results on a second window (mutation: bypass the cache → still correct but proves the cache path is exercised via a spy); rescan's keys-only index produces the SAME sorted window as the memory tier on an identical fixture (the cross-tier parity test, forced onto the rescan tier with BudgetMB=1 as E4/E5/E6 do).
- **SQL pushdown**: a pushable column's ORDER BY result equals the Go comparator's (mutation: drop `COLLATE BINARY` → a NOCASE column diverges); a tainted/mixed column falls back to Go-sort and still matches (mutation: push it anyway → divergence).
- **Row.Index invariant**: under a sort, the emitted rows' `Row.Index` values are the absolute ordinals (a permutation), NOT `0..limit` (mutation: stamp sorted rank → getCell for a visible row returns the wrong cell - an end-to-end identity test).
- **Store**: `setSort` supersedes + resets + recounts like `setSearch` (mutation: skip the `total=-1` reset → a stale count survives a sort change); the overlay/getCell survive a sort (identity).
- **Frontend**: header click cycles none→asc→desc→none with the ▲/▼ indicator; a sort does not fire the focus/scroll-to-column dispatch.
- **Codegen**: SQL `ORDER BY` + jq `sort_by` appear under a sort and are absent without one; real-jq/real-SQLite equivalence for a single-column sort where the tiers agree.

## Scope / deliverables

The comparator + `CompiledSort`; the `SortSpec` DTO threaded through `QueryRequest`/`Window`/`CompiledPlan` + regenerated bindings; the four backends' sorted-window serving (mem permutation, sql pushdown+fallback, parquet keys+seek, rescan keys-only index) each with its cache; the store `setSort` seam; the DataTable header sort affordance + ▲/▼ indicator; codegen `ORDER BY`/`sort_by`; docs (both READMEs, incl. the honest gutter/go-to-row/export-order notes). Follows the per-phase rhythm: plan → adversarial plan review → task-by-task TDD → whole-branch review → merge (the user performs/authorizes the merge). Branch: `feat/e9-sort` off current master. This is a large phase (comparable to E4/E5); the plan will decompose it into ~9-11 tasks, engine-core-first so the frontend has an exact backend to drive.
