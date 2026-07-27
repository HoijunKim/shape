# E7: Inline cell editing, highlighting, edited-only view, and Save-a-copy

> **For agentic workers:** implement task-by-task, TDD with an explicit mutation proof per test (this repo has chronically shipped cannot-fail tests). **This plan is v2 - the v1 design (reuse the flattening/projecting export machinery for save) was adversarially reviewed and found NOT READY: it destroyed data (overwrite wrote the filtered/projected view over the source; nested paths flattened; number literals lost). v2 folds in every survivor. Do NOT route save through the export encoders, do NOT add overwrite-original, do NOT carry numbers through a JS number.**

**Goal:** shape is read-only today. Add a controlled, honest EDIT capability: change a scalar cell's value, see edited cells highlighted, view only the edited rows, and **save a copy** with the edits applied - preserving the source's nested structure and exact number literals. Every edit is tracked, revertable, and typed.

**Architecture:** an EDIT OVERLAY (never mutating the backend) + a SAVE that writes the ORIGINAL records back with edits applied at the source path (NOT the projecting export).

1. **The overlay is the single source of truth for edits** (`store.ts`): keyed by absolute `Row.Index` + the **resolvable source path** → `{ literal, original, snapshot }`, where `literal` is the exact typed value as it should serialize (a number keeps its source-literal string - JS has no `json.Number`, so a number edit is carried as its literal token, never a rounded JS `number`), and `snapshot` is the displayed `Row` captured at edit time (the row is on-screen when edited) so the edited-only view can render it without the virtualized band.
2. **Save writes the WHOLE source back with edits, as JSON/NDJSON, to a NEW file** (`internal/query`): it streams every source record (nested `map[string]any` from the reader, numbers already `json.Number`), applies the overlay by SETTING the value at the source path in the nested structure, and marshals the record verbatim - so nesting and number literals survive. It IGNORES the active filter/search/transform (those are a view; save writes the complete file). **Save-a-copy only** - the self-export guard keeps it off the open file; **no overwrite-original** (v1's critical data-loss path). No CSV/TSV/Parquet output (those flatten/retype; deferred).
3. **Frontend**: double-click a scalar cell → a type-aware inline editor (numbers carried as their literal); edited cells render highlighted with a row marker; a "Show edited only" view renders the edited rows from their snapshots; a Save control writes the edited copy.

**Tech Stack:** Go 1.25 stdlib (`encoding/json` with `UseNumber`); Svelte 3.49 + vitest. **No new dependency, cgo-free.**

**AUTHORITATIVE SOURCES:** `internal/query/columns.go` (`resolve`/`resolveSegs`/`parsePath`/`Seg`, `toCell`, cell kinds, `sanitizeValue`, `Row.Index`), `internal/query/backend.go` (the four backends' record access - `scan`/`records`), `internal/query/memstore.go` (`records []any` "never mutated"), `internal/query/rescan.go`/`sqlbackend.go`/`parquetbackend.go` (`scan(ctx, fn)`), `internal/query/exportquery.go` (`validateExportTarget`, the atomic temp-then-rename, `progressEncoder`), `internal/query/engine.go` (DTO/binding layer, `GetCell`, `sourceMetaOf`), `internal/readers` (records decode with `UseNumber` → `json.Number`), `gui/frontend/src/lib/explorer/{store.ts,DataTable.svelte,CellView.svelte,ExportDialog.svelte,Explorer.svelte,paging.ts}`.

## Decisions locked before this plan (do not relitigate)

1. **Scalar leaf edits only**, and only on an **unambiguously editable column**: its column resolves to a SINGLE, non-Elem source path (`resolveSegs` yields segs with no `Elem`), with a unique, non-empty display path. Elem/array-element columns, `$`/empty-path columns, and columns whose display path collides with another are NON-editable (no editor opens). This closes review F2 (path desync/collision) and F-elem. A scalar may be edited to another scalar of any kind (string↔number↔bool↔null) - the output is JSON, which is schemaless, so a type change is valid (no typed-format concern - save is JSON/NDJSON only).
2. **The overlay is keyed by absolute `Row.Index` + SOURCE path** (the dotted path `resolveSegs` accepts, e.g. `user.name`), NOT the display name. It holds `{ literal, original, snapshot }`. It is applied at RENDER (show + highlight the edited value) and at SAVE (set the source path in the nested record). Backends are NEVER mutated. The overlay survives filter/search/transform/scroll and is cleared only by `revertAllEdits`, a successful save that the user marks as saved, or `open()`/`close()`.
3. **Numbers keep their exact source literal end to end** (review F1/T1). The editor validates a numeric input parses as a JSON number and stores the **literal string**; `CellEdit.Value` is emitted as the bare (unquoted) number token from that string - NEVER a JS `Number()`/`JSON.stringify` of a parsed number. Go decodes `CellEdit.Value` with `json.Decoder.UseNumber()` → `json.Number`, and the writer marshals it verbatim. The fidelity test edits a value `float64` cannot hold (`9007199254740993` and a >17-digit decimal) and asserts the exact literal bytes survive.
4. **Save = write ALL source records, with edits, verbatim** (review C1/C3). It never applies the filter/search/transform (a save of "only the filtered view" over a copy is a footgun; and over the source it was the critical loss). Row count out == source record count. Nesting preserved (records are nested `map[string]any`; the writer sets the source path in place and marshals the whole record). Number literals preserved (`json.Number` marshals verbatim). NOTE (honest limitation, documented): JSON object key ORDER is not preserved (Go map decode discards it) - the saved file is semantically identical but keys may reorder; state this in the docs (decision 10).
5. **Save-a-copy only; no overwrite** (review C1/C2/C3). The output goes to a user-chosen NEW path via a file picker; `validateExportTarget` still refuses writing onto the open source file. Overwrite-original is a documented follow-up (decision 10). Output format is JSON (one array) or NDJSON (one object per line) only - matching where nested write-back is faithful. CSV/TSV/Parquet output (flat/typed) is deferred.
6. **Setting the source path in the nested record** uses a new `setAtPath(record, segs, value) (map[string]any, error)` that walks/creates the object chain for a non-Elem path and sets the leaf; it operates on a SHALLOW-COPIED spine so the backend's own records are never mutated (decision 2). A path that cannot be set (an Elem segment, or a scalar where an object is needed) is an error surfaced as `EditsUnapplied`, never a silent corruption.
7. **Edited cells are highlighted and revertable.** A cell with an overlay entry renders the edited `literal` (not the backend value) with an accent token treatment (both themes) and a row "edited" marker; a title shows the original. Reverting a cell (or editing it back to `original`) removes the entry; "Revert all" clears the overlay.
8. **"Show edited only" renders from the row SNAPSHOTS**, not the absolute-index virtualization band (review F4). Each edit captures the on-screen `Row` (the row exists on screen when a cell in it is edited); the edited-only view lists those rows (sorted by index), applying the overlay to their cells, in a simple non-virtualized list (edits are a small set). It shows regardless of the active filter (edits are the point of the view), with an empty state "No edits yet".
9. **Honesty (review F3/M2):** save reports `RowsOut`, `EditsApplied`, and `EditsUnapplied` (an edit whose record was not in the stream - impossible here since all records are written - or whose source path could not be set); a nonzero `EditsUnapplied` is surfaced, not hidden. The save dialog shows these counts.
10. **NOT in this plan (follow-ups):** overwrite-original; CSV/TSV/Parquet save; add/delete rows or columns; editing container structure or array elements; editing ambiguous/duplicate/Elem columns; multi-cell paste; undo/redo beyond per-cell revert + revert-all; preserving JSON key order.

## Global Constraints

- **Zero new dependencies, cgo-free.** No change to any read path: an empty overlay makes render and save-availability behave exactly as today; every existing test passes unchanged.
- **Every new test states its exact mutation and confirms it kills the test.**
- **Bindings regenerate** for the new `SaveRequest`/`SaveResult`/`CellEdit` DTOs; commit the `models.ts` diff with the Go change; never `git add -A` after `wails build`.
- **Commits: Conventional Commits, lowercase imperative subject, NO co-author trailer.** Branch off master; the user performs/authorises the merge.
- Gates: Go - `go build ./... && go test ./... -count=1` + gofmt. Frontend - `npm run check` + `npm run test`; component tasks add `cd gui && wails build`.

---

### Task 1: `StreamRecords`, `setAtPath`, the record writer, and `Engine.SaveEdits`

**Files:** Create `internal/query/save.go`, `save_test.go`. Modify `backend.go` (a `StreamRecords` method on `Backend`), each backend `.go`, `engine.go` (DTOs + `SaveEdits` + binding), `gui/app.go`; regenerate bindings.

**Interfaces (produces):**
- `StreamRecords(ctx, fn func(index int64, rec any) error) error` on `Backend` - streams every source record (the raw nested `map[string]any` the reader produced, numbers as `json.Number`), in absolute-index order. memBackend iterates `records`; rescan/sql/parquet wrap their existing `scan` (which already yields `(idx, rec)`), so this is a thin, uniform accessor. It NEVER filters or projects.
- `type CellEdit struct { Index int64 json:"index"; Path string json:"path"; Value json.RawMessage json:"value" }` (Value = the new typed scalar as raw JSON; numbers are a bare token).
- `type SaveRequest struct { RequestID, Handle, Format, OutPath string; Edits []CellEdit }` (Format: `json`|`ndjson`).
- `type SaveResult struct { OutPath string; RowsOut, EditsApplied, EditsUnapplied, BytesOut, ElapsedMs int64; Warnings []string }`.
- `func (e *Engine) SaveEdits(ctx, req SaveRequest, progress func(rows int64)) (SaveResult, error)`.
- `func setAtPath(rec any, segs []Seg, val any) (any, error)` (columns.go or save.go): returns a record with the leaf at `segs` set to `val`, shallow-copying only the touched object spine so the input is not mutated; errors on an Elem segment or a non-object ancestor.
- The writer: JSON (a streamed `[` … `,` … `]`) / NDJSON (one compact object per line), marshalling each edited record; `json.Number` marshals verbatim (literal preserved). Reuses `validateExportTarget` (refuse the open source path) and the atomic temp-then-rename from exportquery.go (factor a tiny shared helper if clean).

- `SaveEdits`: index the edits by `Index` → `[]CellEdit`; decode each `Value` with `UseNumber`; stream records; for each record apply its edits via `setAtPath` (counting applied/unapplied), marshal, write; report counts.

- [ ] **Step 1: Write failing tests** (`save_test.go`): a nested NDJSON source (`{"user":{"name":"a","age":30},"tag":"x"}` × N) saved with an edit to `user.name` writes the record with the NESTED structure preserved and only that leaf changed - parse the output back and assert the object shape + the one changed leaf (**mutation: write the flat projected columns instead of the nested record → the reparsed object is flat/wrong and the test fails**); **a numeric edit to `9007199254740993` (and a >17-digit decimal) survives byte-exact** (**mutation: decode `CellEdit.Value` without `UseNumber` → the literal changes and the byte assertion fails**); `RowsOut` == source record count regardless of any filter the caller might have set elsewhere (there is none in SaveEdits - assert all rows written); an edit whose path is an Elem/unsettable path is counted `EditsUnapplied`, not written, no corruption; the backend's `records` are unchanged after a save (no mutation); `validateExportTarget` refuses the open source path; an empty `Edits` writes every record unchanged (a faithful copy). Cross-backend: `StreamRecords` yields byte-identical JSON for the same logical record from mem and rescan. **Wails: regenerate, `npm run check` compiles the DTOs.**
- [ ] **Steps 2-4** (implement → PASS + `go test ./... -count=1` + gofmt). **Step 5: Commit** - `feat(query): SaveEdits writes source records back with a cell-edit overlay`.

---

### Task 2: Store - the edit overlay, snapshots, and saveEdits

**Files:** Modify `gui/frontend/src/lib/explorer/store.ts` (+ `store.test.ts`).

**Interfaces (produces):**
- `ExplorerState.edits` - a serialisable overlay `{ [index]: { [sourcePath]: { literal: string; original: unknown; snapshot: Row } } }` (or a Map), plus derived `editedCount` and `editedIndices: number[]` (sorted).
- `explorer.setEdit(index, sourcePath, literal, kind, snapshotRow): void` - records the edit, storing `original` (the current cell value) the first time and the on-screen `snapshot`; if the new value equals `original`, REMOVES the entry (editing back to the original is not an edit). `literal` is the exact serialisation token (a number's source string).
- `explorer.revertCell(index, path)`, `explorer.revertAllEdits()`, `explorer.editFor(index, path)`.
- `explorer.saveEdits(format, outPath): Promise<SaveResult>` - serialises the overlay to `CellEdit[]` (number literals as bare JSON tokens, strings quoted, bool/null literal) and calls `SaveEdits`; mirrors `runExport`'s request-id/progress/supersede discipline. Does NOT thread filter/search/transform (decision 4).

- The overlay is keyed by absolute index → survives setFilter/setSearch/setTransform/scroll; cleared only by `revertAllEdits`, `open()`/`close()`.

- [ ] **Step 1: Write failing tests** (`store.test.ts`, mock the bridge incl. `SaveEdits`): `setEdit` adds an entry, bumps `editedCount`, records the snapshot; editing back to the original REMOVES it (**mutation: keep the entry on edit-to-original → editedCount stays 1 and the test fails**); a number edit stores the exact literal string, and `saveEdits` sends it as a BARE JSON token in `CellEdit.Value` (**mutation: `JSON.stringify(Number(literal))` for a big int → the emitted token differs from the literal and the test fails**); the overlay survives a `setFilter`; `revertAll` clears it; `saveEdits` does NOT include the current filter/search in the request.
- [ ] **Steps 2-4** + `npm run check`. **Step 5: Commit** - `feat(gui): edit overlay with row snapshots and saveEdits`.

---

### Task 3: DataTable inline editing + edited-cell rendering

**Files:** Modify `gui/frontend/src/lib/explorer/DataTable.svelte`, `CellView.svelte` (+ tests). Editability uses the column's SOURCE path; the column list already carries `path`.

- Double-click / Enter on an editable SCALAR cell (decision 1) → an inline, type-aware editor; numbers keep their literal string (a text field validated as a JSON number, NOT a JS-number field); Esc cancels, Enter/blur commits via `explorer.setEdit(row.index, col.path, literal, kind, row)`. An invalid number is rejected (editor stays, marked invalid); never committed. Non-editable columns (Elem/duplicate/`$`) open no editor.
- A cell with an overlay entry renders the edited `literal` via `explorer.editFor`, with an accent highlight token (both themes) and a title showing the original; the row gutter shows an "edited" dot.

- [ ] **Step 1: Build it**, then tests (`DataTable.test.ts`): double-clicking an editable scalar cell opens an editor; committing a number calls `setEdit` with the exact literal string, not a JS number (**mutation: pass `Number(input)` → a big-int literal is altered and the test fails**); an invalid number is rejected (no `setEdit`); a cell with an overlay entry renders the edited value + the highlight class (**mutation: render the backend cell ignoring the overlay → the edited value is absent and the test fails**); an Elem/duplicate column opens no editor.
- [ ] **Step 2: BUILD.** `wails build`, `git checkout -- gui/frontend/dist/.gitkeep`. **Look at it.**
- [ ] **Step 3: Commit** - `feat(gui): inline scalar cell editing with edited-cell highlighting`.

---

### Task 4: "Show edited only" view (from snapshots) + the edit toolbar

**Files:** Modify `gui/frontend/src/lib/explorer/{Explorer.svelte, DataTable.svelte or a small EditBar}` (+ tests).

- An "Edited only" toggle that renders the overlay's rows FROM THEIR SNAPSHOTS (decision 8) - a simple non-virtualized list applying the overlay to each snapshot's cells, sorted by index, with an empty state "No edits yet". It does not touch the absolute-index virtualization / `rowAt` / `ensurePages`.
- An edit toolbar: `editedCount` ("N edited cells"), **Revert all** (`revertAllEdits`), **Save** (opens Task 5's dialog). Shown only when `editedCount > 0`.

- [ ] **Step 1: Build it**, then tests: the toggle renders exactly the edited rows from snapshots and nothing else (**mutation: render all rows / ignore the toggle → a non-edited row appears and the test fails**); the count reflects the overlay; Revert-all clears it and exits the view; the empty state shows with no edits.
- [ ] **Step 2: Commit** - `feat(gui): edited-only snapshot view and the edit toolbar`.

---

### Task 5: Save dialog (copy, JSON/NDJSON) + wiring

**Files:** Create `gui/frontend/src/lib/explorer/SaveDialog.svelte` (mirror `ExportDialog`) (+ test); wire into `Explorer.svelte`.

- A focus-trapped dialog: pick format (JSON / NDJSON only), pick a file (picker → `saveEdits(format, path)`); shows progress, then the result (`RowsOut`, `EditsApplied`, and any `EditsUnapplied` surfaced honestly - decision 9), then closes. It is "Save a copy" only (no overwrite; the picker cannot target the open source, enforced server-side by `validateExportTarget`).

- [ ] **Step 1: Build it**, then tests (`SaveDialog.test.ts`): choosing a path + format calls `saveEdits(fmt, path)`; the result panel shows `EditsApplied` AND `EditsUnapplied` (**mutation: hide `EditsUnapplied` → a dropped-edit save looks clean and the test fails**); the format selector offers only json/ndjson.
- [ ] **Step 2: BUILD + RUN.** `wails build`, edit cells across types (incl. a big-int and a nested field), save a copy, re-open it, confirm exactly those cells changed, numbers kept their literal, and nesting survived. **Look at it.**
- [ ] **Step 3: Commit** - `feat(gui): save-a-copy dialog for edited data`.

---

### Task 6: Full-stack verification and docs

**Files:** `gui/README.md`, `README.md`. Source changes only if verification finds a defect (as its own `fix(...)`).

- [ ] **Step 1: Gates.** `go test ./... -count=1` (16 pkgs); frontend `npm run check` 0 + `npm run test` green (state the count, > the current 357); `wails build`; `wails generate module` diff empty after the binding commits; `go.mod`/`go.sum` unchanged; no new deps; an UNEDITED session is byte-identical to today.
- [ ] **Step 2: The checks jsdom cannot do.** Edit cells across kinds incl. a >2^53 int and a nested `user.name`; save a copy to NDJSON and to JSON; re-open both; confirm exactly those cells changed, the big int kept its literal, and the nested structure is intact (not flattened); confirm all rows are present (not just the filtered view); confirm edited-only shows exactly the edited rows; confirm the picker cannot overwrite the open file.
- [ ] **Step 3: Docs.** Document edit / highlight / edited-only / save-a-copy in both READMEs, with the honest constraints (scalar leaves only; save is a COPY as JSON/NDJSON; nesting preserved but JSON key ORDER may change; overwrite-original and CSV/Parquet save are follow-ups). **Step 4: Commit** - `docs: document cell editing and save-a-copy`.

---

## Self-Review

**Coverage:** StreamRecords + setAtPath + record writer + SaveEdits (T1) · store overlay + snapshots + saveEdits (T2) · inline editing + highlighting (T3) · edited-only snapshot view + toolbar (T4) · save-a-copy dialog (T5) · verification + docs (T6).

**Explicitly NOT in this plan (review-driven follow-ups):** overwrite-original (v1 critical data-loss - dropped); CSV/TSV/Parquet save (flatten/retype); nested key-order preservation; add/delete rows/columns; array-element / ambiguous-column editing; undo/redo history.

**Correctness checks (each mutation-proven):** a nested source round-trips with the structure intact and only the edited leaf changed (T1, the C3 fix) · a >2^53 number keeps its exact literal (T1/T2/T3, the F1/T1 fix) · save writes ALL rows, never the filtered view (T1, the C1 fix) · save is a copy that cannot target the open file (T1) · the backend records are never mutated (T1) · editing a cell back to its original is not an edit (T2) · the overlay survives filter/search (T2) · an edited cell renders the edited value + highlight (T3) · only unambiguous scalar columns are editable (T3, the F2 fix) · edited-only shows exactly the edited rows from snapshots (T4, the F4 fix) · a dropped edit (`EditsUnapplied`) is surfaced (T5).

**The risk this plan is built around:** editing must never corrupt data or the read path. v2's guardrails: the overlay never mutates the backend; save writes the ORIGINAL nested records (not the flattening export) with number literals intact; it writes ALL rows to a NEW file (no overwrite, no filtered-subset loss); and an empty overlay makes the whole feature invisible to every existing behavior and test.
