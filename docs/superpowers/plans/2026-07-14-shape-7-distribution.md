# Shape Plan 7: distribution (release, Homebrew, npm, GitHub Action, CI)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make shape installable and adoptable: cross-platform CLI release binaries (GoReleaser, cgo-free), a Homebrew tap formula, an npm wrapper, a reusable GitHub Action that runs the `shape diff` breaking-change gate in CI, and CI workflows (test the core + build the GUI). This is the adoption lever the whole project was built for.

**Architecture:** `.goreleaser.yaml` (v2) cross-compiles the cgo-free CLI for linux/darwin/windows x amd64/arm64, produces archives + checksums, publishes a GitHub release, and pushes a Homebrew formula to `hoijun-kim/homebrew-tap`. `.github/workflows/` holds `ci.yml` (go test/vet + a Windows GUI build job that runs `wails generate module` before the frontend build) and `release.yml` (GoReleaser on a `v*` tag). A composite `action.yml` at the repo root downloads the release binary and runs `shape diff --fail-on breaking`. An `npm/` package downloads the platform binary on install. A top-level `README.md` documents every install path.

**Tech Stack:** GoReleaser v2, GitHub Actions, Homebrew, Node/npm (wrapper). Verification is config-validity (`goreleaser check`, `goreleaser build --snapshot`, `node --check`, yaml lint) not behavioral - releasing itself requires pushing a tag.

## Global Constraints

- Module `github.com/hoijun-kim/shape`. The CLI stays cgo-free (`CGO_ENABLED=0`); GoReleaser builds it for all platforms without cgo. The GUI (wails) is NOT part of the GoReleaser matrix (needs cgo on mac/linux); its Windows build is a CI job that can upload `shape-gui.exe` to the release, macOS/Linux GUI deferred.
- Version is injected via ldflags into `internal/cmd.Version` (currently `0.1.0-dev`).
- Plain ASCII hyphen `-` only. Conventional Commits, NO co-author trailer. These tasks are config/validity-gated (not TDD): the gate is that configs parse and validate.
- Licensing: this plan adds a `LICENSE` (MIT) so the Homebrew formula and release have one. MIT is the default for such tools - the USER should confirm or change the license before making the repo public.
- Secrets the workflows reference (documented, set by the user in the GitHub repo, not in the plan): `HOMEBREW_TAP_TOKEN` (a PAT with `contents:write` on `hoijun-kim/homebrew-tap`, since the default `GITHUB_TOKEN` cannot push to another repo). `GITHUB_TOKEN` is auto-provided.

---

### Task 1: GoReleaser config + release workflow + LICENSE

**Files:**
- Create: `.goreleaser.yaml`
- Create: `.github/workflows/release.yml`
- Create: `LICENSE`

- [ ] **Step 1: Install goreleaser (local validation tool)**

