# shape E10 — row detail view

Status: design approved 2026-07-27. Next: implementation plan (writing-plans).

## Goal

Click a row's number (the gutter cell) to open the WHOLE record as a collapsible
tree — the row-level companion to E6's per-cell value tree. It shows the full,
untruncated, nested source record (not the table's truncated/flattened preview
cells), so a user can inspect everything in one row without hunting cell by cell.

## Architecture — pure reuse of E6

No new engine method, no new binding, no new component. The pieces exist:

- **Data:** `Engine.GetCell(index, path)` (E6) already fetches a full untruncated
  value by absolute row index. With `path == ""` the resolver returns the WHOLE
  record: `resolveSegs("")` → empty segs (validatePath allows it), and
  `resolve(record, [])` returns `[record]` (columns.go:114), so `resolveFullCell`
  marshals the entire record. Verified. `store.getCell(index, "")` therefore
  returns `{ value: <whole record>, found: true }`.
- **Display:** reuse `ValueTreeOverlay` (E6) verbatim — the collapsible tree +
  Copy button, capped huge-container rendering, focus trap. Label = `Row {index}`.
- **State/guard:** Explorer already owns the overlay + a `cellReq` concurrency
  guard for `onExpandCell`. Row detail is a sibling handler `onExpandRow(index)`
  that fetches `getCell(index, "")` into the SAME overlay, sharing the same guard
  so a slow row fetch cannot land after the user opens a different row/cell.

## The trigger

`DataTable`'s gutter cell (the row number, `role="rowheader"`) has no click
handler today. Make it open the row detail:

- The gutter cell dispatches `expandRow: { index }` on click, guarded so it only
  fires for a loaded row (a skeleton gutter does nothing).
- Discoverability: `cursor: pointer` + a hover highlight + `title="Show full
  record"` on a loaded gutter cell. The edit-dot indicator is unchanged.
- No trigger collision: double-click = edit a cell (E7), the cell expand button =
  cell tree (E6), header click = focus/sort (E9). The gutter click is free.
- a11y: the gutter becomes a `role="button"` (or carries a nested button) with an
  `aria-label="Show full record for row {index}"` and keyboard activation
  (Enter/Space), mirroring how the sidebar rows and the cell expand button are
  keyboard-operable.

## Non-goals (v1)

- Read-only, like the cell tree (no inline editing in the detail view; E7 editing
  stays in the table).
- No prev/next row navigation in the overlay (a follow-up).
- Row detail shows the SOURCE record (by absolute index), independent of the
  active projection/filter/search/sort — the same index contract E6/E7/E8 use.

## Edge cases

- A gutter click on a not-yet-loaded (skeleton) row is a no-op.
- `found` is always true for a real row index; if a fetch errors, the overlay
  shows the error (the E6 overlay already has loading/error/value states).
- The concurrency guard (shared `cellReq`) drops a superseded row/cell fetch.

## Testing (TDD + mutation proof)

- **DataTable:** clicking a loaded gutter cell dispatches `expandRow` with the
  row's ABSOLUTE index (mutation: dispatch the render-slot i, not row.index →
  fails on a page-1 row); a skeleton gutter dispatches nothing.
- **Explorer:** `onExpandRow` calls `getCell(index, "")` and opens the overlay
  with the returned record + a `Row {index}` label (mutation: fetch a non-empty
  path / wrong index → wrong value); the shared `cellReq` guard drops a
  superseded row fetch (mutation: drop the guard → a stale row lands over a newer
  cell open).
- Reuse E6's existing ValueTree/overlay tests unchanged.

## Scope / deliverables

A gutter click + `expandRow` event + a11y in `DataTable.svelte`; an `onExpandRow`
handler in `Explorer.svelte` reusing the ValueTreeOverlay + `getCell(index, "")`;
tests for each; docs (both READMEs — a "Row detail" note next to the cell tree).
Branch `feat/e10-row-detail` off current master. Small; ~3 tasks.
