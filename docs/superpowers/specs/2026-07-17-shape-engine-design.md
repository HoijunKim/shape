# shape Data-Explorer Engine Design (E0 detail spec)

Date: 2026-07-17
Status: Approved for planning
Author: hoijun (with Claude; judge-panel synthesis of 3 independent engine designs)

Detail spec for the `internal/query` engine behind the data-explorer pivot
(parent: `2026-07-17-shape-data-explorer-design.md` §4). Chosen by scoring three
architectures — A stateless re-scan (49), C cursor+offset-index (48), B in-memory
store (43) — then grafting: **bounded in-memory store (default) + stateless
re-scan fallback past a memory budget, sharing one compiled predicate/projection;
SQLite/Parquet routed to native pushdown; jq+SQL codegen from one model.**

## Verification & refinements (checked against the current codebase)

- `pipeline.openSource(src string) (readers.Source, func() error, error)` exists →
  promote to exported `pipeline.OpenSource` (identical body). ✔
- cgo-free deps confirmed in `go.mod`: `modernc.org/sqlite v1.53.0` (pure-Go) and
  `github.com/parquet-go/parquet-go v0.30.1` (pure-Go); the project already ships
  cgo-free (Plan 7). No DuckDB. ✔
- `readers.RecordStream` is `Next() (any, error)` + `Skipped() int` (no seek); 4
  `readers.Format` values (JSON, CSV, Parquet, SQLite) with json/ndjson and
  csv/tsv as sub-modes; `ProfileResult.Fields` is alphabetized (→ a first-seen
  `columnDiscoverer` is required for column order). ✔
- **Parquet random access refinement:** `internal/readers/parquetreader` does not
  yet use `SeekToRow`/row-group skipping. In E1, VERIFY `parquet-go` supports
  row-group seek; if not, `parquetBackend` falls back to a scan-with-early-stop
  window read. Either way the row-level predicate runs in Go, so results are
  identical — the seek is a latency optimization, not a correctness dependency.
- `sqlBackend` uses the Go **residual** for regex/nested (SQLite has no built-in
  REGEXP function); generated SQL's `REGEXP` is illustrative-with-caveat only,
  never executed. ✔

The full synthesized architecture follows verbatim.

---

I have full ground truth. One correction that shapes the whole synthesis: the codebase defines **four** `readers.Format` values — `FormatJSON`, `FormatCSV`, `FormatParquet`, `FormatSQLite`. The "six formats" are really json/ndjson (one `FormatJSON`, sub-mode chosen by `jsonreader.DetectMode`) and csv/tsv (one `FormatCSV`, comma chosen by extension). All three designs treated NDJSON/TSV as first-class formats with their own capabilities; the real capability boundary runs along the four `Format` values plus the array-vs-line JSON sub-mode. The synthesis routes on the four `Format`s. Also confirmed: `RecordStream` is only `Next() (any, error)` + `Skipped() int` (no offsets/seek), records carry values in `{nil,bool,string,json.Number,float64}` + nested `map[string]any`/`[]any`, `ProfileResult.Fields` is **sorted alphabetically** (first-seen order is discarded in `Result()`), and SQLite rows are already flat `map[string]any` while Parquet can nest.

---

# Scored comparison

| Criterion | A (re-scan) | B (in-mem store) | C (offset-index) |
|---|:--:|:--:|:--:|
| (a) cgo-free fit + reuse of readers/profiler | 9 | 7 | 8 |
| (b) huge-file memory SAFETY (never OOM / never mislead) | 9 | 6 | 9 |
| (c) interactive query/scroll speed | 5 | 9 | 8 |
| (d) testability (golden, deterministic) | 9 | 8 | 8 |
| (e) implementation simplicity / low risk | 8 | 5 | 6 |
| (f) codegen cleanliness (one model → jq+SQL) | 9 | 8 | 9 |
| **Total** | **49** | **43** | **48** |

**A** — simplest, safest, purest reuse (one `readers.Open` path, constant memory, honest estimated totals), but the base case (deep scroll / low-selectivity filter over multi-GB text) is O(offset)/O(N). Its filter/codegen spec is the most directly reusable.
**B** — fastest interaction (vectorized bitset filters, O(1) windows, exact loaded-set counts), but the columnar store + dict + promotion lattice + budget accounting is the most code/risk, and a capped interactive view can mislead if labels are ignored. Its "export is never capped, always streams full file" escape hatch is essential.
**C** — best latency-without-cap profile (O(window)+stride random access, exact text counts, footer counts), and the most precise codegen, but the on-the-fly strided offset-index build with per-format byte-offset capture (`json.Decoder.InputOffset`, `csv.Reader.InputOffset`, JSON leading-comma skipping) is fiddly and couples to reader internals.

