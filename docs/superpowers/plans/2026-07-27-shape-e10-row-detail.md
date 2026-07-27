# shape E10 — Row Detail View Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Click a row's gutter number to open the whole record as a collapsible tree (the row-level companion to E6's cell tree).

**Architecture:** Pure reuse of E6 — `store.getCell(index, "")` returns the whole record (empty path → `resolve(record, [])` → `[record]`), rendered by the existing `ValueTreeOverlay`. DataTable's gutter dispatches `expandRow{index}`; Explorer's `onExpandRow` fetches into the shared overlay behind the existing `cellReq` guard. No new engine method, binding, or component.

**Tech Stack:** Svelte 3 + TypeScript, Vitest. (No Go change.)

## Global Constraints

- No new deps; no Go/binding change (frontend-only). `npm run check` 0 errors.
- Every test carries a mutation proof.
- After `wails build`, never `git add -A`; revert build churn.
- User performs the `--no-ff` merge. Branch: `feat/e10-row-detail` off current master.

---

### Task 1: DataTable gutter → `expandRow` trigger

**Files:**
- Modify: `gui/frontend/src/lib/explorer/DataTable.svelte` (the gutter cell ~:549; the `createEventDispatcher` typing ~:33)
- Create: `gui/frontend/src/lib/explorer/DataTable.rowdetail.test.ts`

**Interfaces:**
- Produces: DataTable dispatches `expandRow: { index: number }` when a LOADED gutter cell is clicked/keyboard-activated; the gutter is `role="button"`, keyboard-operable, with `aria-label` + `title="Show full record"`. A skeleton (unloaded) gutter dispatches nothing.

- [ ] **Step 1: Write the failing test**

