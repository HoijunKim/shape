# E1: internal/query Engine Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Build the `internal/query` engine core - one compiled filter+transform model served over four backends (bounded in-memory default, stateless re-scan fallback, SQLite/Parquet pushdown) - so the GUI can open any data file and query/window/filter/project actual rows within bounded memory.

**Architecture:** Per the E0 detail spec. One `CompiledPlan{Filter, Transform, Columns}` evaluated by a `Backend` chosen at open from `readers.DetectFormat`. Reuses `internal/readers` + `internal/profile`. cgo-free, no DuckDB.

**Tech Stack:** Go, stdlib + `regexp` + existing internal packages + already-vendored pure-Go `modernc.org/sqlite`, `github.com/parquet-go/parquet-go`.

**AUTHORITATIVE SPEC:** `docs/superpowers/specs/2026-07-17-shape-engine-design.md`. Every task implements a named section verbatim - exact types, semantics tables, and algorithms. Each task names its section; where a task shows code it is the deliverable, where it names a spec section transcribe/implement that section exactly.

## Global Constraints

- Package `query` at `internal/query`; imports stdlib, `regexp`, `internal/readers`, `internal/profile`, `internal/pipeline`, and the two pure-Go deps only. No cgo, no DuckDB. (Spec §9.)
- Deterministic: reader row order is authoritative; predicate/projection/codegen pure; NO Go map iterated into ordered output; first-seen column order via `columnDiscoverer` (NOT `ProfileResult.Fields`, which is alphabetized). (Spec §3, §9.)
- Bounded memory: Tier-1 in-memory store ≤ `MemBudgetBytes` (default `512<<20`), enforced per-record during ingest; over budget → downgrade to `rescanBackend` (constant memory, estimated totals). Export always streams the full file regardless of tier. (Spec §4.)
- Filter null semantics are SQL-native: missing/null → false for every comparison op; only `isnull`/`notnull` match nullish. Identical predicate across all backends. (Spec §5.)
- Constants: `MemBudgetBytes=512<<20`, `MaxColumns=512`, `previewCap=200`, cursor cache size `8`. (Spec §10.)
- Backends must return IDENTICAL rows for the same logical query (cross-backend invariant). Pushdown (SQL/Parquet/bitset/cursor) is an acceleration that must equal the Go reference. (Spec §9.)

---

### Task 1: Cell/Row/Seg core - resolve + toCell

**Files:** Create `internal/query/columns.go` (Seg, resolve, Cell, Row, toCell), `internal/query/columns_test.go`. Implement spec §3.

**Interfaces (produces):** `Seg{Key string; Elem bool}`; `resolve(record any, segs []Seg) []any`; `CellKind` consts; `Cell{Kind,Str,Num,Bool,Count,HasMore}`; `Row{Index int64; Cells []Cell}`; `toCell(v any) Cell`; `parsePath(dotted string) []Seg` (parse `a.b`, `a[]`, `$` per `profile.Flatten` grammar).

- [ ] **Step 1: Write failing tests** covering: `parsePath` on `$`, `a.b`, `user.tags[]`, a key containing `.` (bracket form); `resolve` returning 0/1 value for scalar leaf and 0..n for an `Elem` path (array membership); `toCell` classifying via `profile.KindOf` - `json.Number` → CellInt/CellFloat with both `Num` parsed AND `Str` = exact literal; `map` → CellObject with `Str`=truncated compactJSON, `Count`=len, `HasMore`; `[]any` → CellArray; empty resolve → CellMissing; `nil` → CellNull; container preview truncation at `previewCap=200`.
- [ ] **Step 2: Run - FAIL** (`go test ./internal/query/ -run 'TestResolve|TestToCell|TestParsePath' -v`).
- [ ] **Step 3: Implement** `columns.go` per spec §3 (Seg/resolve/Cell/Row/toCell, `previewCap=200`, reuse `profile.KindOf`).
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(query): cell/row/seg core with resolve and toCell`.

---

### Task 2: columnDiscoverer + ColumnModel

**Files:** Extend `internal/query/columns.go`; add tests. Implement spec §3 (Column/ColumnModel + first-seen discovery + `MaxColumns` cap).

**Interfaces:** `Column{Path,Name,Type,Nullable,Presence,Distinct,Container,Index}`; `ColumnModel{Columns []Column; segs [][]Seg; byPath map[string]int; Truncated bool; TotalPaths int}`; `columnDiscoverer` (accumulates first-seen paths during ingest); `buildColumnModel(disc, prof profile.ProfileResult) *ColumnModel`; `(*ColumnModel) resolveCol(i int, rec any) Cell`.

- [ ] **Step 1: Write failing tests:** first-seen order preserved (NOT alphabetized - feed records whose key order differs from alpha and assert column order matches first-seen); a pure-interior object path dropped but a drift (sometimes-scalar/sometimes-object) path kept and rendered as preview; `Elem` paths excluded from columns; type/nullable/presence/distinct sourced from the matching `FieldProfile` (dominant `TypeDist` kind; `mixed` when `profile.IsTypeDrift`); `MaxColumns=512` cap sets `Truncated`/`TotalPaths`, keeps by presence-desc then first-seen.
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** per spec §3.
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(query): first-seen column discovery and model`.

