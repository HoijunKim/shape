# shape E11 — Saved Views Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Save the current query shape (filter + search + sort + reshape) under a name and re-apply it across restarts, persisted to a config JSON file.

**Architecture:** A new `App.LoadViews`/`SaveViews` pair persists an OPAQUE JSON string to `os.UserConfigDir()/shape/views.json` (atomic temp+rename); the view schema lives in the frontend. The store holds a `views: SavedView[]` slice loaded at init, with `saveView`/`applyView`/`deleteView`. A header `ViewsMenu` dropdown drives them. `applyView` restores all four dimensions by reusing the existing setters (their requery supersession makes intermediate re-queries harmless).

**Tech Stack:** Go 1.25 (`gui`), Wails v2.12.0 bindings, Svelte 3 + TypeScript, Vitest, `go test`.

## Global Constraints

- cgo-free; no new runtime deps (go.mod/go.sum unchanged; no `dependencies` growth).
- Conventional Commits, lowercase imperative, NO `Co-Authored-By` trailer.
- Every test carries a mutation proof.
- Go tests MUST NOT touch the real user profile — redirect the config dir with `t.Setenv` (Windows: `APPDATA`; the test asserts via the returned path).
- After `wails build` never `git add -A`; revert build churn. `wails generate module` output committed WITH the Go change; final generate diff empty.
- User performs the `--no-ff` merge. Branch: `feat/e11-saved-views` off current master.

---

### Task 1: `App.LoadViews` / `App.SaveViews` (config-file persistence)

**Files:**
- Modify: `gui/app.go` (new methods + a `viewsPath()` helper + a small atomic write)
- Modify: `gui/app_test.go`
- Regenerate: `gui/frontend/wailsjs/go/main/App.d.ts`, `App.js`

**Interfaces:**
- Produces: `func (a *App) LoadViews() (string, error)` (returns `("", nil)` if the file is absent), `func (a *App) SaveViews(payload string) error` (atomic, creates the dir). Both are pure persistence — they neither parse nor validate the payload.

- [ ] **Step 1: Write the failing test**

Add to `gui/app_test.go` (redirect the config dir to a temp dir so the real profile is untouched):

```go
func TestApp_ViewsRoundTripAndAbsent(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // os.UserConfigDir() reads APPDATA on Windows
	a := &App{eng: query.NewEngine()}

	// Absent file -> ("", nil), not an error.
	got, err := a.LoadViews()
	if err != nil || got != "" {
		t.Fatalf("LoadViews(absent) = (%q, %v), want (\"\", nil)", got, err)
	}

	payload := `[{"name":"v1","filter":{"combinator":"and"}}]`
	if err := a.SaveViews(payload); err != nil {
		t.Fatalf("SaveViews: %v", err)
	}
	got, err = a.LoadViews()
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if got != payload {
		t.Fatalf("round-trip = %q, want %q", got, payload)
	}
	// It landed under shape/views.json, and no temp file survives.
	dir := filepath.Dir(viewsPathForTest(t))
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "views.json" {
			t.Fatalf("stray file in config dir: %s (atomic write must clean up)", e.Name())
		}
	}
}
```

(`viewsPathForTest` is a tiny test helper calling the same `viewsPath()`; or assert `os.Stat` on `filepath.Join(os.UserConfigDir(), "shape", "views.json")`. On non-Windows CI `os.UserConfigDir` reads `XDG_CONFIG_HOME`/`HOME` — set `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` too so the test is cross-platform.)

- [ ] **Step 2: Run — FAIL** (`a.LoadViews` undefined). `go test ./gui/ -run TestApp_Views -count=1`

- [ ] **Step 3: Implement in `gui/app.go`**

```go
// viewsPath is the saved-views config file: <UserConfigDir>/shape/views.json.
func viewsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shape", "views.json"), nil
}

// LoadViews returns the saved-views JSON blob, or "" if none has been saved yet.
// The payload is opaque here -- the frontend owns and validates the view schema.
func (a *App) LoadViews() (string, error) {
	path, err := viewsPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SaveViews atomically writes the saved-views JSON blob (temp file + rename in
// the same dir, so a crash mid-write never corrupts an existing views.json).
func (a *App) SaveViews(payload string) error {
	path, err := viewsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "views-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(payload); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
```
Add imports `errors`, `os`, `path/filepath` to `gui/app.go` if missing. (These are NOT on the `sourceEngine` interface — they are pure App methods, no engine involvement.)

- [ ] **Step 4: Run — PASS.**

- [ ] **Step 5: Prove the mutation**

In `SaveViews`, replace the temp+rename with a direct `os.WriteFile(path, []byte(payload), 0o644)` AND change the loop's temp-file guard... simpler: mutate `LoadViews`'s absent-file branch to `return "", err` (drop the `errors.Is(os.ErrNotExist)` special-case). Run Step 4 — the absent-file assertion fails (LoadViews returns an error instead of ""). Restore. (Atomicity is covered by the "no stray temp file" assertion; a mutation dropping `os.Remove(tmpName)` on the error paths leaves a stray tmp — but those paths need a write failure to reach, so the primary mutation is the absent-file one.)

