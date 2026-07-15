# Shape - Visual Dashboard Design Spec (v2)

Date: 2026-07-15
Status: Approved for planning
Author: hoijun (with Claude)

## 1. Summary

Reposition `shape` from a CLI-first profiling tool into an **instant visual data
X-ray**, led by the desktop GUI. Today the Wails GUI already does drag-drop -> a
plain profile table -> field detail -> JSON Schema export. This spec turns that
plain table into a gorgeous, screenshot-worthy **visual dashboard**: per-field
histograms, sparklines, type-mix bars, categorical distributions, health-warning
badges, and a two-file breaking-change diff view - plus a self-contained,
shareable HTML report.

The CLI stays exactly as it is (its CI / hacker-credibility role is unchanged).
The GUI becomes the face that earns GitHub stars, because a single screenshot -
"drag a messy CSV, instantly see a beautiful profile dashboard" - reaches a broad
audience with no terminal literacy required.

The whole feature set is built on the existing Go core as the single source of
truth. A new `internal/visual` package computes all chart geometry once, in Go,
fully unit-tested; both the Svelte GUI and a Go HTML template consume that same
model, so chart math is never duplicated.

## 2. Why this exists (positioning)

### The goal
The author wants to build a developer tool that earns GitHub stars. The existing
`shape` is solid engineering but a weak star magnet: its value is *preventive*
(it stops silent schema breakage) and therefore invisible - it does not
screenshot. Stars come from an instant "wow" that a screenshot or GIF captures.

### The wedge
`shape` already streams any structured file (JSON, NDJSON, CSV, TSV, Parquet,
SQLite) in one bounded-memory pass and knows each field's types, null rate,
distinct count, ranges, and top values. That profile is exactly the raw material
for a striking visual dashboard - nobody bundles "drop any data file -> instant
visual profile + drift diff + shareable report" across both nested and tabular
data, zero setup, local-first.

### Success criteria
- PRIMARY: a single dashboard screenshot is compelling enough to drive stars when
  posted (Product Hunt / Twitter / Show HN). Dropping a messy real-world file
  produces an immediately readable, beautiful profile.
- SECONDARY: the shareable HTML report creates a viral loop (a shared report links
  back to the tool).
- TERTIARY: the CLI keeps working unchanged for CI use.

### Honest risks (carry into planning)
- The GUI must look *genuinely* polished, not merely functional - a mediocre
  dashboard screenshot earns nothing. The dataviz method (form-first, validated
  colorblind-safe palette, proper marks) is a hard requirement, not a nicety.
- Streaming histograms add real complexity to the profiler; accuracy vs. the
  bounded-memory / single-pass identity must be tested, not assumed.
- Scope is meaty for a v2. Mitigation: strict phasing (Section 9); each phase is
  independently shippable, and the star-bearing screenshot lands at Phase 3.

## 3. Architecture

Chosen approach: **Go computes a `VisualModel` (chart geometry); both the
interactive GUI and the static HTML report consume it.** This matches the
existing "Go core is the single source of truth, views are thin" architecture,
makes the HTML report reusable from both GUI and CLI, and keeps chart math
testable in Go.

```
readers -> pipeline -> profile (core, source of truth)
                          |
               internal/visual : ProfileResult -> VisualModel
                          |        (histogram bins, bar segments, sparkline
                          |         points, badge list, diff rows) - pure data
        +-----------------+------------------+
   Svelte (GUI)      Go html/template     CLI: `shape report`
  interactive SVG    static SVG report
```

### New / changed units

- **`internal/profile` (extend): streaming numeric histogram sketch.**
  A bounded-bin streaming histogram (Ben-Haim & Tom-Tov style): maintains a fixed
  maximum number of (centroid, count) bins, merging the closest pair when the cap
  is exceeded. Single pass, bounded memory - same family as the existing HLL and
  Space-Saving sketches. Yields histogram bins plus approximate quantiles
  (median, p95) for numeric fields. Added to `fieldAccumulator` alongside the
  existing numeric min/max tracking, surfaced on `FieldProfile`.
  - What it does: approximate distribution of a numeric field in bounded memory.
  - Interface: `add(float64)`, `bins() []Bin`, `quantile(q float64) float64`.
  - Depends on: nothing beyond the standard library.

- **`internal/visual` (new): VisualModel computation.**
  Turns a `ProfileResult` into render-ready geometry: per-field chart type
  selection, histogram bar rectangles, categorical top-k bar segments, sparkline
  point sequences, type-mix stacked-bar segments, presence/null meter values, the
  health-badge list, the health score, and (for diff) the diff-row model.
  - What it does: all chart math, once, as pure data.
  - Interface: `FromProfile(ProfileResult) VisualModel`,
    `FromDiff(diff.DiffResult) DiffVisualModel`.
  - Depends on: `internal/profile`, `internal/diff`.

- **`internal/report` (new): self-contained HTML report.**
  A Go `html/template` that renders a `VisualModel` (and `DiffVisualModel`) into a
  single HTML file with inlined CSS and server-side SVG, zero external requests.
  - What it does: static, shareable HTML from the same VisualModel.
  - Interface: `Render(w io.Writer, vm VisualModel) error` (+ diff variant).
  - Depends on: `internal/visual`.