Create `DataTable.rowdetail.test.ts` (mirror `DataTable.expand.test.ts`'s mocked-store harness: one page-0 row whose absolute index (7) differs from its slot (0), so the assertion proves the dispatch carries `row.index`, not the slot):

```ts
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";
import type { Column } from "./types";
import { CellKind } from "./types";

const scalarRow = { index: 7, cells: [{ kind: CellKind.STRING, str: "hi" }] };

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (v: any) => void) => { run({ version: 1, edits: {}, sort: { path: "", desc: false } }); return () => {}; },
    rowAt: (i: number) => ({ row: i === 0 ? scalarRow : null }),
    ensurePages: () => Promise.resolve(),
  },
}));

import DataTable from "./DataTable.svelte";

let target: HTMLElement;
let cmp: any = null;
afterEach(() => { cmp?.$destroy(); cmp = null; target?.remove(); });

function col(path: string): Column {
  return { path, name: path, type: "string", nullable: false, presence: 1, distinct: 1, container: false, index: 0 } as Column;
}
async function mount(): Promise<HTMLElement> {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new DataTable({ target, props: { columns: [col("a")], total: 10, focusPath: "" } });
  await tick();
  const vp = target.querySelector(".viewport") as HTMLElement;
  Object.defineProperty(vp, "clientHeight", { value: 500, configurable: true });
  Object.defineProperty(vp, "clientWidth", { value: 400, configurable: true });
  window.dispatchEvent(new Event("resize"));
  await tick();
  return target;
}

describe("DataTable row detail (E10)", () => {
  it("clicking a loaded gutter cell dispatches expandRow with the row's ABSOLUTE index", async () => {
    const t = await mount();
    const events: { index: number }[] = [];
    cmp.$on("expandRow", (e: CustomEvent) => events.push(e.detail));
    const gutter = t.querySelector(".gutter-cell[data-row-index]") as HTMLElement
      ?? (t.querySelector(".gutter-cell") as HTMLElement);
    gutter.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(events).toEqual([{ index: 7 }]); // mutation: dispatch slot i (0) -> fails
  });
});
```

- [ ] **Step 2: Run — FAIL** (no `expandRow`). `cd gui/frontend && npx vitest run src/lib/explorer/DataTable.rowdetail.test.ts`

- [ ] **Step 3: Implement**

Add `expandRow: { index: number };` to the `createEventDispatcher<{...}>` type block (next to `expandCell`). On the gutter cell (the `<div class="gutter-cell" ...>` at ~:549), when `row` is truthy, make it activate the row detail:
```svelte
<div
  class="gutter-cell"
  class:row-edited={row && !!$explorer.edits?.[row.index]}
  class:clickable={!!row}
  role={row ? "button" : "rowheader"}
  tabindex={row ? 0 : undefined}
  aria-label={row ? `Show full record for row ${row.index}` : undefined}
  title={row ? "Show full record" : undefined}
  style="width:{GUTTER_W}px;"
  on:click={() => row && dispatch("expandRow", { index: row.index })}
  on:keydown={(e) => { if (row && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); dispatch("expandRow", { index: row.index }); } }}
>
```
(Keep the existing gutter contents — the edit-dot span + `{row.index}` / skeleton bar — unchanged inside.) Add `.gutter-cell.clickable { cursor: pointer; }` and a hover rule `.gutter-cell.clickable:hover { background: var(--surface-2); }` + a focus-visible outline, to the styles.

- [ ] **Step 4: Run — PASS.**

- [ ] **Step 5: Prove the mutation**

Change the dispatch to `dispatch("expandRow", { index: i })` (the render slot). Run Step 4 — the test fails (`{index:0}` ≠ `{index:7}`). Restore. (A second assertion for the skeleton no-op: add a row at a slot with `rowAt → {row:null}` and assert a click there dispatches nothing — optional, the `row &&` guard covers it.)

- [ ] **Step 6: check + commit**

`cd gui/frontend && npm run check` (0 errors); run the test file.
```bash
git add gui/frontend/src/lib/explorer/DataTable.svelte gui/frontend/src/lib/explorer/DataTable.rowdetail.test.ts
git commit -m "feat(gui): dispatch expandRow when a row's gutter is activated"
```

---

### Task 2: Explorer `onExpandRow` → row detail overlay

**Files:**
- Modify: `gui/frontend/src/lib/explorer/Explorer.svelte` (the DataTable mount ~:246; the cell overlay state/handlers ~:88-119)
- Modify: `gui/frontend/src/lib/explorer/Explorer.test.ts` (a row-detail test)

**Interfaces:**
- Consumes: `explorer.getCell(index, "")` (E6); the existing `ValueTreeOverlay` + `cellReq`/`cellOpen`/`cellValue`/`cellLabel` state.
- Produces: DataTable `on:expandRow={onExpandRow}`; `onExpandRow(e)` fetches the whole record into the shared overlay with a `Row {index}` label, behind the shared `cellReq` guard.

- [ ] **Step 1: Write the failing test**

Add to `Explorer.test.ts` (real Explorer + mocked bridge; mirror the existing GetCell mock). Mock `GetCell` to resolve `{ value: { a: 1 }, found: true }`, mount, open, then dispatch `expandRow` from the DataTable (or call the handler via a gutter click) and assert the ValueTreeOverlay opens with the record + `Row 0` label, and that `GetCell` was called with `path: ""` and the row index:

```ts
it("opens the row detail overlay with the whole record on a gutter click (E10)", async () => {
  const columns = [makeColumn("a")];
  vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", columns, [makeField("a")]));
  vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));
  vi.mocked(GetCell).mockResolvedValue({ value: { a: 1 }, found: true } as any);
  cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
  await explorer.open("/f.ndjson");
  await flush(); await tick();

  const gutter = target.querySelector(".gutter-cell[role='button']") as HTMLElement;
  expect(gutter, "a loaded gutter is a button").toBeTruthy();
  gutter.click();
  await flush(); await tick();

  const last = vi.mocked(GetCell).mock.calls.at(-1)![0] as any;
  // Mutation: fetch a non-empty path / wrong index -> these fail.
  expect(last.path).toBe("");
  expect(last.index).toBe(0);
  // The overlay is open showing the record (ValueTreeOverlay renders a tree).
  expect(target.querySelector(".value-tree, .tree-node, [role='dialog']")).toBeTruthy();
});
```
(Adjust the overlay selector to whatever `ValueTreeOverlay` renders; confirm by reading it. `rowSetFor` returns a row at index 0.)

- [ ] **Step 2: Run — FAIL** (no `on:expandRow` handler; gutter click does nothing).

- [ ] **Step 3: Implement**

In `Explorer.svelte`, add `on:expandRow={onExpandRow}` to the `<DataTable ... />` mount. Add the handler beside `onExpandCell`, reusing the same overlay state + `cellReq` guard:
```ts
async function onExpandRow(e: CustomEvent<{ index: number }>): Promise<void> {
  const { index } = e.detail;
  const myReq = ++cellReq;
  cellOpen = true;
  cellLoading = true;
  cellError = "";
  cellLabel = `Row ${index}`;
  try {
    const res = await explorer.getCell(index, ""); // "" = the whole record
    if (myReq !== cellReq) return;
    cellValue = res.value;
    cellFound = res.found;
    cellLoading = false;
  } catch (err) {
    if (myReq !== cellReq) return;
    cellError = String(err);
    cellLoading = false;
  }
}
```

- [ ] **Step 4: Run — PASS.**

- [ ] **Step 5: Prove the mutation**

Change the fetch to `explorer.getCell(index, "a")` (a non-root path). Run Step 4 — the `last.path === ""` assertion fails. Restore. (The `cellReq` guard is already proven by E6's onExpandCell tests; the row handler shares it verbatim.)

- [ ] **Step 6: full suite + check + commit**

`cd gui/frontend && npx vitest run` (all green); `npm run check` (0 errors).
```bash
git add gui/frontend/src/lib/explorer/Explorer.svelte gui/frontend/src/lib/explorer/Explorer.test.ts
git commit -m "feat(gui): row detail overlay shows the whole record on a gutter click"
```

---

### Task 3: verification + docs

- [ ] **Step 1: Gates** — `cd gui/frontend && npx vitest run` (note the total); `npm run check` (0/0/1 pre-existing hint); `cd gui && wails build` (succeeds), revert build churn (`go.mod`, `dist/.gitkeep`, `wailsjs/runtime/*`), do NOT `git add -A`; `git diff --stat go.mod go.sum` empty (no Go change).

- [ ] **Step 2: Docs** — README.md: a bullet in the Desktop GUI list (near Expand/Column stats):
```markdown
- **Row detail** — click a row's number to open the whole record as a
  collapsible tree (the full, untruncated nested value — the row-level companion
  to the cell view), with a Copy button for the exact JSON.
```
gui/README.md: a short note next to the Cell value tree entry — clicking the row-number gutter opens the WHOLE source record (by absolute index, independent of projection/filter/sort) in the same tree overlay, read-only.

- [ ] **Step 3: Commit** `docs: document the row detail view`.

- [ ] **Step 4: Hand off** for the whole-branch adversarial review, then the user's `--no-ff` merge.

---

## Self-review (against the spec)

**Coverage:** gutter trigger + `expandRow` + a11y (T1); `onExpandRow` reusing overlay + `getCell(index,"")` + shared guard (T2); docs (T3). Read-only, no prev/next, source-record-by-index — all honored (no code adds them). ✓
**Placeholders:** every step has concrete code; the two "confirm the overlay selector" / "adjust rowSetFor" notes point at real files to read. No TODO/TBD. ✓
**Types:** `expandRow: { index: number }` dispatched (T1) == consumed by `onExpandRow(e: CustomEvent<{ index: number }>)` (T2); `getCell(index, "")` returns `{ value, found }` (E6 signature). ✓
