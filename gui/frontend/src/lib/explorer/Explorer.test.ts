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
import { OpenSource, QueryRows, CloseSource, Cancel, CountMatches, GetCell } from "../../../wailsjs/go/main/App";

vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
  CountMatches: vi.fn(),
  // E5: store.ts calls Codegen; a bare vi.fn() resolves undefined and the
  // first property read on the result throws.
  Codegen: vi.fn(() => Promise.resolve({ jq: ".", sql: "SELECT * FROM data;", warnings: [] })),
  // E4: Explorer now mounts ExportDialog, which imports these.
  ExportQuery: vi.fn(),
  SaveFileDialog: vi.fn(),
  // E6: store.getCell -> GetCell for the value-tree overlay.
  GetCell: vi.fn(() => Promise.resolve({ value: null, found: false })),
}));
vi.mock("../../../wailsjs/runtime", () => ({ EventsOn: vi.fn(() => () => {}) }));

function makeColumn(path: string, over: Record<string, unknown> = {}): any {
  return {
    path, name: path, type: "string", nullable: true, presence: 1, distinct: 1,
    container: false, index: 0, ...over,
  };
}

function makeField(path: string, over: Record<string, unknown> = {}): any {
  return {
    path,
    types: [{ kind: "string", share: 1 }],
    presence: 1,
    nullRate: 0,
    distinct: 1,
    distinctExact: true,
    drift: false,
    ...over,
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

// A macrotask flush, not just a microtask `tick()`: store.test.ts's own
// setFilter coverage uses the same pattern (its `flush` helper) because the
// setFilter -> ensurePages -> QueryRows -> update() chain crosses more
// microtask hops (the async function body, Promise.all, the mocked
// Promise's own resolution) than a single `tick()` reliably drains.
const flush = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

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
    vi.mocked(Cancel).mockReset().mockResolvedValue(undefined as any);
    vi.mocked(CountMatches).mockReset();
    vi.mocked(GetCell).mockReset().mockResolvedValue({ value: null, found: false } as any);
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
      // MINOR-4 fix: pageRowsFor clamps to its 200-row max for ANY column
      // count <= 150 (30000 / 150 === 200 exactly), so with only 3 columns
      // the `lastCall.limit` assertion below was unconditionally 200 on
      // both sides (pageRowsFor(3) and store.ts's own hardcoded-200-in-
      // practice behavior) -- it could not fail no matter what store.ts
      // actually computed. Padding past 150 columns makes pageRowsFor
      // return something other than 200 (147 for 203 columns), so the
      // assertion only passes if store.ts genuinely derives its fetch limit
      // from this exact column count.
      ...Array.from({ length: 200 }, (_, i) => makeColumn(`extra${i}`)),
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
  it("resets the edited-only view when the overlay empties, so it never silently re-arms on the next edit", async () => {
    const columns = [makeColumn("name")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", columns, [makeField("name")]));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("/a.ndjson");
    await flush();
    await tick();

    const strVal = { kind: "string", literal: "z", display: "z" };
    const orig = { kind: "string", literal: "a", display: "a" };
    const snap = { index: 0, cells: [] } as any;

    // Edit a cell, then switch into the edited-only diff view via the toolbar.
    explorer.setEdit(0, "name", strVal, orig, snap);
    await tick();
    const toggle = target.querySelector(".edit-bar .toggle") as HTMLButtonElement;
    expect(toggle, "the toolbar appears once there is an edit").toBeTruthy();
    toggle.click();
    await tick();
    expect(target.querySelector(".edited-rows"), "diff view is showing").toBeTruthy();

    // Drain the overlay WITHOUT the toolbar's Revert-all (which resets the flag
    // itself) -- this is the "revert the last cell in the diff view" / "open a
    // new file" path that only the store touches.
    explorer.revertAllEdits();
    await tick();

    // Re-edit: the view must NOT come back on its own.
    explorer.setEdit(0, "name", strVal, orig, snap);
    await tick();
    // Mutation that must break this: drop the `$: if (editedCount === 0)
    // editedOnly = false` reset -> editedOnly stays true and the next edit
    // dumps the user back into a diff view they never re-selected.
    expect(target.querySelector(".edited-rows")).toBeNull();
    expect(target.querySelector(".viewport"), "the table is shown instead").toBeTruthy();
  });

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
      // M-3: pin `total` itself, not just the rendered text. Without this,
      // a regression that left `total` at its default -1 (rowEstimate never
      // flowing through) would still show "counting…" below -- but via
      // rowCount.ts's OTHER "counting…" branch (`total < 0`), not the
      // `total === 0 && !rowsLoaded` branch this test exists to cover -- so
      // the textContent assertion alone could pass for the wrong reason.
      expect(get(explorer).total).toBe(0);

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

  // E3 Task 8: the filtered empty state (GAP 3) + the live counting
  // affordance's store -> StatusBar wiring (GAP 1/6).
  describe("filtered count + empty state (E3 Task 8)", () => {
    it("renders 'No rows match this filter' (not 'No rows in this file') when a memory-tier filter matches nothing, and 'Clear filter' returns to the unfiltered view", async () => {
      const columns = [makeColumn("id")];
      const fields = [makeField("id")];
      vi.mocked(OpenSource).mockResolvedValueOnce(openResultFor("h20", columns, fields));
      vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns)); // default: unfiltered has rows

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      await explorer.open("filterable.ndjson");
      await tick();
      await tick();

      expect(target.querySelector(".viewport")).toBeTruthy(); // sanity: rows showing, unfiltered

      // The next QueryRows call (setFilter's own page-0 refetch) answers as
      // the memory tier does for a filter matching nothing: an exact,
      // immediately-reconciled zero.
      const zeroRows = { ...rowSetFor(columns), rows: [], total: 0, totalExact: true };
      vi.mocked(QueryRows).mockResolvedValueOnce(zeroRows as any);
      explorer.setFilter({ combinator: "and", conditions: [{} as any] } as any);
      await flush();
      await tick();
      await tick();

      expect(get(explorer).filterActive).toBe(true);
      expect(get(explorer).total).toBe(0);
      expect(target.textContent).toContain("No rows match this filter");
      expect(target.textContent).not.toContain("No rows in this file");

      const clearBtn = Array.from(target.querySelectorAll("button")).find(
        (b) => b.textContent?.trim() === "Clear filter",
      ) as HTMLButtonElement;
      expect(clearBtn).toBeTruthy();

      clearBtn.click();
      await flush();
      await tick();
      await tick();

      expect(get(explorer).filterActive).toBe(false);
      expect(target.querySelector(".viewport")).toBeTruthy(); // rows are back
      expect(target.textContent).not.toContain("No rows match this filter");
    });

    it("shows the counting… affordance (with Cancel) in the status bar while a non-memory-tier filtered count is in flight, then the resolved count once it lands", async () => {
      const columns = [makeColumn("id")];
      const fields = [makeField("id")];
      const res = openResultFor("h21", columns, fields);
      res.tier = "rescan"; // non-memory: setFilter's active branch calls startCount -> CountMatches
      vi.mocked(OpenSource).mockResolvedValueOnce(res);
      vi.mocked(QueryRows).mockResolvedValueOnce(rowSetFor(columns)); // open()'s own unfiltered page 0
      // Every filtered page-0 fetch after setFilter: on the rescan tier
      // QueryRows(wantTotal:false) returns Total:-1 (per store.ts's own
      // comment -- CountMatches is the ONLY eager exact source there), so the
      // mock must not hand back a total here or this test could not tell
      // "counting…" apart from a real (if coincidentally identical) row
      // count landing from the page fetch itself.
      vi.mocked(QueryRows).mockResolvedValue({ ...rowSetFor(columns), total: -1, totalExact: false });

      let resolveCount!: (r: unknown) => void;
      const countPromise = new Promise((resolve) => { resolveCount = resolve; });
      vi.mocked(CountMatches).mockReturnValueOnce(countPromise as any);

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      await explorer.open("counted.ndjson");
      await tick();
      await tick();

      explorer.setFilter({ combinator: "and", conditions: [{} as any] } as any);
      await flush();
      await tick();
      await tick();

      expect(get(explorer).counting).toBe(true);
      const metric = target.querySelector(".metric.mono") as HTMLElement;
      expect(metric.textContent).toBe("counting…");
      expect(target.querySelector("button.cancel-count")).toBeTruthy();

      resolveCount({ total: 3, exact: true });
      await flush();
      await tick();
      await tick();

      expect(get(explorer).counting).toBe(false);
      expect(target.querySelector("button.cancel-count")).toBeNull();
      expect((target.querySelector(".metric.mono") as HTMLElement).textContent).toBe("3 rows");
    });

    it("wires the StatusBar's cancelCount event to explorer.cancelCount()", async () => {
      const columns = [makeColumn("id")];
      const fields = [makeField("id")];
      const res = openResultFor("h22", columns, fields);
      res.tier = "rescan";
      vi.mocked(OpenSource).mockResolvedValueOnce(res);
      vi.mocked(QueryRows).mockResolvedValueOnce(rowSetFor(columns)); // open()'s own unfiltered page 0
      vi.mocked(QueryRows).mockResolvedValue({ ...rowSetFor(columns), total: -1, totalExact: false });

      const neverResolves = new Promise(() => {});
      vi.mocked(CountMatches).mockReturnValueOnce(neverResolves as any);

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      await explorer.open("cancelme.ndjson");
      await tick();
      await tick();

      explorer.setFilter({ combinator: "and", conditions: [{} as any] } as any);
      await flush();
      await tick();
      await tick();

      expect(get(explorer).counting).toBe(true);
      const cancelBtn = target.querySelector("button.cancel-count") as HTMLButtonElement;
      expect(cancelBtn).toBeTruthy();

      cancelBtn.click();
      await tick();

      expect(get(explorer).counting).toBe(false);
      expect(target.querySelector("button.cancel-count")).toBeNull();
    });
  });

  // E3 Task 9: Explorer is the router between the sidebar's seedFilter event
  // and FilterBar -- a funnel click must open the (initially closed) bar via
  // the bindable `filterOpen` prop and hand the seed down so FilterBar
  // appends a condition defaulted to the column's op, with its value input
  // focused.
  describe("click-to-seed routes the sidebar's seedFilter into FilterBar (E3 Task 9)", () => {
    it("opens the filter bar and adds a condition for the seeded column, defaulted to its type's op, with the value input focused", async () => {
      const columns = [makeColumn("age", { type: "int" }), makeColumn("id")];
      const fields = [
        makeField("age", { types: [{ kind: "int", share: 1 }] }),
        makeField("id", { types: [{ kind: "int", share: 1 }] }),
      ];
      vi.mocked(OpenSource).mockResolvedValueOnce(openResultFor("h30", columns, fields));
      vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));

      cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
      await explorer.open("seed.ndjson");
      await tick();
      await tick();

      expect(target.querySelector(".filter-bar")).toBeNull(); // sanity: bar starts closed

      const ageRow = target.querySelector('.row[data-path="age"]') as HTMLElement;
      expect(ageRow).toBeTruthy();
      const seedBtn = ageRow.querySelector("button.seed") as HTMLButtonElement;
      expect(seedBtn).toBeTruthy();

      seedBtn.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await tick();
      await tick();

      const bar = target.querySelector(".filter-bar");
      expect(bar).toBeTruthy(); // filterOpen flipped true through the bindable prop

      const colSelect = bar!.querySelector('select[aria-label="Column"]') as HTMLSelectElement;
      expect(colSelect).toBeTruthy();
      expect(colSelect.value).toBe("age");

      const opSelect = bar!.querySelector('select[aria-label="Operator"]') as HTMLSelectElement;
      expect(opSelect.value).toBe("gte"); // defaultOpForType("int")

      const valueInput = bar!.querySelector('input[aria-label="Value"]') as HTMLInputElement;
      expect(valueInput).toBeTruthy();
      expect(document.activeElement).toBe(valueInput); // focused per the wiring brief

      // Typing a value and letting the (real, un-mocked) 250ms debounce
      // elapse reaches the store's setFilter -- the end-to-end path from a
      // funnel click to a live-filtered query.
      const setFilterSpy = vi.spyOn(explorer, "setFilter");
      valueInput.value = "20";
      valueInput.dispatchEvent(new Event("input"));
      await tick();
      await new Promise((r) => setTimeout(r, 300));
      await tick();

      expect(setFilterSpy).toHaveBeenCalled();
      const filter = setFilterSpy.mock.calls.at(-1)![0] as any;
      expect(filter.conditions).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ path: "age", op: "gte", value: { kind: "number", num: 20 } }),
        ]),
      );
      setFilterSpy.mockRestore();
    });
  });

  // E6 §6: the search box must be reachable without opening the filter panel.
  // Mutation: nest SearchBar inside FilterBar's `{#if open}` -> with the panel
  // closed the input is absent and this fails.
  it("renders the global search box even when the filter panel is closed", async () => {
    const columns = [makeColumn("a")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", columns, [makeField("a")]));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));

    cmp = new Explorer({ target, props: { filterOpen: false } }) as unknown as { $destroy: () => void };
    await explorer.open("file.ndjson");
    await tick();

    const searchInput = target.querySelector('input[aria-label="Search all fields"]');
    expect(searchInput, "the search box must be visible with the filter panel closed").toBeTruthy();
  });

  // E6 Task 7: clicking a container cell's expand affordance fetches the full
  // value (explorer.getCell -> GetCell) and mounts it in the ValueTree overlay.
  it("expands a container cell into the value-tree overlay via getCell", async () => {
    const columns = [makeColumn("obj")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", columns, [makeField("obj")]));
    // Page 0's single row has an OBJECT cell and a NON-zero absolute index (7),
    // so the test proves the dispatch carries row.index, not the render slot.
    vi.mocked(QueryRows).mockResolvedValue({
      columns,
      rows: [{ index: 7, cells: [{ kind: "object", str: "{…}", count: 2, hasMore: true }] }],
      offset: 0, total: 10, totalExact: true, scanned: 1, truncated: false, elapsedMs: 0,
      columnsTruncated: false, totalPaths: 1,
    } as any);
    vi.mocked(GetCell).mockResolvedValue({ value: { alpha: 1, beta: "greeting" }, found: true } as any);

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("file.ndjson");
    await tick();
    await tick();

    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    Object.defineProperty(viewportEl, "clientHeight", { value: 500, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 600, configurable: true });
    window.dispatchEvent(new Event("resize")); // recompute the row/col window with real dims
    await tick();

    const expandBtn = target.querySelector(".expand-btn") as HTMLButtonElement;
    expect(expandBtn, "a container cell must show an expand affordance").toBeTruthy();
    expandBtn.click();
    await flush();
    await tick();

    expect(vi.mocked(GetCell)).toHaveBeenCalledWith(
      expect.objectContaining({ index: 7, path: "obj" }),
    );
    const dialog = target.querySelector('[role="dialog"]') as HTMLElement;
    expect(dialog, "the value-tree overlay must open").toBeTruthy();
    expect(dialog.textContent).toContain("alpha");
    expect(dialog.textContent).toContain("greeting");
  });

  // E6 review #7: when a filter AND a search are both active and zero rows
  // match, the empty state must name both, not mislabel it "No rows match this
  // filter" (clearing the filter alone may not restore rows). Mutation: let the
  // filterActive-only branch win -> the text reads "No rows match this filter".
  it("labels the empty state for filter+search when both are active and zero rows match", async () => {
    const columns = [makeColumn("city")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", columns, [makeField("city")]));
    vi.mocked(QueryRows).mockResolvedValueOnce(rowSetFor(columns)); // open's page 0 (non-empty)

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("file.ndjson");
    await tick();

    // Both active, and the query now returns zero rows.
    vi.mocked(QueryRows).mockResolvedValue({
      columns, rows: [], offset: 0, total: 0, totalExact: true, scanned: 0,
      truncated: true, elapsedMs: 0, columnsTruncated: false, totalPaths: 1,
    } as any);
    explorer.setFilter({
      combinator: "and",
      conditions: [{ path: "city", op: "eq", value: { kind: "string", str: "zzz" } }],
    } as any);
    explorer.setSearch("zzz");
    await flush();
    await tick();

    const empty = target.querySelector(".empty-state") as HTMLElement;
    expect(empty, "an empty state must show").toBeTruthy();
    expect(empty.textContent).toContain("filter and search");
    expect(empty.textContent).not.toContain("No rows match this filter");
  });
});

// --- E4 Task 11: a projection must not shrink the filter's vocabulary --------

describe("Explorer under a column projection", () => {
  let target2: HTMLElement;
  let cmp2: { $destroy: () => void } | null = null;

  beforeEach(async () => {
    vi.mocked(OpenSource).mockReset();
    vi.mocked(QueryRows).mockReset();
    vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
    vi.mocked(Cancel).mockReset().mockResolvedValue(undefined as any);
    vi.mocked(CountMatches).mockReset();
    await explorer.close();
    target2 = document.createElement("div");
    document.body.appendChild(target2);
  });

  afterEach(() => {
    cmp2?.$destroy();
    cmp2 = null;
    target2.remove();
  });

  it("keeps the filter bar and the structure map on the BASE columns", async () => {
    const cols = [makeColumn("id", { type: "int" }), makeColumn("user.name")];
    const fields = [makeField("id"), makeField("user.name")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", cols, fields));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(cols));
    await explorer.open("/two.ndjson");
    await flush();

    cmp2 = new Explorer({ target: target2, props: { filterOpen: true } });
    await tick();

    // Project down to one column.
    explorer.setTransform({ select: [{ path: "id", as: "id" }] } as any, [makeColumn("id", { type: "int" })]);
    await flush();
    await tick();
    expect(get(explorer).columns).toHaveLength(1);

    // The filter bar must still offer BOTH columns: a filter runs on the
    // record, before projection.
    //
    // Mutation that must break this: pass $explorer.columns to FilterBar
    // instead of $explorer.baseColumns -> "user.name" disappears from the
    // column <select> and this fails.
    const addBtn = Array.from(target2.querySelectorAll("button")).find((b) =>
      b.textContent?.includes("Add condition"),
    ) as HTMLButtonElement;
    addBtn.click();
    await tick();
    const colSelect = target2.querySelector('[aria-label="Column"]') as HTMLSelectElement;
    const offered = Array.from(colSelect.options).map((o) => o.value);
    expect(offered).toContain("id");
    expect(offered).toContain("user.name");

    // And the sidebar must still show the hidden column as a real column
    // (undimmed), because the SOURCE still has it.
    const dimmed = Array.from(target2.querySelectorAll(".row.dimmed")).map(
      (el) => el.textContent?.trim() ?? "",
    );
    expect(dimmed.some((t) => t.includes("user.name"))).toBe(false);
  });
});

// --- E4 branch review, finding 3 --------------------------------------------

describe("transform errors do not outlive the file that caused them", () => {
  let target3: HTMLElement;
  let cmp3: { $destroy: () => void } | null = null;

  beforeEach(async () => {
    vi.mocked(OpenSource).mockReset();
    vi.mocked(QueryRows).mockReset();
    vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
    vi.mocked(Cancel).mockReset().mockResolvedValue(undefined as any);
    vi.mocked(CountMatches).mockReset();
    await explorer.close();
    target3 = document.createElement("div");
    document.body.appendChild(target3);
  });

  afterEach(() => {
    cmp3?.$destroy();
    cmp3 = null;
    target3.remove();
  });

  it("re-enables Export after opening a different file", async () => {
    const colsA = [makeColumn("a"), makeColumn("b")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", colsA, [makeField("a"), makeField("b")]));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(colsA));
    await explorer.open("/a.ndjson");
    await flush();

    cmp3 = new Explorer({ target: target3, props: { columnsOpen: true, exportOpen: true } });
    await tick();

    // Make file A's draft invalid: rename "b" to collide with "a".
    const renames = Array.from(target3.querySelectorAll(".rename")) as HTMLInputElement[];
    renames[1].value = "a";
    renames[1].dispatchEvent(new Event("input"));
    await tick();
    const exportBtn = () =>
      Array.from(target3.querySelectorAll("button")).find((b) => b.textContent?.trim() === "Export") as
        | HTMLButtonElement
        | undefined;
    expect(exportBtn()?.disabled).toBe(true);

    // Open a different file: the panel remounts with a clean draft.
    const colsB = [makeColumn("x"), makeColumn("y")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h2", colsB, [makeField("x"), makeField("y")]));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(colsB));
    await explorer.open("/b.ndjson");
    await flush();
    await tick();

    // Mutation that must break this: drop TransformPanel's onMount dispatch ->
    // Explorer keeps file A's transformErrors and Export stays disabled on a
    // file whose columns are perfectly valid.
    expect(exportBtn()?.disabled).toBe(false);
  });
});