```bash
go install github.com/goreleaser/goreleaser/v2@latest
```
(Adds `goreleaser` to `$(go env GOPATH)/bin`; ensure it's on PATH for the checks below.)

- [ ] **Step 2: Create the LICENSE (MIT)**

`LICENSE` - a standard MIT license, copyright `2026 hoijun-kim`. (Use the canonical MIT text.)

- [ ] **Step 3: Write `.goreleaser.yaml`**

```yaml
version: 2

project_name: shape

before:
  hooks:
    - go mod tidy

builds:
  - id: shape
    main: .
    binary: shape
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w -X github.com/hoijun-kim/shape/internal/cmd.Version={{ .Version }}

archives:
  - id: shape
    ids: [shape]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]

checksum:
  name_template: "checksums.txt"

brews:
  - name: shape
    ids: [shape]
    repository:
      owner: hoijun-kim
      name: homebrew-tap
      token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"
    homepage: "https://github.com/hoijunkim/shape"
    description: "See the real shape of your structured data files"
    license: "MIT"
    test: |
      system "#{bin}/shape --version"

release:
  github:
    owner: hoijun-kim
    name: shape

changelog:
  use: github
```

- [ ] **Step 4: Write the release workflow**

`.github/workflows/release.yml`:

```yaml
name: release
on:
  push:
    tags:
      - 'v*'
permissions:
  contents: write
jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
      - uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: '~> v2'
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
```

- [ ] **Step 5: Validate (the gate)**

Run:
```bash
goreleaser check                       # config is valid
HOMEBREW_TAP_TOKEN=x goreleaser build --snapshot --clean   # cross-compiles all CLI targets, no publish
```
Expected: `goreleaser check` reports the config is valid; `goreleaser build --snapshot` produces `dist/` binaries for every goos/goarch (proving the cgo-free CLI cross-compiles). If `goreleaser check` flags a v2 schema difference (e.g. `formats` vs `format`), adjust to what `check` accepts and note it. Add `dist/` to `.gitignore`.

- [ ] **Step 6: Commit**

```bash
git add .goreleaser.yaml .github/workflows/release.yml LICENSE .gitignore
git commit -m "build: GoReleaser cross-platform release + Homebrew tap"
```

---

### Task 2: CI workflow (test the core + build the GUI)

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write `ci.yml`**

`.github/workflows/ci.yml`:

```yaml
name: ci
on:
  push:
    branches: [master]
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
      - run: go vet ./...
      - run: go test ./... -count=1
      - name: CLI builds cgo-free
        run: CGO_ENABLED=0 go build -o /dev/null .
  gui-build:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - name: Install wails
        run: go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
      - name: Generate bindings (before the frontend build)
        run: wails generate module
        working-directory: gui
      - name: Build frontend
        run: npm ci && npm run build && npm run check
        working-directory: gui/frontend
      - name: Build the desktop app
        run: wails build
        working-directory: gui
```

- [ ] **Step 2: Validate (the gate)**

Run `actionlint .github/workflows/ci.yml .github/workflows/release.yml` if actionlint is installed (`go install github.com/rhysd/actionlint/cmd/actionlint@latest`); otherwise validate the YAML parses (e.g. `python -c "import yaml,sys; yaml.safe_load(open(sys.argv[1]))" .github/workflows/ci.yml`). Confirm the go steps mirror commands that already pass locally (`go vet ./...`, `go test ./... -count=1`).

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: test the core and build the desktop GUI"
```

---

### Task 3: reusable GitHub Action for the `shape diff` CI gate

**Files:**
- Create: `action.yml` (composite action at repo root)
- Create: `docs/action-usage.md` (or a README section - see Task 4)

- [ ] **Step 1: Write `action.yml`**

```yaml
name: 'shape diff'
description: 'Fail CI when data-shape changes break consumers of the old snapshot'
branding:
  icon: 'git-commit'
  color: 'blue'
inputs:
  old:
    description: 'Path to the old (baseline) data file'
    required: true
  new:
    description: 'Path to the new data file'
    required: true
  fail-on:
    description: 'breaking | any | none'
    required: false
    default: 'breaking'
  version:
    description: 'shape release version tag (e.g. v1.2.0) or "latest"'
    required: false
    default: 'latest'
runs:
  using: composite
  steps:
    - name: Install shape and diff
      shell: bash
      env:
        SHAPE_VERSION: ${{ inputs.version }}
        SHAPE_OLD: ${{ inputs.old }}
        SHAPE_NEW: ${{ inputs.new }}
        SHAPE_FAIL_ON: ${{ inputs.fail-on }}
      run: |
        set -euo pipefail
        os="$(uname -s | tr '[:upper:]' '[:lower:]')"
        arch="$(uname -m)"
        case "$arch" in
          x86_64|amd64) arch=amd64 ;;
          aarch64|arm64) arch=arm64 ;;
          *) echo "unsupported arch: $arch" >&2; exit 2 ;;
        esac
        repo="hoijun-kim/shape"
        if [ "$SHAPE_VERSION" = "latest" ]; then
          base="https://github.com/$repo/releases/latest/download"
          ver="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed 's#.*/tag/##')"
        else
          base="https://github.com/$repo/releases/download/$SHAPE_VERSION"
          ver="$SHAPE_VERSION"
        fi
        v="${ver#v}"
        url="$base/shape_${v}_${os}_${arch}.tar.gz"
        echo "downloading $url"
        curl -fsSL "$url" | tar -xz shape
        chmod +x shape
        ./shape diff --fail-on "$SHAPE_FAIL_ON" "$SHAPE_OLD" "$SHAPE_NEW"
