<!--
  LAUNCH ASSETS TO CAPTURE (human task -- cannot be automated/recorded from CI):
  A demo GIF + a couple of stills belong right here, above the tagline, since
  this is the first thing a visitor sees. Record ONE continuous ~20s take of the
  "wow" path, in the desktop GUI (`cd gui && wails build`), on a messy nested
  file (gui/testdata/nested.ndjson works, or something larger and real):
    1. Drag the file in -> the explorer profiles it and shows real rows.
    2. Click a truncated nested object/array cell -> the value tree opens; Copy.
    3. Type a value in the search box -> rows narrow, the status count follows.
    4. Open the Code panel -> the jq/SQL reflects the filter + search; copy it.
    5. Export -> the same result written out in full.
  Capture in both light and dark (the header toggle) for the two stills. Put the
  files under docs/ (e.g. docs/demo.gif) and embed here. Do NOT fake or mock
  these -- they must be the real app.

  A design MOCK-UP landing page (stylized UI, fictional data, not screen
  captures) lives at docs/index.html and is served via GitHub Pages at
  https://hoijun-kim.github.io/shape/ -- it is labeled as a mock-up in-page and
  is NOT a substitute for the real stills above.

  That page's favicon, touch icon and Open Graph card are generated from
  gui/build/appicon.png by `python tools/make-site-icons.py` (Pillow only).
  Re-run it if the app icon changes.
-->

# shape

**Drag in any data file - JSON, NDJSON, CSV, TSV, Parquet, SQLite - and explore
the real rows. Filter, reshape, edit, export. No jq. No SQL.**

