// @vitest-environment jsdom
//
// E7 inline editing. DataTable pulls rows from the explorer singleton, so the
// store is mocked here (as in DataTable.expand.test.ts) to hand back one scalar
// cell and spy on setEdit.
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";
import type { Column } from "./types";

const h = vi.hoisted(() => ({
  state: { version: 1, edits: {} as Record<number, any> },
  cells: [{ kind: "string", str: "a" }] as any[],
  setEdit: vi.fn(),
  listeners: new Set<(s: any) => void>(),
}));

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (s: any) => void) => {
      run(h.state);
      h.listeners.add(run);
      return () => h.listeners.delete(run);
    },
    rowAt: (i: number) => ({ row: i === 0 ? { index: 0, cells: h.cells } : null }),
    ensurePages: () => Promise.resolve(),
    setEdit: h.setEdit,
  },
}));

import DataTable from "./DataTable.svelte";

let target: HTMLElement;
let cmp: { $destroy: () => void } | null = null;

afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
  h.state.edits = {};
  h.cells = [{ kind: "string", str: "a" }];
  h.setEdit.mockReset();
});

function col(path: string, type = "string"): Column {
  return { path, name: path, type, nullable: false, presence: 1, distinct: 1, container: false, index: 0 } as Column;
}

async function mount(columns: Column[]): Promise<HTMLElement> {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new DataTable({ target, props: { columns, total: 1, focusPath: "" } }) as any;
  await tick();
  const vp = target.querySelector(".viewport") as HTMLElement;
  Object.defineProperty(vp, "clientHeight", { value: 400, configurable: true });
  Object.defineProperty(vp, "clientWidth", { value: 400, configurable: true });
  window.dispatchEvent(new Event("resize"));
  await tick();
  return target;
}

function dataCell(t: HTMLElement): HTMLElement {
  return t.querySelector(".data-cell") as HTMLElement;
}

describe("DataTable inline editing (E7)", () => {
  it("double-clicking a string cell and committing calls setEdit with the typed value", async () => {
    const t = await mount([col("name")]);
    dataCell(t).dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    await tick();
    const input = t.querySelector("input.cell-editor") as HTMLInputElement;
    expect(input, "editor must open").toBeTruthy();
    input.value = "hello";
    input.dispatchEvent(new Event("input"));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
    await tick();
    expect(h.setEdit).toHaveBeenCalledTimes(1);
    const [index, path, value] = h.setEdit.mock.calls[0];
    expect({ index, path, kind: value.kind, literal: value.literal }).toEqual({
      index: 0, path: "name", kind: "string", literal: "hello",
    });
  });

  it("commits a >2^53 number literal verbatim, never through a JS number", async () => {
    h.cells = [{ kind: "int", str: "30", num: 30 }];
    const t = await mount([col("age", "int")]);
    dataCell(t).dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    await tick();
    const input = t.querySelector("input.cell-editor") as HTMLInputElement;
    input.value = "9007199254740993";
    input.dispatchEvent(new Event("input"));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
    await tick();
    // Mutation: commitEdit does Number(editText) -> the big int rounds to ...992.
    expect(h.setEdit.mock.calls[0][2].literal).toBe("9007199254740993");
  });

  it("rejects an invalid number and does not commit", async () => {
    h.cells = [{ kind: "int", str: "30", num: 30 }];
    const t = await mount([col("age", "int")]);
    dataCell(t).dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    await tick();
    const input = t.querySelector("input.cell-editor") as HTMLInputElement;
    input.value = "not a number";
    input.dispatchEvent(new Event("input"));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
    await tick();
    expect(h.setEdit).not.toHaveBeenCalled();
    expect(t.querySelector("input.cell-editor.invalid"), "editor stays open, marked invalid").toBeTruthy();
  });

  it("renders an edited cell's overlay value with the highlight, not the backend value", async () => {
    h.state.edits = {
      0: { name: { value: { kind: "string", literal: "Z", display: "Z" }, original: { kind: "string", literal: "a", display: "a" }, snapshot: { index: 0, cells: [] } } },
    };
    const t = await mount([col("name")]);
    const cell = dataCell(t);
    // Mutation: render the backend cell ignoring the overlay -> CellView (.cell)
    // renders the backend value "a" and .edited-value is absent.
    expect(cell.querySelector(".edited-value")?.textContent).toBe("Z");
    expect(cell.classList.contains("edited")).toBe(true);
    expect(cell.querySelector(".cell"), "no backend CellView for an edited cell").toBeNull();
  });

  it("does not open an editor for a non-editable (array-element) column", async () => {
    const t = await mount([col("tags[]")]); // Elem path -> not editable
    dataCell(t).dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    await tick();
    expect(t.querySelector("input.cell-editor")).toBeNull();
  });
});
