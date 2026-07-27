# shape E8 - Column Statistics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expand a field in the structure-map sidebar to show that column's full profile (distribution histogram / top-values / type-mix / meters / quantiles / badges), fetched lazily from the profile the backend already retains.

**Architecture:** A new lazy `Engine.ColumnStats(handle, path)` returns the `internal/visual.FieldCard` for one source field (built from `backend.Profile()` - no rescan), mirroring E6's per-cell `GetCell`. A new Wails binding + a `store.getColumnStats` wrapper feed a new `FieldStatsPanel.svelte`, which `TreeNode` mounts inline under a field row on a stats toggle, rendering the already-built (but currently unmounted) `FieldDetail` + `charts/*` components.

**Tech Stack:** Go 1.25 (`internal/query`, `internal/visual`, `internal/profile`), Wails v2.12.0 bindings, Svelte 3 + TypeScript, Vitest, `go test`.

## Global Constraints

- cgo-free; `CGO_ENABLED=0 go build` must stay green. No DuckDB.
- No new runtime dependencies (no `dependencies` block growth in `gui/frontend/package.json`; `go.mod`/`go.sum` unchanged).
- Conventional Commits, lowercase imperative subject, **NO** `Co-Authored-By` trailer.
- Every test carries a mutation proof: break the logic, watch the specific test fail, restore.
- Windows: gofmt check via `git show :file | tr -d '\r' | gofmt -l` (or run `gofmt -l` on the file after `tr -d '\r'`); core.autocrlf=true.
- After `wails build`: **never** `git add -A` (it deletes the tracked `gui/frontend/dist/.gitkeep`); revert build churn to `go.mod` / `gui/frontend/dist/.gitkeep` / `gui/frontend/wailsjs/runtime/*` (line-ending-only) before committing.
- `wails generate module` output (`App.d.ts`, `App.js`, `models.ts`) is committed WITH the Go change; the final `wails generate module` must produce an empty diff against what is committed.
- The user performs/authorizes the `--no-ff` merge. Branch: `feat/e8-column-stats` off current master.

---

### Task 1: `Engine.ColumnStats` (the lazy per-column stats method)

**Files:**
- Create: `internal/query/columnstats.go`
- Create: `internal/query/columnstats_test.go`

**Interfaces:**
- Consumes: `(*Engine).lookup(handle) (Backend, error)` (existing, `engine.go`); `Backend.Profile() profile.ProfileResult` (existing, `backend.go:111`); `visual.FromProfile(profile.ProfileResult, visual.Options) visual.VisualModel` and `visual.FieldCard` (existing, `internal/visual`).
- Produces: `ColumnStatsRequest{Handle, Path string}`, `ColumnStatsResult{Card visual.FieldCard; Found bool}`, `func (e *Engine) ColumnStats(ctx context.Context, req ColumnStatsRequest) (ColumnStatsResult, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/query/columnstats_test.go`:

```go
package query

import (
	"context"
	"testing"
)

// numericStatsFixture: a field "n" with a spread of numeric values so the
// profiler builds a histogram, plus a string field "s".
func numericStatsFixture() []map[string]any {
	recs := make([]map[string]any, 0, 20)
	for i := 0; i < 20; i++ {
		recs = append(recs, map[string]any{"n": i, "s": "row"})
	}
	return recs
}

func TestColumnStats_NumericFieldHasHistogram(t *testing.T) {
	eng, handle, _ := openExportFixture(t, numericStatsFixture(), 0)

	res, err := eng.ColumnStats(context.Background(), ColumnStatsRequest{Handle: handle, Path: "n"})
	if err != nil {
		t.Fatalf("ColumnStats error = %v, want nil", err)
	}
	if !res.Found {
		t.Fatalf("Found = false, want true for an existing field")
	}
	// The mutation (return Fields[0] ignoring Path) can still pass this if "n"
	// sorts first, so ALSO assert the returned card is actually "n".
	if res.Card.Path != "n" {
		t.Fatalf("Card.Path = %q, want %q", res.Card.Path, "n")
	}
	if res.Card.Histogram == nil {
		t.Fatalf("Card.Histogram = nil, want a histogram for a numeric field")
	}
}

func TestColumnStats_UnknownPathIsNotFound(t *testing.T) {
	eng, handle, _ := openExportFixture(t, numericStatsFixture(), 0)

	res, err := eng.ColumnStats(context.Background(), ColumnStatsRequest{Handle: handle, Path: "does_not_exist"})
	if err != nil {
		t.Fatalf("ColumnStats error = %v, want nil", err)
	}
	if res.Found {
		t.Fatalf("Found = true, want false for an unknown path")
	}
}

func TestColumnStats_UnknownHandleErrors(t *testing.T) {
	eng := NewEngine()
	_, err := eng.ColumnStats(context.Background(), ColumnStatsRequest{Handle: "nope", Path: "n"})
	if err == nil {
		t.Fatalf("ColumnStats error = nil, want an unknown-handle error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/query/ -run TestColumnStats -count=1`