[![ci](https://github.com/hoijun-kim/shape/actions/workflows/ci.yml/badge.svg)](https://github.com/hoijun-kim/shape/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/hoijun-kim/shape?sort=semver)](https://github.com/hoijun-kim/shape/releases)
[![license](https://img.shields.io/badge/license-PolyForm%20Noncommercial%201.0.0-blue)](LICENSE)

Most tools for poking at a data file assume you already know jq or SQL. `shape`
is for everyone who bounces off them. Open a file and you get a fast, virtualized
table of the actual rows beside a structure map of every field - then a
click-to-build visual filter, a global search, in-place cell editing, and a
one-click export, with the **equivalent jq and SQL shown for whatever you built**
so you can take the query with you.

It reads in a single streaming pass with bounded memory, so a multi-gigabyte file
opens without loading into RAM (past 16,384 distinct values per field it switches
to an approximate mode - HyperLogLog cardinality + Space-Saving top-k). The same
cgo-free core also ships as a command-line profiler (schema inference + breaking-
change diffs) and a `hoijun-kim/shape@v1` GitHub Action for CI.

→ [What the desktop app does](#desktop-gui) · [Install](#install)

## Install

**Desktop app (the explorer).** Download the build for your platform from the
[Releases page](https://github.com/hoijun-kim/shape/releases) and run it -
`shape-gui_<version>_windows_amd64.zip`, `..._darwin_universal.zip`, or
`..._linux_amd64.tar.gz`. The binaries are unsigned, so the first launch needs
"Open anyway" (macOS Gatekeeper) or "More info → Run anyway" (Windows SmartScreen).

**CLI (`shape`).** The command-line profiler ships separately:

```
go install github.com/hoijun-kim/shape@latest        # any platform with Go
brew install --cask hoijun-kim/tap/shape             # macOS / Linux
```

or grab a `shape_<version>_<os>_<arch>` archive from the same
[Releases page](https://github.com/hoijun-kim/shape/releases).

## Usage

### profile

```
shape profile data.ndjson
```

Reads JSON, NDJSON, CSV, TSV, Parquet, or SQLite (format is auto-detected
from the extension, or pass `--format`) and prints a per-field shape summary:
types, null rate, distinct count, min/max, and top values. Pass `-` to read
from stdin. Add `--json` for machine-readable output.

### schema

```
shape schema data.ndjson -o schema.json
```

Infers a JSON Schema (Draft 2020-12) from the same input formats and writes
it to a file (`-o`/`--out`) or stdout.

### diff

```
shape diff old.ndjson new.ndjson --fail-on breaking
```

Diffs two snapshots and reports what changed: field additions/removals, type
widening/narrowing, nullability changes, and enum drift. `--fail-on` controls
the exit code: `breaking` (default) exits 1 only on breaking changes, `any`
exits 1 on any change, `none` never fails. Add `--json` for machine-readable
output.

## GitHub Action

Gate a pull request on breaking data-shape changes:

```yaml
- uses: hoijun-kim/shape@v1
  with:
    old: baseline.ndjson
    new: current.ndjson
    fail-on: breaking
```

See [action.yml](action.yml) for the full set of inputs.

## Desktop GUI

A Wails v2 desktop app under [`gui/`](gui/README.md) reuses the same Go core as
a **data explorer**: drop in any supported file and browse the actual rows, no
jq or SQL required.

- **Explore** - a virtualized table over the real rows, with a structure map of
  the file's fields alongside it. Files larger than memory stream instead of
  loading, and counts that are estimates say so.
- **Filter** - a visual condition builder (type-aware operators, AND/OR),
  applied live, with a cancellable exact match count.
- **Search** - a global search box: type any text and the rows narrow to those
  where any field's value contains it (case-insensitive, no column to pick),
  combined with the filter and reflected in the count, the export and the code.
- **Expand** - click a truncated object/array cell to open its full value as a
  collapsible tree, with a Copy button for the exact JSON.
- **Row detail** - click a row's number to open the whole record as a
  collapsible tree - the full, untruncated nested value (the row-level companion
  to Expand), with the same Copy button.
- **Column stats** - expand a field in the structure map to see its full profile
  in place: a distribution histogram for numbers, a top-values chart for
  categorical fields, type mix, presence/null meters, quantiles and health flags
  - all from the single open-time profiling pass, fetched on demand, no rescan.
- **Sort** - click a column header to sort by it (ascending → descending → off),
  exact over the whole result on every tier - even a multi-gigabyte streaming
  file, via a bounded keys-only index. Row numbers stay the true source ordinals
  (so they read non-contiguously under a sort), which keeps editing and cell
  lookups pointing at the right record.
- **Reshape** - choose, reorder and rename the columns you want.
- **Saved views** - save the current filter, search, sort and reshape under a
  name from the header's Views menu, and re-apply it anytime - across restarts.
  Views are global (they apply to whatever file is open) and live in a plain JSON
  file in your config dir.
- **Edit** - double-click a scalar cell to change its value in place. Edited
  cells are highlighted (and the row flagged in the gutter); an "Edited only"
  toggle lists just the changes as *was → now*, each revertable. Number literals
  keep their exact text, so a 19-digit id never loses a digit. Editing is limited
  to unambiguous scalar columns (a single, non-array leaf), and it never touches
  the file on disk - see **Save a copy** below.
- **Save a copy** - write the whole file back out with your edits applied, as
  JSON or NDJSON, to a *new* file. The original is left untouched, the nested
  structure is preserved (edits land at the source path, not a flattened one),
  and every row is written - not the filtered/reshaped view. The dialog reports
  how many edits applied and warns if any could not be. (Overwrite-in-place and
  CSV/Parquet saving are deliberately out of scope for now; object key order may
  change on rewrite.)
- **Export** - write the filtered, reshaped result to JSON, NDJSON, CSV, TSV or
  Parquet. The export is always the complete result, never the windowed view,
  and it lands atomically: a cancelled or failed export leaves no partial file.
- **Take the query with you** - the Code panel shows the equivalent `jq`
  expression and SQL query for whatever you built by clicking, ready to copy,
  with the places the three engines genuinely differ called out rather than
  glossed over. On a SQLite source shape also *runs* that SQL: a filter it can
  translate exactly is pushed into the database (measured ~12x faster on a
  200k-row count), and anything it cannot vouch for falls back to the same Go
  predicate every other format uses, so the answer never changes.

First launch opens a **"?" help overlay** explaining every feature; the header's
"?" button reopens it anytime. It still exports the inferred JSON Schema too (the
header's "Schema" button).
Build it with `wails build` (see `gui/README.md` for the required build order).

## Supported formats

JSON, NDJSON, CSV, TSV, Parquet, SQLite.

## Author

Made by **Hoijun Kim** ([hoijun-kim](https://github.com/hoijun-kim)).

## License

**PolyForm Noncommercial License 1.0.0.** shape is source-available and **free
for any noncommercial purpose** - personal projects, research, teaching,
evaluation, and use by nonprofits or government. You may run, read, modify, fork
and redistribute it for those uses.

**Commercial use is not permitted under this license.** Using shape in or for a
business's paid product, service, or operations needs a separate commercial
license - contact **Hoijun Kim &lt;hoijun.kim00@gmail.com&gt;**.

Full terms - including the precise definitions of "noncommercial" and "permitted
purpose" - are in [LICENSE](LICENSE).