- [ ] **Step 6: Regenerate bindings + commit**

`cd gui && wails generate module` → `App.d.ts`/`App.js` gain `LoadViews`/`SaveViews`. Revert `wailsjs/runtime/*` churn. `gofmt -l gui/app.go` clean; `go test ./gui/ -count=1`.
```bash
git add gui/app.go gui/app_test.go gui/frontend/wailsjs/go/main/App.d.ts gui/frontend/wailsjs/go/main/App.js
git commit -m "feat(gui): LoadViews/SaveViews persist saved views to a config file"
```

---

### Task 2: store `views` slice + saveView/applyView/deleteView

**Files:**
- Modify: `gui/frontend/src/lib/explorer/types.ts` (a `SavedView` interface)
- Modify: `gui/frontend/src/lib/explorer/store.ts`
- Modify: `gui/frontend/src/lib/explorer/store.test.ts`

**Interfaces:**
- Consumes: generated `LoadViews`/`SaveViews`; `transformModel.projectedColumns`; the existing `setFilter`/`setSearch`/`setSort`/`setTransform` + `currentFilter`/`currentSearch`/`currentSort`/`currentTransform`.
- Produces: `SavedView { name; filter: Filter; search: string; sort: SortSpec; transform: Transform }`; `ExplorerState.views: SavedView[]`; `explorer.saveView(name)`, `explorer.applyView(name)`, `explorer.deleteView(name)`.

- [ ] **Step 1: types + init load (no test yet)**

In `types.ts`:
```ts
export interface SavedView {
  name: string;
  filter: Filter;
  search: string;
  sort: SortSpec;
  transform: Transform;
}
```
In `store.ts`: add `views: SavedView[]` to `ExplorerState` + `empty` (`views: []`). Add a module-level load at store creation:
```ts
let views: SavedView[] = [];
(async () => {
  try {
    const raw = await LoadViews();
    if (raw) views = JSON.parse(raw) as SavedView[];
    update((s) => ({ ...s, views }));
  } catch (e) {
    console.warn("saved views: could not load", e); // a bad file must not crash the app
  }
})();
```
`import { LoadViews, SaveViews } from "../../../wailsjs/go/main/App";` and `SavedView` from `./types`.

- [ ] **Step 2: Write the failing test**

In `store.test.ts` add `LoadViews: vi.fn(() => Promise.resolve(""))`, `SaveViews: vi.fn(() => Promise.resolve())` to the App mock + reset in beforeEach. Then:

```ts
it("saveView snapshots the current query shape and persists it; applyView restores it", async () => {
  await openMemory();
  explorer.setSort({ path: "n", desc: true } as any);
  await flush();

  explorer.saveView("v1");
  await flush();
  // Persisted with the ACTIVE sort in the snapshot (mutation: omit sort -> fails).
  const payload = JSON.parse(vi.mocked(SaveViews).mock.calls.at(-1)![0] as string);
  expect(payload[0]).toMatchObject({ name: "v1", sort: { path: "n", desc: true } });

  // Change the sort, then applyView restores v1's sort.
  explorer.setSort({ path: "", desc: false } as any);
  await flush();
  explorer.applyView("v1");
  await flush();
  // Mutation: applyView skips the sort restore -> the next QueryRows lacks it.
  const q = vi.mocked(QueryRows).mock.calls.at(-1)![0] as any;
  expect(q.sort).toEqual({ path: "n", desc: true });
});

it("deleteView removes and persists; saveView upserts by name", async () => {
  await openMemory();
  explorer.saveView("v1"); await flush();
  explorer.saveView("v1"); await flush(); // upsert, not append
  expect(get(explorer).views.filter((v) => v.name === "v1")).toHaveLength(1);
  explorer.deleteView("v1"); await flush();
  expect(get(explorer).views.some((v) => v.name === "v1")).toBe(false);
  expect(vi.mocked(SaveViews)).toHaveBeenCalled();
});
```

- [ ] **Step 3: Run — FAIL** (`saveView` not a function).

- [ ] **Step 4: Implement**

