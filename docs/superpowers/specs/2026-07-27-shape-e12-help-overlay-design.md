# shape E12 - in-app help / onboarding overlay

Status: design approved 2026-07-27. Next: implementation plan (writing-plans).

## Goal

Give first-time users a way in: a **Help ("?") button** in the header that opens
an **opaque overlay** explaining every feature, and - on the very first launch -
that overlay opens automatically once. Serves the launch/onboarding goal (the
recorded GIF/screenshots remain a human task the recording environment can't do).

## The overlay

A modal `HelpOverlay.svelte`:

- **Opaque backdrop** covering the whole app (a solid dim, not the transparent
  scrim the dropdowns use), so the help is the sole focus.
- A **centered, scrollable panel** titled "shape - quick help", holding the
  feature explanations grouped into sections. Each item = a bold **name** + a
  one-line what + the **gesture** (how to reach it). Content (from the GUI
  feature set):
  - **Getting started** - Open a file (drag a JSON/NDJSON/CSV/TSV/Parquet/SQLite
    file onto the window, or the Open button); big files stream, no full load.
  - **Explore** - the virtualized table of real rows (Go-to-row jumps anywhere on
    huge files); the structure-map sidebar (every field + type/presence/distinct;
    click a field to focus its column).
  - **Shape the query** - Filter (the Filter button: type-aware AND/OR conditions
    by clicking; the funnel on a field seeds one); Search (the box above the
    table: any value, any field); Sort (a column header's ▲/▼ caret: none → asc →
    desc); Reshape (the Columns button: choose/reorder/rename output columns).
  - **Inspect** - Column stats (the chart caret on a sidebar field: histogram /
    top values / quantiles); Cell value (click a truncated object/array cell for
    the full tree); Row detail (click a row's number for the whole record).
  - **Edit & save** - Edit (double-click a scalar cell; exact number literals;
    edited cells highlight, "Edited only" lists them); Save a copy (the whole file
    written back with your edits, original untouched).
  - **Reuse & take away** - Export (the full result to 5 formats); Code (the
    equivalent jq + SQL to copy); Saved views (the Views button: save
    filter+search+sort+reshape under a name).
- Closes on `×` / Escape / backdrop click, with focus trap + restore (mirror the
  existing dialogs). Mounted at **App level** (always reachable, before any file
  is open).

## The Help button

A "?" button in the header (near the theme toggle / Views), dispatching
`toggleHelp`, `aria-pressed` reflecting `helpOpen` (mirror the Views button).

## First-launch auto-open (persisted)

On the very first launch the overlay opens once, then never again unless the user
clicks "?". The "seen" flag persists to the config dir, like saved views:

- `App.HelpSeen() (bool, error)` - true iff `<UserConfigDir>/shape/help-seen`
  exists (an absent/unreadable flag → false, never an error that blocks startup).
- `App.MarkHelpSeen() error` - creates the `shape` dir + writes the flag file
  (`os.WriteFile`, content is irrelevant; write-once, so no atomic-rename needed).
- A shared `configPath(name string) (string, error)` helper factors the
  `<UserConfigDir>/shape/<name>` join (E11's `viewsPath()` becomes
  `configPath("views.json")` - a one-line change, no behaviour change).
- Frontend: in `App.svelte`'s `onMount`, `if (!(await HelpSeen())) { helpOpen =
  true; void MarkHelpSeen(); }`, wrapped so a persistence failure just skips the
  auto-open (never crashes startup). The button path is unaffected.

## Non-goals (v1)

- No per-feature interactive coach-marks / spotlight on the real UI (a single
  static explanatory panel, not a guided tour).
- No versioned "what's new" - `help-seen` is a single boolean, not a version.
- The overlay content is static copy maintained in the component (not generated).

## Edge cases

- `HelpSeen`/`MarkHelpSeen` failing (no config dir, read-only FS) → the button
  still works; the auto-open is simply skipped (or shown again next launch). A
  persistence failure must never crash or block the app.
- Opening Help while a dropdown/dialog is open: Help is App-level and modal; its
  own Escape/backdrop close it, returning to the app.

## Testing (TDD + mutation proof)

- **Go**: `MarkHelpSeen` creates the flag file under `<configdir>/shape/` and
  `HelpSeen` then returns true; `HelpSeen` on an absent flag returns
  `(false, nil)`. `t.Setenv` redirect (APPDATA + XDG_CONFIG_HOME + HOME) so the
  real profile is untouched. Mutation: `HelpSeen` returns true unconditionally →
  the absent-flag case fails.
- **HelpOverlay**: renders the sections when open, nothing when closed; `×` /
  Escape dispatch `close`; the backdrop is opaque (a class assertion). Mutation:
  Escape doesn't close → the close test fails.
- **Header**: the "?" button dispatches `toggleHelp` and reflects `helpOpen`
  (mirror the Views-button test). Mutation: wrong event.
- **App first-run**: mount with `HelpSeen` resolving false → `helpOpen` becomes
  true and `MarkHelpSeen` is called; mount with `HelpSeen` true → it stays
  closed. Mutation: auto-open ignores `HelpSeen` → the already-seen case fails.

## Scope / deliverables

`App.HelpSeen`/`MarkHelpSeen` + a `configPath` helper (+ regenerated bindings); a
`HelpOverlay.svelte` with the grouped content; a header "?" button; App-level
`helpOpen` state + first-run auto-open; tests per layer; docs (a one-line note in
gui/README about the Help button). Branch `feat/e12-help-overlay` off current
master. Comparable to E10/E11 in size; ~4 tasks.
