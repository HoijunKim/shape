// @vitest-environment jsdom
//
// A3: StatusBar had NO test at all before T9 -- nothing pinned the "never
// present an estimate as exact" rule this component exists to enforce (spec
// §4), the columns-truncated text, or the warnings gate. These mount the
// real StatusBar.svelte directly with plain props (no store): they cover
// StatusBar's OWN rendering logic. The separate store -> prop WIRING (does
// Explorer.svelte pass the right $explorer field into each of these props)
// is pinned in Explorer.test.ts instead, since that wiring lives in
// Explorer.svelte, not here -- a component-only test like this one cannot
// tell a correct `totalExact={$explorer.totalExact}` binding apart from a
// mis-wired `totalExact={!$explorer.sampled}` one.
import { describe, it, expect, afterEach } from "vitest";
import StatusBar from "./StatusBar.svelte";

type Instance = { $set: (p: Record<string, unknown>) => void; $destroy: () => void };

describe("StatusBar", () => {
  let target: HTMLElement;
  let cmp: Instance | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  function mount(props: Record<string, unknown>): HTMLElement {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StatusBar({ target, props }) as unknown as Instance;
    return target;
  }

  it("renders an exact total with no tilde", () => {
    const t = mount({ total: 1234, totalExact: true, rowsLoaded: true });
    const metric = t.querySelector(".metric.mono") as HTMLElement;
    expect(metric.textContent).toBe("1,234 rows");
  });

  it("renders an inexact total with a leading tilde, regardless of `sampled`", () => {
    // sampled is deliberately false here: the tilde must come from
    // totalExact alone (formatRowCount), never a `sampled` shortcut.
    const t = mount({ total: 1234, totalExact: false, sampled: false, rowsLoaded: true });
    const metric = t.querySelector(".metric.mono") as HTMLElement;
    expect(metric.textContent).toBe("~1,234 rows");
  });

  it("shows the truncated-columns form with the uncapped totalPaths, not the capped columnCount alone", () => {
    const t = mount({
      total: 10, totalExact: true, columnCount: 12, columnsTruncated: true, totalPaths: 512,
    });
    const columnsMetric = t.querySelectorAll(".metric.mono")[1] as HTMLElement;
    expect(columnsMetric.textContent).toBe("showing 12 of 512 columns");
  });

  it("shows a plain column count when columns are not truncated", () => {
    const t = mount({ total: 10, totalExact: true, columnCount: 5, columnsTruncated: false, totalPaths: 5 });
    const columnsMetric = t.querySelectorAll(".metric.mono")[1] as HTMLElement;
    expect(columnsMetric.textContent).toBe("5 columns");
  });

  it("renders a warning string verbatim, byte-for-byte, including the streaming-mode em dash", () => {
    const t = mount({
      total: 10, totalExact: false, sampled: true,
      warnings: ["large file — streaming mode (totals are estimates)"],
    });
    const warning = t.querySelector(".warning") as HTMLElement;
    expect(warning.textContent).toBe("large file — streaming mode (totals are estimates)");
  });

  it("renders warnings even when NOT sampled (A3: the `sampled &&` gate was removed -- a future non-rescan warning must not be silently swallowed)", () => {
    const t = mount({
      total: 10, totalExact: true, sampled: false,
      warnings: ["a hypothetical non-rescan warning"],
    });
    const warning = t.querySelector(".warning") as HTMLElement;
    expect(warning).toBeTruthy();
    expect(warning.textContent).toBe("a hypothetical non-rescan warning");
  });

  it("renders no warnings section at all when there are none", () => {
    const t = mount({ total: 10, totalExact: true, warnings: [] });
    expect(t.querySelector(".warnings")).toBeNull();
  });

  it("shows the loading pip only while fetching", () => {
    const fetching = mount({ total: 10, totalExact: true, fetching: true });
    expect(fetching.querySelector(".loading")).toBeTruthy();
  });

  it("shows no loading pip when not fetching", () => {
    const idle = mount({ total: 10, totalExact: true, fetching: false });
    expect(idle.querySelector(".loading")).toBeNull();
  });
});