```ts
function persistViews(): void {
  update((s) => ({ ...s, views: [...views] }));
  void SaveViews(JSON.stringify(views)).catch((e) => console.warn("saved views: could not save", e));
}

function saveView(name: string): void {
  if (!name) return;
  const v: SavedView = { name, filter: currentFilter, search: currentSearch, sort: currentSort, transform: currentTransform };
  const i = views.findIndex((x) => x.name === name);
  if (i >= 0) views[i] = v; else views.push(v); // upsert by name
  persistViews();
}

function applyView(name: string): void {
  const v = views.find((x) => x.name === name);
  if (!v) return;
  // Restore all four dimensions. The setters each requery; requery supersedes,
  // so only the final (setTransform) completes -- one effective query.
  setFilter(v.filter);
  setSearch(v.search);
  setSort(v.sort);
  const s = get({ subscribe });
  setTransform(v.transform, projectedColumns(s.baseColumns, v.transform));
}

function deleteView(name: string): void {
  views = views.filter((x) => x.name !== name);
  persistViews();
}
```
Import `projectedColumns` from `./transformModel`. Export `saveView, applyView, deleteView` in the returned object. (Confirm `projectedColumns`'s exact signature by reading transformModel.ts; adjust the call. If `setSearch("")` on an empty search is a wasteful no-op requery, it is still correct — requery supersedes.)

- [ ] **Step 5: Run — PASS. Prove the mutations** — (a) omit `sort` from the `saveView` snapshot → the persist test fails; (b) drop the `setSort(v.sort)` line in `applyView` → the restore test's `q.sort` fails; (c) `views.push(v)` unconditionally (no upsert) → the upsert test finds two. Restore each.

- [ ] **Step 6: check + commit** — `npm run check` 0 errors; full `store.test.ts`. Commit `feat(gui): store saveView/applyView/deleteView over a persisted views slice`.

---

### Task 3: `ViewsMenu.svelte` header dropdown

**Files:**
- Create: `gui/frontend/src/lib/explorer/ViewsMenu.svelte`
- Create: `gui/frontend/src/lib/explorer/ViewsMenu.test.ts`

**Interfaces:**
- Consumes: `$explorer.views`, `explorer.saveView/applyView/deleteView`, `$explorer.status`.
- Produces: a dropdown (bindable `open`) with a name input + Save (disabled when blank or `status !== "ready"`), a list of views (click → apply, `×` → delete), an empty state; Escape/backdrop close.

- [ ] **Step 1: Write the failing test** (mocked store, mirroring `SaveDialog.test.ts`): with `open` + a `views` list, a name input + Save calls `saveView(name)`; a view-row click calls `applyView("v1")`; the `×` calls `deleteView("v1")`; Save is disabled when the input is blank. Mutation for each (Save calls applyView instead of saveView; row click passes the wrong name).

- [ ] **Step 2–5:** FAIL → implement the dropdown (reuse SaveDialog's backdrop/Escape/focus pattern; a `<ul>` of view rows) → PASS → prove the mutations.

- [ ] **Step 6: check + commit** `feat(gui): ViewsMenu dropdown to save, apply, and delete views`.

---

### Task 4: App/Header wiring (the Views toggle)

**Files:**
- Modify: `gui/frontend/src/App.svelte` (a `viewsOpen` state + mount `ViewsMenu`, or route it into `Explorer` like the other panels), `gui/frontend/src/lib/Header.svelte` (a "Views" button), and `Explorer.svelte` if the menu mounts there (mirror `exportOpen`/`codeOpen`).
- Modify: the relevant test (`Header.test.ts` or `Explorer.test.ts`).

**Interfaces:**
- Produces: a header **Views** button toggling `viewsOpen`; `ViewsMenu` mounted with `open={viewsOpen}`, closing back via its `close` event.

- [ ] **Steps:** TDD the Header button (dispatches/toggles `viewsOpen`, mirroring the Export/Code buttons — read `Header.svelte` + `Header.test.ts` for the exact pattern) → wire `ViewsMenu` into the mount point that owns the other dialogs (`Explorer.svelte`) → mutation (the button toggles the wrong flag) → commit `feat(gui): Views button in the header opens the saved-views menu`.

---

### Task 5: verification + docs

- [ ] **Step 1: Gates** — `go test ./... -count=1`; `CGO_ENABLED=0 go build -o /dev/null .`; `cd gui/frontend && npm run check` (0 errors); `npx vitest run` (note the total); `git diff --stat go.mod go.sum` empty; `cd gui && wails build` (succeeds), revert build churn, do NOT `git add -A`; `wails generate module` empty diff vs committed.

- [ ] **Step 2: Docs** — README.md: a "Saved views" bullet in the Desktop GUI list (save the current filter/search/sort/reshape under a name, re-apply anytime). gui/README.md: a section — what a view captures, that it is global/best-effort across files, and that it persists to `<user config dir>/shape/views.json`.

- [ ] **Step 3: Commit** `docs: document saved views`.

- [ ] **Step 4: Hand off** for the whole-branch adversarial review, then the user's `--no-ff` merge.

---

## Self-review (against the spec)

**Coverage:** config-file persistence with atomic write + absent-file handling (T1); the `SavedView` type + views slice + save/apply/delete + init load + upsert + best-effort apply (T2); the header dropdown UI (T3); the Views toggle wiring (T4); docs incl. the file location (T5). Non-goals (no per-file scoping, no rename, opaque Go blob) honored — no task adds them. ✓
**Placeholders:** every step has concrete code or an exact file:line pattern to mirror; the two "confirm `projectedColumns` signature" / "read Header pattern" notes point at real files. No TODO/TBD. ✓
**Types:** `SavedView { name, filter, search, sort, transform }` used identically T2↔T3; the Go blob is opaque (T1) and the frontend serialises `SavedView[]` (T2); `applyView` reuses the four existing setters with their known signatures. ✓
