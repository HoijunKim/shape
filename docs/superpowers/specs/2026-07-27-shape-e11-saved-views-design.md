# shape E11 - saved views

Status: design approved 2026-07-27. Next: implementation plan (writing-plans).

## Goal

Save the current query shape - filter + search + sort + column reshape - under a
name, and re-apply it later, across app restarts. The last of the four "explore
harder" features; the app's FIRST persistence.

## What a view is

A **global**, file-independent query shape (not tied to the open file, so the
same view applies to any file):

```ts
interface SavedView {
  name: string;
  filter: Filter;       // the visual filter AST (E3)
  search: string;       // the global search term (E6)
  sort: SortSpec;       // the column sort (E9)
  transform: Transform; // the column reshape: select/reorder/rename (E4)
}
```

The store already holds all four as `currentFilter` / `currentSearch` /
`currentSort` / `currentTransform`, so a view is a snapshot of those.

## Persistence - a config JSON file (user's choice)

A new Go binding pair on `App`, storing an **opaque JSON string** (the view
schema lives in the frontend; Go is a dumb, validated-by-the-frontend store):

```go
func (a *App) LoadViews() (string, error)   // "" if the file does not exist yet
func (a *App) SaveViews(payload string) error
```

- Location: `filepath.Join(os.UserConfigDir(), "shape", "views.json")` (e.g.
  `%AppData%\shape\views.json` on Windows). `SaveViews` creates the `shape` dir
  if needed and writes **atomically** (temp file in the same dir + `os.Rename`,
  mirroring `atomicWriteFile` in save.go - a rename is atomic on the same
  volume, so a crash mid-write never corrupts an existing views.json).
- `LoadViews` returns `("", nil)` when the file is absent (a fresh install is not
  an error). A read error on an existing file IS returned.
- The frontend serialises `SavedView[]` to JSON for `SaveViews` and `JSON.parse`s
  `LoadViews`; a corrupt/unparseable payload is treated as an empty list (never
  crash the app over a bad file), with a one-line console warning.

## Store

- State: `views: SavedView[]` on `ExplorerState`, loaded ONCE at store init via
  `LoadViews` (independent of any open file).
- `saveView(name)`: snapshot `{ name, currentFilter, currentSearch, currentSort,
  currentTransform }`, **upsert by name** (saving over an existing name
  replaces it), then persist via `SaveViews`.
- `applyView(name)`: look the view up, restore all four dimensions, and re-query
  ONCE. Reuse the existing setters (`setFilter`/`setSearch`/`setSort`/
  `setTransform`); their `requery` supersession makes the intermediate re-queries
  harmless (each cancels the previous in-flight; only the final completes).
  `setTransform` needs the projected `Column[]`, recomputed from the view's
  transform + the current file's `baseColumns` via the existing
  `transformModel.projectedColumns` - so a view applies to whatever file is open.
- `deleteView(name)`: remove + persist.
- Applying to a DIFFERENT file is best-effort: a filter condition on a path the
  new file lacks zero-matches (the engine already handles an absent path), and a
  reshape selecting an absent column yields empty cells. Documented, not an error.

## UI - a header "Views" menu

The header row today is `theme | Columns | Filter | Open | Schema | Export |
Code`. Add a **Views** button that toggles a small dropdown menu (a bindable
`viewsOpen` prop, routed like `filterOpen`/`exportOpen`):

- A "Save current view" row: a text input for the name + a Save button (disabled
  when the name is blank or no file is open - a view of nothing is meaningless).
- A list of saved views: each row is the name (click → `applyView`) + a `×`
  delete button. An empty state ("No saved views yet").
- Closes on Escape / outside click (mirror the existing dialogs' behaviour).

## Non-goals (v1)

- No per-file scoping, no import/export UI, no view for the open FILE itself (a
  view is a query shape, applied to whatever is open).
- No rename (delete + re-save under the new name).
- The Go side stores an opaque blob - it does not validate or migrate the view
  schema (the frontend owns it).

## Edge cases

- No file open: `applyView` is a no-op on the query (the setters guard on the
  handle); `saveView` still records the shape but Save is disabled in the UI
  until a file is open.
- Corrupt `views.json`: treated as an empty list (frontend try/catch), the app
  still starts.
- A concurrent second app instance writing `views.json`: last-write-wins (atomic
  rename), acceptable for v1 (a single-user desktop tool).
- Saving over an existing name upserts (no duplicate-name entries).

## Testing (TDD + mutation proof)

- **Go** (`App.SaveViews`/`LoadViews`): SaveViews writes to
  `UserConfigDir/shape/views.json` and LoadViews reads the exact payload back;
  LoadViews on an absent file returns `("", nil)`; SaveViews is atomic (no
  partial file survives a mid-write failure) and creates the dir. Use a
  `t.Setenv` to point the config dir at a temp dir so the test never touches the
  real user profile. Mutation: write non-atomically / to the wrong path →
  round-trip fails.
- **Store**: `saveView("v")` calls `SaveViews` with a payload whose parsed array
  contains a view carrying the CURRENT filter+search+sort+transform (mutation:
  omit `sort` from the snapshot → the saved view lacks the active sort);
  `applyView` restores all four and re-queries (mutation: skip the `sort`
  restore → the applied view is unsorted); `deleteView` removes + persists;
  upsert-by-name replaces (mutation: append instead of upsert → a duplicate).
- **UI** (`ViewsMenu`): Save with a name calls `saveView`; a view row click calls
  `applyView(name)`; `×` calls `deleteView`; Save disabled when the name is blank.

## Scope / deliverables

`App.LoadViews`/`SaveViews` + regenerated bindings; a store `views` slice +
`saveView`/`applyView`/`deleteView` + init load + `SavedView` type; a
`ViewsMenu.svelte` header dropdown; App/Header wiring for the `viewsOpen` toggle;
tests per layer; docs (both READMEs, incl. the views.json location + the
best-effort cross-file note). Branch `feat/e11-saved-views` off current master.
This touches Go (a new binding + first config-file persistence), the store, and a
new UI surface - comparable to E8 in size; the plan will decompose into ~6 tasks.