**Graft:** default to a **bounded in-memory store (B)** for files that fit a budget — that is where interactivity is won and it is where almost every real file lands. Fall back to **stateless re-scan (A)** past the budget, sharing **one compiled predicate/projection** so both tiers are identical by construction (no vectorized-vs-scalar divergence to test). Route **SQLite and Parquet off both tiers** onto native pushdown (SQL `WHERE/LIMIT/OFFSET/COUNT`; Parquet footer count + row-group skip), taking C's exact-count and seek ideas. Keep A's codegen spec, adopting the **SQL-native null semantics** (below) because it makes the generated SQL clean. C's strided offset index is documented as an optional Tier-2 accelerator behind the same interface, not required for the plan.

---

# Synthesized engine — `internal/query`

## 1. Architecture & the trade-off it commits to

One compiled query model — `CompiledPlan{Filter, Transform, Columns}` — is evaluated by one of four **backends** chosen at open by `readers.DetectFormat`:

| `readers.Format` | Backend | Strategy |
|---|---|---|
| `FormatJSON`, `FormatCSV` | `memBackend` if the source fits the budget, else `rescanBackend` | in-RAM decoded store / stateless re-scan; **same predicate + projection** |
| `FormatSQLite` | `sqlBackend` | push filter/projection/window/count to SQL (reuse SQL codegen); regex + nested = Go residual |
| `FormatParquet` | `parquetBackend` | footer `NumRows` (exact count, free) + column projection + row-group pruning; predicate in Go |

`OpenSource` runs **one bounded ingest pass** over the streamable formats that simultaneously (a) feeds `profile.NewProfiler()` (the sidebar structure map, reused verbatim), (b) feeds a first-seen `columnDiscoverer` (column order/set — needed because `ProfileResult.Fields` is alphabetized), and (c) appends decoded records to the `memStore` **until the memory budget is hit**. If EOF arrives first → `memBackend` (full file in RAM, exact counts, O(1) windows). If the budget is hit first → the store is dropped and the handle becomes a `rescanBackend` (constant memory, estimated totals). SQLite/Parquet skip the ingest pass entirely (metadata gives structure + count).

**Trade-off, stated:** we spend **bounded RAM (default 512 MiB estimated-decoded) to buy interactive latency on typical files**, and fall back to **constant-memory O(offset)/O(N) re-scan (never OOM, honest estimated totals) on files past the budget** — with SQLite/Parquet escaping both via native pushdown, and **export always streaming the full file regardless of tier** so a capped interactive view never costs the user the complete result. This fits a cgo-free, no-DuckDB desktop tool: the in-RAM tier is the only way to get sub-frame filtering without a query engine, and the re-scan tier + streaming export keep multi-GB files correct and usable instead of OOM-ing or silently sampling. The single slow path (deep random scroll into a low-selectivity filter over a >budget text file) is rare in the open→filter→narrow→export workflow, is bounded by local-disk sequential throughput (prefix skipping counts record boundaries, decoding only the window), is accelerated by a sequential cursor cache, and is surfaced honestly ("row X of ~N", "counting…", cancellable exact count).

## 2. Package layout

```
internal/query/
  engine.go        // Engine: handle registry, OpenSource, QueryRows, CountMatches, Codegen, ExportQuery, GetCell, Cancel, CloseSource
  source.go        // uses pipeline.OpenSource (promoted); Source descriptor; backend selection
  backend.go       // Backend interface + Window/RowSet/Row/Cell + CompiledPlan
  columns.go       // columnDiscoverer (first-seen), ColumnModel, Seg, resolve, toCell
  memstore.go      // memBackend: []any decoded store, budget accounting, cached match bitset
  rescan.go        // rescanBackend: stateless scan, cursor cache, estimated totals
  sqlbackend.go    // sqlBackend: SQL pushdown via readonly modernc conn, residual in Go
  parquetbackend.go// parquetBackend: NumRows + row-group prune + projection
  filter.go        // Filter AST, Op, Value; CompileFilter -> CompiledFilter
  transform.go     // Transform, ColumnSpec; CompileTransform -> CompiledTransform
  codegen.go       // Codegen(Filter,Transform,ctx) -> {jq, sql}
  export.go        // streaming full-file export (any tier)
  dto.go           // Wails DTOs (json tags)
  testdata/        // one fixture per format sharing the same logical rows
```

