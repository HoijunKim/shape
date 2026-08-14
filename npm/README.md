# @hoijunkim/shape

npm wrapper for [shape](https://github.com/hoijunkim/shape), a CLI that profiles
structured data files, infers a JSON Schema, and diffs snapshots for breaking
changes.

## Install

```
npm install -g @hoijunkim/shape
```

The `postinstall` script downloads the matching `shape` release binary for
your platform (linux/darwin/windows, amd64/arm64) from GitHub Releases; no
compiler or Go toolchain required.

## Usage

See the main [README](https://github.com/hoijunkim/shape#readme) for the
full command reference (`shape profile`, `shape schema`, `shape diff`).

```
shape profile data.ndjson
shape schema data.ndjson -o schema.json
shape diff old.ndjson new.ndjson --fail-on breaking
```
