// @vitest-environment jsdom
//
// Regression coverage for the two "real bugs if you skip them" obligations
// carried forward into T8's Explorer shell:
//
//   1. columnPaths must be rebuilt fresh from $explorer.columns every time
//      it changes (in particular across a second open()) -- StructureMap
//      deliberately does not own this set, Explorer does.
//   2. focusToken must be bumped UNCONDITIONALLY on every focus event, from
//      either StructureMap or DataTable, even when the newly-focused path is
//      the SAME path already focused -- Svelte no-ops a same-value prop
//      re-assignment, so re-dispatching an unchanged focusPath alone can
//      never re-trigger TreeNode's re-expand-a-manually-collapsed-ancestor
//      check.
//
// Both mount the REAL Explorer.svelte (which mounts real StructureMap/
// TreeNode/DataTable), driving the real `explorer` store through a mocked
// Wails bridge -- the same pattern store.test.ts and StructureMap.test.ts
// already use -- so a regression shows up as wrong DOM, not just a wrong
// return value from an isolated helper.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";
import { get } from "svelte/store";
import Explorer from "./Explorer.svelte";
import { explorer } from "./store";
import { pageRowsFor } from "./paging";
import { OpenSource, QueryRows, CloseSource } from "../../../wailsjs/go/main/App";

vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
}));

function makeColumn(path: string, over: Record<string, unknown> = {}): any {
  return {
    path, name: path, type: "string", nullable: true, presence: 1, distinct: 1,
    container: false, index: 0, ...over,
  };
}

function makeField(path: string): any {
  return {
    path,
    types: [{ kind: "string", share: 1 }],
    presence: 1,
    nullRate: 0,
    distinct: 1,
    distinctExact: true,
    drift: false,
  };
}

function openResultFor(handle: string, columns: any[], fields: any[]): any {
  return {
    handle,
    format: "ndjson",
    tier: "memory",
    columns,
    profile: { records: columns.length ? 10 : 0, skipped: 0, fields },
    sampled: false,
    rowEstimate: 10,
    rowExact: true,
    warnings: [],
    columnsTruncated: false,
    totalPaths: columns.length,
  };
}

function rowSetFor(columns: any[]): any {
  return {
    columns,
    rows: [{ index: 0, cells: columns.map(() => ({ kind: "string", str: "x" })) }],
    offset: 0,
    total: 10,
    totalExact: true,
    scanned: 1,
    truncated: false,
    elapsedMs: 0,
    columnsTruncated: false,
    totalPaths: columns.length,
  };
}

