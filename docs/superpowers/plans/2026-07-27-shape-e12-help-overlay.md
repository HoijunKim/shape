# shape E12 — Help Overlay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** A header "?" Help button opens an opaque overlay explaining every feature; it auto-opens once on first launch.

**Architecture:** `App.HelpSeen`/`MarkHelpSeen` persist a one-boolean flag file next to views.json (a shared `configPath` helper factors the path). A `HelpOverlay.svelte` (opaque modal, grouped static content) is mounted App-level with a `helpOpen` state; `App.svelte`'s onMount auto-opens it when `HelpSeen()` is false.

**Tech Stack:** Go 1.25 (`gui`), Wails v2.12.0 bindings, Svelte 3 + TS, Vitest.

## Global Constraints

- No new deps. cgo-free. Conventional Commits, lowercase imperative, NO co-author trailer. Every test carries a mutation proof.
- Go tests MUST NOT touch the real profile — redirect the config dir (`t.Setenv` APPDATA + XDG_CONFIG_HOME + HOME to one temp dir).
- After `wails build` never `git add -A`; revert build churn; bindings committed with the Go change (final `wails generate module` diff empty).
- User performs the `--no-ff` merge. Branch: `feat/e12-help-overlay` off current master.

---

### Task 1: `App.HelpSeen` / `MarkHelpSeen` + `configPath`

**Files:** Modify `gui/app.go`, `gui/app_test.go`; regenerate `App.d.ts`/`App.js`.

**Interfaces:**
- Produces: `func (a *App) HelpSeen() (bool, error)` (false if the flag file is absent, never an error for absence); `func (a *App) MarkHelpSeen() error`; `func configPath(name string) (string, error)`.

- [ ] **Step 1: Refactor viewsPath → configPath (no behaviour change)**

