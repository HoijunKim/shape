// @vitest-environment jsdom
//
// Finding (a) regression test: `total` can SHRINK after mount. On the
// rescan tier (totalExact === false), paging.ts's reconcileEof optimistically
// projects `total = pageEnd + pageRows` (one page past the true end) and
// later corrects it DOWN once the true EOF page lands. Before the fix,
// firstRow/lastRow were only recomputed inside recomputeRange(), which ran
// on scroll, mount, resize, or a `columns` identity change -- never on a
// bare `total` change -- so a shrink left the render window (and the
// skeleton rows it produces) pointing past the new end, permanently: those
// rows have no page to land, because no such row exists.
//
// This mounts the REAL DataTable.svelte component (not a mock of it) in
// jsdom, scrolls deep into a large table, then shrinks `total` with
// deliberately NO scroll/resize event in between, and asserts nothing
// renders past the new last row. It does not call explorer.open(), so the
// store never invokes the real Wails bridge (ensurePages() short-circuits
// on `status !== "ready"`); only DataTable's own reactive wiring is under
// test.
import { describe, it, expect, afterEach, vi } from "vitest";
import { tick } from "svelte";
import DataTable from "./DataTable.svelte";
import { explorer } from "./store";
import type { Column } from "./types";

const ROW_H = 28;

function makeColumn(path: string): Column {
  return {
    path,
    name: path,
    type: "int",
    nullable: false,
    presence: 1,
    distinct: 1,
    container: false,
    index: 0,
  } as Column;
}

/** Row indices actually rendered (parsed back out of each `.row`'s
 *  `top: {i*ROW_H}px` inline style), sorted ascending. */
function renderedRowIndices(target: HTMLElement): number[] {
  return Array.from(target.querySelectorAll(".row"))
    .map((el) => Math.round(parseFloat((el as HTMLElement).style.top) / ROW_H))
    .sort((a, b) => a - b);
}

describe("DataTable: row window reacts to a shrinking total (finding a)", () => {
  let target: HTMLElement;
  let cmp: { $set: (props: Record<string, unknown>) => void; $destroy: () => void } | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  it("clamps rendered rows to the new total after `total` shrinks with no scroll/resize/columns event", async () => {
    const columns = [makeColumn("a")];
    target = document.createElement("div");
    document.body.appendChild(target);

    cmp = new DataTable({ target, props: { columns, total: 1000, focusPath: "" } }) as unknown as {
      $set: (props: Record<string, unknown>) => void;
      $destroy: () => void;
    };
    await tick();

    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    expect(viewportEl).toBeTruthy();
    // jsdom does no layout, so clientHeight/clientWidth default to 0 --
    // stand in for a real viewport size.
    Object.defineProperty(viewportEl, "clientHeight", { value: 500, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 300, configurable: true });

    // Scroll deep into the table (row ~892 at ROW_H=28) so the window sits
    // far from the origin.
    viewportEl.scrollTop = 25000;
    viewportEl.dispatchEvent(new Event("scroll"));
    await new Promise((resolve) => requestAnimationFrame(resolve)); // onScroll is rAF-throttled
    await tick();

    const deepIndices = renderedRowIndices(target);
    expect(deepIndices.length).toBeGreaterThan(0);
    expect(Math.max(...deepIndices)).toBeGreaterThan(800); // sanity: this really is a deep-scroll window

    // `total` shrinks to 10 (simulating reconcileEof correcting an
    // optimistic rescan-tier estimate down) -- deliberately NO scroll,
    // resize, or columns-identity change accompanies it.
    cmp.$set({ total: 10 });
    await tick();

    const afterShrink = renderedRowIndices(target);
    expect(afterShrink.length).toBeGreaterThan(0); // still rendering rows, not blank
    expect(Math.max(...afterShrink)).toBeLessThanOrEqual(9); // never a row past the new total-1
    expect(Math.min(...afterShrink)).toBeGreaterThanOrEqual(0);
  });
});

