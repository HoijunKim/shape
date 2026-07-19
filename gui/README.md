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

## Known limitations

- **Very large files hit a virtualization ceiling around ~800k rows.**
  `DataTable.svelte` sizes its scrollable content with a plain CSS
  `height: HEADER_H + total * ROW_H` on the scroll container. Blink (the
  WebView2/Chromium engine this app renders in) caps any element's height at
  ~33.5M physical pixels; divided by the display's DPR and `ROW_H` (28px),
  that ceiling lands around 800,000 rows in practice. Past it, the browser
  silently clamps the scrollable area, so on a multi-million-row file
  (confirmed live on a 4M-row source) roughly the last 80% of rows are
  unreachable by any scroll gesture -- and there is currently no go-to-row
  affordance to jump there directly either. Fixing this needs segmented or
  virtual (re-based) scrolling in `DataTable`, deferred as real follow-up
  work rather than patched around here.
