// @vitest-environment jsdom
//
// E9 header sort. DataTable pulls rows/sort from the explorer singleton, so the
// store is mocked to control the active sort and spy on setSort.
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";
import type { Column } from "./types";

const h = vi.hoisted(() => ({
  state: { version: 1, edits: {} as Record<number, any>, sort: { path: "", desc: false } },
  setSort: vi.fn(),
  listeners: new Set<(s: any) => void>(),
}));

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (s: any) => void) => {
      run(h.state);
      h.listeners.add(run);
      return () => h.listeners.delete(run);
    },
    rowAt: (i: number) => ({ row: i === 0 ? { index: 0, cells: [{ kind: "int", str: "1" }] } : null }),
    ensurePages: () => Promise.resolve(),
    setSort: h.setSort,
  },
}));

import DataTable from "./DataTable.svelte";

let target: HTMLElement;
let cmp: any = null;

afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
  h.state.sort = { path: "", desc: false };
  h.setSort.mockReset();
});

function col(path: string): Column {
  return { path, name: path, type: "int", nullable: false, presence: 1, distinct: 1, container: false, index: 0 } as Column;
}

async function mount(): Promise<HTMLElement> {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new DataTable({ target, props: { columns: [col("n")], total: 1, focusPath: "" } });
  await tick();
  const vp = target.querySelector(".viewport") as HTMLElement;
  Object.defineProperty(vp, "clientHeight", { value: 400, configurable: true });
  Object.defineProperty(vp, "clientWidth", { value: 400, configurable: true });
  window.dispatchEvent(new Event("resize"));
  await tick();
  return target;
}

describe("DataTable header sort (E9)", () => {
  it("clicking the sort caret cycles none -> asc -> desc -> none", async () => {
    const t = await mount();
    const caret = () => t.querySelector(".sort-caret") as HTMLElement;

    caret().dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(h.setSort).toHaveBeenLastCalledWith({ path: "n", desc: false }); // none -> asc

    h.state.sort = { path: "n", desc: false };
    h.listeners.forEach((r) => r(h.state));
    await tick();
    caret().dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(h.setSort).toHaveBeenLastCalledWith({ path: "n", desc: true }); // asc -> desc

    h.state.sort = { path: "n", desc: true };
    h.listeners.forEach((r) => r(h.state));
    await tick();
    caret().dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(h.setSort).toHaveBeenLastCalledWith({ path: "", desc: false }); // desc -> none
  });

  it("shows the ▲/▼ indicator for the active sort column", async () => {
    h.state.sort = { path: "n", desc: true };
    const t = await mount();
    expect((t.querySelector(".sort-caret") as HTMLElement).textContent).toBe("▼");
  });

  it("a sort-caret click does NOT fire the focus (scroll-to-column) dispatch", async () => {
    const t = await mount();
    const focuses: unknown[] = [];
    cmp.$on("focus", () => focuses.push(1));
    (t.querySelector(".sort-caret") as HTMLElement).dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    // Mutation: onHeaderClick does not gate on .sort-caret -> a sort click also
    // dispatches focus.
    expect(focuses).toHaveLength(0);
    expect(h.setSort).toHaveBeenCalledTimes(1);
  });

  it("a header-body click focuses the column and does not sort", async () => {
    const t = await mount();
    const focuses: unknown[] = [];
    cmp.$on("focus", () => focuses.push(1));
    (t.querySelector(".header-name") as HTMLElement).dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    expect(focuses).toHaveLength(1);
    expect(h.setSort).not.toHaveBeenCalled();
  });
});