Expected: FAIL - `eng.ColumnStats` undefined (does not compile).

- [ ] **Step 3: Write the implementation**

Create `internal/query/columnstats.go`:

```go
package query

import (
	"context"

	"github.com/hoijun-kim/shape/internal/visual"
)

// ColumnStatsRequest asks for the rich profile of ONE source field.
type ColumnStatsRequest struct {
	Handle string `json:"handle"`
	Path   string `json:"path"`
}

// ColumnStatsResult carries the visual FieldCard for that field. Found is false
// when no field with the requested path is in the source's profile (e.g. a
// projected/renamed output column, which is not a source field).
type ColumnStatsResult struct {
	Card  visual.FieldCard `json:"card"`
	Found bool             `json:"found"`
}

// ColumnStats returns the visual FieldCard for one source field, built from the
// profile the backend already retains from the open-time scan (no rescan). It
// mirrors GetCell: a lazy, per-item lookup that owns no state. ctx is accepted
// for binding-signature symmetry; the lookup is in-memory and does not block.
func (e *Engine) ColumnStats(ctx context.Context, req ColumnStatsRequest) (ColumnStatsResult, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return ColumnStatsResult{}, err
	}
	// FromProfile is pure geometry over already-computed stats. Only the
	// per-field Fields are consumed here, so Options can be zero (its
	// Name/Format feed the whole-model Summary/KPIs, which E8 ignores).
	model := visual.FromProfile(backend.Profile(), visual.Options{})
	for _, c := range model.Fields {
		if c.Path == req.Path {
			return ColumnStatsResult{Card: c, Found: true}, nil
		}
	}
	return ColumnStatsResult{Found: false}, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/query/ -run TestColumnStats -count=1`
Expected: PASS (3 tests).

- [ ] **Step 5: Prove the mutations**

Mutation A - ignore the path, return the first card. In `columnstats.go` replace the loop body's match with `if true {`:
```go
	for _, c := range model.Fields {
		if true { // MUTATION
			return ColumnStatsResult{Card: c, Found: true}, nil
		}
	}
```
Run: `go test ./internal/query/ -run TestColumnStats -count=1`
Expected: FAIL - `TestColumnStats_UnknownPathIsNotFound` (Found=true for a missing path). Restore.

Mutation B - always Found. Change the final return to `return ColumnStatsResult{Card: model.Fields[0], Found: true}, nil`.
Run: same. Expected: FAIL - `TestColumnStats_UnknownPathIsNotFound`. Restore.

- [ ] **Step 6: gofmt + full package + commit**

Run: `gofmt -l internal/query/columnstats.go internal/query/columnstats_test.go` (expect no output; on Windows pipe each through `tr -d '\r'` first).
Run: `go test ./internal/query/ -count=1` (expect ok).
```bash
git add internal/query/columnstats.go internal/query/columnstats_test.go
git commit -m "feat(query): ColumnStats returns one field's visual profile card"
```

---

### Task 2: `App.ColumnStats` Wails binding + regenerated bindings

**Files:**
- Modify: `gui/app.go` (the `sourceEngine` interface at `:25`, and a new method near `App.GetCell` at `:336`)
- Modify: `gui/app_test.go` (new test)
- Regenerate: `gui/frontend/wailsjs/go/main/App.d.ts`, `App.js`, `gui/frontend/wailsjs/go/models.ts`

