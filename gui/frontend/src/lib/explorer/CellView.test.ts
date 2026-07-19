// @vitest-environment jsdom
//
// The project's hardest constraint -- "numbers render Cell.str, never
// Cell.num" (spec §3) -- lived ONLY as a comment at CellView.svelte:47-48
// before this file existed. Cell.num is a lossy float64; Cell.str is the
// exact source literal, preserved byte-for-byte by the Go side
// (internal/query/columns_test.go pins the DTO shape). Nothing on the
// frontend pinned the RENDER: mutating `{str}` to `{cell.num}` on the
// INT/FLOAT branch left all 121 pre-existing frontend tests green. An
// earlier task's "visual confirmation" used values (1 / 1.001 / 6.978) that
// round-trip identically through a float64 and a string, so it proved
// nothing -- this file uses 0.1, whose float64 value and decimal string
// representation are provably different, and the FLOAT test below checks the
// DOM's ACTUAL numeric value against that string to make the round-trip
// distinction explicit (not just eyeballed).
//
// Also covers the two other untested branch families CellView.svelte has:
// MISSING (the diagonal-hatch background, an empty render with no text node)
// and OBJECT/ARRAY (the `{n}`/`[n]` count badge).
import { describe, it, expect, afterEach } from "vitest";
import { tick } from "svelte";
import CellView from "./CellView.svelte";
import { CellKind } from "./types";
import type { Cell } from "./types";

describe("CellView", () => {
  let target: HTMLElement;
  let cmp: { $destroy: () => void } | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  function mount(cell: Cell): HTMLElement {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new CellView({ target, props: { cell } }) as unknown as { $destroy: () => void };
    return target;
  }

  it("renders a FLOAT cell's str verbatim, never its lossy num -- the project's hardest constraint", async () => {
    // 0.1 cannot be represented exactly in float64: its str is the exact
    // source literal (arbitrary precision), its num is the nearest double.
    // These provably differ once printed, so a `{cell.num}` regression is
    // observable, not coincidental.
    const cell: Cell = { kind: CellKind.FLOAT, num: 0.1, str: "0.10000000000000000555" };
    const t = mount(cell);
    await tick();

    const text = t.querySelector(".cell .text.mono") as HTMLElement;
    expect(text).toBeTruthy();
    // Regression: {cell.num} would render "0.1" (String(0.1) === "0.1"),
    // which is a DIFFERENT string than cell.str here -- that mismatch is
    // exactly what this assertion depends on to fail if the branch is
    // mutated.
    expect(text.textContent).toBe("0.10000000000000000555");
    expect(text.textContent).not.toBe(String(cell.num));
  });

  it("renders an INT cell's str verbatim too (INT and FLOAT deliberately share the same branch)", async () => {
    const cell: Cell = { kind: CellKind.INT, num: 42, str: "42" };
    const t = mount(cell);
    await tick();
    const text = t.querySelector(".cell .text.mono") as HTMLElement;
    expect(text.textContent).toBe("42");
  });

  it("renders MISSING as an empty hatch -- no text node, just the .missing class", async () => {
    const cell: Cell = { kind: CellKind.MISSING };
    const t = mount(cell);
    await tick();

    const div = t.querySelector(".cell") as HTMLElement;
    expect(div.classList.contains("missing")).toBe(true);
    // Regression: any branch that renders visible text for MISSING (e.g.
    // falling through to the STRING branch) would leave content here; the
    // brief's ".missing IS the render" comment requires this to stay empty.
    expect(div.textContent).toBe("");
    expect(div.querySelector(".text")).toBeNull();
  });

  it("renders an OBJECT cell with its str and a {count} badge", async () => {
    const cell: Cell = { kind: CellKind.OBJECT, str: '{"a":1}', count: 3, hasMore: false };
    const t = mount(cell);
    await tick();

    const badge = t.querySelector(".badge") as HTMLElement;
    expect(badge).toBeTruthy();
    expect(badge.textContent).toBe("{3}");
    const text = t.querySelector(".text.mono") as HTMLElement;
    expect(text.textContent).toBe('{"a":1}');
  });

  it("appends an ellipsis to an OBJECT's str when hasMore is true", async () => {
    const cell: Cell = { kind: CellKind.OBJECT, str: '{"a":1', count: 5, hasMore: true };
    const t = mount(cell);
    await tick();
    const text = t.querySelector(".text.mono") as HTMLElement;
    expect(text.textContent).toBe('{"a":1…');
  });

  it("renders an ARRAY cell with its str and a [count] badge, distinguishable from OBJECT's {count}", async () => {
    const cell: Cell = { kind: CellKind.ARRAY, str: "[1,2,3", count: 3, hasMore: true };
    const t = mount(cell);
    await tick();

    const badge = t.querySelector(".badge") as HTMLElement;
    expect(badge.textContent).toBe("[3]");
    const text = t.querySelector(".text.mono") as HTMLElement;
    expect(text.textContent).toBe("[1,2,3…");
  });
});
