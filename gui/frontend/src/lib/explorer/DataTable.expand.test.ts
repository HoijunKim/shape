// @vitest-environment jsdom
//
// Isolated proof that a container cell's expand affordance dispatches
// `expandCell` with the row's ABSOLUTE index and the column path. DataTable
// pulls its rows from the `explorer` singleton (not a prop), so the store is
// mocked here to hand back one object cell -- a mock in the main
// DataTable.test.ts would break its real-store finding-(a) coverage.
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";
import type { Column } from "./types";
import { CellKind } from "./types";

// One page-0 row whose absolute index (7) differs from its render slot (0), so
// the assertion proves the dispatch carries row.index, not the slot.
const containerRow = {
  index: 7,
  cells: [{ kind: CellKind.OBJECT, str: "{…}", count: 2, hasMore: true }],
};

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (v: { version: number }) => void) => {
      run({ version: 1 });
      return () => {};
    },
    rowAt: (i: number) => ({ row: i === 0 ? containerRow : null }),
    ensurePages: () => Promise.resolve(),
  },
}));

import DataTable from "./DataTable.svelte";

let target: HTMLElement;
let cmp: { $on: (e: string, cb: (ev: CustomEvent) => void) => void; $destroy: () => void } | null = null;

afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
});

function makeColumn(path: string): Column {
  return {
    path, name: path, type: "object", nullable: false, presence: 1,
    distinct: 1, container: true, index: 0,
  } as Column;
}

describe("DataTable expand affordance", () => {
  it("dispatches expandCell with the row's absolute index and the column path", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);

    cmp = new DataTable({ target, props: { columns: [makeColumn("obj")], total: 10, focusPath: "" } }) as any;
    await tick();

    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    Object.defineProperty(viewportEl, "clientHeight", { value: 500, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 400, configurable: true });
    window.dispatchEvent(new Event("resize"));
    await tick();

    const events: { index: number; path: string }[] = [];
    cmp!.$on("expandCell", (e: CustomEvent) => events.push(e.detail));

    const btn = target.querySelector(".expand-btn") as HTMLButtonElement;
    expect(btn, "a container cell must render an expand affordance").toBeTruthy();
    btn.click();

    expect(events).toHaveLength(1);
    expect(events[0]).toEqual({ index: 7, path: "obj" });
  });
});
