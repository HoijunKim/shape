# Shape - Design Spec (v1)

Date: 2026-07-13
Status: Approved for planning
Author: hoijun (with Claude)

## 1. Summary

Shape is a local-first tool that shows the real structure of a structured data
file - JSON, NDJSON, CSV, Parquet, or SQLite - instantly, with zero setup and no
cloud. Drop a file (or pipe it) and Shape streams every record and reports, per
field, what is actually in the data: presence rate, type distribution, null rate,
ranges, distinct counts, top values, and outliers. From that profile it can
export a JSON Schema and diff two snapshots to answer "did this payload's shape
change, and did it break?".

It ships CLI-first (the adoption lever: it runs in CI as a payload-shape
guardrail) with a desktop GUI companion (Wails) for exploration. A single Go core
library powers both front ends.

## 2. Why this exists (positioning)

### The pain
You pull a large JSON/NDJSON dump from an API, scraper, or export - or open a CSV
/ Parquet / SQLite table you did not produce - and you do not know its true shape:
which fields exist, how often they are null, whether a field is an int in 99% of
records and a string in the other 1%, what the ranges and distinct counts are.
`jq` will not summarize it. Everyone writes the same throwaway inspection script
over and over. And when an upstream payload silently changes shape, nothing warns
you until it breaks downstream.

### The wedge (what nobody bundles today)
The individual pieces exist in fragments, but no single tool bundles all four,
zero-setup, across both nested and tabular data:

1. Automatic per-field (including per-nested-path) profiling.
2. Explicit type-drift surfacing - the exact thing loaders silently coerce away
   (DuckDB's `read_json_auto` schema inference actively hides it).
3. One-click JSON Schema export.
4. Snapshot diff with a breaking-change contract (`--fail-on breaking`) that turns
   an intermittent GUI nicety into an automatable CI contract test.

### Success criteria
- PRIMARY: broad developer adoption - a dev consuming untrusted/upstream data
  reaches for `shape` instead of writing another inspection script, and puts
  `shape diff --fail-on breaking` in CI.
- SECONDARY: the author personally uses it daily (profiling API dumps, ML dataset
  files, exports).

### Honest risks (carry these into planning)
- For the profiling core on TABULAR data (null rate, cardinality, ranges), DuckDB's
  one-line `SUMMARIZE read_*()` already satisfies data-literate devs. Shape must
  win on: zero-SQL drop-a-file UX, NESTED JSON profiling (where DuckDB is weak),
  explicit drift surfacing, JSON Schema export, and the CI breaking-change contract
  - not on raw tabular stats.
- Broadening v1 to CSV/Parquet/SQLite (chosen deliberately) enlarges the build and
  increases DuckDB overlap on tabular. Mitigation: the unified "any structured file
  -> shape/drift/schema" story and the CLI/CI contract are the differentiators, not
  the tabular stats.
- Drift detection only pays off where it runs continuously; the CLI + GitHub Action
  path (not the desktop GUI) is what makes drift detection actually deliver. Keep
  the CLI/CI path first-class.

## 3. Scope

### v1 - IN
- Input formats: JSON (array or single object), NDJSON / JSON-lines, CSV, Parquet,
  SQLite (per-table). Read from a file path, a directory (glob), or stdin (for the
  streamable text formats).
- Per-field profile: presence %, type distribution, null rate, min/max (numeric and
  lexical), distinct count / cardinality, top-K values, numeric outliers, string
  length distribution.
- Type-drift surfacing: highlight fields whose type is not stable across records
  (e.g. `int` 99% / `string` 1% / `null` 12%).
- JSON Schema inference and export (Draft 2020-12): `required` from 100%-presence
  fields, `enum` from low-cardinality string sets, `format` heuristics
  (date-time, email, uuid, uri), nullable via type unions.
- Snapshot diff: `shape diff A B` reports fields added/removed and changes in type
  distribution, null rate, and cardinality, with a `--fail-on breaking` contract
  that exits non-zero on breaking changes.
- Output: human-readable TTY table (default), `--json` machine output for CI.
- Desktop GUI (Wails): drop a file -> field tree + profile panel + drift highlight
  + schema export button + two-file drift view.

### v1 - OUT (future)
- Type generation to TypeScript / Go structs / Zod (quicktype owns this; revisit
  later as an additive output).
- Remote sources (S3, HTTP URLs, databases over the wire).
- Real-time / watch mode on a growing file.
- A hosted history/collaboration server for team drift tracking.
- Additional formats (Avro, XML, Excel).

## 4. Architecture

One pure Go core library; two front ends (CLI and GUI) that never diverge because
they call the same core.

| Unit          | Responsibility                                                        | Interface (conceptual)                              |
|---------------|-----------------------------------------------------------------------|-----------------------------------------------------|
| `readers`     | Format adapters that normalize any input to a stream of records        | `Open(source) -> RecordStream`                       |
| `core`        | Path flattening, streaming aggregation, sketches, profile assembly      | `Profile(RecordStream) -> ProfileResult`             |
| `schema`      | Infer a JSON Schema from a ProfileResult                                | `InferSchema(ProfileResult) -> JSONSchema`           |
| `diff`        | Compare two ProfileResults; classify changes; decide breaking          | `Diff(a, b, policy) -> DiffResult`                   |
| `cli`         | `shape profile|schema|diff`; flags; TTY vs `--json`; exit codes         | wraps core; the CI-facing surface                    |
| `gui` (Wails) | drop/tree/profile/drift/schema-export UI                                | Wails bindings over the same core                    |

Key boundary: `readers` isolate all format-specific parsing so the profiling engine
sees a uniform record stream. Row-oriented formats (JSON/NDJSON/CSV/SQLite) emit
records; Parquet (columnar) can feed column statistics directly into the same
aggregators. Adding a format later means adding one reader, nothing else changes.

## 5. Data model

A "field" is identified by a path:
- Tabular (CSV / SQLite / Parquet): the flat column name.
- JSON / NDJSON: a flattened path with array notation, e.g. `user.email`,
  `items[].price`. Array elements aggregate element-wise under the `[]` path.

For each record, every observed path contributes one observation to that path's
accumulator. A path's profile is the accumulated statistics over all records
(including "absent" observations, which drive presence %).

`ProfileResult` = record count + per-path `FieldProfile` (counts by JSON type,
null count, numeric summary, string-length summary, cardinality sketch, top-K
sketch, sample outliers) + parse-error/skipped-record counts.

## 6. The three artifacts (all from one profile pass)

1. Profile view - the entry point and the delight: "here is what your data actually
   looks like," including the drift highlight (`user.email` is null 12% and a string
   1% of the time).