---

### Task 3: Filter AST + CompileFilter (SQL-native semantics)

**Files:** Create `internal/query/filter.go`, `filter_test.go`. Implement spec §5.

**Interfaces:** `Op` consts (eq,ne,lt,lte,gt,gte,contains,regex,in,isnull,notnull,bool); `ValueKind`+`Value{Kind,Str,Num,Bool,List}`; `Condition{Path,Op,Value,CaseInsensitive}`; `Combinator`(and/or); `Filter{Combinator,Conditions,Groups,Negate}`; `CompiledFilter{pred func(any)bool}`; `(*CompiledFilter) Match(rec any) bool`; `CompileFilter(f Filter, cm *ColumnModel) (*CompiledFilter, error)`.

- [ ] **Step 1: Write failing tests** - the §5 semantics table, one assertion per row + the decisive null rule:
  - eq/ne on matched type; cross-type → both false; `tags[] eq "x"` = array membership (existential).
  - lt/lte/gt/gte numeric and lexicographic; mismatched type → false (never error).
  - contains (CS + CI lowercasing); regex (RE2, compiled once); in (type-matched, empty list → false).
  - isnull/notnull on empty-set / JSON-null / present.
  - **Missing or null → false for every comparison op** (`age > 18` and `age != 18` both exclude rows lacking `age`).
  - AND/OR groups + `Negate`; empty Filter matches everything.
  - `CompileFilter` errors on bad regex / bad path (fallible work at compile; `Match` cannot error).
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** per spec §5 (compile: parse path→segs via `cm`, `regexp.Compile`, build in-sets, pre-lowercase CI; `apply` allocation-light, existential over `resolve` value set, SQL-native null).
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(query): filter AST and compiled predicate`.

---

### Task 4: Transform/projection + CompiledPlan

**Files:** Create `internal/query/transform.go`, `transform_test.go`; add `CompiledPlan` + `FilterKey` (in `backend.go` or `transform.go`). Implement spec §6.

**Interfaces:** `ColumnSpec{Path,As}`; `Transform{Select,Drop,FlattenObjects}`; `CompiledTransform{cols []outCol}`; `CompileTransform(t Transform, cm *ColumnModel) (*CompiledTransform,error)`; `(*CompiledTransform) Columns() []Column`; `(*CompiledTransform) Project(rec any, idx int64) Row`; `CompiledPlan{Filter *CompiledFilter; Transform *CompiledTransform; Columns *ColumnModel}`; `(*CompiledPlan) FilterKey() string` (canonical hash for caches).

- [ ] **Step 1: Write failing tests:** empty Select+Drop → base column set; `Select` gives exact output cols in order (reorder), `As` renames, naming a deep leaf flattens, naming a path beyond `MaxColumns` un-caps; `Drop` expanded against ColumnModel (all-but-X); `Project` returns a Row aligned to `Columns()`; `FilterKey` stable + distinct for different filter/transform.
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** per spec §6.
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(query): transform/projection and compiled plan`.

---

### Task 5: Backend interface + memBackend (Tier 1)