- **`gui/app.go` (extend):** `ProfileFile` returns a VisualModel-backed view;
  add `DiffFiles(a, b string)` and `ExportHTML(...)` bindings.

- **`internal/cmd` (extend):** new `shape report <file> -o report.html`
  subcommand (reuses `internal/report`).

### Frontend (Svelte)
The existing components evolve rather than get replaced:
- `FileDrop.svelte` - accept one OR two files; two triggers diff mode.
- `ProfileTable.svelte` -> a **field-card grid** rendering VisualModel charts.
- `FieldDetail.svelte` - extended detail (full histogram, quantiles, top-k).
- New `Badge.svelte`, `Histogram.svelte`, `MiniBar.svelte`, `TypeMixBar.svelte`,
  `KpiTile.svelte`, and a `DiffView.svelte`.
- `Header.svelte` - adds Export-HTML action and diff-mode awareness.

## 4. The dashboard (drop one file)

Top **KPI tile row**: records, fields, format, warning count, and a **health
score** as the hero number.

Below, a **field-card grid**. Each card's chart form is chosen by the field's
nature (dataviz form-first heuristic):

| Field kind | Form |
|---|---|
| Numeric (int/float) | Histogram + stat tile (min / median / mean / max) |
| Categorical / low-cardinality string | Horizontal top-k bar |
| High-cardinality string | Cardinality stat + top-k sample + string-length mini-bar |
| Multi-type field | Stacked composition bar (type mix) = "is this field clean?" |
| Presence / null | Thin meter stat tile |
| Array (`field[]`) | Element-type breakdown |

Clicking a card opens the extended detail panel (evolved `FieldDetail`). Hover
tooltips are present by default on every plotted mark.

## 5. Health-warning badges

Scan the profile and attach badges, each with an icon + label (never color
alone), using the reserved status palette:

- CRITICAL: an always-present field removed (diff), numeric type narrowing.
- SERIOUS / WARNING: type drift (mixed types in one field), high null rate, enum
  drift.
- GOOD: clean field.

An overall **health score** (weighted sum of badges) feeds the hero KPI tile.
Exact thresholds (e.g. null-rate warning band, cardinality "high" cutoff) are
defined during Phase 4 implementation and unit-tested in `internal/visual`.

## 6. Diff view (drop two files)

Dropping two files switches the window to diff mode. KPI row: compared / added /
removed / changed / **breaking** (breaking shown in the critical status color).
Change rows are grouped (added / removed / changed); each row shows `old -> new`
with a status icon + label. Reuses the existing `internal/diff` core
(`diff.Diff(old, new) diff.DiffResult`); renders from the `DiffVisualModel`.

## 7. Shareable HTML report

`internal/report` renders a VisualModel into a single self-contained HTML file
(inline CSS + server-side SVG, zero external requests). Exposed two ways:
- GUI: an "Export HTML" action in the header.
- CLI: `shape report <file> -o report.html`.

A shared report links back to the project - the intended viral loop.

## 8. Visual direction (dataviz-grounded)

- Form first, color last. Forms per the Section 4 table.
- Categorical colors: a fixed-order palette, validated colorblind-safe with
  `dataviz/scripts/validate_palette.js` during implementation (CVD >= 12 target).
- Status colors are reserved and distinct from series colors; always paired with
  an icon + label.
- Dark mode is designed as its own set of steps (the existing `app.css` already
  has a dark block to extend), validated against the dark surface - not an auto
  flip.
- Thin marks, 4px rounded data-ends, recessive grid/axes, a legend whenever a
  chart has >= 2 series, selective direct labels.
- Hover crosshair / tooltip by default on plotted marks.

## 9. Build sequence (incremental; each phase shippable)

1. **P1** - Profiler streaming histogram + quantile sketch (backend foundation).
2. **P2** - `internal/visual` VisualModel + Go unit tests (chart geometry).
3. **P3** - GUI dashboard: KPI tiles + field-card charts. **<- first screenshot;
   stars start here.**
4. **P4** - Health badges + health score.
5. **P5** - Diff view (two-file drop).
6. **P6** - `internal/report` HTML + GUI export button + CLI `shape report`.
7. **P7** - Dark-mode polish + palette validation + README GIF/screenshots +
   launch.

Each phase is its own spec-derived plan -> implementation cycle.

## 10. Out of scope (v2 non-goals; YAGNI)

- Row preview (see actual sample rows).
- Query / filter over rows.
- Format conversion (file -> other format export).
- Interactive terminal TUI.
- Cloud-hosted report sharing.

These may become a future v3; they are explicitly excluded here to keep v2
focused on the star-bearing dashboard.

## 11. Testing strategy

- `internal/profile` histogram: accuracy against known distributions, bounded
  memory, single-pass golden tests.
- `internal/visual`: golden tests of ProfileResult -> VisualModel (chart geometry
  snapshots) and diff.Result -> DiffVisualModel.
- `internal/report`: HTML render golden tests + a self-contained check (assert no
  external URLs / network references in output).
- GUI: extend `gui/app_test.go` for `DiffFiles` and `ExportHTML`.
- CLI: snapshot test for `shape report`.