**Interfaces:**
- Consumes: `Engine.ColumnStats` (Task 1); `App.reqCtx() context.Context` (existing); the embedded-`*query.Engine` spy engines already inherit `ColumnStats`, so no spy needs a new method.
- Produces: `func (a *App) ColumnStats(req query.ColumnStatsRequest) (query.ColumnStatsResult, error)`; the generated TS `ColumnStats` in `App.js`/`App.d.ts` and `query.ColumnStatsResult` (+ transitive `visual.FieldCard`) in `models.ts`.

- [ ] **Step 1: Write the failing test**

Add to `gui/app_test.go` (uses a real engine + an opened NDJSON source, since `App.ColumnStats` is a pass-through and `visual.FieldCard` is cheap to build):

```go
func TestApp_ColumnStats_ForwardsAndReturnsACard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nums.ndjson")
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "{\"n\": %d}\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &App{eng: query.NewEngine()}
	open, err := a.OpenSource(query.OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseSource(open.Handle) })

	res, err := a.ColumnStats(query.ColumnStatsRequest{Handle: open.Handle, Path: "n"})
	if err != nil {
		t.Fatalf("ColumnStats error = %v, want nil", err)
	}
	if !res.Found || res.Card.Path != "n" {
		t.Fatalf("got Found=%v Card.Path=%q, want true and \"n\"", res.Found, res.Card.Path)
	}
}
```

