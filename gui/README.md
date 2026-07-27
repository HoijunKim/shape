# shape desktop GUI

A Wails v2 desktop app that reuses shape's Go core (`internal/query`,
`internal/pipeline`) to open a data file and browse it directly: drag a file
in (or use Open), and the explorer view profiles it, then lets you scroll
real rows and drill into nested structure. It can also export the inferred
JSON Schema. Part of the same Go module as the CLI; the CLI (`shape.exe`)
never links Wails and stays cgo-free.

## The explorer view

Dropping a file (or picking one via Open/native drag-and-drop) opens it
through the query engine (`internal/query`) and switches to the explorer,
the app's only view once a file is open:

- **Structure map (left sidebar).** A tree built from the file's inferred
  field structure (`ProfileDTO.Fields`), independent of which paths made it
  into the queryable column set. A path that IS a real column is clickable
  and focusable; a path that is a pure interior object, an array-element
  preview, or otherwise dropped from the column model renders dimmed --
  visible for context, but not a column you can jump to. A field whose type
  genuinely drifts across records (e.g. a JSON value that is sometimes a
  string, sometimes an object) carries a `drift` badge.
- **Data table (center).** A hand-rolled, two-axis virtualized grid (no
  table library, per the zero-runtime-dependency constraint): only the rows
  and columns near the current scroll position are ever in the DOM, so
  memory and DOM node count stay bounded regardless of file size or column
  count. Clicking a column header focuses that column in the structure map;
  clicking a focusable row in the structure map scrolls the table to and
  highlights that column -- the focus is bidirectional, joined by column
  `path`, never by index.
- **Status bar (bottom).** The one place the app ever states a row or column
  count, so it is the one place the "never present an estimate as exact"
  rule is enforced: an exact count reads e.g. "1,234 rows"; a known-but-
  inexact total (the rescan tier, for a file too large to fully ingest)
  reads "~1,234 rows" with a leading tilde; a still-unconfirmed total reads
  "counting...". The source tier (`memory`/`rescan`/`sqlite`/`parquet`) and,
  on the rescan tier, a "large file -- streaming mode (totals are estimates)"
  warning are shown alongside. A page-fetch failure while scrolling shows as
  a dismissible/retryable alert bar above the table rather than discarding
  the whole rendered grid -- only a failure while *opening* a file replaces
  the full pane.