```

- [ ] **Step 2: Validate (the gate)**

- `bash -n` the embedded script (extract the `run:` block or lint with shellcheck if available: `go install`-free check via `shellcheck` if present).
- `actionlint action.yml` if actionlint is installed, else confirm the YAML parses.
- Sanity-check the archive name template matches Task 1's `name_template` (`shape_{version}_{os}_{arch}` + `.tar.gz`), so the download URL is correct.

- [ ] **Step 3: Commit**

```bash
git add action.yml
git commit -m "feat: reusable GitHub Action for the shape diff CI gate"
```

---

### Task 4: npm wrapper + README install/usage docs

**Files:**
- Create: `npm/package.json`, `npm/install.js`, `npm/bin/shape.js`, `npm/README.md`
- Create: `README.md` (repo top-level)
- Modify: `.gitignore` (npm/binary output)

- [ ] **Step 1: Write the npm wrapper**

`npm/package.json`:

```json
{
  "name": "@hoijun-kim/shape",
  "version": "0.0.0",
  "description": "See the real shape of your structured data files (JSON/NDJSON/CSV/Parquet/SQLite): profile, infer a JSON Schema, and diff snapshots for breaking changes.",
  "bin": { "shape": "bin/shape.js" },
  "scripts": { "postinstall": "node install.js" },
  "os": ["linux", "darwin", "win32"],
  "cpu": ["x64", "arm64"],
  "repository": { "type": "git", "url": "https://github.com/hoijunkim/shape" },
  "license": "MIT",
  "files": ["bin/", "install.js"]
}
```

`npm/install.js` (downloads the platform binary from GitHub releases into `bin/`):

```js
const fs = require("fs");
const path = require("path");
const https = require("https");
const { execSync } = require("child_process");

const REPO = "hoijun-kim/shape";
const version = require("./package.json").version;

const platform = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
if (!platform || !arch) {
  console.error(`shape: unsupported platform ${process.platform}/${process.arch}`);
  process.exit(1);
}

const ext = platform === "windows" ? "zip" : "tar.gz";
const asset = `shape_${version}_${platform}_${arch}.${ext}`;
const url = `https://github.com/${REPO}/releases/download/v${version}/${asset}`;
const binDir = path.join(__dirname, "bin");
fs.mkdirSync(binDir, { recursive: true });

function download(u, dest, redirects = 0) {
  return new Promise((resolve, reject) => {
    https.get(u, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        if (redirects > 10) return reject(new Error("too many redirects"));
        return resolve(download(res.headers.location, dest, redirects + 1));
      }
      if (res.statusCode !== 200) return reject(new Error(`HTTP ${res.statusCode} for ${u}`));
      const out = fs.createWriteStream(dest);
      res.pipe(out);
      out.on("finish", () => out.close(resolve));
    }).on("error", reject);
  });
}

