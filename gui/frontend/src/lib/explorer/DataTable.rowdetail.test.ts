// @vitest-environment jsdom
//
// E10 row detail. Clicking a loaded gutter cell dispatches `expandRow` with the
// row's ABSOLUTE index. The store is mocked (as in DataTable.expand.test.ts) to
// hand back one row whose absolute index (7) differs from its render slot (0),
// so the assertion proves the dispatch carries row.index, not the slot.
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";
import type { Column } from "./types";
import { CellKind } from "./types";

const scalarRow = { index: 7, cells: [{ kind: CellKind.STRING, str: "hi" }] };

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (v: any) => void) => {
      run({ version: 1, edits: {}, sort: { path: "", desc: false } });
      return () => {};
    },
    rowAt: (i: number) => ({ row: i === 0 ? scalarRow : null }),
    ensurePages: () => Promise.resolve(),
  },
}));

import DataTable from "./DataTable.svelte";

let target: HTMLElement;
let cmp: any = null;
afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
});

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
    const gutter = t.querySelector(".gutter-cell.clickable") as HTMLElement;
    expect(gutter, "a loaded gutter cell is clickable").toBeTruthy();
    gutter.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    // Mutation: dispatch the render slot i (0) instead of row.index -> fails.
    expect(events).toEqual([{ index: 7 }]);
  });
});
