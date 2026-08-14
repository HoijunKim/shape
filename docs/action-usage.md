# shape diff GitHub Action

A reusable composite action (`action.yml` at the repo root) that downloads a
released `shape` binary and runs `shape diff` as a CI gate, failing the build
when a data-shape change breaks consumers of the old snapshot.

## Usage

```yaml
- uses: hoijunkim/shape@v1
  with:
    old: baseline.ndjson
    new: current.ndjson
    fail-on: breaking
```

Add this step to any job that has both the baseline (old) and current (new)
data files checked out or produced earlier in the job.

## Inputs

| Input     | Required | Default    | Description                                              |
|-----------|----------|------------|------------------------------------------------------------|
| `old`     | yes      | -          | Path to the old (baseline) data file                     |
| `new`     | yes      | -          | Path to the new data file                                 |
| `fail-on` | no       | `breaking` | `breaking` \| `any` \| `none`                              |
| `version` | no       | `latest`   | `shape` release version tag (e.g. `v1.2.0`) or `latest`  |

## How it works

The action's single step runs on `bash` and:

1. Detects the runner OS and architecture (`uname -s` / `uname -m`), mapping
   `x86_64`/`amd64` to `amd64` and `aarch64`/`arm64` to `arm64`.
2. Resolves the requested `shape` release: if `version` is `latest`, it
   follows the GitHub "latest release" redirect to find the tag; otherwise it
   uses the given tag directly.
3. Downloads the matching release archive
   (`shape_<version>_<os>_<arch>.tar.gz`) from
   `https://github.com/hoijunkim/shape/releases/...` and extracts the
   `shape` binary.
4. Runs `shape diff --fail-on <fail-on> <old> <new>`, so the step's exit code
   is `shape diff`'s exit code.

This only supports Linux and macOS runners (the download step extracts a
`.tar.gz` archive); Windows runners are not supported by this action.

## Requirements

- The `hoijunkim/shape` repository must be public and have at least one
  GitHub release with the standard GoReleaser archive naming
  (`shape_<version>_<os>_<arch>.tar.gz`) for this action to find a binary to
  download.
- No additional secrets or permissions are required to run the action itself;
  it only downloads a public release asset and executes it.