(async () => {
  const archivePath = path.join(binDir, asset);
  await download(url, archivePath);
  if (ext === "zip") {
    execSync(`tar -xf "${archivePath}" -C "${binDir}"`); // bsdtar on win handles zip
  } else {
    execSync(`tar -xzf "${archivePath}" -C "${binDir}"`);
  }
  fs.rmSync(archivePath);
  const bin = path.join(binDir, platform === "windows" ? "shape.exe" : "shape");
  if (platform !== "windows") fs.chmodSync(bin, 0o755);
})().catch((e) => {
  console.error("shape: failed to download the binary:", e.message);
  process.exit(1);
});
```

`npm/bin/shape.js` (the bin shim that execs the downloaded binary):

```js
#!/usr/bin/env node
const path = require("path");
const { spawnSync } = require("child_process");
const bin = path.join(__dirname, process.platform === "win32" ? "shape.exe" : "shape");
const r = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
process.exit(r.status === null ? 1 : r.status);
```

`npm/README.md`: a short "npm install -g @hoijun-kim/shape" + link to the main README.

- [ ] **Step 2: Write the top-level README**

`README.md` covering: what shape is (profile / schema / diff, 6 formats, bounded-memory approximate mode, desktop GUI); install (`go install github.com/hoijun-kim/shape@latest`, `brew install hoijun-kim/tap/shape`, `npm i -g @hoijun-kim/shape`, download from Releases); usage examples for `shape profile`, `shape schema`, `shape diff --fail-on breaking`; the GitHub Action snippet:

```yaml
- uses: hoijun-kim/shape@v1
  with:
    old: baseline.ndjson
    new: current.ndjson
    fail-on: breaking
```

and a note on the desktop GUI (`gui/`, built with `wails build`).

- [ ] **Step 3: Validate (the gate)**

- `node --check npm/install.js` and `node --check npm/bin/shape.js` (syntax).
- Confirm `npm/package.json` is valid JSON (`node -e "require('./npm/package.json')"`).
- Confirm the asset name in `install.js` matches Task 1's archive `name_template` and `action.yml`'s URL exactly.
- Add `npm/bin/shape` and `npm/bin/shape.exe` and `npm/node_modules/` to `.gitignore`.

- [ ] **Step 4: Commit**

```bash
git add npm/package.json npm/install.js npm/bin/shape.js npm/README.md README.md .gitignore
git commit -m "docs: npm wrapper and install/usage README"
```

---

## Plan 7 self-review

Coverage: GoReleaser cross-platform CLI release + Homebrew tap + LICENSE (Task 1), CI workflows for the core + GUI (Task 2), a reusable `shape diff` GitHub Action (Task 3), the npm wrapper + README (Task 4). Every distribution path (go install, brew, npm, Action, direct download) is covered. The CI GUI job runs `wails generate module` before the frontend build (closing the reproducibility gap flagged in Plan 6).

Validity gates (not TDD - releasing needs a tag): `goreleaser check` + `goreleaser build --snapshot` (proves the cgo-free CLI cross-compiles for all targets), yaml/actionlint validation of the workflows and action, `node --check` on the wrapper. The archive/asset name template is kept consistent across `.goreleaser.yaml`, `action.yml`, and `npm/install.js` so the download URLs line up.

Consistency: the version string flows from a git tag -> GoReleaser ldflags -> `internal/cmd.Version`; the npm `version` and the release tag must match at publish time (a release step, not a code concern).

Decisions the USER must make before publishing (flagged, not assumed): confirm/replace the MIT LICENSE; create the `hoijun-kim/homebrew-tap` repo and set the `HOMEBREW_TAP_TOKEN` secret; decide the npm scope/name and reserve it; flip the repo public for release. The Action, brew, and npm paths only work once the repo is public and a `v*` tag is pushed.

Out of scope (later): macOS/Linux GUI release binaries (need cgo + native webkit runners); signing/notarization; a Scoop/winget manifest; a Docker image.

## Done
This is the final planned distribution work. After Plan 7, shape is: a cgo-free CLI (profile/schema/diff over JSON/NDJSON/CSV/TSV/Parquet/SQLite with bounded-memory approximate mode), a Wails desktop GUI, and a full distribution surface (release binaries, Homebrew, npm, a CI Action, CI). Remaining items are the user-owned publish steps above and the deferred feature polish tracked in earlier plans.
