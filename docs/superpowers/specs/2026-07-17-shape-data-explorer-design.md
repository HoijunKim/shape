# shape - Data Explorer Product Spec (v3, pivot)

Date: 2026-07-17
Status: Approved for planning (beyond-MVP / full product)
Author: hoijun (with Claude)

## 0. Why this pivot

P1-P3 built a visual profiler dashboard. Cold review: a "pretty profiler" is a
red-ocean local maximum - `ydata-profiling` (and DuckDB `SUMMARIZE`) already own
batch data-profiling, and prettiness is not differentiation. It did not produce
"wow / I needed this." The real goal is **real user adoption** (stars follow),
via a **broad developer pain**.

New product: reuse shape's streaming any-format engine and the Wails/Svelte GUI
as a **desktop data explorer** - "drag any data file → explore rows, filter,
transform, export, WITHOUT writing jq or SQL." The profiler becomes a supporting
"structure map," not the product. The decisive missing piece in the old
dashboard was that it showed profile *stats* but never the actual *rows* - an
explorer shows and manipulates real data.

## 1. Positioning

**"Any data file. Explore, filter, reshape, export - no jq, no SQL."**

- **Audience:** the majority of developers/analysts/ML folks who work with data
  files but bounce off jq's syntax and don't want to spin up pandas/DuckDB just
  to look at and slice a file. NOT the terminal jq power-users (small, served).
- **Wedge (what nobody bundles, zero-setup, desktop, cgo-free):** drag ANY
  format (JSON, NDJSON, CSV, TSV, Parquet, SQLite) → see a structure map + the
  actual rows → filter/transform visually → export the data AND the equivalent
  jq/SQL. Handles large files via streaming (no full load), where pandas/GUI
  viewers choke.
- **Comparables & gaps:** TablePlus / DB Browser for SQLite (DB-only), Tad
  (tabular viewer, no query/transform, stale), visidata (TUI, tabular, Python),
  jless/fx (JSON view/JS only). None do "any-format + structure-aware +
  visual filter/transform + export-with-codegen + huge-file," desktop-native.
- **Name stays `shape`:** see the *shape* of your data + *shape* (reshape) it.

## 2. Success criteria
- PRIMARY: real adoption - a dev reaches for `shape` instead of jq/pandas when
  they need to look at and slice an unfamiliar data file; installs it, uses it
  repeatedly. Stars follow use.
- The "wow": drop a gnarly 500 MB JSON → instantly browse rows, click a field to
  filter, extract the subset - without one line of jq - and get the jq/SQL for
  free.

## 3. Full feature set (beyond-MVP)

1. **Open any format** (reuse `internal/readers`): JSON, NDJSON, CSV, TSV,
   Parquet, SQLite; auto-detected; drag-drop or picker; also stdin/large files.
2. **Structure map** (reuse `internal/profile` + `internal/visual`): a field
   tree (nested paths) with type, presence/null, distinct, distribution, and
   drift/health flags. Clicking a field focuses/filters it. This is the profiler,
   demoted to a navigation sidebar.
3. **Data view - the new core:**
   - **Table view:** virtualized rows × columns (flattened paths as columns),
     tabular for any input; cell values typed/aligned; huge-file scroll.
   - **Tree view:** nested JSON/record tree with expand/collapse for deep data.
4. **Filter (visual, no jq):** a condition builder - field + type-aware operator
   + value (numeric range, string contains/regex/equals, enum select, null/not-
   null, bool), combinable with AND/OR groups; plus a global search box. Live
   result count + updated rows.
5. **Transform / reshape:** select/reorder/rename columns; flatten nested; drop
   fields; (later: derive/compute, unnest arrays, group/aggregate). Output shape
   is what you export.
6. **Export:** the filtered+transformed result to JSON/NDJSON/CSV/Parquet; AND
   the **equivalent `jq` expression and SQL query** (codegen) - the power-user
   hook and a jq/SQL learning aid.
7. **Large files:** streaming/windowed reads, bounded memory; filtering/transform
   applied during scan, never a full in-RAM load. cgo-free; NO DuckDB.
8. **Both themes**, the premium-overview + dense-detail design system from P3.

Out of scope for the explorer (kept as the existing separate capability): the
diff/drift/CI breaking-change gate (CLI + Action) stays as-is; it may later
rejoin as a "compare snapshots" tab.

## 4. Architecture (direction; deep design deferred to the engine judge-panel)

Reuse: `internal/readers`, `internal/pipeline`, `internal/profile`,
`internal/visual`, the Wails app shell, and the Svelte chart/card components.

**The one big new backend piece - a query engine over streaming data:** the
current engine only *aggregates* (profiles); the explorer must *serve rows*,
*filter*, and *project/transform* over files too large to hold in memory. This
needs a design pass (its own detail-spec via a judge-panel workflow, like P2's
VisualModel). Constraints for that design:
- Streaming / windowed: serve a virtualized row window (offset+limit) without
  loading the whole file; re-scan or index as needed; bounded memory.
- A **filter predicate** model (typed conditions → a compiled predicate applied
  during scan) and a **projection/transform** model (column select/rename/
  flatten) - both pure, testable, deterministic.
- A **codegen** module: the same filter+transform model → an equivalent `jq`
  expression and SQL string.
- cgo-free, stdlib + existing internal packages only. No DuckDB.

New frontend: a virtualized data table + tree view, a visual filter builder, a
transform panel, and export UI - layered onto the existing GUI shell, with the
profiler moved into the structure-map sidebar.

## 5. Phasing (each phase shippable; build the full product across phases)

- **E0 - Engine architecture** (judge-panel design → detail-spec): the row/query/
  filter/transform/codegen model.
- **E1 - Query engine backend:** windowed row reader + filter predicate +
  projection, over all readers; Go-tested. Wails bindings (`QueryRows`, etc.).
- **E2 - Data table view:** virtualized rows×columns in the GUI, wired to E1;
  structure-map sidebar (profiler reuse) drives column focus.
- **E3 - Visual filter:** the condition builder (type-aware ops, AND/OR) → live
  filtered rows + count.
- **E4 - Transform + export:** column select/rename/flatten + export data
  (JSON/NDJSON/CSV/Parquet).
- **E5 - Codegen:** equivalent jq + SQL for the current filter+transform; shown
  and exportable.
- **E6 - Tree view + search + polish:** nested tree, global search, dark-mode/
  palette polish, README GIF/screenshots, launch.

The existing CLI (`profile`/`schema`/`diff`) and the Action stay working
throughout; the explorer is additive on the GUI side plus the new engine.

## 6. Risks
- The query engine over huge files is the hard, novel part - accuracy + memory +
  speed must be tested (streaming re-scan vs indexing trade-off). Deferred to the
  E0 judge-panel.
- Desktop GUI adoption still requires distribution (already have GoReleaser +
  brew + npm from Plan 7); the explorer must be genuinely faster/easier than
  "open it in VS Code / pandas" to win use.
- Scope is large; strict phasing keeps each step shippable and demoable, with the
  first real "wow" screenshot at E2-E3 (rows + one-click filter).