**Files:** Create `internal/query/backend.go` (Backend iface + Window/RowSet + RowEncoder), `internal/query/memstore.go`, `memstore_test.go`. Implement spec §4 (memBackend) + the interface.

**Interfaces:** `Window{Offset int64; Limit int}`; `RowSet{Columns,Rows,Offset,Total,TotalExact,Scanned,Truncated,ElapsedMs}`; `Backend` interface (Columns/Profile/RowCount/Query/Count/Export/Close); `newMemBackend(records []any, cm *ColumnModel, prof profile.ProfileResult) *memBackend`; a `bitset` type + per-`FilterKey` match-bitset cache.

- [ ] **Step 1: Write failing tests:** build a memBackend from decoded records; `Query` with an empty filter returns the window `[offset,offset+limit)` with `Total`=len, `TotalExact=true`; `Query` with a filter returns only matching rows, `Total`=bitset count (exact); re-query same filter reuses the cached bitset (assert same result); window past end sets `Truncated`; rows aligned to transform Columns.
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** per spec §4 memBackend (apply shared predicate over `records`, materialize only the window, cache match bitset keyed by `FilterKey`).
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(query): backend interface and in-memory backend`.

---

### Task 6: rescanBackend + OpenSource routing (JSON/CSV tiers)

**Files:** Create `internal/query/rescan.go`, `internal/query/source.go`, `internal/query/engine.go` (Engine handle registry + OpenSource + QueryRows for the mem/rescan path), tests. Modify `internal/pipeline/pipeline.go` (promote `openSource`→`OpenSource`). Implement spec §1, §4 (rescanBackend), §2 (routing).

**Interfaces:** `pipeline.OpenSource(src string) (readers.Source, func() error, error)` (promoted); `newRescanBackend(path string, fmt readers.Format, cm *ColumnModel, prof profile.ProfileResult, avgBytes float64, fileSize int64) *rescanBackend`; `Engine{...}`; `(*Engine) OpenSource(req OpenRequest) (OpenResult, error)` (ingest pass: profiler + columnDiscoverer + memStore until budget; EOF→mem, budget-hit→rescan); `(*Engine) QueryRows(req QueryRequest) (RowSet, error)`; `(*Engine) CloseSource(handle string) error`.

- [ ] **Step 1: Write failing tests:** promote-and-still-works (`pipeline` tests green); a small NDJSON opens as `memBackend` (Tier "memory", exact total); forcing a tiny `BudgetMB` on the same file downgrades to `rescanBackend` (Tier "rescan", `Sampled=true`, estimated total) AND returns the SAME rows as the mem tier for a given filter+window (cross-tier invariant); rescan window skips `[0,offset)` matches decoding only the window; `rescanBackend` unfiltered total is an estimate (`TotalExact=false`).
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** per spec §1/§2/§4. Promote `pipeline.openSource` (identical body, update the one internal caller). Engine ingest pass with `sizeOf` estimator + budget downgrade; rescan loop with cursor cache (≤8) + estimated totals.
- [ ] **Step 4: Run - PASS + full `go test ./...`** (pipeline promotion touches nothing else).
- [ ] **Step 5: Commit** - `feat(query): re-scan backend, engine, and OpenSource routing`.

---

### Task 7: sqlBackend (SQLite pushdown + Go residual)

**Files:** Create `internal/query/sqlbackend.go`, `sqlbackend_test.go`. Wire routing in `source.go`/`engine.go`. Implement spec §4 (sqlBackend). NOTE: the SQL string is generated by E5's `Codegen`; for E1, sqlBackend builds its pushable WHERE/projection inline (a minimal SQL builder) OR is limited to projection+window+count pushdown with the FULL filter as a Go residual - pick the latter if E5 codegen is not yet available, so E1 stays self-contained. Row-level correctness always comes from the shared Go predicate.

**Interfaces:** `newSQLBackend(path, table string, cm, prof) (*sqlBackend, error)` (read-only `modernc.org/sqlite`, `immutable=1`); implements `Backend`. `Query`: `SELECT <proj> FROM t ORDER BY _rowid_ LIMIT ? OFFSET ?` with the Go predicate applied over returned rows for correctness (E1 baseline); `Count` via `SELECT COUNT(*)` when the filter is empty, else Go count. Random access native (no O(offset)).

- [ ] **Step 1: Write failing tests:** open a SQLite fixture → `sqlBackend` (Tier "sqlite"); `Query` returns rows in `_rowid_` order matching the mem/rescan backends on the SAME logical rows (cross-backend invariant - build the same logical data as an NDJSON fixture and assert identical `Rows`); windowing via LIMIT/OFFSET; unfiltered `Count` exact via COUNT(*); a filtered query returns the same rows as the Go reference.
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** per spec §4 sqlBackend (read-only conn, projection+window pushdown, Go predicate for correctness; keep the pushdown boundary conservative in E1 - full SQL-WHERE pushdown is an E5 acceleration once `Codegen` exists).
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(query): sqlite backend with window pushdown`.