In `gui/app.go`, replace `viewsPath()` with a general helper + keep views using it:
```go
// configPath is <UserConfigDir>/shape/<name> -- the app's config-file home.
func configPath(name string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shape", name), nil
}
```
Replace the body of `viewsPath()` with `return configPath("views.json")` (or delete `viewsPath` and change its two call sites in LoadViews/SaveViews to `configPath("views.json")`). Run `go test ./gui/ -run TestApp_Views -count=1` — the existing E11 round-trip test MUST still pass (this is the refactor's regression net).

- [ ] **Step 2: Write the failing test**

Add to `gui/app_test.go`:
```go
func TestApp_HelpSeen(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	a := &App{eng: query.NewEngine()}

	seen, err := a.HelpSeen()
	if err != nil || seen {
		t.Fatalf("HelpSeen(absent) = (%v, %v), want (false, nil)", seen, err)
	}
	if err := a.MarkHelpSeen(); err != nil {
		t.Fatalf("MarkHelpSeen: %v", err)
	}
	seen, err = a.HelpSeen()
	if err != nil || !seen {
		t.Fatalf("HelpSeen(after mark) = (%v, %v), want (true, nil)", seen, err)
	}
}
```

- [ ] **Step 3: Run — FAIL** (`a.HelpSeen` undefined). `go test ./gui/ -run TestApp_HelpSeen -count=1`

- [ ] **Step 4: Implement**

```go
// HelpSeen reports whether the first-launch help overlay has been shown (a flag
// file under the config dir). An absent/unreadable flag is "not seen", never a
// startup-blocking error.
func (a *App) HelpSeen() (bool, error) {
	path, err := configPath("help-seen")
	if err != nil {
		return false, nil // no config dir -> treat as not seen, don't block startup
	}
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	return true, nil
}

// MarkHelpSeen records that the help overlay has been shown (write-once, so no
// atomic rename needed).
func (a *App) MarkHelpSeen() error {
	path, err := configPath("help-seen")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("1"), 0o644)
}
```

- [ ] **Step 5: Run — PASS. Prove the mutation** — make `HelpSeen` `return true, nil` unconditionally → the absent-flag assertion fails. Restore.

- [ ] **Step 6: Regenerate bindings + commit** — `cd gui && wails generate module`; revert runtime churn; `gofmt -l gui/app.go` clean; `go test ./gui/`.
```bash
git add gui/app.go gui/app_test.go gui/frontend/wailsjs/go/main/App.d.ts gui/frontend/wailsjs/go/main/App.js
git commit -m "feat(gui): HelpSeen/MarkHelpSeen persist the first-launch flag"
```

---

### Task 2: `HelpOverlay.svelte`

**Files:** Create `gui/frontend/src/lib/HelpOverlay.svelte`, `gui/frontend/src/lib/HelpOverlay.test.ts`.

**Interfaces:**
- Produces: `HelpOverlay` with a bindable `open`; an OPAQUE backdrop, a scrollable `role="dialog"` panel with the grouped feature sections; `×`/Escape/backdrop dispatch `close`; focus trap + restore.

- [ ] **Step 1: Write the failing test** (mirror SaveDialog.test's harness): renders nothing when `open=false`; when open, renders the section headings (e.g. "Getting started", "Shape the query", "Reuse & take away") and a feature name (e.g. "Saved views"); `×` and Escape each dispatch `close`; the backdrop has an `.opaque` class (not the transparent scrim). Mutation: Escape doesn't dispatch close.

- [ ] **Step 2: Run — FAIL.**

- [ ] **Step 3: Implement** — model the structure on `SaveDialog.svelte` (backdrop + dialog + Escape/focus-trap), but: the backdrop is `.backdrop.opaque` with a solid dim (`background: color-mix(in srgb, var(--surface-1) 88%, transparent)` or a solid rgba), the panel is wider + `max-height: 85vh; overflow-y: auto`, and the body is the grouped content from the spec: a `<section>` per group with an `<h3>` heading and a `<dl>`/list of `{name, description}` items. Keep the copy from the spec verbatim. Add a favicon-free, self-contained layout (no external assets).

- [ ] **Step 4: Run — PASS. Prove the mutation** — remove the Escape branch → the Escape-close test fails. Restore.

- [ ] **Step 5: check + commit** — `npm run check`; the test file.
```bash
git add gui/frontend/src/lib/HelpOverlay.svelte gui/frontend/src/lib/HelpOverlay.test.ts
git commit -m "feat(gui): HelpOverlay explains every feature in an opaque modal"
```

---

### Task 3: Header "?" button + App wiring + first-run auto-open

**Files:** Modify `gui/frontend/src/lib/Header.svelte` (a "?" button + `helpOpen` prop + `toggleHelp` dispatch), `gui/frontend/src/App.svelte` (mount HelpOverlay App-level + `helpOpen` state + onMount auto-open), `gui/frontend/src/lib/Header.test.ts`, and an App-level first-run test (a new small test file or App.test.ts if present; else Header covers the button and the auto-open is proven at the store/binding boundary).

**Interfaces:**
- Consumes: `HelpSeen`/`MarkHelpSeen` (generated); `HelpOverlay`.
- Produces: a header "?" button toggling `helpOpen`; `HelpOverlay open={helpOpen}` mounted in App; first-run auto-open.

- [ ] **Step 1: Header button** — TDD the "?" button in Header.test.ts (dispatches `toggleHelp`, `aria-pressed` reflects `helpOpen`), mirroring the Views-button test. Add `export let helpOpen = false;` + `toggleHelp: void;` to Header, and the button (a "?" glyph, `title="Help"`). Mutation: the button dispatches the wrong event.

- [ ] **Step 2: App wiring** — in `App.svelte`: `let helpOpen = false;`; import `HelpOverlay` + `HelpSeen, MarkHelpSeen` from the App bindings; pass `{helpOpen}` to Header + `on:toggleHelp={() => (helpOpen = !helpOpen)}`; mount `<HelpOverlay open={helpOpen} on:close={() => (helpOpen = false)} />` at App level (sibling of ViewsMenu). In `onMount`, wrapped in try/catch:
```ts
try {
  if (!(await HelpSeen())) {
    helpOpen = true;
    void MarkHelpSeen();
  }
} catch { /* persistence failure -> just skip the auto-open, never block startup */ }
```

- [ ] **Step 3: First-run test** — if App.test.ts exists, TDD there: mock `HelpSeen` → false → after mount `helpOpen` is true + `MarkHelpSeen` called; `HelpSeen` → true → `helpOpen` stays false (mutation: auto-open ignores HelpSeen → the seen case fails). If App.svelte has no test harness, add one modeled on the existing component tests (mock the App bindings + mount App), OR — if mounting App is impractical — extract the first-run decision into a tiny pure helper `shouldAutoOpenHelp(seen: boolean): boolean { return !seen; }` and unit-test that + assert the onMount calls it. Prefer the real App-mount test if the harness allows.

- [ ] **Step 4: full suite + check + commit** — `npx vitest run`; `npm run check`.
```bash
git add gui/frontend/src/lib/Header.svelte gui/frontend/src/lib/Header.test.ts gui/frontend/src/App.svelte  # + any App test
git commit -m "feat(gui): Help button + first-launch auto-open of the help overlay"
```

---

### Task 4: verification + docs

- [ ] **Step 1: Gates** — `go test ./...`; `CGO_ENABLED=0 go build`; `npm run check` (0 errors); `npx vitest run`; `git diff --stat go.mod go.sum` empty; `wails build` (revert churn, no `git add -A`); `wails generate module` empty diff.
- [ ] **Step 2: Docs** — gui/README.md: a short note that the header "?" opens a help overlay covering every feature, shown automatically on first launch. (README.md optional — a one-liner in the GUI list if it fits.)
- [ ] **Step 3: Commit** `docs: document the help overlay`.
- [ ] **Step 4: Hand off** for the whole-branch review, then the user's merge.

---

## Self-review (against the spec)

**Coverage:** persisted first-launch flag + configPath refactor (T1); the opaque overlay with grouped content + close/focus (T2); the "?" button + App mount + first-run auto-open (T3); docs (T4). Non-goals (no coach-marks, no what's-new, static copy) honored. ✓
**Placeholders:** T2 Step 3 references the spec's verbatim copy + the SaveDialog pattern to mirror (concrete files); T3 Step 3 gives a fallback if App has no test harness. No TODO/TBD. ✓
**Types:** `HelpSeen()(bool,error)` / `MarkHelpSeen()error` used in T1↔T3; `helpOpen`/`toggleHelp` dispatched (Header) == consumed (App); `configPath("views.json")` preserves E11's path (regression-guarded by TestApp_Views). ✓
