# Changelog

All notable changes to shape are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and shape aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_Nothing yet._

## [0.1.2] - 2026-08-14

### Changed

- **Relicensed to AGPL-3.0.** shape was source-available under the PolyForm
  Noncommercial License 1.0.0, which barred commercial use outright. It is now
  open source: run it, read it, modify it, fork it, redistribute it, commercial
  use included. The obligation that replaces the old prohibition is the AGPL's -
  modify shape and let others use it over a network, and you owe them the
  modified source.
- **Commercial licensing** covers the cases the AGPL does not: shipping shape
  inside a closed-source product, or running a modified version as a service
  without publishing the changes. Contact the copyright holder. See
  [LICENSING.md](LICENSING.md).
- `npm/package.json` declared `MIT`, which was never the licence. It declares
  `AGPL-3.0-only` with everything else.

### Note

Releases up to and including 0.1.1 shipped under PolyForm and keep it - the
artifacts published then carry the licence they were published under. The
relicence applies from this version. Every line is the copyright holder's own
work, and the dependencies (Apache-2.0, MIT, BSD-3-Clause) are all compatible
with an AGPL-3.0 outbound licence.

## [0.1.1] - 2026-08-14

The author's GitHub account was renamed from `hoijun-kim` to `hoijunkim`, which
moves everything that names it. No behaviour changed; this release exists so
the new paths have a version to resolve to.

### Changed

- **Module path** is `github.com/hoijunkim/shape`. A module has to declare the
  path it is fetched from, so `go install github.com/hoijunkim/shape@latest`
  needs a tag cut after the move - 0.1.0 still declares the old path and cannot
  be installed under the new one.
- **Install paths** follow the account: the cask is `hoijunkim/tap/shape`, the
  npm package `@hoijunkim/shape`, the Action `hoijunkim/shape@v1`, and the
  winget package `hoijunkim.shape`.
- **Links** in the README, the landing page and the docs point at
  `github.com/hoijunkim/shape` and `hoijunkim.github.io/shape`.

### Note

`github.com/hoijun-kim/shape` keeps working only while GitHub redirects the old
account name, which it stops doing if someone else claims it. Prefer the new
path.

## [0.1.0] - 2026-07-27

First release. shape is a cgo-free Go tool for structured data - a command-line
profiler and a desktop explorer sharing one streaming engine.

### Desktop explorer (Wails + Svelte)

Drag any data file in and work with the actual rows - no jq or SQL.

- **Open & explore** - JSON, NDJSON, CSV, TSV, Parquet and SQLite. A virtualized
  table of the real rows (millions scroll smoothly, and files larger than memory
  stream instead of loading), beside a structure-map sidebar of every field with
  its type, presence and distinct count. Row counts are honest - an estimate says
  so with a leading "~".
- **Huge-file scrolling** - every row stays reachable past the browser's element-
  height cap, with a go-to-row box for exact navigation.
- **Filter** - a visual, type-aware AND/OR condition builder applied live, with a
  cancellable exact match count; seed a condition from a field in the sidebar.
- **Search** - a global search box: any value, any field, case-insensitive,
  combined with the filter.
- **Sort** - click a column header to sort (none → asc → desc), exact over the
  whole result on every tier - memory, SQLite, Parquet, and the >512 MiB
  streaming tier (via a bounded keys-only index). Row numbers stay the true
  source ordinals.
- **Reshape** - choose, reorder and rename the output columns.
- **Column statistics** - expand a field in the sidebar for its full profile: a
  distribution histogram, top values, quantiles and health flags.
- **Cell value tree** - click a truncated object/array cell to see its whole,
  untruncated value as a collapsible tree, with Copy.
- **Row detail** - click a row's number to see the whole record as a tree.
- **Edit & save a copy** - double-click a scalar cell to edit it (number literals
  stay exact); edited cells highlight and an "Edited only" view lists the changes.
  Save the whole file back with your edits as JSON/NDJSON - the original is never
  touched.
- **Export** - the full result (never just the window) to JSON, NDJSON, CSV, TSV
  or Parquet, written atomically.
- **Code panel** - the equivalent `jq` expression and SQL query for whatever you
  built, ready to copy; on a SQLite source shape also runs the SQL, pushing a
  translatable filter into the database (~12× faster on a 200k-row count) and
  falling back to the same Go predicate for anything it cannot vouch for.
- **Saved views** - save the current filter + search + sort + reshape under a
  name and re-apply it anytime, across restarts (persisted to a config file).
- **Help overlay** - a "?" button (and a one-time first-launch pop-up) explaining
  every feature.

### Command-line (`shape`)

- **profile** - a single streaming pass over a file: per-field types, presence,
  null rate, cardinality (exact, then HyperLogLog past 16,384 distinct values),
  numeric min/max/quantiles and a streaming histogram, top values (Space-Saving),
  and string-length stats. Bounded memory on multi-gigabyte files.
- **schema** - infer a JSON Schema (Draft 2020-12).
- **diff** - compare two snapshots and flag breaking changes before they reach
  downstream consumers.
- **GitHub Action** - `hoijunkim/shape@v1` for CI.

### Engineering

- cgo-free; no DuckDB. One compiled filter/transform model over four backends
  (bounded in-memory, streaming re-scan, native SQLite, native Parquet) returning
  byte-identical rows, so results are the same whatever the source or size.

[Unreleased]: https://github.com/hoijunkim/shape/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/hoijunkim/shape/releases/tag/v0.1.2
[0.1.1]: https://github.com/hoijunkim/shape/releases/tag/v0.1.1
[0.1.0]: https://github.com/hoijunkim/shape/releases/tag/v0.1.0
