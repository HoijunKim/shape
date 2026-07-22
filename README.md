# shape

See the real shape of your structured data files.

`shape` profiles JSON, NDJSON, CSV, TSV, Parquet, and SQLite files, infers a
JSON Schema (Draft 2020-12) from them, and diffs two snapshots to flag
breaking changes before they reach downstream consumers. It reads in a single
streaming pass with bounded memory - past 16384 distinct values per field it
automatically switches to an approximate mode (HyperLogLog cardinality +
Space-Saving top-k), so profiling a multi-gigabyte file does not require
loading it into memory. A cgo-free CLI, a `hoijun-kim/shape@v1` GitHub Action
for CI, and a Wails desktop GUI share the same core.

## Install

**Go:**

```
go install github.com/hoijun-kim/shape@latest
```

**Homebrew (macOS/Linux):**

```
brew install --cask hoijun-kim/tap/shape
```

**npm:**

```
npm install -g @hoijun-kim/shape
```

**Direct download:** grab the archive for your platform from the
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

- **Explore** — a virtualized table over the real rows, with a structure map of
  the file's fields alongside it. Files larger than memory stream instead of
  loading, and counts that are estimates say so.
- **Filter** — a visual condition builder (type-aware operators, AND/OR),
  applied live, with a cancellable exact match count.
- **Reshape** — choose, reorder and rename the columns you want.
- **Export** — write the filtered, reshaped result to JSON, NDJSON, CSV, TSV or
  Parquet. The export is always the complete result, never the windowed view,
  and it lands atomically: a cancelled or failed export leaves no partial file.

It still exports the inferred JSON Schema too (the header's "Schema" button).
Build it with `wails build` (see `gui/README.md` for the required build order).

## Supported formats

JSON, NDJSON, CSV, TSV, Parquet, SQLite.

## License

MIT - see [LICENSE](LICENSE).
