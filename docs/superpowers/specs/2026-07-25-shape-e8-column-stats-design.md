# shape E8 — column statistics in the explorer sidebar

Status: design approved 2026-07-25. Next: implementation plan (writing-plans).

## Goal

Give the explorer a **per-column statistics view**: click a field in the
structure-map sidebar and it expands in place to show that column's full
profile — a distribution histogram for numbers, a top-values bar chart for
categorical fields, a type-mix bar, presence/null meters, quantiles, and health
badges. This is the single most-requested "tell me about this column" affordance,
and it re-connects the explorer to `shape`'s profiler heritage without adding a
separate dashboard.

The data and the chart geometry **already exist** and are computed for free
during the open-time profiling pass; today they are dropped at the DTO boundary
or rendered by components the pivot to the explorer shelved. E8 is overwhelmingly
a *wiring + revival* feature, not new computation.

### Reuse map (what already exists)

| Layer | Asset | State today |
|---|---|---|
| P1 | `profile.FieldProfile.Histogram/Median/P95/StrLen*` (streaming centroid histogram + quantiles) | computed at open, **dropped** at the `FieldDTO` boundary |
| P2 | `internal/visual.FromProfile(ProfileResult) VisualModel` → per-field `FieldCard` (chart-form selection, equal-width display re-binning, top-k, type-mix, meters, badges) | built + golden-tested, **not routed to** in the explorer |
| P3 | `FieldDetail.svelte`, `FieldCard.svelte`, `charts/{Histogram,CategoryBars,Meter,TypeMixBar,Sparkline}.svelte` | in the repo, **no longer mounted** (explorer replaced the dashboard) |
| retained | `Backend.Profile() profile.ProfileResult` (interface method, `engine.go:407`) | live per open source; the full profile is still in memory server-side |

## Non-goals

- No recomputation or rescan. E8 reads the profile already retained by the
  backend. If a field is not in that profile, it simply has no stats.
- No new charts. Reuse `internal/visual`'s `FieldCard` and the P3 chart
  components as-is (styling adjustments only).
- No stats under a transform's *output* names. Stats describe the SOURCE field
  (the sidebar is already joined to `baseColumns`/`fields`, not the projected
  set). A renamed/derived output column is out of scope.
- No editing/filtering *from* the stats view (that is E3's job / a later idea).
  It is read-only, like the E6 value tree.

## Architecture & data flow

Mirrors the E6 cell-value-tree pattern exactly (lazy, per-item fetch, no store
state of its own beyond the fetch guard):

```
StructureMap field row (leaf) --stats toggle--> Explorer/StructureMap fetch
   -> store.getColumnStats(path)
      -> App.ColumnStats({handle, path})
         -> Engine.ColumnStats(handle, path)
            -> backend.Profile()            (already in memory)
            -> visual.FromProfile(...)      (pure geometry; optional per-handle cache)
            -> pick the FieldCard whose Path == path
   -> render FieldDetail(card) with the charts/* components
```

### Engine

New method:

```go
// ColumnStats returns the visual FieldCard for one source field, or found=false
// if no field with that path is in the retained profile.
func (e *Engine) ColumnStats(ctx context.Context, req ColumnStatsRequest) (ColumnStatsResult, error)

type ColumnStatsRequest struct {
    Handle string `json:"handle"`
    Path   string `json:"path"`
}
type ColumnStatsResult struct {
    Card  visual.FieldCard `json:"card"`
    Found bool             `json:"found"`
}
```

- Look up the backend by handle (unknown handle → error, like `GetCell`).
- Build the `VisualModel` from `backend.Profile()` via
  `visual.FromProfile(res, opts)`, then index its `Fields` by `Path`. Only the
  per-field `Fields` are consumed, so `opts` can be minimal (its
  name/format/warnings feed the whole-dataset Summary/KPIs/Badges, which E8
  ignores). `FromProfile` is pure geometry over already-computed stats, so it is
  cheap; a small per-handle cache of the built `VisualModel` (invalidated on
  close) is an allowed optimization, not required for v1.
- `Found=false` when no `FieldCard.Path == req.Path` (e.g. a projected/renamed
  column, or a path the sidebar shows but the profiler did not emit). `Card` is
  the zero value in that case; the GUI shows "No statistics for this column".
- `req.Path` matches the profiler's field path grammar (the same `fields[].path`
  the sidebar already renders), so no path re-parsing is needed — it is an exact
  string match against `FieldCard.Path`.