---

### Task 8: parquetBackend + cross-backend invariant suite

**Files:** Create `internal/query/parquetbackend.go`, `parquetbackend_test.go`; add `internal/query/testdata/` fixtures (one per format, same logical rows) + a cross-backend invariant test. Implement spec §4 (parquetBackend) + §9 testing.

**Interfaces:** `newParquetBackend(path string, cm, prof) (*parquetBackend, error)`; implements `Backend`. `Total` = Σ row-group `NumRows()` from the footer (exact, free). `Query`: compute covering row groups from the cumulative row prefix, read only needed columns + the window's rows; **VERIFY** `parquet-go` supports row-group seek/skip - if it does, use it; if not, fall back to a scan-with-early-stop to `offset+limit` (note which in the report). The row-level predicate ALWAYS runs in Go (identical results).

- [ ] **Step 1: Write failing tests:** open a Parquet fixture (≥2 row groups) → `parquetBackend` (Tier "parquet"); `Total` exact from footer; windowed `Query` returns the same rows as the other backends; projection reads only requested columns. THEN the **cross-backend invariant test:** the same logical rows encoded as ndjson + csv + sqlite + parquet, run an identical set of (filter × transform × window) queries, assert every backend returns identical `Rows` (this is the §9 headline guarantee).
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** per spec §4 parquetBackend (footer count, projection, row-group prune where min/max available, Go predicate; verify/fallback the seek).
- [ ] **Step 4: Run - PASS + full `go test ./...`.**
- [ ] **Step 5: Commit** - `feat(query): parquet backend and cross-backend invariant suite`.

---

## Self-Review

**Coverage (E1 scope from spec §10):** Seg/resolve/toCell (T1) · columnDiscoverer/ColumnModel (T2) · CompileFilter+semantics (T3) · CompileTransform/Project/CompiledPlan (T4) · memBackend+Backend iface (T5) · rescanBackend+OpenSource routing+pipeline promotion (T6) · sqlBackend (T7) · parquetBackend + cross-backend invariant (T8). Codegen (E5), streaming Export (E4), and the full Wails binding surface (§8) are SEPARATE later plans - E1 delivers the engine core + `OpenSource`/`QueryRows`/`Count` enough to drive the table view (E2). The `App`/Wails DTO layer (§8) is E2's concern; E1 exposes `Engine` methods.

**Placeholder note:** T7 deliberately keeps SQLite pushdown conservative (projection/window/count only, Go predicate for row correctness) because full SQL-WHERE pushdown depends on E5's `Codegen`; this is a stated phase boundary, not a gap - correctness is guaranteed by the shared Go predicate in every task.

**Type consistency:** `CompiledFilter`/`CompiledTransform`/`CompiledPlan`/`ColumnModel`/`Backend`/`RowSet`/`Window`/`Cell`/`Row`/`Column` names are used identically across tasks and match the spec. `Backend` (T5) is implemented by memBackend (T5), rescanBackend (T6), sqlBackend (T7), parquetBackend (T8) - all satisfying the same interface, enabling the cross-backend invariant test (T8).

**Determinism/memory checks:** first-seen columns (T2, not alphabetized `ProfileResult.Fields`); SQL-native null (T3); budget downgrade preserving rows (T6); cross-backend row-identity (T8) - each has an explicit test.