// E3 Task 9 (recon GAP 9): a filter change bumps the store's resetToken
// (store.ts's setFilter), and DataTable is the sole owner of the scroll
// viewport, so it must react by scrolling back to row 0 -- otherwise a user
// scrolled deep into a large table would have the rows swapped out from under
// them by the new filter while still looking at a scroll position that made
// sense for the OLD result set. Mutation: dropping the `$: if (resetToken !==
// prevResetToken)` reactive block (or its call to scrollToTop()) leaves
// scrollTop at its deep-scrolled value and this test fails.
describe("DataTable: resetToken bump scrolls back to the top (T9)", () => {
  let target: HTMLElement;
  let cmp: { $set: (props: Record<string, unknown>) => void; $destroy: () => void } | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  it("bumping resetToken after a deep scroll sets scrollTop back to 0 and recomputes the row window", async () => {
    const columns = [makeColumn("a")];
    target = document.createElement("div");
    document.body.appendChild(target);

    cmp = new DataTable({
      target,
      props: { columns, total: 1000, focusPath: "", resetToken: 0 },
    }) as unknown as { $set: (props: Record<string, unknown>) => void; $destroy: () => void };
    await tick();

    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    expect(viewportEl).toBeTruthy();
    Object.defineProperty(viewportEl, "clientHeight", { value: 500, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 300, configurable: true });

    viewportEl.scrollTop = 25000;
    viewportEl.dispatchEvent(new Event("scroll"));
    await new Promise((resolve) => requestAnimationFrame(resolve)); // onScroll is rAF-throttled
    await tick();

    expect(viewportEl.scrollTop).toBe(25000); // sanity: genuinely scrolled deep
    const deepIndices = renderedRowIndices(target);
    expect(Math.max(...deepIndices)).toBeGreaterThan(800); // sanity: deep window rendered

    cmp.$set({ resetToken: 1 });
    await tick();

    expect(viewportEl.scrollTop).toBe(0);
    const afterReset = renderedRowIndices(target);
    expect(Math.min(...afterReset)).toBe(0); // recomputeRange() re-ran against the reset scrollTop
  });

  it("does not scroll on mount, even though resetToken starts at a value equal to its own default (guard against a false initial trigger)", async () => {
    const columns = [makeColumn("a")];
    target = document.createElement("div");
    document.body.appendChild(target);

    cmp = new DataTable({
      target,
      props: { columns, total: 1000, focusPath: "", resetToken: 0 },
    }) as unknown as { $set: (props: Record<string, unknown>) => void; $destroy: () => void };
    await tick();

    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    Object.defineProperty(viewportEl, "clientHeight", { value: 500, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 300, configurable: true });

    viewportEl.scrollTop = 12000;
    viewportEl.dispatchEvent(new Event("scroll"));
    await new Promise((resolve) => requestAnimationFrame(resolve));
    await tick();

    // Re-passing the SAME resetToken value (0) that mount already saw must
    // not scroll -- only an actual bump does.
    cmp.$set({ resetToken: 0 });
    await tick();

    expect(viewportEl.scrollTop).toBe(12000); // unchanged: no spurious reset
  });
});

// V1: virtualization past the height cap. A tiny injected maxContentPx crosses
// the cap with a few-hundred-row fixture (a real 33M-px fixture is impossible
// in jsdom). Assertions bind to data-row-index (the absolute index) and the
// computed viewport-y, NOT top/ROW_H (which decodes the window offset in scaled
// mode) or mere DOM presence (a row can be present yet clipped below the fold).
describe("DataTable scaled virtualization (V1)", () => {
  const HEADER_H = 32;
  let target: HTMLElement;
  let cmp: { $set: (p: Record<string, unknown>) => void; $destroy: () => void } | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  it("caps contentHeight and seats the true tail row on-screen at maxScroll", async () => {
    const columns = [makeColumn("a")];
    const total = 300;
    const CAP = 5000; // 32 + 300*28 = 8432 > 5000 -> scaled
    target = document.createElement("div");
    document.body.appendChild(target);
    const ensureSpy = vi.spyOn(explorer, "ensurePages").mockResolvedValue(undefined as any);

    cmp = new DataTable({ target, props: { columns, total, focusPath: "", maxContentPx: CAP } }) as any;
    await tick();

    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    Object.defineProperty(viewportEl, "clientHeight", { value: 800, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 300, configurable: true });

    // (i) the spacer is capped, not 32 + total*ROW_H.
    // Mutation: contentHeight = HEADER_H + total*ROW_H (no cap) -> this fails.
    const content = target.querySelector(".content") as HTMLElement;
    expect(content.style.height).toBe(`${CAP}px`);

    // Scroll to the very bottom.
    const maxScroll = CAP - 800;
    viewportEl.scrollTop = maxScroll;
    viewportEl.dispatchEvent(new Event("scroll"));
    await new Promise((r) => requestAnimationFrame(r));
    await tick();

    // (ii) the true tail row (index total-1) renders AND is on-screen.
    // Mutation: header-blind visible count, or a floor(scrollTop/ROW_H) mapping,
    // -> the tail row is below the fold (viewport-y > clientHeight) or absent.
    const tail = target.querySelector(`[data-row-index="${total - 1}"]`) as HTMLElement;
    expect(tail, "the tail row must render at maxScroll").toBeTruthy();
    const viewportY = HEADER_H + parseFloat(tail.style.top);
    expect(viewportY).toBeLessThanOrEqual(800);
    expect(viewportY).toBeGreaterThanOrEqual(0);

    // (iii) the fetch window straddles the tail (decision 2: pages by abs index).
    const lastCall = ensureSpy.mock.calls.at(-1) as [number, number] | undefined;
    expect(lastCall).toBeTruthy();
    expect(lastCall![1]).toBe(total - 1);
    ensureSpy.mockRestore();
  });

  it("positions scaled rows window-relative: the top rendered row sits at top 0", async () => {
    // In scaled mode a row is placed at (i - effectiveFirstRow)*ROW_H, so the
    // window's first row is at 0 and the tail at the band bottom -- NOT at
    // absolute i*ROW_H (which past the cap would be off the clamped spacer).
    // Mutation: rowTopFor ignores `scaled` and uses i*ROW_H -> the first row's
    // top is firstRow*ROW_H (thousands), not 0.
    const columns = [makeColumn("a")];
    const total = 300, CAP = 5000;
    target = document.createElement("div");
    document.body.appendChild(target);
    vi.spyOn(explorer, "ensurePages").mockResolvedValue(undefined as any);

    cmp = new DataTable({ target, props: { columns, total, focusPath: "", maxContentPx: CAP } }) as any;
    await tick();
    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    Object.defineProperty(viewportEl, "clientHeight", { value: 800, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 300, configurable: true });
    viewportEl.scrollTop = CAP - 800; // maxScroll
    viewportEl.dispatchEvent(new Event("scroll"));
    await new Promise((r) => requestAnimationFrame(r));
    await tick();

    const tops = Array.from(target.querySelectorAll(".row"))
      .map((el) => parseFloat((el as HTMLElement).style.top))
      .sort((a, b) => a - b);
    expect(tops[0]).toBe(0); // window-relative, not firstRow*ROW_H
    for (const t of tops) expect(t).toBeGreaterThanOrEqual(0);
  });
});