(Confirm the existing imports in `gui/app_test.go` already include `fmt`, `os`, `path/filepath`, `strings`; add any that are missing.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./gui/ -run TestApp_ColumnStats -count=1`
Expected: FAIL - `a.ColumnStats` undefined.

- [ ] **Step 3: Add the interface method + the binding**

In `gui/app.go`, add to the `sourceEngine` interface (after the `GetCell` line):
```go
	ColumnStats(ctx context.Context, req query.ColumnStatsRequest) (query.ColumnStatsResult, error)
```

Add the method (after `App.GetCell`):
```go
// ColumnStats returns the rich profile (visual FieldCard) of one source field
// for the sidebar's expandable stats view (spec §GUI). A reqCtx pass-through,
// like GetCell.
func (a *App) ColumnStats(req query.ColumnStatsRequest) (query.ColumnStatsResult, error) {
	return a.eng.ColumnStats(a.reqCtx(), req)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./gui/ -run TestApp_ColumnStats -count=1`
Expected: PASS.

- [ ] **Step 5: Prove the mutation**

In `App.ColumnStats`, change the forwarded path: `req.Path = "does_not_exist"` before the call. Run Step 4 - expect FAIL (`Found=false`). Restore.

- [ ] **Step 6: Regenerate bindings**

Run: `cd gui && wails generate module`
Confirm the diff is ONLY additive: `App.d.ts`/`App.js` gain `ColumnStats`; `models.ts` gains `query.ColumnStatsResult` and keeps/uses the `visual` namespace. Revert any line-ending-only churn in `wailsjs/runtime/*`.

- [ ] **Step 7: gofmt + gates + commit**

Run: `gofmt -l gui/app.go` (no output); `go test ./gui/ -count=1`; `cd gui/frontend && npm run check` (0 errors).
```bash
git add gui/app.go gui/app_test.go gui/frontend/wailsjs/go/main/App.d.ts gui/frontend/wailsjs/go/main/App.js gui/frontend/wailsjs/go/models.ts
git commit -m "feat(gui): App.ColumnStats binding for the sidebar stats view"
```

---

### Task 3: `store.getColumnStats` + `types.ts` re-exports

**Files:**
- Modify: `gui/frontend/src/lib/explorer/types.ts` (re-export `FieldCard`)
- Modify: `gui/frontend/src/lib/explorer/store.ts` (import `ColumnStats`; add `getColumnStats`; export it)
- Modify: `gui/frontend/src/lib/explorer/store.test.ts` (new test + mock entry)

**Interfaces:**
- Consumes: generated `ColumnStats` from `../../../wailsjs/go/main/App`; `visual.FieldCard` from `../../../wailsjs/go/models`.
- Produces: `type FieldCard` (from `types.ts`); `explorer.getColumnStats(path: string): Promise<{ card: FieldCard; found: boolean }>`.

- [ ] **Step 1: Add the type re-export**

In `types.ts`, add a **type-only** import for the `visual` namespace (it is used only in a type position, so a value `import { visual }` fails `svelte-check` with TS1371 - `importsNotUsedAsValues: "error"` is set by the base `@tsconfig/svelte` config; `FieldDetail.svelte:2` already uses `import type { visual }` for the identical usage):
```ts
import type { visual } from "../../../wailsjs/go/models";
export type FieldCard = visual.FieldCard;
```
(Do NOT use a plain `import { visual }` here - unlike the existing `import { query }`, which is a value import only because `types.ts` uses `query.CellKind` at runtime; `visual` has no runtime use.)

- [ ] **Step 2: Write the failing test**

In `store.test.ts`, add `ColumnStats` to the static App import at the top of the file (line 4, next to `GetCell`/`SaveEdits`), add `ColumnStats: vi.fn(() => Promise.resolve({ card: { path: "n" }, found: true }))` to the App mock factory, and reset it in `beforeEach` like the others (`vi.mocked(ColumnStats).mockReset()...`). Place the test in the same `describe` block as the E6 `getCell` forward test (it uses that block's `openMemory()` helper, which sets `OpenSource` → handle `"h-search"`; a bare `explorer.open()` would otherwise leave `handle=""` because `beforeEach` resets `OpenSource`, and `getColumnStats` would throw `"no source open"`). Mirror the `getCell` forward test (store.test.ts:~994) exactly, INCLUDING the handle assertion:

```ts
it("getColumnStats forwards handle+path and returns the binding result", async () => {
  await openMemory();
  vi.mocked(ColumnStats).mockResolvedValueOnce({ card: { path: "user.age" }, found: true } as any);

  const out = await explorer.getColumnStats("user.age");

  // Assert the HANDLE too (a dropped/hardcoded handle must fail this) - the
  // getCell sibling asserts handle+index+path the same way.
  expect(vi.mocked(ColumnStats).mock.calls.at(-1)![0]).toMatchObject({ handle: "h-search", path: "user.age" });
  // Mutation (Step 6): return a constant instead of the binding result -> this fails.
  expect(out).toEqual({ card: { path: "user.age" }, found: true });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd gui/frontend && npx vitest run src/lib/explorer/store.test.ts -t "getColumnStats"`
Expected: FAIL - `explorer.getColumnStats` is not a function.

- [ ] **Step 4: Implement `getColumnStats`**

In `store.ts`, import `ColumnStats` alongside the other App imports (`GetCell`, `SaveEdits`, …). Add `FieldCard` to the `types` import. Add, next to `getCell`:

```ts
/** E8: fetches one column's rich profile (visual FieldCard) for the sidebar's
 *  expandable stats view. Thin async wrapper, owning no store state (the panel
 *  owns its own loading/error), rejecting on failure. `found` is false when the
 *  path is not a source field (e.g. a projected column). Sibling of getCell. */
async function getColumnStats(path: string): Promise<{ card: FieldCard; found: boolean }> {
  const s = get({ subscribe });
  if (!s.handle) throw new Error("no source open");
  const res = await ColumnStats({ handle: s.handle, path } as any);
  return { card: (res as any).card as FieldCard, found: (res as any).found as boolean };
}
```

Add `getColumnStats` to the returned object (next to `getCell`).

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd gui/frontend && npx vitest run src/lib/explorer/store.test.ts -t "getColumnStats"`
Expected: PASS.

- [ ] **Step 6: Prove the mutation**

In `getColumnStats`, replace the return with `return { card: { path: "" } as any, found: false };`. Run Step 5 - expect FAIL. Restore.

- [ ] **Step 7: check + commit**

Run: `cd gui/frontend && npm run check` (0 errors); `npx vitest run src/lib/explorer/store.test.ts`.
```bash
git add gui/frontend/src/lib/explorer/types.ts gui/frontend/src/lib/explorer/store.ts gui/frontend/src/lib/explorer/store.test.ts
git commit -m "feat(gui): store.getColumnStats wrapper + FieldCard type re-export"
```

---

### Task 4: `FieldStatsPanel.svelte` (fetch + render, with the concurrency guard) + theme audit

**Files:**
- Create: `gui/frontend/src/lib/explorer/FieldStatsPanel.svelte`
- Create: `gui/frontend/src/lib/explorer/FieldStatsPanel.test.ts`
- Audit (modify only if a token is undefined): `gui/frontend/src/lib/FieldDetail.svelte`, `gui/frontend/src/lib/charts/*.svelte`

**Interfaces:**
- Consumes: `explorer.getColumnStats(path)` (Task 3); `FieldDetail.svelte` (existing, prop `card: visual.FieldCard`); `type FieldCard` (Task 3).
- Produces: `FieldStatsPanel` with prop `path: string`; renders `.stats-loading` / `.stats-error` / `.stats-empty` / `FieldDetail`.

- [ ] **Step 1: Write the failing test**

Create `FieldStatsPanel.test.ts`:

```ts
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";

const h = vi.hoisted(() => ({ getColumnStats: vi.fn() }));
vi.mock("./store", () => ({ explorer: { getColumnStats: h.getColumnStats } }));
// FieldDetail renders the card; stub it so this test targets the panel, not the charts.
vi.mock("../FieldDetail.svelte", async () => {
  const Stub = (await import("./__fixtures__/CardStub.svelte")).default;
  return { default: Stub };
});

import FieldStatsPanel from "./FieldStatsPanel.svelte";

let target: HTMLElement;
let cmp: any = null;
afterEach(() => { cmp?.$destroy(); cmp = null; target?.remove(); h.getColumnStats.mockReset(); });

function mount(path: string) {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new FieldStatsPanel({ target, props: { path } });
  return target;
}
const flush = () => new Promise((r) => setTimeout(r, 0));

describe("FieldStatsPanel (E8)", () => {
  it("fetches the path's stats and renders the card", async () => {
    h.getColumnStats.mockResolvedValue({ card: { path: "n" }, found: true });
    const t = mount("n");
    await flush(); await tick();
    expect(h.getColumnStats).toHaveBeenCalledWith("n");
    expect(t.querySelector(".card-stub")?.getAttribute("data-path")).toBe("n");
  });

  it("shows a not-found message when the path is not a source field, and does NOT render the card", async () => {
    h.getColumnStats.mockResolvedValue({ card: { path: "" }, found: false });
    const t = mount("proj");
    await flush(); await tick();
    expect(t.querySelector(".stats-empty")).toBeTruthy();
    // Mutation (Step 5): render the card ignoring `found` -> a .card-stub appears here.
    expect(t.querySelector(".card-stub")).toBeNull();
  });
});
```

> **Design note (folded in from the plan review):** an earlier draft carried a `curPath`/`alive` concurrency guard and a "ignores a response after destroy" test. The review proved both are inert here: TreeNode mounts one panel per field with a FIXED `path` and destroys it on collapse, so `path` never changes while mounted (no cross-column race like E6's single shared overlay), and a response that lands after `$destroy()` is already a silent no-op in Svelte 3 (the destroyed component's fragment is null - no DOM patch, no throw, no warning). The guard therefore protected nothing and its test was vacuous (it passed with the guard removed). The panel below fetches once in `onMount` - no guard.

Also create the stub `gui/frontend/src/lib/explorer/__fixtures__/CardStub.svelte`:
```svelte
<script lang="ts">export let card: { path: string };</script>
<div class="card-stub" data-path={card.path}></div>
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd gui/frontend && npx vitest run src/lib/explorer/FieldStatsPanel.test.ts`
Expected: FAIL - component file does not exist.

- [ ] **Step 3: Implement `FieldStatsPanel.svelte`**

```svelte
<script lang="ts">
  // E8: the inline stats panel for one sidebar field. Owns its own fetch +
  // loading/error/not-found state (TreeNode only decides WHETHER to mount it).
  // TreeNode mounts one panel per field with a FIXED `path` and destroys it on
  // collapse, so `path` never changes while mounted -- there is no cross-column
  // race to guard (unlike E6's single shared overlay), and a response that lands
  // after $destroy() is a silent no-op in Svelte 3. So: fetch once on mount.
  import { onMount } from "svelte";
  import { explorer } from "./store";
  import FieldDetail from "../FieldDetail.svelte";
  import type { FieldCard } from "./types";

  export let path: string;

  let loading = true;
  let error = "";
  let found = true;
  let card: FieldCard | null = null;

  onMount(async () => {
    try {
      const res = await explorer.getColumnStats(path);
      card = res.card;
      found = res.found;
      loading = false;
    } catch (e) {
      error = String(e);
      loading = false;
    }
  });
</script>

<div class="field-stats" role="region" aria-label="Statistics for {path}">
  {#if loading}
    <p class="stats-loading">Loading…</p>
  {:else if error}
    <p class="stats-error" role="alert">{error}</p>
  {:else if !found || !card}
    <p class="stats-empty">No statistics for this column.</p>
  {:else}
    <FieldDetail {card} />
  {/if}
</div>

<style>
  .field-stats {
    padding: var(--space-2) var(--space-3);
    border-top: 1px solid var(--border);
    background: var(--surface-1);
  }
  .stats-loading, .stats-empty { margin: 0; font-size: 12px; color: var(--text-muted); }
  .stats-error {
    margin: 0; font-size: 12px; color: var(--status-critical);
    background: var(--status-critical-bg); padding: var(--space-2); border-radius: var(--radius-sm);
  }
</style>
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd gui/frontend && npx vitest run src/lib/explorer/FieldStatsPanel.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Prove the mutation**

In the template, change the not-found guard `{:else if !found || !card}` to `{:else if !card}` (render the card even when `found` is false). Run Step 4 - expect the not-found test to FAIL (a `.card-stub` renders where `.stats-empty` was expected). Restore.

- [ ] **Step 6: Theme audit of the revived components**

The `FieldDetail` + `charts/*` were built for the P3 dashboard and unmounted since the pivot. Audit them for CSS custom properties that do not exist in `gui/frontend/src/app.css` (precedent: E6 shipped a `--surface-3` that resolved to nothing).

Run: `grep -rhoE "var\(--[a-z0-9-]+" gui/frontend/src/lib/FieldDetail.svelte gui/frontend/src/lib/charts/ | sort -u` and cross-check each token against `grep -oE "^  --[a-z0-9-]+|^    --[a-z0-9-]+" gui/frontend/src/app.css` (and the `[data-theme]` blocks). For any token with no definition, replace it with the nearest real token (e.g. a missing `--surface-3` → `color-mix(in srgb, var(--text-muted) 8%, var(--surface-2))`), matching the E6 fix. If every token resolves, make NO change and note "theme audit clean" in the commit body.

- [ ] **Step 7: check + commit**

Run: `cd gui/frontend && npm run check` (0 errors); `npx vitest run src/lib/explorer/FieldStatsPanel.test.ts`.
```bash
git add gui/frontend/src/lib/explorer/FieldStatsPanel.svelte gui/frontend/src/lib/explorer/FieldStatsPanel.test.ts gui/frontend/src/lib/explorer/__fixtures__/CardStub.svelte
# add any FieldDetail/charts files the theme audit changed
git commit -m "feat(gui): FieldStatsPanel fetches and renders a field's stats card"
```

---

### Task 5: `TreeNode` stats toggle + inline panel

**Files:**
- Modify: `gui/frontend/src/lib/explorer/TreeNode.svelte`
- Modify: `gui/frontend/src/lib/explorer/StructureMap.test.ts` (new test; StructureMap mounts real TreeNodes so the toggle is reachable through it)

**Interfaces:**
- Consumes: `FieldStatsPanel.svelte` (Task 4); the existing `node.field` (a row has profile data iff `node.field` is set), `node.path`.
- Produces: a `.stats-toggle` button on `node.field` rows; when toggled on, `<FieldStatsPanel path={node.path} />` renders after the row.

- [ ] **Step 1: Write the failing test**

Two mocks must be added at the TOP of `StructureMap.test.ts` (it has none today, and mounts the real `StructureMap→TreeNode→FieldStatsPanel` chain):
1. `vi.mock("./store", () => ({ explorer: { getColumnStats: vi.fn(() => Promise.resolve({ card: { path: "n" }, found: true })) } }));` - the panel's only store dependency. (Neither `StructureMap.svelte` nor `TreeNode.svelte` imports `./store`, so this stubs only the panel.)
2. `vi.mock("../FieldDetail.svelte", async () => ({ default: (await import("./__fixtures__/CardStub.svelte")).default }));` - **required**: without it the real `FieldDetail` renders the minimal `{ path: "n" }` card and `FieldDetail.svelte:69` (`card.observations.toLocaleString()`) throws a `TypeError` (unhandled rejection → the run fails and can poison sibling test files). Task 4's `CardStub` (`data-path={card.path}`) is reused.

Mount `StructureMap` with a profiled field `"n"` (this file's `f()` helper attaches a `FieldDTO`, e.g. `f("n", { types: [{ kind: "int", share: 1 }] })`) and include `"n"` in `columnPaths` so the row is not dimmed. Then:

```ts
it("toggling a field's stats affordance mounts the inline stats panel and fetches that path", async () => {
  const store = await import("./store");
  const row = target.querySelector('.row[data-path="n"]') as HTMLElement;
  const toggle = row.querySelector(".stats-toggle") as HTMLButtonElement;
  expect(toggle, "a profiled field row shows a stats toggle").toBeTruthy();

  // Gate mutation ({#if node.field} without statsExpanded): a panel appears here,
  // before any click.
  expect(target.querySelector(".field-stats"), "no panel before the toggle").toBeNull();

  toggle.click();
  await tick();
  await Promise.resolve(); // let the panel's onMount fetch settle
  await tick();

  expect(target.querySelector(".field-stats"), "panel mounts on toggle").toBeTruthy();
  // Path-forwarding mutation (path={node.path} -> a wrong literal): the panel
  // must fetch THIS row's path, and the stub must render that path.
  expect(vi.mocked(store.explorer.getColumnStats).mock.calls.at(-1)![0]).toBe("n");
  expect(target.querySelector(".field-stats .card-stub")?.getAttribute("data-path")).toBe("n");
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd gui/frontend && npx vitest run src/lib/explorer/StructureMap.test.ts -t "stats"`
Expected: FAIL - no `.stats-toggle`.

- [ ] **Step 3: Wire the toggle into `TreeNode.svelte`**

Add the import + state (script section):
```ts
  import FieldStatsPanel from "./FieldStatsPanel.svelte";
  // ...
  let statsExpanded = false;
```

In `onKeydown`, guard the new button the same way the seed button is guarded (so Enter/Space on it doesn't also activate the row) - extend the early-return:
```ts
    if ((e.target as HTMLElement).closest(".seed, .stats-toggle")) return;
```

Add the toggle button in the row template, inside the `{#if node.field}` block (after the drift badge), so it only shows for profiled fields:
```svelte
    <button
      type="button"
      class="stats-toggle"
      class:active={statsExpanded}
      aria-label="Show statistics for {node.path}"
      aria-pressed={statsExpanded}
      title="Show statistics"
      on:click|stopPropagation={() => (statsExpanded = !statsExpanded)}
    >
      <svg viewBox="0 0 16 16" width="11" height="11" aria-hidden="true" focusable="false">
        <path d="M2 14h2V7H2v7zm4 0h2V2H6v12zm4 0h2V9h-2v5z" />
      </svg>
    </button>
```

Render the panel after the `.row` div (before the `{#if hasChildren && expanded}` children block):
```svelte
{#if node.field && statsExpanded}
  <div style="padding-left: {depth * INDENT + 14}px;">
    <FieldStatsPanel path={node.path} />
  </div>
{/if}
```

Add CSS mirroring `.seed` (quiet-until-hover), plus an `.active` state:
```css
  .stats-toggle {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    padding: 0;
    border: none;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--text-muted);
    opacity: 0.4;
    cursor: pointer;
  }
  .stats-toggle svg { fill: currentColor; }
  .row:hover .stats-toggle,
  .stats-toggle:hover,
  .stats-toggle.active,
  .stats-toggle:focus-visible { opacity: 1; }
  .stats-toggle:hover, .stats-toggle.active { color: var(--accent); background: var(--surface-2); }
  .stats-toggle:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd gui/frontend && npx vitest run src/lib/explorer/StructureMap.test.ts -t "stats"`
Expected: PASS.

- [ ] **Step 5: Prove the mutations (two)**

Mutation A (gate) - change the panel render guard from `{#if node.field && statsExpanded}` to `{#if node.field}` (always render). Run the test - the pre-click `expect(...".field-stats").toBeNull()` fails. Restore.

Mutation B (path forwarding) - change `<FieldStatsPanel path={node.path} />` to `path="__wrong__"`. Run the test - the `getColumnStats` call-arg assertion (`toBe("n")`) and the stub `data-path` assertion fail. Restore.

- [ ] **Step 6: full suite + check + commit**

Run: `cd gui/frontend && npx vitest run` (all green); `npm run check` (0 errors).
```bash
git add gui/frontend/src/lib/explorer/TreeNode.svelte gui/frontend/src/lib/explorer/StructureMap.test.ts
git commit -m "feat(gui): expandable per-column stats in the structure-map sidebar"
```

---

### Task 6: verification + docs

**Files:**
- Modify: `README.md` (Desktop GUI feature list)
- Modify: `gui/README.md` (explorer-view feature list)

- [ ] **Step 1: Full gate run**

Run each and record the result:
- `go test ./... -count=1` (expect all packages ok)
- `CGO_ENABLED=0 go build -o /dev/null .` (cgo-free)
- `cd gui/frontend && npm run check` (0 errors)
- `cd gui/frontend && npx vitest run` (all green; note the new total)
- `git diff --stat go.mod go.sum` (empty)
- `cd gui && wails build` (succeeds), then revert build churn (`go.mod`, `gui/frontend/dist/.gitkeep`, `wailsjs/runtime/*`); do NOT `git add -A`
- `cd gui && wails generate module` (empty diff vs committed bindings)

- [ ] **Step 2: Document in `README.md`**

Add a bullet to the Desktop GUI feature list (after **Expand**):
```markdown
- **Column stats** - click a field in the structure map to expand its full
  profile in place: a distribution histogram for numbers, a top-values chart for
  categorical fields, type mix, presence/null meters, quantiles and health flags
  - all from the single profiling pass, no rescan.
```

- [ ] **Step 3: Document in `gui/README.md`**

Add a bullet (near the Cell value tree entry) describing the sidebar stats expand: what triggers it (the stats toggle on a profiled field row, distinct from the tree caret), that it is read-only and lazy (fetched on first expand from the retained profile, no rescan), that it shows source-field stats (not projected/renamed output columns), and that an unknown path shows "No statistics for this column".

- [ ] **Step 4: Commit**

```bash
git add README.md gui/README.md
git commit -m "docs: document the sidebar column-stats view"
```

- [ ] **Step 5: Hand off for the whole-branch adversarial review**

All 6 tasks complete. Next (outside this plan): the whole-branch adversarial review (5 lenses × per-finding verify → synthesis) over `feat/e8-column-stats`, fix survivors mutation-proven, then the user performs the `--no-ff` merge.

---

## Self-review (against the spec)

**Spec coverage:**
- Lazy `Engine.ColumnStats` (no rescan, `backend.Profile()` + `visual.FromProfile`, `Found` on missing path) → Task 1. ✓
- `App.ColumnStats` binding + regenerated bindings, `sourceEngine` += method, embedded-engine spies inherit it → Task 2. ✓
- `store.getColumnStats` (getCell sibling, owns no state, rejects on failure) + `FieldCard` re-export → Task 3. ✓
- Sidebar expand-in-place, revived `FieldDetail` + `charts/*`, concurrency guard, theme audit → Tasks 4 + 5. ✓
- Source-field-only (not projected names) → enforced by matching `FieldCard.Path` (Task 1) + documented (Task 6). ✓
- Read-only, lazy per E6 pattern → Tasks 3–5. ✓
- Edge cases (path-not-found, all-null/non-numeric form, approximate distinct, theme tokens) → Task 1 (Found), Task 4 (audit), FieldCard forms handled by the existing `FieldDetail`. ✓

**Placeholder scan:** every code step carries real code; the only "read the file first" note is the theme-audit grep (Task 4 Step 6) and the StructureMap fixture reuse (Task 5 Step 1), both with concrete commands. No TODO/TBD. ✓

**Type consistency:** `ColumnStatsRequest{Handle, Path}` / `ColumnStatsResult{Card visual.FieldCard, Found bool}` are used identically in Tasks 1→2→3; `getColumnStats(path) → {card, found}` matches `FieldStatsPanel`'s `explorer.getColumnStats(p)` consumption (Task 4) and the store return (Task 3); `FieldCard` type flows types.ts → store.ts → FieldStatsPanel. ✓