- **Filter bar (toggled via the header's "Filter" button).** A visual
  condition builder, no query language required: each row picks a column and
  a type-aware operator (numeric columns offer `=`/`≠`/`<`/`≤`/`>`/`≥`/
  `in list`/`is null`/`is not null`; strings offer `=`/`≠`/`contains`/
  `matches regex`/`in list`/`is null`/`is not null`, several with a
  case-insensitive toggle; booleans offer `is`/`is null`/`is not null`;
  objects/arrays/drifting-type columns offer only the two null checks, since
  those are the only comparisons that mean anything for a non-scalar value).
  Every row in the bar combines with a single AND/OR (nested groups are a
  later phase). Typing is debounced ~250ms before it reaches the query
  engine, so a query isn't re-run on every keystroke, and a half-typed or
  invalid value (an unparseable number, an empty regex) is simply never sent
  rather than erroring. Clicking the small funnel icon next to a column in
  the sidebar is the fast path in: it seeds a fresh condition for that
  column, defaulted to a sensible operator for its type, and focuses the
  value input directly. The row count in the status bar follows while a
  filter is active, with the same honesty rule as the unfiltered total: it
  reads "counting..." while the engine works it out, then "N rows" once
  exact or "~N rows" if only an estimate is available. On the memory tier
  the exact filtered count is already known from the first page, so nothing
  extra runs; on the rescan/sqlite/parquet tiers it is a real background
  scan, shown with a "Cancel" button next to "counting..." for exactly that
  reason -- cancelling leaves the last-known (inexact) estimate in place
  rather than hanging or lying about a final answer. Editing the filter
  again, or switching files, supersedes any count already in flight so a
  stale number can never land.

- **Columns panel (toggled via the header's "Columns" button).** Choose which
  columns are shown, in what order, and under what name. Hiding, reordering or
  renaming a column reprojects the table (and everything the export writes)
  without re-reading the file; "Reset" restores the source's own column set.
  Two details that are deliberate rather than incidental: a column is named by
  its FULL dotted path (`user.name`, not `name`), so two nested fields sharing
  a leaf name never collide; and the FILTER always keeps offering every base
  column, because a filter runs on the record itself, before any projection --
  hiding a column from the table does not remove it from the filter's
  vocabulary. Two columns renamed to the same thing is refused, with the reason
  shown inline, since JSON keys, a CSV header and a Parquet schema all collapse
  duplicates.
- **Export (the header's primary "Export" button).** Writes the CURRENT filter
  and column selection to JSON (one array), NDJSON, CSV, TSV or Parquet. The
  export is a fresh full pass over the file, so it is never capped by the
  interactive view: on a file too large to hold in memory, the table shows a
  window and an estimated total, and the export still contains every matching
  row. Values are written in full -- a nested object or array that the table
  shows as a truncated preview is exported complete, and numbers keep their
  exact source literal rather than passing through a float. While it runs, the
  dialog shows a live row count and a Cancel button; cancelling (or Esc) stops
  the scan and leaves NO partial file behind, because bytes go to a temporary
  file next to the destination and are moved into place only on success -- an
  existing file at that path is left untouched unless the export succeeds.
  Parquet needs one type per column, so a value that cannot be represented in
  its column's type is written as null AND reported in the dialog, rather than
  disappearing quietly; exporting as JSON or NDJSON keeps those values.
  *Known limitation:* a column whose name contains a `.` (which is the default
  for nested fields) re-opens in shape itself with empty cells, because shape's
  own reader treats a dot as nesting. The exported file is correct for every
  other consumer; re-importing into shape is the case to watch.

- **Code panel (toggled via the header's "Code" button).** The same filter and
  column selection, written out as an equivalent `jq` expression and SQL query,
  with a Copy button for each. It is the power-user hook -- take the query
  somewhere else -- and a way to learn either syntax from your own data rather
  than from a tutorial. Read-only by design: it shows what shape is doing, it
  is not a query console.
  The panel is honest about the places the three engines genuinely differ, and
  says so inline rather than quietly generating something that means something
  else: `REGEXP` needs a user-defined function SQLite does not ship (shape
  matches with Go RE2); case-insensitive matching folds ASCII only in both jq
  (`ascii_downcase`) and SQLite (`lower()`) while shape folds full Unicode; and
  for a non-database source the SQL is labelled illustrative, over an imagined
  flat table named `data`.

- **Global search (the search box above the table).** Find a value without
  knowing which column it is in: type any text and the table narrows to the
  rows where ANY scalar leaf value contains it, case-insensitively (full
  Unicode folding, not ASCII-only). It matches VALUES, never keys -- searching
  `name` does not surface every row just because they all have a `name` field.
  The box is always visible the moment a file is open (deliberately not tucked
  behind the Filter panel), and its typing is debounced the same ~250ms as the
  filter. Search combines with the visual filter by AND -- a searched, filtered
  view shows only rows that satisfy both -- and it participates in the row
  count, the export, and the Code panel exactly like the filter does: the
  status bar counts the searched rows, an export writes only them, and the jq/
  SQL updates to include the search (jq walks every scalar leaf; the SQL is an
  illustrative `instr` over the top-level columns, since a generic leaf search
  has no faithful one-line SQL). An empty box is a true no-op -- byte-identical
  to no search.
- **Cell value tree (click a truncated object/array cell).** The table shows a
  nested object or array as a ~200-character preview; hovering such a cell
  reveals an expand affordance, and clicking it opens an overlay showing the
  cell's WHOLE value as a collapsible tree -- objects as `key: value`, arrays
  as `[i]: value`, scalars coloured by kind the same way the table cells are.
  The first level or two expand by default; deeper levels open on demand, and a
  very large array renders a capped set of children plus an "N more" note so a
  100k-element value never freezes the window. A Copy button puts the value's
  exact JSON on the clipboard. It is read-only -- a way to SEE the full value a
  table cell can only hint at, not edit or filter from it. (The value is fetched
  by the cell's absolute row index, so it is the same value regardless of which
  filter or search is active.)

- **Column statistics (a field's stats toggle in the sidebar).** Each profiled
  field row in the structure map carries a small chart affordance (distinct from
  the tree caret, which expands sub-fields). Clicking it expands the field in
  place to its full profile: a distribution histogram for numeric fields, a
  top-values bar chart for low-cardinality/categorical fields, a type-mix bar, a
  presence/null meter, quantiles (median/p95, min/max), the distinct count, and
  any health flags. It is read-only and lazy — the rich card is fetched on first
  expand from the profile the backend already retained from the open-time scan
  (no rescan), reusing the same chart geometry the profiler dashboard computes.
  It describes the SOURCE field, so under a column projection a renamed/derived
  output column is not covered (its source path is), and a path with no profile
  shows "No statistics for this column".

- **Sort (click a column header).** Each header carries a small sort caret;
  clicking it cycles the column none → ascending → descending → none, with a
  ▲/▼ indicator on the active column (a header-body click still just scrolls the
  column into view). The sort is **exact over the entire result on every storage
  tier** — in-memory, SQLite, Parquet, and the >512 MiB streaming tier (which
  sorts via a bounded keys-only ordinal index, never materialising the rows), so
  it is never limited to the on-screen window. One column at a time.
  *Honest edges:* the row-number gutter shows each row's TRUE source ordinal, so
  under a sort the numbers are non-contiguous down the screen ("row 5, 900, 12")
  — that is deliberate and is what keeps editing, cell-expand and column-stats
  pointing at the right record (sort never renumbers rows). Go-to-row under a
  sort scrolls to the Nth row *in sorted order*, not source row N. Exporting
  still writes source order for now (the filter and search narrow an export;
  sort does not reorder it). Numbers compare by exact value, so a float column
  read as `float64` from Parquet sorts identically to the same value read as a
  JSON number from NDJSON.

- **Row detail (click the row number).** Clicking a row's gutter number opens
  the WHOLE source record in the same collapsible tree overlay as the cell view
  — the full, untruncated nested record (not the table's truncated preview
  cells), fetched by the row's absolute index so it is the true record
  regardless of the active projection, filter, search, or sort. Read-only, with
  a Copy button for the exact JSON.

- **Edit a cell + save a copy.** Double-click a scalar cell to edit its value in
  place. The editor validates as you go -- a number field rejects non-numeric
  text before it can be committed -- and a boolean toggles directly. A number
  keeps its EXACT source literal: the edited value is carried as a string all the
  way to disk, never through a JavaScript number, so a 19-digit id or a
  high-precision decimal survives a round-trip that `Number()` would corrupt.
  Edited cells are highlighted and their row is flagged in the gutter; an **Edit
  toolbar** appears above the table showing how many cells are edited, with
  **Revert all** and an **Edited only** toggle that swaps the grid for a diff
  list -- one entry per edited cell, `was → now`, each individually revertable.
  That list reads the edit overlay directly (keyed by absolute row index + source
  path), so it works no matter where the edited rows sit in a multi-million-row
  file, and the overlay survives filtering, searching, reshaping and scrolling.

  Editing is offered only for **unambiguous scalar columns** -- a leaf that
  appears exactly once and is not an array element -- so an edit always has one
  unambiguous place to land in the record.

  **Save a copy** (the toolbar's Save button) writes the whole file back out with
  the overlay applied, to a *new* JSON or NDJSON file. This is deliberately NOT
  the export path: it writes the ORIGINAL nested records verbatim with each edit
  placed at its SOURCE path (so `{"user":{"name":…}}` stays nested, not
  flattened), it writes EVERY row (never the filtered/reshaped view), and it
  preserves number literals. It cannot target the file you have open, so the
  original is never at risk. The dialog reports the row count and how many edits
  were applied, and warns if any could not be placed. *Out of scope for now, by
  design:* overwriting the original in place, saving to CSV/Parquet, and
  preserving object key order (a rewrite may reorder keys within an object).

## Build order (important)

`frontend/wailsjs/` (the generated Wails TypeScript bindings, from the bound
Go `App` methods) IS committed and tracked in this repo -- unlike a typical
Wails scaffold, it is not regenerated-and-gitignored, so a clean checkout can
typecheck and test the frontend without ever invoking `wails`. It must still
be kept in sync by hand: after changing any bound Go method's signature or
the DTOs it returns, regenerate and diff before committing:

    cd gui
    wails generate module        # rewrites frontend/wailsjs/ from gui/app.go
    git diff --exit-code frontend/wailsjs/   # must be empty once committed bindings are current

    cd frontend && npm ci && npm run build   # vite build -> frontend/dist
    npm run check                # svelte-check typecheck (0 errors, 0 warnings)
    npm run test                 # vitest run -- unit + component tests (jsdom), no build required

`wails dev` and `wails build` also run `wails generate module` for you, so
day-to-day:

    cd gui && wails build         # -> gui/build/bin/shape-gui.exe (Windows: cgo-free)
    cd gui && wails dev           # hot-reload dev window

## Requirements

Wails v2, Node 18+, and (on Windows) the WebView2 runtime. On macOS/Linux the
GUI build needs cgo + native webkit; the CLI stays cgo-free on every OS.

## Huge-file scrolling

Blink (the WebView2/Chromium engine) caps any element's height at ~33.5M
physical pixels, so a table that sized its scroll spacer at `HEADER_H +
total * ROW_H` would, past ~600–800k rows (the cap divided by the display's
DPR and the 28px row height), have the browser silently clamp the scrollable
area and leave most of the file unreachable. `DataTable.svelte` avoids that:
below the cap it scrolls 1:1 as usual; past it the scroll spacer is capped
and the rows switch to a **scaled** mapping — the scrollbar still spans the
whole file and the true last row seats on-screen at the bottom of a drag,
and the rows layer is natively `position: sticky` so it does not shear while
scrolling. A **Go-to-row** box (the top-left corner cell) jumps to any exact
row. The cap is recomputed when the window moves between monitors of
different scale.

Two honest caveats past the cap: dragging the scrollbar is *coarse* (each
pixel covers many rows, so land near your target and use Go-to-row for the
exact one), and a very fast flick shows a brief content-lag as the visible
rows catch up (the rows layer itself stays put — it does not shear).

## Known limitations

- **No keyboard PageUp/PageDown/Home/End yet** in the table — scroll,
  drag, and Go-to-row cover navigation; keyboard paging is a small
  follow-up.