**Prerequisite refactor:** promote `pipeline.openSource` → `pipeline.OpenSource` (identical body) and reuse in both packages. The engine re-opens the path per scan (each scan gets its own `*os.File`, so re-scans and concurrent scans are trivially safe). stdin (`Path==""`) is rejected at open — a stateless/re-scannable engine needs a real path (the GUI always passes one; matches the existing SQLite/Parquet readers).

## 3. Row & column representation *(deliverable 1)*

Navigation uses compiled **segments** (not a re-parsed dotted string), so keys containing `.` and array wildcards are handled correctly; the dotted string is kept only for display/codegen (byte-identical to `profile.Flatten`'s form).

```go
type Seg struct { Key string; Elem bool } // Elem == the "[]" array-element wildcard

// resolve walks record along segs, returning every value reached (the value set):
// scalar leaf -> 0 or 1 value; a path with an Elem seg -> 0..n values.
// One primitive powers cells, filters, and array membership.
func resolve(record any, segs []Seg) []any
```

```go
type CellKind string
const ( CellMissing CellKind="missing"; CellNull="null"; CellBool="bool"
        CellInt="int"; CellFloat="float"; CellString="string"
        CellObject="object"; CellArray="array" )

type Cell struct {
	Kind    CellKind `json:"kind"`
	Str     string   `json:"str,omitempty"`  // string value OR truncated compact-JSON preview for containers
	Num     float64  `json:"num,omitempty"`
	Bool    bool     `json:"bool,omitempty"`
	Count   int      `json:"count,omitempty"`   // element/key count for containers
	HasMore bool     `json:"hasMore,omitempty"` // container preview truncated
}
type Row struct {
	Index int64  `json:"index"`  // absolute record ordinal (file order / _rowid_ / parquet row order)
	Cells []Cell `json:"cells"`  // positionally aligned to RowSet.Columns
}
```

`toCell(v any)` classifies via `profile.KindOf`: `json.Number` → `CellInt`/`CellFloat` with `Num` parsed **and** `Str` set to the exact number literal (so 64-bit ints / precise decimals round-trip without float loss); `map` → `CellObject`, `[]any` → `CellArray` with `Str = truncate(compactJSON(v), previewCap=200)`, `Count=len`, `HasMore` when truncated; empty resolve set → `CellMissing`; `nil` → `CellNull`.

**Column set** (from the first-seen `columnDiscoverer`, typed from `ProfileResult`): a discovered path is a column iff it (1) contains no `Elem` seg (array elements are previews, not fixed columns — unnesting is a later transform) and (2) is not a pure interior object (an always-object path with deeper columns is dropped; a **drifting** path that is sometimes scalar sometimes object is kept and renders object occurrences as preview cells — drift is shown, not hidden). **Order = first-seen** (matches CSV header order / JSON key order; `ProfileResult.Fields` is alphabetized and cannot supply this). Type/nullable/presence/distinct come from the matching `FieldProfile` (dominant kind of `TypeDist`; `mixed` when `profile.IsTypeDrift`).

```go
type Column struct {
	Path string `json:"path"`; Name string `json:"name"`; Type string `json:"type"`
	Nullable bool `json:"nullable"`; Presence float64 `json:"presence"`
	Distinct int `json:"distinct"`; Container bool `json:"container"`; Index int `json:"index"`
}
type ColumnModel struct {
	Columns    []Column `json:"columns"`
	segs       [][]Seg  // parallel to Columns; not serialized
	byPath     map[string]int
	Truncated  bool `json:"truncated"`  // MaxColumns cap hit
	TotalPaths int  `json:"totalPaths"`
}
```

**Wide-data bound:** cap at `MaxColumns=512`, keep by presence-desc then first-seen, set `Truncated`/`TotalPaths`. Overflow paths stay addressable — naming one in `Transform.Select` overrides the cap (an explicit projection is unbounded). Discovery holds a bounded path-set, O(distinct paths) not O(rows).

## 4. Backend interface, windowed reader & memory strategy *(deliverable 2)*

```go
type Window struct { Offset int64 `json:"offset"`; Limit int `json:"limit"` }

type RowSet struct {
	Columns    []Column `json:"columns"`
	Rows       []Row    `json:"rows"`
	Offset     int64    `json:"offset"`
	Total      int64    `json:"total"`      // -1 = unknown
	TotalExact bool     `json:"totalExact"` // false = estimate or lower bound
	Scanned    int64    `json:"scanned"`
	Truncated  bool     `json:"truncated"`  // fewer than Limit rows: EOF reached
	ElapsedMs  int64    `json:"elapsedMs"`
}

type Backend interface {
	Columns() *ColumnModel
	Profile() profile.ProfileResult
	RowCount() (n int64, exact bool)                 // exact for mem/sqlite/parquet; estimate for rescan
	Query(ctx context.Context, p *CompiledPlan, w Window, wantTotal bool) (RowSet, error)
	Count(ctx context.Context, f *CompiledFilter) (total int64, exact bool, err error)
	Export(ctx context.Context, p *CompiledPlan, enc RowEncoder) (rows int64, err error)
	Close() error
}
```

**`memBackend` (Tier 1, default for fitting JSON/CSV):** holds `records []any` (decoded once at open). `Query` = compile-free apply of the shared predicate over the slice, materializing only the window. A per-`FilterKey` **match bitset** is cached (`map[string]*bitset`), so re-scrolling an unchanged filter is O(limit) and re-filtering is O(rows) over RAM. `Total` = `bitset.Count()`, **exact**. Memory bound = the decoded records (≤ budget) + one small bitset per active filter.

**`rescanBackend` (Tier 2, >budget JSON/CSV):** the stateless loop — re-open path, stream via `readers.Open`, apply predicate, skip `[0,Offset)` matches (decoding only the window; records before the window are matched but not projected), fill `Limit`, early-stop when `!wantTotal`. Memory O(Limit × columns), independent of file size. A bounded **sequential cursor cache** (≤8 hints keyed by `(FilterKey, endOffset)` → resume position) turns contiguous forward scroll from O(offset) into O(window); it is a cache (dropped on filter/source change), never required for correctness. `Total`: unfiltered = `fileSize/avgBytesPerRecord` estimate (avg from the open sample), `TotalExact=false`; filtered/`wantTotal=false` = matched-so-far lower bound; exact filtered count only via `CountMatches` (a separate cancellable full scan, memoized per `FilterKey`).

**`sqlBackend`:** keeps the read-only `modernc.org/sqlite` connection (`readonlyURI`, `immutable=1`) + table. `Query` runs `SELECT <proj> FROM t WHERE <pushable> ORDER BY _rowid_ LIMIT ? OFFSET ?` with **bound params**; `Count` runs `SELECT COUNT(*) … WHERE …` (exact). Regex and nested-path conditions become a **Go residual** applied over the returned rows (and mark filtered counts inexact until finalized). Random access is native — no O(offset).

**`parquetBackend`:** `Total` = Σ `RowGroup.NumRows()` from the footer (exact, free). `Query` computes covering row groups from the cumulative row prefix, seeks (`SeekToRow`/group skip), reads only needed columns (projection pushdown) and only the window's rows; **row-group pruning** via column-chunk min/max/null-count skips groups that cannot match range/eq/isnull conjuncts; the row-level predicate always runs in Go on surviving groups (so results are identical to the other backends). Random access O(window), file-size-independent.

**Budget & over-budget policy:** `MemBudgetBytes` default 512 MiB (configurable via `OpenRequest`). During ingest a lightweight `sizeOf(rec)` estimator increments a running total; when it would exceed the budget the store is discarded and the handle downgrades to `rescanBackend`, `OpenResult.Tier="rescan"`, `Sampled=true`, and `RowEstimate` is set. The UI shows "large file — streaming mode (totals are estimates)". **Export never downgrades results:** `ExportQuery` always streams a fresh full-file pass (§8) through the shared predicate/projection regardless of tier, so a capped interactive view never truncates the exported result. This is the honest-safety graft from B; the estimate/lower-bound totals are the honest-safety graft from A/C.

## 5. Filter model *(deliverable 3)*

```go
type Op string
const ( OpEq="eq"; OpNe="ne"; OpLt="lt"; OpLte="lte"; OpGt="gt"; OpGte="gte"
        OpContains="contains"; OpRegex="regex"; OpIn="in"
        OpIsNull="isnull"; OpNotNull="notnull"; OpBool="bool" )

type ValueKind string
const ( ValString="string"; ValNumber="number"; ValBool="bool"; ValNull="null" )
type Value struct {
	Kind ValueKind `json:"kind"`
	Str  string    `json:"str,omitempty"`; Num float64 `json:"num,omitempty"`; Bool bool `json:"bool,omitempty"`
	List []Value   `json:"list,omitempty"` // OpIn
}
type Condition struct {
	Path string `json:"path"`; Op Op `json:"op"`; Value Value `json:"value,omitempty"`
	CaseInsensitive bool `json:"ci,omitempty"` // contains/regex/string-eq
}
type Combinator string; const ( And="and"; Or="or" )
type Filter struct {                         // empty Filter matches everything
	Combinator Combinator  `json:"combinator"`
	Conditions []Condition `json:"conditions,omitempty"`
	Groups     []Filter    `json:"groups,omitempty"`
	Negate     bool        `json:"negate,omitempty"`
}
```

**Compile → pure predicate** (all fallible/expensive work once: parse `Path`→`[]Seg` against the `ColumnModel`, `regexp.Compile`, build `in`-sets, pre-lowercase CI operands). `apply` is allocation-light and cannot error → deterministic, golden-testable, and **identical across all backends** (the Go residual in sqlBackend and the Go predicate in parquet/mem/rescan are the same function).

```go
type CompiledFilter struct{ pred func(rec any) bool } // nil ⇒ match-all
func (cf *CompiledFilter) Match(rec any) bool
func CompileFilter(f Filter, cm *ColumnModel) (*CompiledFilter, error)
```

**Semantics (the decisive rule — SQL-native, so generated SQL needs no null-guards):** a condition resolves its path to a value set (existential: matches if *any* value satisfies, so `tags[] eq "x"` is array membership). **Missing or null → false for every comparison op** (`eq, ne, lt, lte, gt, gte, contains, regex, in, bool`); only `isnull` (empty set or JSON null) and `notnull` match on nullish values. This mirrors SQL `WHERE` three-valued logic exactly (`p = V`, `p <> V`, `p < V` all exclude NULL rows with no extra predicate) and is the least-surprising behavior for data users (`age > 18` and `age != 18` both exclude rows lacking `age`).

| Op | matches iff | notes |
|---|---|---|
| eq/ne | some value equals/≠ operand under matched type | cross-type ⇒ eq false ⇒ ne false (both false on mismatch) |
| lt/lte/gt/gte | numeric operand ⇒ numeric compare; string operand ⇒ lexicographic | mismatched type ⇒ false, never error |
| contains | some string value contains operand | non-strings ignored; CI lowercases both |
| regex | some string value matches RE2 | compiled once; **never pushed to SQL** |
| in | some value equals a list element (type-matched) | empty list ⇒ false |
| isnull / notnull | value set empty-or-null / present-and-non-null | |
| bool | some value == `Value.Bool` | non-bool ignored |

## 6. Transform / projection *(deliverable 4)*

```go
type ColumnSpec struct { Path string `json:"path"`; As string `json:"as,omitempty"` }
type Transform struct {
	Select         []ColumnSpec `json:"select,omitempty"`  // non-empty ⇒ exact output cols, in order (select+reorder+rename+flatten+un-cap)
	Drop           []string     `json:"drop,omitempty"`    // used only when Select empty
	FlattenObjects bool         `json:"flattenObjects"`    // default true: nested objects explode into dotted cols
}
type CompiledTransform struct{ cols []outCol }            // outCol{name, segs, col Column}
func CompileTransform(t Transform, cm *ColumnModel) (*CompiledTransform, error)
func (ct *CompiledTransform) Columns() []Column
func (ct *CompiledTransform) Project(rec any, idx int64) Row
```

`Select` expresses reorder (slice order), rename (`As`), drop (omit), flatten (name any deep leaf), and un-cap (name any path beyond `MaxColumns`). `Drop` is expanded against the `ColumnModel` at compile time (SQL cannot say "all but X"). Empty `Select`+`Drop` ⇒ the base column set. **Output record shape:** for the table, a `Row` aligned to `Columns()`; for export, a flat object `{As: value}` in `Select` order (containers serialized as nested JSON for JSON/NDJSON, compact-JSON string for CSV). `CompiledFilter`+`CompiledTransform`+`ColumnModel` bundle into `CompiledPlan` with a canonical `FilterKey()` hash for the bitset/cursor/count caches.

## 7. Codegen — one model → jq **and** SQL *(deliverable 5)*

```go
type CodegenContext struct { Format readers.Format; Table string; Cols *ColumnModel }
type Generated struct { JQ string `json:"jq"`; SQL string `json:"sql"` }
func Codegen(f Filter, t Transform, ctx CodegenContext) (Generated, error)
```

**Path encoding.** jq: identifier segment → `.a.b`; else `.["escaped"]`; `[]` → `.a.b[]`. SQL (SQLite/JSON1 dialect — the dialect we actually execute for pushdown): real top-level column → `"col"` (quotes doubled); dotted/nested → `json_extract("root",'$.rest')`; `[]` wildcard → `EXISTS(SELECT 1 FROM json_each("col") j WHERE j.value <op> V)`. For non-SQLite sources the SQL is illustrative over a table `data` with flattened quoted identifiers, prefixed by a `-- ` comment.

**Operator mapping** (null semantics from §5 make SQL guard-free; jq guards non-strings/nulls to match the Go reference):

| Op | jq | SQL |
|---|---|---|
| eq | `(.p != null and .p == V)` | `"p" = V` |
| ne | `(.p != null and .p != V)` | `"p" <> V` |
| lt/lte/gt/gte | `(.p != null and .p < V)` … | `"p" < V` … |
| contains (CS/CI) | `((.p\|type=="string") and (.p\|contains(V)))` / `…ascii_downcase…` | `instr("p",V)>0` / `instr(lower("p"),lower(V))>0` |
| regex | `((.p\|type=="string") and (.p\|test(R)))` | `"p" REGEXP 'R'` + caveat comment; **never pushed down** |
| in | `(.p as $x\|any(V1,V2,…;.==$x))` | `"p" IN (V1,V2,…)` |
| isnull / notnull | `(.p == null)` / `(.p != null)` | `"p" IS NULL` / `"p" IS NOT NULL` |
| bool | `(.p == true)` / `(.p == false)` | `"p" = 1` / `"p" = 0` |
| `[]`+op | `any(.tags[]; . <op> V)` | `EXISTS(SELECT 1 FROM json_each("tags") j WHERE j.value <op> V)` |

> **Correction (E5, verified against jq 1.7.1).** The jq templates in this
> section were written with the parentheses in the wrong place. jq's `|` is the
> LOWEST-precedence operator and rebinds `.`, so `(.p|type=="string" and
> (.p|contains(V)))` parses as `.p | (type=="string" and (.p|contains(V)))`;
> the inner `.p` then indexes the already-piped string and jq aborts with
> "Cannot index string with string" -- on precisely the rows the condition was
> meant to match, because `and` short-circuits. Every `|` inside a generated
> expression must be parenthesised on its own. Three further deviations E5
> makes deliberately, all for the same reason (this section's forms do not
> match the Go semantics in §5): `ne` and the ordering ops carry an explicit
> guard on the OPERAND's kind, since jq orders across types while the engine
> returns false on a mismatch; every path segment is `?`-suffixed and each
> condition wrapped in `(... ) // false`, so one sparse record cannot abort the
> whole stream; and `[]`-paths use `any(.tags[]?; ...)`. See
> `docs/superpowers/plans/2026-07-23-shape-e5-codegen.md` decisions 13-15.

**Groups/negate:** jq `(… and …)`/`(… or …)`/`(… \| not)`; SQL `(… AND …)`/`(… OR …)`/`NOT (…)` — always parenthesized (stable golden strings). **Value escaping:** jq strings/regex → `json.Marshal` (exact JSON escaping); SQL strings → single-quoted with `'`→`''`; numbers as canonical `strconv`; bool→`true/false` (jq) / `1/0` (SQL); null→`null`/`NULL`. `contains` uses `instr` to sidestep `%`/`_`/`ESCAPE` foot-guns. Empty `in`-list → jq `false` / SQL `1=0` with a warning (never a syntax error). **Regex caveat comment** notes RE2 (shape's authority) ≠ jq Oniguruma ≠ SQLite REGEXP-function-required.

**Projection:** jq `{ "as1": .p1, "as2": .p2 }` (order preserved) / `del(.d1,.d2)` for Drop; SQL `SELECT "p1" AS "as1", … FROM T WHERE …` / enumerated kept columns for Drop. **Full program:** jq `select(<F>) | <P>` with a per-format invocation note (`.[] |` prefix for JSON arrays, plain for NDJSON, "convert first / use SQL" for csv/parquet/sqlite); SQL whole with `WHERE` omitted when the filter is empty. `Codegen` is pure ⇒ golden-string testable; and **SQLite pushdown reuses this exact SQL** (parameterized), golden-tested to return the same rows as the Go path on shared fixtures.

**Uniform vs pushdown (the SQLite/Parquet question):** the Go predicate/projection is the single source of truth, evaluated identically on all four backends. SQLite pushes the whole thing to SQL (huge win — DB does filter+window+count) except regex/nested → Go residual. Parquet pushes projection + row-group pruning + exact `NumRows`, predicate in Go. Everything else runs the shared Go path. One model, defined once, accelerated where free.

## 8. Wails bindings + DTOs *(deliverable 6)*

```go
type App struct{ eng *query.Engine }

func (a *App) OpenSource(req OpenRequest) (OpenResult, error)
func (a *App) QueryRows(req QueryRequest) (RowSet, error)
func (a *App) CountMatches(req CountRequest) (CountResult, error) // cancellable, cached
func (a *App) Codegen(req CodegenRequest) (Generated, error)
func (a *App) ExportQuery(req ExportRequest) (ExportResult, error)
func (a *App) GetCell(req CellRequest) (json.RawMessage, error)   // full nested value for the tree view
func (a *App) Cancel(requestID string) error
func (a *App) CloseSource(handle string) error
```

```go
type OpenRequest struct {
	Path string `json:"path"`; Format string `json:"format,omitempty"`   // "" = auto
	Table string `json:"table,omitempty"`; CSVRaw bool `json:"csvRaw,omitempty"`
	BudgetMB int `json:"budgetMB,omitempty"`                              // 0 ⇒ 512
}
type OpenResult struct {
	Handle string `json:"handle"`; Format string `json:"format"`; Tier string `json:"tier"` // "memory"|"rescan"|"sqlite"|"parquet"
	Columns []Column `json:"columns"`; Profile ProfileDTO `json:"profile"`
	Sampled bool `json:"sampled"`; RowEstimate int64 `json:"rowEstimate"`; RowExact bool `json:"rowExact"`
	Warnings []string `json:"warnings,omitempty"`
}
type QueryRequest struct {
	RequestID string `json:"requestId,omitempty"`; Handle string `json:"handle"`
	Filter Filter `json:"filter"`; Transform Transform `json:"transform"`
	Offset int64 `json:"offset"`; Limit int `json:"limit"`; WantTotal bool `json:"wantTotal"`
}
type CountRequest struct { RequestID string `json:"requestId,omitempty"`; Handle string `json:"handle"`; Filter Filter `json:"filter"` }
type CountResult struct { Total int64 `json:"total"`; Exact bool `json:"exact"`; ElapsedMs int64 `json:"elapsedMs"` }
type CodegenRequest struct { Handle string `json:"handle"`; Filter Filter `json:"filter"`; Transform Transform `json:"transform"` }
type ExportRequest struct {
	RequestID string `json:"requestId,omitempty"`; Handle string `json:"handle"`
	Filter Filter `json:"filter"`; Transform Transform `json:"transform"`
	Format string `json:"format"` // json|ndjson|csv|tsv|parquet
	OutPath string `json:"outPath"`; Unflatten bool `json:"unflatten,omitempty"`
}
type ExportResult struct { OutPath string `json:"outPath"`; RowsOut int64 `json:"rowsOut"`; BytesOut int64 `json:"bytesOut"`; ElapsedMs int64 `json:"elapsedMs"` }
type CellRequest struct { Handle string `json:"handle"`; Index int64 `json:"index"`; Path string `json:"path"` }

// ProfileDTO adapts profile.ProfileResult to a TS-friendly shape (map[JSONKind]float64 -> []TypeShare).
type ProfileDTO struct { Records int `json:"records"`; Skipped int `json:"skipped"`; Fields []FieldDTO `json:"fields"` }
type FieldDTO struct {
	Path string `json:"path"`; Types []TypeShare `json:"types"`; Presence float64 `json:"presence"`
	NullRate float64 `json:"nullRate"`; Distinct int `json:"distinct"`; DistinctExact bool `json:"distinctExact"`
	Min *float64 `json:"min,omitempty"`; Max *float64 `json:"max,omitempty"`
	TopValues []ValueCount `json:"topValues,omitempty"`; Drift bool `json:"drift"`
}
type TypeShare struct { Kind string `json:"kind"`; Share float64 `json:"share"` }
```

`ExportQuery` streams a fresh full-file pass through the shared `CompiledPlan` into an encoder (`encoding/json` stream, `encoding/csv`, `parquet-go` writer) — bounded memory at any file size, and never capped by the interactive tier. Long ops (`CountMatches`, `ExportQuery`, deep `QueryRows`) emit Wails progress events `shape:progress{requestId,scanned,total}`; `Cancel` cancels the request's `context.Context`. Each scan opens its own file handle, so ops on one handle run concurrently; a mutex guards only the caches.

## 9. Constraints, determinism, testing *(deliverable 7)*

- **cgo-free / no DuckDB:** stdlib + `regexp` + `internal/readers` + `internal/profile`; the only third-party deps are the already-vendored pure-Go `modernc.org/sqlite` and `parquet-go`. ✔
- **Bounded memory:** Tier 1 ≤ `MemBudgetBytes` (enforced per-record during ingest) + small per-filter bitsets; Tier 2/export = O(window)/O(1). Over-budget → automatic downgrade to re-scan with estimated totals. ✔
- **Deterministic:** row order is each reader's fixed order (file order / `_rowid_` / parquet row order); predicate/projection/codegen are pure; first-seen column order and sorted/lookup structures avoid Go map-iteration dependence; pushdown asserted equal to the Go path. ✔
- **Golden-testable:** `testdata/` has one fixture per `Format` (json array, ndjson, csv, tsv, parquet ≥2 row groups, sqlite incl. WITHOUT ROWID) sharing the same logical rows with nested objects, arrays, nulls/missing, drift, wide-row, dotted/unicode keys. Golden `RowSet` per (format × filter × transform × window) **plus a cross-format invariant** that all backends return identical `Rows` for the same logical query, and that mem/rescan/sql/parquet agree. Golden jq+SQL strings for every op/group edge (ne, regex caveat+no-pushdown, contains-via-instr, `[]` via json_each/any, dotted-key escaping, in-lists, bool→1/0). Property tests: full-file `Match` count == golden rows across windows; early-stop == full scan; cursor-cache path == cold path; over-budget downgrade produces the same rows as a forced re-scan.

## 10. Phased TDD mapping (no architecture left open)

- **E1 (engine core):** `Seg`/`resolve`/`toCell` → `columnDiscoverer`+`ColumnModel` → `CompileFilter`+semantics table → `CompileTransform`/`Project` → `memBackend` (ingest+budget+bitset window) → `rescanBackend` (scan+cursor+estimate) → `OpenSource` routing → `sqlBackend` → `parquetBackend`. Base path first; pushdown/cursor/bitset are additive accelerations that must equal the Go reference.
- **E4 (export):** streaming `Export` per format via the shared `CompiledPlan`, full-file regardless of tier; progress+cancel.
- **E5 (codegen):** `Codegen` jq+SQL as a pure function against golden strings; then wire the same SQL (parameterized) into `sqlBackend` pushdown and golden-test row-equality with the Go path.

**Constants to encode:** `MemBudgetBytes=512<<20`, `MaxColumns=512`, `previewCap=200`, cursor cache size `8`, ingest `avgBytes` sampling for estimates. **Optional (documented, post-E1):** C's strided byte-offset index as a `rescanBackend` random-access accelerator behind the same `Backend` interface — not required for the plan.

Ground-truth anchors: `internal/readers/readers.go` (4 `Format`s, `RecordStream.Next/Skipped`, `Source`, `ToProfileValue`, `DetectFormat`), `internal/profile/{flatten,kind,accumulator,profiler}.go` (path grammar `a.b`/`a[]`/`$`, `KindOf`, `FieldProfile`, alphabetized `Result()`), `internal/pipeline/pipeline.go` (`Options`, `openSource` to promote), and the four reader packages (SQLite flat rows + `readonlyURI` + `_rowid_` order; Parquet `OpenFile`/`GenericReader`/`convertDeep` + footer `NumRows`; JSON `WholeMode`/`LineMode`; CSV header+`inferValue`).