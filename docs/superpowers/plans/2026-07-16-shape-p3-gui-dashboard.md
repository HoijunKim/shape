# P3: GUI Visual Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. Frontend/visual tasks end with a `wails build` + screenshot verification, not only a unit test.

**Goal:** Turn the existing Wails+Svelte GUI (a plain profile table) into a gorgeous **visual data dashboard** that consumes the P2 `internal/visual` VisualModel - the star-bearing screenshot. Drop one file → premium overview; click a field → dense cockpit detail; drop two files → visual breaking-change diff.

**Aesthetic (decided):** **Hybrid** - clean premium (Linear/Vercel: whitespace, muted surface, one accent, crisp type, soft-rounded tiles) at the OVERVIEW level; dense data-cockpit (Grafana/Observable: information-rich, full charts, all stats) at the DETAIL level when a card is expanded. Theme-aware light + dark.

**Palette (validated, locked - dataviz `validate_palette.js`):**
- Categorical (type kinds; frontend maps `TypeSegment.Series`/`FieldCard.Kind` → slot): number `#2a78d6`/`#3987e5`, string `#1baf7a`/`#199e70`, bool `#eda100`/`#c98500`, array `#4a3aa7`/`#9085e9`, object `#eb6834`/`#d95926` (light/dark). Light worst-adjacent CVD ΔE 47.2; dark all ≥3:1. aqua+yellow are <3:1 on light → **relief rule**: charts carry visible direct labels (already in the model as `Percent`/`Count` text).
- Status (fixed, never themed; frontend maps `Severity` → color): good `#0ca30c`, warning `#fab219`, serious `#ec835a`, critical `#d03b3b`. Always paired with `Icon`+`Label` from the model, never color-alone.
- Surfaces: light `#fcfcfb` / dark `#1a1a19`; text primary `#0b0b0b`/`#ffffff`, secondary `#52514e`/`#c3c2b7`.

**Tech Stack:** Go + Wails v2 (backend bindings), Svelte + TypeScript + Vite (frontend), inline SVG charts (no chart lib - CSP-free, self-contained).

## Global Constraints

- The Go core is untouched; `internal/visual` is the contract. `gui/app.go` becomes a thin adapter that calls `visual.FromProfile`/`visual.FromDiff` and returns those types directly (Wails regenerates `wailsjs/go/models.ts` from them - the frontend consumes the exact VisualModel shape). Do NOT hand-write TS models.
- Charts are inline SVG in Svelte components; geometry comes from the model as fractions in `[0,1]` (`Frac`/`Offset`/`SparkPoint`) - components do `frac × extent`, never recompute stats. Direct value labels come from the model's preformatted `*Text`/`Percent`/`Count`.
- Renderer contract notes from the P2 spec (`2026-07-16-shape-p2-visualmodel-design.md` §8/§9): `card.typeMix` is `null` when `card.form === "empty"` - gate on `form !== "empty"`. Per-bar `Percent` may not sum to 100 (use `Frac` for widths). Missing-value glyph is `-` (already in model text).
- Theme-aware: define light as default, dark via `@media (prefers-color-scheme: dark)` AND a `data-theme` toggle scope (dark must win both ways per dataviz). Charts validated in BOTH modes.
- Every plotted mark gets a hover tooltip. A legend/label carries identity, never color alone.
- Wails build order: `frontend` builds first (`wails build` handles it); binding changes require regenerating (`wails generate module` or a full `wails build`).

---

### Task 1: Backend - app.go returns VisualModel + DiffFiles

**Files:**
- Modify: `gui/app.go` (replace the ProfileView/FieldView view structs + `ProfileFile` mapping with a `visual.FromProfile` call; add `DiffFiles`)
- Modify: `gui/app_test.go`
- Regenerated (by wails, do not hand-edit): `gui/frontend/wailsjs/go/models.ts`, `.../App.d.ts`, `.../App.js`

**Interfaces:**
- Produces (Wails bindings): `ProfileFile(path string) (visual.VisualModel, error)`, `DiffFiles(oldPath, newPath string) (visual.DiffVisualModel, error)`, unchanged `SchemaJSON`, `OpenFileDialog`, `SaveText`.