// V1: go-to-row -- exact navigation (the only way to hit a precise row past the
// cap where drag is coarse; a convenience under it).
describe("DataTable go-to-row (V1)", () => {
  let target: HTMLElement;
  let cmp: { $set: (p: Record<string, unknown>) => void; $destroy: () => void } | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  function mountReady(total: number, maxContentPx?: number): HTMLInputElement {
    target = document.createElement("div");
    document.body.appendChild(target);
    vi.spyOn(explorer, "ensurePages").mockResolvedValue(undefined as any);
    cmp = new DataTable({ target, props: { columns: [makeColumn("a")], total, focusPath: "", maxContentPx } }) as any;
    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    Object.defineProperty(viewportEl, "clientHeight", { value: 800, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 300, configurable: true });
    return target.querySelector("input.goto-row") as HTMLInputElement;
  }

  function goto(input: HTMLInputElement, value: string): void {
    input.value = value;
    input.dispatchEvent(new Event("input"));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
  }

  it("scrolls an unscaled table so the requested row is in the rendered window", async () => {
    const input = mountReady(1000); // 32 + 1000*28 = 28032 < default cap -> unscaled
    await tick();
    goto(input, "500"); // 1-based
    await tick();
    expect(target.querySelector('[data-row-index="499"]')).toBeTruthy(); // 0-based
  });

  it("lands the exact row in a SCALED table (the scaled inverse, not row*ROW_H)", async () => {
    const input = mountReady(300, 5000); // scaled
    await tick();
    goto(input, "150"); // 1-based -> 0-based 149
    await tick();
    // Mutation: scrollTopForRow uses row*ROW_H (unscaled inverse) for the scaled
    // case -> scrollTop maps to a far-off firstVisible and 149 is not rendered.
    const el = target.querySelector('[data-row-index="149"]') as HTMLElement;
    expect(el, "the target row must be in the scaled window").toBeTruthy();
    expect(32 + parseFloat(el.style.top)).toBeLessThanOrEqual(800); // on-screen
  });

  it("clamps an out-of-range row to the last row", async () => {
    const input = mountReady(1000);
    await tick();
    goto(input, "999999");
    await tick();
    expect(target.querySelector('[data-row-index="999"]')).toBeTruthy(); // total-1
  });

  it("is disabled when there are no rows", async () => {
    const input = mountReady(0);
    await tick();
    expect(input.disabled).toBe(true);
  });

  it("Enter on an empty box does not navigate (no yank to row 0)", async () => {
    const input = mountReady(1000);
    await tick();
    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    viewportEl.scrollTop = 14000;
    viewportEl.dispatchEvent(new Event("scroll"));
    await new Promise((r) => requestAnimationFrame(r));
    await tick();
    expect(viewportEl.scrollTop).toBe(14000); // sanity: scrolled deep

    // Empty box + Enter must be a no-op. Mutation: drop the empty guard ->
    // Number("")/Number(null) === 0 -> the view is yanked to the top (0).
    input.value = "";
    input.dispatchEvent(new Event("input"));
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
    await tick();
    expect(viewportEl.scrollTop).toBe(14000);
  });
});
