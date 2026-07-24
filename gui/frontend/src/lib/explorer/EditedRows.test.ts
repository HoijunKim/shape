// @vitest-environment jsdom
//
// E7 "Show edited only" diff view. EditedRows reads the overlay off the explorer
// singleton, so the store is mocked to hand back a fixed edits map and spy on
// revertCell.
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";

const h = vi.hoisted(() => ({
  state: { edits: {} as Record<number, any> },
  revertCell: vi.fn(),
}));

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (s: any) => void) => {
      run(h.state);
      return () => {};
    },
    revertCell: h.revertCell,
  },
}));

import EditedRows from "./EditedRows.svelte";

let target: HTMLElement;
let cmp: { $destroy: () => void } | null = null;

afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
  h.state.edits = {};
  h.revertCell.mockReset();
});

function entry(kind: string, was: string, now: string) {
  return {
    value: { kind, literal: now, display: now },
    original: { kind, literal: was, display: was },
    snapshot: { index: 0, cells: [] },
  };
}

function mount(): HTMLElement {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new EditedRows({ target }) as any;
  return target;
}

describe("EditedRows (E7 edited-only view)", () => {
  it("renders one group per edited row, sorted, showing the NEW value and the original", () => {
    h.state.edits = {
      5: { "user.name": entry("string", "Al", "Zed") },
      2: { age: entry("int", "30", "31") },
    };
    const t = mount();

    const heads = [...t.querySelectorAll(".row-head")].map((e) => e.textContent?.trim());
    expect(heads).toEqual(["Row 2", "Row 5"]); // numeric-sorted

    const now = t.querySelector(".row-group:nth-child(2) .now");
    // Mutation: render entry.original.display in the .now slot -> "Al", fails.
    expect(now?.textContent).toBe("Zed");
    expect(t.querySelector(".row-group:nth-child(2) .was")?.textContent).toBe("Al");
  });

  it("reverts a single cell through the store", async () => {
    h.state.edits = { 5: { "user.name": entry("string", "Al", "Zed") } };
    const t = mount();
    const btn = t.querySelector(".cell-diff .revert") as HTMLButtonElement;
    btn.click();
    await tick();
    expect(h.revertCell).toHaveBeenCalledWith(5, "user.name");
  });

  it("shows an empty state when there are no edits", () => {
    h.state.edits = {};
    const t = mount();
    expect(t.querySelector(".empty")?.textContent).toContain("No edits yet");
    expect(t.querySelector(".row-group")).toBeNull();
  });
});