describe("Explorer", () => {
  let target: HTMLElement;
  let cmp: { $destroy: () => void } | null = null;

  beforeEach(async () => {
    vi.mocked(OpenSource).mockReset();
    vi.mocked(QueryRows).mockReset();
    vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
    await explorer.close();
    vi.mocked(CloseSource).mockClear(); // don't count close()'s own no-op call
    target = document.createElement("div");
    document.body.appendChild(target);
  });

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target.remove();
  });

  it("bumps focusToken on every focus event, independent of any store change, so re-focusing an already-focused, manually-collapsed path re-reveals it", async () => {
    // Isolating focusToken's OWN contribution matters here: `fields` and
    // `columnPaths` are array/Set-typed props, and neither StructureMap nor
    // TreeNode opts into `<svelte:options immutable>`, so Svelte 3's default
    // `safe_not_equal` treats ANY re-pass of an object-typed prop as
    // "changed" regardless of reference/content equality (see
    // svelte/internal's `safe_not_equal`: for objects it unconditionally
    // ORs in `true`). That means a bare `explorer.focus()` call -- which
    // reassigns the WHOLE `$explorer` store object even when only
    // `focusPath` logically changes -- already forces `fields`/`columnPaths`
    // to be re-passed and thus treated as dirty everywhere downstream,
    // which by itself re-triggers TreeNode's re-expand check REGARDLESS of
    // focusToken. A naive version of this test that drives a real
    // `explorer.focus()` call cannot tell a correct unconditional bump apart
    // from the exact anti-pattern the brief warns against (bumping only when
    // the path changed) -- both "pass" once, because `explorer.focus()`
    // alone already saturates the cascade. Stubbing `explorer.focus` to a
    // no-op freezes the store completely, so on the second click the ONLY
    // thing that can possibly cause a re-expand is focusToken itself.
    const columns = [makeColumn("user.address.city"), makeColumn("id")];
    const fields = [makeField("user.address.city"), makeField("id")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", columns, fields));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    // "user.address.city" is columns[0].path, so store.open() itself
    // defaults focusPath to it -- no explicit explorer.focus() call needed
    // to establish the initial focus/auto-expand.
    await explorer.open("file.ndjson");
    await tick();
    await tick();

    expect(target.querySelector('.row[data-path="user.address.city"]')).toBeTruthy(); // sanity: auto-expanded from the initial focus

    // User manually collapses the "user" ancestor via its caret.
    const userRow = target.querySelector('.row[data-path="user"]') as HTMLElement;
    expect(userRow).toBeTruthy();
    const caret = userRow.querySelector(".caret") as HTMLElement;
    caret.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    expect(target.querySelector('.row[data-path="user.address.city"]')).toBeNull(); // sanity: genuinely collapsed

    const focusSpy = vi.spyOn(explorer, "focus").mockImplementation(() => {});

    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    Object.defineProperty(viewportEl, "clientHeight", { value: 500, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 600, configurable: true });
    const header = Array.from(target.querySelectorAll(".header-cell")).find(
      (el) => (el as HTMLElement).title === "user.address.city",
    ) as HTMLElement;
    expect(header).toBeTruthy();

    // Click the SAME already-focused header again. `explorer.focus` is
    // stubbed to a no-op, so $explorer does not change at all -- if this
    // re-reveals the row, it can only be because Explorer bumped focusToken.
    header.click();
    await tick();

    expect(focusSpy).toHaveBeenCalledWith("user.address.city");
    expect(target.querySelector('.row[data-path="user.address.city"]')).toBeTruthy(); // regression: without an unconditional focusToken bump, this stays null

    focusSpy.mockRestore();
  });

  it("recomputes columnPaths fresh on every open, so a stale column from a previously-open file is never treated as a real column on the new one", async () => {
    const columnsA = [makeColumn("a")];
    // "dropped" carries a FieldDTO on file A but is not staged as a column.
    const fieldsA = [makeField("a"), makeField("dropped")];
    vi.mocked(OpenSource).mockResolvedValueOnce(openResultFor("h1", columnsA, fieldsA));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columnsA));

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("fileA.ndjson");
    await tick();

    let droppedRow = target.querySelector('.row[data-path="dropped"]') as HTMLElement;
    expect(droppedRow).toBeTruthy();
    expect(droppedRow.classList.contains("dimmed")).toBe(true); // not a column on file A

    // File B: "dropped" IS a real column this time.
    const columnsB = [makeColumn("dropped")];
    const fieldsB = [makeField("dropped")];
    vi.mocked(OpenSource).mockResolvedValueOnce(openResultFor("h2", columnsB, fieldsB));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columnsB));
    await explorer.open("fileB.ndjson");
    await tick();

    droppedRow = target.querySelector('.row[data-path="dropped"]') as HTMLElement;
    expect(droppedRow).toBeTruthy();
    // Regression: a columnPaths Set computed once (e.g. at mount, or memoized
    // outside of $explorer.columns) would still hold file A's "a" only, and
    // "dropped" would incorrectly stay dimmed here.
    expect(droppedRow.classList.contains("dimmed")).toBe(false);
  });

  it("renders the zero-columns empty state, including the skipped count, without a blank frame", async () => {
    vi.mocked(OpenSource).mockResolvedValueOnce(
      (() => {
        const r = openResultFor("h3", [], []);
        r.profile.skipped = 7;
        return r;
      })(),
    );
    // ensurePages(0, 0) still runs unconditionally after a successful open()
    // even with zero columns -- must resolve to a valid RowSet or the fetch
    // itself throws and status flips to "error" instead of staying "ready".
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor([]));

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("empty.csv");
    await tick();

    expect(target.textContent).toContain("No columns detected");
    expect(target.textContent).toContain("7");
  });

  // Obligation 3 (A2): DataTable's `columns` prop must be $explorer.columns
  // DIRECTLY, never a filtered/derived subset (e.g. dropping `container`
  // columns for display) -- see Explorer.svelte's comment at the DataTable
  // call site. A filtered subset would desync two things at once: the
  // rendered column/header count (fewer headers than real columns) AND the
  // cell-index alignment (row.cells[c] indexes into the FULL column set the
  // backend actually queried, not the filtered one DataTable would then be
  // iterating). Nothing before T9 asserted either half of this.
  it("passes $explorer.columns to DataTable unfiltered, keeping the rendered column count and the fetch window in agreement (A2)", async () => {
    const columns = [
      makeColumn("id"),
      makeColumn("meta", { container: true }), // a container column: the case a naive filter would drop
      makeColumn("meta.detail"),
    ];
    const fields = columns.map((c) => makeField(c.path));
    vi.mocked(OpenSource).mockResolvedValueOnce(openResultFor("h4", columns, fields));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("wide.ndjson");
    await tick();
    await tick();

    // Rendered column count: DataTable sets aria-colcount straight from its
    // `columns` prop length. If Explorer.svelte filtered container columns
    // out before passing them down, this would read 2, not 3.
    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    expect(viewportEl).toBeTruthy();
    expect(viewportEl.getAttribute("aria-colcount")).toBe(String(columns.length));

    // Fetch window: the page size requested from the backend must be keyed
    // off the SAME column count the table renders, per pageRowsFor's whole
    // reason for existing (T5's cross-file invariant note) -- a fetch window
    // computed off a different column count than the render window can
    // silently disagree without either side visibly breaking.
    const lastCall = vi.mocked(QueryRows).mock.calls.at(-1)?.[0] as any;
    expect(lastCall.limit).toBe(pageRowsFor(columns.length));
  });

  // A3: StatusBar itself has direct-prop tests (StatusBar.test.ts), but
  // nothing pinned the WIRING -- the exact `$explorer.*` expression each
  // Explorer.svelte -> StatusBar prop binds to. Swapping
  // `totalExact={$explorer.totalExact}` for `totalExact={!$explorer.sampled}`
  // passed every test that existed before T9, because every fixture used so
  // far happens to have `sampled` and `totalExact` moving together (rescan:
  // sampled=true/totalExact=false; memory: sampled=false/totalExact=true) --
  // the two booleans were never exercised as independent. These fixtures
  // deliberately decouple them.
  describe("StatusBar wiring (A3)", () => {
    it("wires totalExact straight through, independent of sampled", async () => {
      const columns = [makeColumn("id")];
      const fields = [makeField("id")];
      const res = openResultFor("h5", columns, fields);
      res.sampled = false; // NOT sampled...
      res.rowEstimate = 42;
      res.rowExact = false; // ...yet still inexact -- isolates totalExact from sampled
      vi.mocked(OpenSource).mockResolvedValueOnce(res);
      // total/totalExact must come from rsTotal/rsTotalExact only when
      // rsTotal >= 0 (paging.ts's reconcileEof); rsTotal: -1 here so the
      // landed page does not clobber the inexact 42 set by open().
      const rs = rowSetFor(columns);
      rs.total = -1;
      rs.totalExact = false;
      rs.truncated = false;
      vi.mocked(QueryRows).mockResolvedValue(rs);

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      await explorer.open("weird.ndjson");
      await tick();
      await tick();

      expect(get(explorer).sampled).toBe(false);
      expect(get(explorer).totalExact).toBe(false);

      const metric = target.querySelector(".metric.mono") as HTMLElement;
      // Regression: a `totalExact={!$explorer.sampled}` mis-wiring reads
      // `!false === true` here and would render "42 rows" with no tilde --
      // the one failure mode the spec forbids (presenting an estimate as
      // exact).
      expect(metric.textContent).toBe("~42 rows");
    });

    it("wires columnsTruncated and totalPaths straight through", async () => {
      const columns = [makeColumn("a"), makeColumn("b")];
      const fields = [makeField("a"), makeField("b")];
      const res = openResultFor("h6", columns, fields);
      res.columnsTruncated = true;
      res.totalPaths = 50;
      vi.mocked(OpenSource).mockResolvedValueOnce(res);
      const rs = rowSetFor(columns);
      // store.ts's ensurePages overwrites columnsTruncated/totalPaths from
      // EVERY landed RowSet (engine.go: these travel with each page, not
      // just open()), so the mocked page must carry the same values or the
      // very first page fetched after open() would immediately clobber them
      // back to this RowSet's defaults (false/columns.length).
      rs.columnsTruncated = true;
      rs.totalPaths = 50;
      vi.mocked(QueryRows).mockResolvedValue(rs);

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      await explorer.open("wide2.ndjson");
      await tick();
      await tick();

      const columnsMetric = target.querySelectorAll(".metric.mono")[1] as HTMLElement;
      // Regression: hardcoding columnsTruncated=false, or swapping totalPaths
      // for columnCount, would render "2 columns" instead.
      expect(columnsMetric.textContent).toBe("showing 2 of 50 columns");
    });

    it("wires warnings straight through, even when NOT sampled (A3's paired StatusBar gate fix)", async () => {
      const columns = [makeColumn("id")];
      const fields = [makeField("id")];
      const res = openResultFor("h7", columns, fields);
      res.sampled = false;
      res.warnings = ["a hypothetical non-rescan warning"];
      vi.mocked(OpenSource).mockResolvedValueOnce(res);
      vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      await explorer.open("warned.ndjson");
      await tick();
      await tick();

      // Regression: warnings={[]} (or any other constant/empty binding)
      // would leave this null regardless of the gate fix.
      expect(target.textContent).toContain("a hypothetical non-rescan warning");
    });

    it("wires tier straight through", async () => {
      const columns = [makeColumn("id")];
      const fields = [makeField("id")];
      const res = openResultFor("h8-tier", columns, fields);
      res.tier = "rescan";
      vi.mocked(OpenSource).mockResolvedValueOnce(res);
      vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      await explorer.open("tiered.ndjson");
      await tick();
      await tick();

      // Regression: `tier=""` (or any other constant/empty binding) would
      // leave `.tier` unrendered (StatusBar.svelte's `{#if tier}` gate).
      expect(target.querySelector(".tier")?.textContent).toBe("rescan");
    });

    // T9 IMPORTANT-3: `rowsLoaded={$explorer.version > 0}` (Explorer.svelte)
    // is the only thing standing between a rescan-tier `fileSize/avgBytes`
    // pre-open estimate that floors to 0 and the UI confidently lying "0
    // rows" before the first page has even landed to confirm it
    // (rowCount.ts's `total === 0 && !rowsLoaded` branch). Mutating the prop
    // to a hardcoded `true` survived the entire pre-T9 suite because no
    // fixture ever sat in that exact state (total: 0, totalExact: false,
    // no page landed yet). `fetching` gets the same before/after treatment
    // here for free, since both flip on the same page-landing boundary.
    it("wires rowsLoaded and fetching straight through: 'counting...' (not '0 rows') before the first page lands, then flips once it does", async () => {
      const columns = [makeColumn("id")];
      const fields = [makeField("id")];
      const res = openResultFor("h9-rowsloaded", columns, fields);
      res.tier = "rescan";
      res.sampled = true;
      res.rowEstimate = 0; // a rescan-tier fileSize/avgBytes estimate that floors to 0
      res.rowExact = false;
      vi.mocked(OpenSource).mockResolvedValueOnce(res);

      // Defer QueryRows's resolution so the "open() succeeded, but no page
      // has landed yet" window is directly observable rather than flashing
      // by within a single microtask.
      let resolveFirstPage!: (rs: unknown) => void;
      const firstPage = new Promise((resolve) => { resolveFirstPage = resolve; });
      vi.mocked(QueryRows).mockReturnValueOnce(firstPage as any);

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      const openPromise = explorer.open("empty-estimate.ndjson"); // not awaited: ensurePages(0,0) is left pending
      await Promise.resolve();
      await tick();
      await tick();

      // Sanity: open() itself resolved (status is "ready") and the pending
      // page fetch is in flight -- both required for this window to mean
      // anything.
      expect(get(explorer).status).toBe("ready");
      expect(get(explorer).version).toBe(0);

      // fetching wiring: the open()-triggered ensurePages(0,0) call has not
      // resolved yet.
      expect(target.querySelector(".loading")).toBeTruthy();
      // rowsLoaded wiring: total is 0, totalExact is false, and no page has
      // landed -- must defer to "counting...", never claim "0 rows" (or,
      // per formatRowCount, "~0 rows").
      const metric = target.querySelector(".metric.mono") as HTMLElement;
      expect(metric.textContent).toBe("counting…");

      resolveFirstPage(rowSetFor(columns)); // total: 10, totalExact: true
      await openPromise;
      await tick();
      await tick();

      // Regression: a hardcoded `fetching={false}` would already have hidden
      // the pip above; a hardcoded `fetching={true}` fails here instead.
      expect(target.querySelector(".loading")).toBeNull();
      // Regression: a hardcoded `rowsLoaded={true}` would have already
      // failed the pre-load assertion above (it would have shown "~0 rows"
      // instead of "counting..."), proving the flip below is driven by the
      // real store field, not a constant.
      expect((target.querySelector(".metric.mono") as HTMLElement).textContent).toBe("10 rows");
    });
  });

  // A5: a mid-scroll page-fetch failure must render as a dismissible bar
  // ABOVE the still-mounted table, never replace the whole pane the way the
  // full-screen `status === "error"` branch does. This is the end-to-end DOM
  // check; store.test.ts pins the underlying state transition in isolation.
  it("renders a role=alert bar above the table on a mid-scroll page-fetch failure, without unmounting the table (A5)", async () => {
    const columns = [makeColumn("id")];
    const fields = [makeField("id")];
    vi.mocked(OpenSource).mockResolvedValueOnce(openResultFor("h8", columns, fields));
    vi.mocked(QueryRows).mockResolvedValueOnce(rowSetFor(columns)); // open()'s own ensurePages(0,0)

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("scrolled.ndjson");
    await tick();
    await tick();

    expect(target.querySelector(".viewport")).toBeTruthy(); // sanity: table is up
    expect(target.querySelector(".page-error-bar")).toBeNull(); // sanity: no bar yet

    vi.mocked(QueryRows).mockRejectedValueOnce(new Error("network hiccup"));
    await explorer.ensurePages(1000, 1000); // a page far from the one already cached
    await tick();

    const bar = target.querySelector(".page-error-bar");
    expect(bar).toBeTruthy();
    expect(bar!.getAttribute("role")).toBe("alert");
    expect(bar!.textContent).toContain("network hiccup");
    // The regression this guards against: Explorer.svelte used to render
    // `$explorer.status === "error"` as a FULL-PANE replacement. That branch
    // must not have been taken, and the table (with its already-fetched
    // page) must still be mounted underneath the bar.
    expect(target.querySelector(".viewport")).toBeTruthy();
    expect(target.querySelector(".error-shell")).toBeNull();

    const dismissBtn = bar!.querySelector("button.dismiss") as HTMLButtonElement;
    dismissBtn.click();
    await tick();
    expect(target.querySelector(".page-error-bar")).toBeNull(); // dismissed
    expect(target.querySelector(".viewport")).toBeTruthy(); // table still there
  });
});