### Binding

`App.ColumnStats(req query.ColumnStatsRequest) (query.ColumnStatsResult, error)`
— a `reqCtx` pass-through, identical in shape to `App.GetCell`. Regenerate the
Wails bindings (`wails generate module`), committed with the Go change. The
`sourceEngine` interface gains `ColumnStats`; `gatedOpenEngine` embeds
`*query.Engine` so it needs no change (same as every prior binding).

### Store

`getColumnStats(path: string): Promise<{ card: FieldCard; found: boolean }>` —
a thin async wrapper over `App.ColumnStats`, owning no store state (the sidebar
owns the expanded/loading/error state), rejecting on failure so the caller can
show an error without disturbing anything else. Direct analog of `getCell`.

`FieldCard` (and its sub-types) are re-exported through `types.ts` from the
generated `visual` models.

### GUI (the sidebar expand)

- `TreeNode.svelte` renders each field row. Add a **stats toggle** to leaf field
  rows only, visually and behaviourally distinct from the existing `caret`
  (which expands tree *children*): the caret opens sub-fields; the stats toggle
  opens the profile panel for *this* field. A container/object row that has
  both children and its own stats may show both affordances.
- On first expand, fetch `getColumnStats(path)`; show a loading state, then
  render `FieldDetail.svelte` fed the returned `FieldCard`, which dispatches to
  the `charts/*` components by `card.form` (histogram / categorical /
  highCardString / typeMix / array / meter / empty) — exactly the dispatch the
  P3 dashboard already used.
- A concurrency guard (a request-id counter, the `cellReq` pattern from
  `Explorer.onExpandCell`) ensures a slow fetch for column A cannot land into
  the panel after the user has expanded column B.
- The P3 components were sized for a dashboard grid; the only expected changes
  are CSS to fit the 300px sidebar (and the max-width:100% / overflow-x rules).

## Edge cases & error handling

- **Path not in profile** → `Found=false` → "No statistics for this column".
- **All-null / non-numeric fields** → `FieldCard.Form` is already `empty`/`meter`
  (P2 decides the form; E8 renders whatever form comes back). No special-casing.
- **Approximate mode** (distinct via HyperLogLog past 16,384 values) → the card
  already carries the approximate distinct; render it with the existing "~"
  convention (`DistinctExact=false`), never as if exact.
- **Theme tokens** — audit every P3 chart component for stale/undefined CSS
  custom properties before shipping. Precedent: E6 shipped a `--surface-3`
  reference that resolved to nothing in both themes because the palette tops out
  at `--surface-2`. Each revived component must resolve in both light and dark.
- **Container vs leaf** — object/array container rows: v1 shows stats for any row
  whose `path` has a `FieldCard` (arrays get `FieldCard.Array`); if a container
  path has no card, the toggle is absent.

## Testing (TDD + mutation proof, per project rule)

- **Engine** (`ColumnStats`): a numeric column returns a `FieldCard` with a
  non-nil `Histogram`; an unknown path returns `Found=false`; an unknown handle
  errors. Mutation: ignore `req.Path` and return `Fields[0]` → the wrong-column
  test fails; drop the `Found` gate → the unknown-path test fails.
- **Store** (`getColumnStats`): forwards handle+path, surfaces `{card, found}`;
  a rejected binding rejects (spy engine).
- **Component** (`TreeNode`/`StructureMap`): expanding a leaf field row calls
  `getColumnStats(path)` once and renders `FieldDetail`; the concurrency guard
  drops a late response for a superseded expand. Mutation: expand dispatches the
  wrong path → the argument assertion fails; drop the guard → the late-response
  test shows column A's card under column B.
- Revived P3 components already have (or get) their own unit tests; any theme
  fix is mutation-proven the E6 way.

## Scope / deliverables

One engine method + DTO, one Wails binding (+ regenerated bindings), a store
wrapper + `types.ts` re-exports, the `TreeNode`/`StructureMap` stats toggle and
panel, the revived `FieldDetail` + `charts/*` (styling + theme audit), tests for
each layer, and docs (both READMEs). Follows the established per-phase rhythm:
plan → adversarial plan review → task-by-task TDD → whole-branch review → merge
(the user performs/authorizes the merge). Branch: `feat/e8-column-stats` off
current master.
