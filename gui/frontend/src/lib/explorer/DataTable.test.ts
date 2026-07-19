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
import { describe, it, expect, afterEach } from "vitest";
import { tick } from "svelte";
import DataTable from "./DataTable.svelte";
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