- [ ] **Step 1: Write/adapt the backend test** in `gui/app_test.go`: `ProfileFile` on a testdata NDJSON returns a `visual.VisualModel` with non-empty `Summary`, `KPIs` (len 5), and `Fields`; `DiffFiles` on two fixtures returns a `visual.DiffVisualModel` with the expected `Breaking`/`Verdict`. (Reuse or copy a small fixture into `gui/testdata/`.)
- [ ] **Step 2: Run - FAIL** (`go test ./gui/` - DiffFiles undefined / wrong return type).
- [ ] **Step 3: Implement** `gui/app.go`:
  - Delete `FieldView`/`ValueView`/`ProfileView`/`toView`.
  - `ProfileFile`: `r, err := pipeline.Profile(pipeline.Options{Path: path, Format: "auto"})`; on success `return visual.FromProfile(r, visual.Options{Name: r.Source}), nil` (Name empty→FromProfile derives; Source is set by pipeline).
  - `DiffFiles(oldPath, newPath)`: profile both (`pipeline.Profile` each), `d := diff.Diff(oldR, newR)` (set `d.Old`/`d.New` labels to the base filenames if pipeline doesn't), `return visual.FromDiff(d), nil`. (Check `internal/cmd/diff.go` for how the CLI wires source labels + the `diff.Diff` call, and mirror it.)
  - Keep `SchemaJSON`/`OpenFileDialog`/`SaveText`.
- [ ] **Step 4: Run - PASS** (`go test ./gui/`). Regenerate bindings: `cd gui && wails generate module` (or defer to the Task 7 build). Confirm `models.ts` now has `VisualModel`/`FieldCard`/`Histogram`/... classes.
- [ ] **Step 5: Commit** `gui/app.go`, `gui/app_test.go`, regenerated `wailsjs/` - `feat(gui): return VisualModel from ProfileFile, add DiffFiles`.

---

### Task 2: Design-system CSS (premium tokens + validated palette, light/dark)

**Files:**
- Modify: `gui/frontend/src/app.css`

- [ ] **Step 1:** Replace `app.css` with the premium token system: spacing scale (4/8/12/16/24/32), radius (8/12), the surfaces/text tokens above, one accent (`--accent: var(--series-1)`), the 5 categorical `--kind-{number,string,bool,array,object}` vars, the 4 `--status-{good,warning,serious,critical}` vars, and a subtle elevation/shadow token. Light default; dark via `@media (prefers-color-scheme: dark)` + `:root[data-theme="dark"]` (both, per dataviz). Typography: system sans for UI, mono for values. Generous whitespace for the overview.
- [ ] **Step 2: Verify** by building (Task 7 harness) OR a throwaway `index.html` preview; confirm both themes render the token swatches. (No unit test; visual.)
- [ ] **Step 3: Commit** - `style(gui): premium design tokens + validated chart palette`.

---

### Task 3: Chart primitives - Sparkline, Meter, Badge, StatusDot

**Files:** Create `gui/frontend/src/lib/charts/{Sparkline,Meter,Badge}.svelte`.

- [ ] **Step 1:** `Sparkline.svelte` - props `points: {x,y}[]`; inline SVG polyline in a `viewBox="0 0 100 28"`, `x*100`/`(1-y)*24+2`, thin 2px stroke `--accent`, rounded caps, no axes. `Meter.svelte` - props `presenceRate,nullRate,presenceText,nullText,nullStatus`; a thin horizontal track showing presence fill (accent) with a null segment (status color for `nullStatus`), direct `%` labels. `Badge.svelte` - props `severity,icon,label`; a pill using `--status-{severity}` background tint + the icon glyph + label text (never color alone). All theme-aware, `max-width:100%`.
- [ ] **Step 2: Verify** in a Storybook-free throwaway harness page or during Task 7 build; confirm shapes render in light+dark.
- [ ] **Step 3: Commit** - `feat(gui): sparkline, meter, badge chart primitives`.

---

### Task 4: Chart components - Histogram, CategoryBars, TypeMixBar

**Files:** Create `gui/frontend/src/lib/charts/{Histogram,CategoryBars,TypeMixBar}.svelte`.

- [ ] **Step 1:** `Histogram.svelte` - props the model `Histogram`; SVG bars, height = `bin.frac`, 4px rounded top, 2px surface gap between bars, hover tooltip per bar (`bin.label` + `bin.count`), x-axis min/max labels. `CategoryBars.svelte` - props the model `Categorical`; horizontal bars width = `bar.frac`, direct `label` + `percent%` labels, `Other` bucket muted, hover tooltip. `TypeMixBar.svelte` - props `segments: TypeSegment[]`; a single 100%-stacked horizontal bar, each segment `left = offset*100%`, `width = frac*100%`, fill = `--kind-{seg.kind}`, 2px surface gap between segments, legend/inline labels with `percent%`, hover tooltip per segment. All use model fractions only.
- [ ] **Step 2: Verify** visually via Task 7 build with real data; confirm fracs render, labels present (relief rule), tooltips work, both themes.
- [ ] **Step 3: Commit** - `feat(gui): histogram, category-bars, type-mix chart components`.

---

### Task 5: Overview - KpiRow + FieldCard grid (premium)

**Files:** Create `gui/frontend/src/lib/{KpiTile,KpiRow,FieldCard,FieldGrid}.svelte`.

- [ ] **Step 1:** `KpiTile.svelte` - props one `KPITile`; large value, small label, optional `sub`, status tint if set, `hero` variant (bigger, for health). `KpiRow` renders `model.kpis` (5). `FieldCard.svelte` - props one `FieldCard`; PREMIUM COMPACT: field `displayName` + `kind` chip, a compact chart preview (Sparkline for histogram/categorical, or a mini TypeMixBar for mixed, or Meter for empty/bool), the worst `Badge` (from `card.badges[0]`) if not "clean", and the Meter. Whitespace-generous, soft-rounded, hover-elevate, click → select. `FieldGrid` = responsive grid of FieldCards. Gate `typeMix`/charts on `card.form` (handle `"empty"`).
- [ ] **Step 2: Verify** via build: the overview reads clean and premium with real data; cards legible; grid responsive; no horizontal page scroll.
- [ ] **Step 3: Commit** - `feat(gui): premium KPI row and field-card overview grid`.

---

### Task 6: Detail cockpit - FieldDetail (dense)

**Files:** Rewrite `gui/frontend/src/lib/FieldDetail.svelte`.

- [ ] **Step 1:** Rewrite as the DENSE cockpit panel for the selected `FieldCard`: header (path, kind, EnumLike/ArrayElement chips, worst status), the full hero chart for the form (Histogram / CategoryBars+Other / HighCard sample+strlen / TypeMixBar / Array element breakdown), the full `Stats` grid (min/mean/median/p95/max/distinct with `Approx` markers), the Meter, ALL `Badges` (not just worst), and Sparkline. Information-dense, Grafana-like, but using the same tokens/charts so it's cohesive.
- [ ] **Step 2: Verify** via build: click each fixture field kind, confirm the right cockpit renders (numeric→histogram+quantiles, categorical→bars, high-card→sample+strlen, mixed→type-mix, array→element breakdown, bool/empty→meter).
- [ ] **Step 3: Commit** - `feat(gui): dense cockpit field-detail panel`.

---

### Task 7: App wiring + Header + build/run/screenshot

**Files:** Rewrite `gui/frontend/src/App.svelte`, `gui/frontend/src/lib/Header.svelte`; add a theme toggle.

- [ ] **Step 1:** `App.svelte` - state from `ProfileFile` → `VisualModel`; layout = `Header` + `KpiRow` + `FieldGrid`, with `FieldDetail` opening on card select (side panel or expand). `FileDrop` accepts one OR two files (two → call `DiffFiles`, switch to DiffView - Task 8). `Header` - app name, filename + record/skip counts (from `Summary`), a light/dark theme toggle (stamps `data-theme`), Open + Export-schema actions.
- [ ] **Step 2: BUILD + RUN + SCREENSHOT (the star check).** `cd gui && wails build` (or `wails dev`), launch, drop a real messy CSV/NDJSON, and **capture screenshots** of the overview (light AND dark) and a cockpit detail. **Look at them.** Iterate on spacing/color/scale until the overview reads premium and the screenshot is genuinely share-worthy. A blank/broken frame is a failure.
- [ ] **Step 3: Commit** - `feat(gui): dashboard app shell, header, theme toggle`.

---

### Task 8: Diff view (two-file drop)

**Files:** Create `gui/frontend/src/lib/DiffView.svelte`; wire in `App.svelte`.

- [ ] **Step 1:** `DiffView.svelte` - props `DiffVisualModel`; a KPI row (compared/added/removed/changed/breaking - breaking hero in critical status), the verdict banner (status-tinted), and grouped change rows (added/removed/changed) each showing `path`, status icon+label, and `old → new` details (with the `-` placeholders). Reuse `Badge`/`KpiTile`. `App.svelte` switches to DiffView when two files are dropped; a way back to single-file.
- [ ] **Step 2: BUILD + RUN + SCREENSHOT** the diff view on the diff fixtures (breaking case); confirm breaking rows read critical, groups ordered, light+dark. Iterate.
- [ ] **Step 3: Commit** - `feat(gui): two-file visual diff view`.

---

### Task 9: Launch polish - README screenshots/GIF

**Files:** Modify `README.md`, `gui/README.md`; add screenshots under `docs/` or `gui/`.

- [ ] **Step 1:** Capture final polished screenshots (overview light+dark, cockpit, diff) and optionally a short GIF (drag → dashboard). Embed in README "Desktop GUI" section. Tighten copy to lead with the visual.
- [ ] **Step 2: Verify** the README renders the images; the lead screenshot is share-worthy.
- [ ] **Step 3: Commit** - `docs: dashboard screenshots and GIF for launch`.

---

## Self-Review

**Coverage:** Backend contract (T1); design system + validated palette (T2); primitives (T3) + charts (T4); premium overview (T5); cockpit detail (T6); app shell/theme/build-screenshot (T7); diff view (T8); launch assets (T9). Parent-spec §3 (dashboard), §4 (health badges - via model), §5 (diff view), §7 (visual direction / dataviz), §9 (P7 launch overlaps T9).

**Verification is visual:** T2–T8 end with a `wails build` + screenshot look, not just a unit test - the star quality is only provable by looking. T1 has a real Go test. Each task is independently committable; the first share-worthy screenshot lands at T7.

**Risk:** Wails build toolchain on Windows (needs the WebView2 + Go + node); if `wails build` fails on environment, fall back to `wails dev` or a Vite-only preview of components with a mocked VisualModel JSON fixture, and capture screenshots there. Note any toolchain fix for a future project skill.