2. JSON Schema - the contract artifact you can commit and validate against.
3. Drift diff + breaking contract - the CI lever. Breaking changes (v1 definition):
   a previously-present field removed; a field's dominant type changed
   incompatibly; a field going from always-present to sometimes-absent; an `enum`
   losing or gaining members beyond a threshold. Non-breaking: additive fields,
   widened ranges, small distribution shifts. `--fail-on breaking` gates a merge.

## 7. Performance and scale

Streaming, bounded memory - this is where the Go + data/stats edge shows and where
throwaway scripts and pandas cannot follow.
- Single streaming pass; never load the whole file into memory.
- Distinct/cardinality via HyperLogLog (approximate, constant memory).
- Top-K values via a bounded frequency sketch (e.g. Space-Saving / Count-Min).
- Outliers / samples via reservoir sampling.
- Exact mode available for small inputs; approximate mode engages automatically
  past a size threshold, and the report states which mode was used.
- Target: profile a multi-GB NDJSON file in constant memory.

## 8. CLI surface (sketch)

```
shape profile <file|dir|-> [--json] [--exact] [--top 10] [--format auto|json|ndjson|csv|parquet|sqlite] [--table NAME]
shape schema  <file|dir|-> [-o schema.json]
shape diff    <old> <new> [--fail-on breaking|any|none] [--json]
```
Exit codes: `0` success / no failing drift; `1` breaking drift under `--fail-on`;
`2` usage or read error. `--json` emits stable machine output for CI.

## 9. GUI surface (Wails)

Drop a file -> left: field/path tree with presence and drift badges; right: selected
field profile (type distribution, null rate, ranges, top values, length histogram);
top bar: export JSON Schema, open a second file to enter drift-diff view. Read-only,
fast, keyboard-navigable. The GUI is for exploration; it does not gate CI.

## 10. Error handling

- Malformed NDJSON line: skip, count, and report; never abort the run.
- Mixed/heterogeneous types: this is the core value (drift), handled as normal.
- Huge files: progress indication; automatic approximate mode; constant memory.
- Empty/absent field in one side of a diff: reported explicitly as added/removed.
- Unreadable/binary/unknown format: clear error, exit 2.
- SQLite with multiple tables: require/`--table` or profile a chosen/default table;
  never guess silently.

## 11. Testing strategy

- `core`: golden profile tests on synthetic and real API-dump fixtures; assert type
  distributions, null rates, presence, and drift flags.
- `schema`: round-trip - inferred schema must validate the source data it was
  inferred from; targeted tests for `required`, `enum`, `format`, nullable.
- `diff`: table of breaking / non-breaking cases with asserted classification and
  CLI exit codes.
- sketches: accuracy-bound tests for HyperLogLog / top-K / reservoir against exact
  results on medium inputs.
- `readers`: per-format fixtures (valid, broken, edge cases) mapping to the same
  record-stream contract.
- E2E: planted drift (`int -> string` in 1% of records; a dropped field) must
  surface in both `profile` and `diff`, and `--fail-on breaking` must exit non-zero.

## 12. Distribution

- `go install` (single static binary), Homebrew tap, and an npm wrapper for
  easy install.
- A GitHub Action wrapping `shape diff --fail-on breaking` - the primary adoption
  path (payload-shape guardrail in CI).
- Cross-platform GUI binaries (Windows/macOS/Linux) via Wails.

## 13. Name

Working title "Shape"; binary `shape` (fallback `shp` if the name collides on a
target package registry). Swappable before first release.

## 14. Open questions for planning

- Exact threshold and defaults for auto approximate mode.
- Precise breaking-change policy details (enum-change threshold, numeric-type
  widening rules).
- Parquet reader: pure-Go library choice vs. cgo (prefer pure-Go for single-binary
  distribution).
- SQLite multi-table default behavior.
