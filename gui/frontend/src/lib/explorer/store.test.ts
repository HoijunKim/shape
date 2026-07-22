import { describe, it, expect, vi, beforeEach } from "vitest";
import { get } from "svelte/store";
import { explorer } from "./store";
import { OpenSource, QueryRows, CloseSource, Cancel, CountMatches, ExportQuery } from "../../../wailsjs/go/main/App";
import { EventsOn } from "../../../wailsjs/runtime";
import type { Column, Filter } from "./types";

// C1: a slow open() must not clobber a newer file's state, and it must not
// leak the backend handle it opened. Mock the Wails bridge so we can control
// exactly when each OpenSource() call resolves, relative to the other.
// CountMatches is mocked now (E3 Task 5 needs it) even though Task 4 does not
// call it, so Task 5 needs no re-mock.
vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
  CountMatches: vi.fn(),
  ExportQuery: vi.fn(),
}));

// E4: store.ts subscribes to shape:progress per export. The real module reaches
// for window.runtime, which exists in neither the node nor the jsdom test
// environment, so the bridge is mocked here the same way App is. `emitProgress`
// lets a test deliver an event to whatever callback runExport registered.
let progressHandlers: ((p: any) => void)[] = [];
vi.mock("../../../wailsjs/runtime", () => ({
  EventsOn: vi.fn(),
}));
function emitProgress(payload: any): void {
  for (const h of progressHandlers) h(payload);
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const emptyRowSet = (): any => ({
  columns: [], rows: [], offset: 0, total: 0, totalExact: true,
  scanned: 0, truncated: true, elapsedMs: 0, columnsTruncated: false, totalPaths: 0,
});

// wideColumns builds n base columns. 300 is not arbitrary: pageRowsFor clamps
// to PAGE_ROWS_MAX (200) for every count <= 150, so a narrower fixture cannot
// tell a stale column count from a fresh one (the same trap Explorer.test.ts
// documents at its own paging assertions).
const wideColumns = (n: number): Column[] =>
  Array.from({ length: n }, (_, i) => ({
    path: `c${i}`, name: `c${i}`, type: "string",
    nullable: false, presence: 1, distinct: 1, container: false, index: i,
  })) as Column[];

const openResult = (handle: string, tier = "memory", columns: Column[] = []): any => ({
  handle, format: "json", tier, columns,
  profile: { records: 0, skipped: 0, fields: [] },
  sampled: false, rowEstimate: 0, rowExact: true, warnings: [],
  columnsTruncated: false, totalPaths: 0,
});

beforeEach(async () => {
  vi.mocked(OpenSource).mockReset();
  vi.mocked(QueryRows).mockReset().mockResolvedValue(emptyRowSet());
  vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
  vi.mocked(Cancel).mockReset().mockResolvedValue(undefined as any);
  vi.mocked(CountMatches).mockReset(); // E3 Task 5: each test sets its own resolution explicitly
  vi.mocked(ExportQuery).mockReset();
  progressHandlers = [];
  vi.mocked(EventsOn).mockReset().mockImplementation((_evt: string, cb: any) => {
    progressHandlers.push(cb);
    return () => {
      progressHandlers = progressHandlers.filter((h) => h !== cb);
    };
  });
  await explorer.close();
  vi.mocked(CloseSource).mockClear(); // don't count close()'s own no-op call
});

describe("open() generation guard (C1)", () => {
  it("does not let a slow, superseded open() overwrite a newer file's state, and closes its handle", async () => {
    const a = deferred<any>();
    const b = deferred<any>();
    vi.mocked(OpenSource).mockImplementationOnce(() => a.promise).mockImplementationOnce(() => b.promise);

    const openA = explorer.open("fileA"); // gen -> 1, OpenSource("fileA") pending
    const openB = explorer.open("fileB"); // gen -> 2; prev.handle === "" here, so A is NOT closed by open(B)

    // I-1: each open() must send its own gen, not a constant "", as
    // requestId -- that's what lets Go's resolveOpenSeq key ordering off the
    // client's already-correctly-ordered counter instead of off Go-side
    // goroutine arrival order. A regression back to requestId: "" would not
    // be caught by this suite's other assertions (the mocked OpenSource here
    // ignores its argument entirely), only by inspecting the calls directly.
    // `gen` is module-level state that this describe block's earlier tests
    // (in file execution order) may have already advanced -- close() bumps
    // it but never resets it to 0 -- so this asserts the shape and the
    // relative step (B's gen is exactly one more than A's), not literal
    // "open1"/"open2" values.
    const reqIdA = (vi.mocked(OpenSource).mock.calls[0][0] as any).requestId as string;
    const reqIdB = (vi.mocked(OpenSource).mock.calls[1][0] as any).requestId as string;
    expect(reqIdA).toMatch(/^open\d+$/);
    expect(reqIdB).toMatch(/^open\d+$/);
    expect(Number(reqIdB.slice("open".length))).toBe(Number(reqIdA.slice("open".length)) + 1);

    b.resolve(openResult("handle-B"));
    await openB;
    expect(get(explorer).path).toBe("fileB");
    expect(get(explorer).handle).toBe("handle-B");

    a.resolve(openResult("handle-A")); // A lands late
    await openA;

    const s = get(explorer);
    // Must still reflect B -- A's late landing must not clobber it.
    expect(s.path).toBe("fileB");
    expect(s.handle).toBe("handle-B");
    expect(s.status).toBe("ready");

    // A's now-superseded handle must be closed by the store, or it leaks.
    expect(vi.mocked(CloseSource)).toHaveBeenCalledWith("handle-A");
  });

  it("does not let an older open()'s failure mark a healthy newer file as errored", async () => {
    const a = deferred<any>();
    const b = deferred<any>();
    vi.mocked(OpenSource).mockImplementationOnce(() => a.promise).mockImplementationOnce(() => b.promise);

    const openA = explorer.open("fileA");
    const openB = explorer.open("fileB");

    b.resolve(openResult("handle-B"));
    await openB;

    a.reject(new Error("boom"));
    await openA;

    const s = get(explorer);
    expect(s.status).toBe("ready"); // must NOT be clobbered to "error" by A's failure
    expect(s.path).toBe("fileB");
    expect(s.handle).toBe("handle-B");
  });
});

describe("mid-scroll page-fetch failure is non-destructive (A5)", () => {
  it("sets pageError without flipping status to 'error', and leaves the already-cached page readable", async () => {
    vi.mocked(OpenSource).mockResolvedValueOnce(openResult("handle-1"));
    // open()'s own ensurePages(0, 0) succeeds and lands one real row.
    vi.mocked(QueryRows).mockResolvedValueOnce({
      columns: [], rows: [{ index: 0, cells: [] }], offset: 0, total: -1, totalExact: false,
      scanned: 1, truncated: false, elapsedMs: 0, columnsTruncated: false, totalPaths: 0,
    } as any);

    await explorer.open("file.ndjson");
    expect(get(explorer).status).toBe("ready");
    expect(explorer.rowAt(0).row).not.toBeNull(); // sanity: page 0 really landed

    // A later, mid-scroll fetch for a DIFFERENT page (pageRowsFor(0) === 200,
    // so row 300 is page 1) fails.
    vi.mocked(QueryRows).mockRejectedValueOnce(new Error("boom: backend closed"));
    await explorer.ensurePages(300, 300);

    const s = get(explorer);
    // The one thing this finding requires: no full-pane error takeover.
    expect(s.status).toBe("ready");
    expect(s.error).toBe("");
    expect(s.handle).toBe("handle-1"); // unchanged -- CloseSource was never called
    expect(s.pageError).toContain("boom: backend closed");
    // The already-landed page must still be there -- a mid-scroll failure on
    // a DIFFERENT page must not evict or clear anything already fetched.
    expect(explorer.rowAt(0).row).not.toBeNull();
  });

  it("retryPageError() clears the bar and re-requests the same range that failed", async () => {
    vi.mocked(OpenSource).mockResolvedValueOnce(openResult("handle-2"));
    vi.mocked(QueryRows).mockResolvedValueOnce(emptyRowSet());
    await explorer.open("file2.ndjson");

    vi.mocked(QueryRows).mockRejectedValueOnce(new Error("transient"));
    await explorer.ensurePages(300, 300);
    expect(get(explorer).pageError).toContain("transient");

    const callsBefore = vi.mocked(QueryRows).mock.calls.length;
    // pageRowsFor(0) === 200, so row 300 is offset 100 within page 1
    // ([200, 400)) -- the landed row must sit at array position 100 to be
    // the one rowAt(300) actually reads.
    const page1Rows = Array.from({ length: 101 }, (_, i) => ({ index: 200 + i, cells: [] }));
    vi.mocked(QueryRows).mockResolvedValueOnce({
      columns: [], rows: page1Rows, offset: 200, total: -1, totalExact: false,
      scanned: 101, truncated: false, elapsedMs: 0, columnsTruncated: false, totalPaths: 0,
    } as any);
    await explorer.retryPageError();

    expect(get(explorer).pageError).toBe(""); // bar cleared
    expect(vi.mocked(QueryRows).mock.calls.length).toBe(callsBefore + 1); // re-requested, not just dismissed
    expect(explorer.rowAt(300).row).not.toBeNull(); // and the retry actually landed the row
  });

  it("dismissPageError() clears the bar without re-fetching", async () => {
    vi.mocked(OpenSource).mockResolvedValueOnce(openResult("handle-3"));
    vi.mocked(QueryRows).mockResolvedValueOnce(emptyRowSet());
    await explorer.open("file3.ndjson");

    vi.mocked(QueryRows).mockRejectedValueOnce(new Error("transient"));
    await explorer.ensurePages(300, 300);
    expect(get(explorer).pageError).toContain("transient");

    const callsBefore = vi.mocked(QueryRows).mock.calls.length;
    explorer.dismissPageError();
    expect(get(explorer).pageError).toBe("");
    expect(vi.mocked(QueryRows).mock.calls.length).toBe(callsBefore); // no new fetch
  });
});

// E3 Task 4: the store threads a live Filter into QueryRows and resets
// total/version/resetToken on every filter change. Recon GAPs 2 (stale
// in-flight old-filter page must not land in the new filter's cache slot),
// 3 (stale unfiltered total must not be shown as the filtered count), and 9
// (DataTable needs a signal to scroll back to row 0).
describe("setFilter (E3 Task 4)", () => {
  // wailsjs/go/models's Filter/Condition/Value are TS classes (they carry a
  // convertValues() decoding method), so a plain object literal can't
  // structurally satisfy the type -- same `as unknown as Filter` idiom
  // filterModel.ts already uses for buildFilter's return.
  const filterAge18: Filter = {
    combinator: "and",
    conditions: [{ path: "age", op: "gte", value: { kind: "number", num: 18 } }],
  } as unknown as Filter;

  const matchAll = { combinator: "and" } as unknown as Filter;

  const flush = () => new Promise<void>((r) => setTimeout(r, 0));

  /** Opens a memory-tier file whose page 0 lands with an exact unfiltered
   *  total, so setFilter's total-reset has something stale to overwrite. */
  async function openReady(handle = "handle-f"): Promise<void> {
    vi.mocked(OpenSource).mockResolvedValueOnce(openResult(handle));
    vi.mocked(QueryRows).mockResolvedValueOnce({
      columns: [], rows: [{ index: 0, cells: [] }], offset: 0, total: 100, totalExact: true,
      scanned: 1, truncated: false, elapsedMs: 0, columnsTruncated: false, totalPaths: 0,
    } as any);
    await explorer.open("file.ndjson");
  }

  it("sends the current filter to QueryRows", async () => {
    await openReady();
    const callsBefore = vi.mocked(QueryRows).mock.calls.length;
    vi.mocked(QueryRows).mockResolvedValueOnce({
      columns: [], rows: [], offset: 0, total: 3, totalExact: true,
      scanned: 0, truncated: true, elapsedMs: 0, columnsTruncated: false, totalPaths: 0,
    } as any);

    explorer.setFilter(filterAge18);
    await vi.waitFor(() => expect(vi.mocked(QueryRows).mock.calls.length).toBeGreaterThan(callsBefore));

    const calls = vi.mocked(QueryRows).mock.calls;
    const lastArgs = calls[calls.length - 1][0] as any;
    expect(lastArgs.filter).toEqual(filterAge18);
  });

  it("bumps resetToken so DataTable can scroll back to row 0", async () => {
    await openReady();
    const before = get(explorer).resetToken;
    explorer.setFilter(filterAge18);
    expect(get(explorer).resetToken).toBe(before + 1);
    await flush();
  });

  it("sets filterActive true for a non-empty filter, false for match-all", async () => {
    await openReady();
    explorer.setFilter(filterAge18);
    expect(get(explorer).filterActive).toBe(true);
    explorer.setFilter(matchAll);
    expect(get(explorer).filterActive).toBe(false);
    await flush();
  });

  it("resets total/totalExact/version synchronously, before any refetch resolves", async () => {
    await openReady();
    expect(get(explorer).total).toBe(100); // sanity: stale unfiltered total pre-setFilter

    const gate = deferred<any>();
    vi.mocked(QueryRows).mockImplementationOnce(() => gate.promise);
    explorer.setFilter(filterAge18);

    const s = get(explorer);
    expect(s.total).toBe(-1);
    expect(s.totalExact).toBe(false);
    expect(s.version).toBe(0);

    gate.resolve(emptyRowSet()); // let the pending fetch settle so it can't leak into later tests
    await flush();
  });

  // T10 fix: on a non-memory tier, QueryRows is always called with
  // wantTotal:false (ensurePages), and every non-memory Backend.Query
  // (rescan/sqlite/parquet) returns Total:-1 for that case -- so once a
  // filter is CLEARED back to match-all, the only thing left to feed
  // `total` was reconcileEof's page-boundary guess, which starts small and
  // never climbs back to the file's real rowEstimate on its own. A clear
  // must restore the file-level baseline captured at open() instead of
  // resetting to -1 unconditionally (the way an ACTIVE filter correctly
  // does, per the test above -- its own stale total must not leak through
  // either, just from a different, already-covered direction).
  it("clearing a filter restores the file's rowEstimate synchronously, instead of leaving total at -1 forever (T10)", async () => {
    vi.mocked(OpenSource).mockResolvedValueOnce({
      ...openResult("handle-clear", "rescan"), rowEstimate: 726181, rowExact: false, sampled: true,
    } as any);
    // Mirrors a real non-memory tier's QueryRows(wantTotal:false): Total:-1
    // always (rescan.go/sqlbackend.go/parquetbackend.go), a non-truncated
    // page, so reconcileEof leaves the just-set rowEstimate alone rather than
    // overwriting it with emptyRowSet()'s totalExact:true stand-in.
    vi.mocked(QueryRows).mockResolvedValueOnce({
      columns: [], rows: [{ index: 0, cells: [] }], offset: 0, total: -1, totalExact: false,
      scanned: 1, truncated: false, elapsedMs: 0, columnsTruncated: false, totalPaths: 0,
    } as any);
    await explorer.open("large.ndjson");
    expect(get(explorer).total).toBe(726181); // sanity: the file-level estimate landed on open
    expect(get(explorer).totalExact).toBe(false);

    // A real filter -- total correctly drops to -1 synchronously (existing
    // behavior, already pinned by the test above). Checked without awaiting
    // anything so this filter's own page-0 refetch (left on the default
    // emptyRowSet() mock) cannot have resolved yet and clobbered it.
    explorer.setFilter(filterAge18);
    expect(get(explorer).total).toBe(-1);

    const gate = deferred<any>();
    vi.mocked(QueryRows).mockImplementationOnce(() => gate.promise); // gate the clear's own page-0 refetch
    explorer.setFilter(matchAll); // clear back to empty

    const s = get(explorer); // checked BEFORE the gated refetch resolves, same as the test above
    expect(s.total).toBe(726181); // must be restored, not stuck at -1 or some tiny page-based guess
    expect(s.totalExact).toBe(false);

    gate.resolve(emptyRowSet());
    await flush();
  });

  it("GAP-2: an in-flight old-filter page cannot land in the new filter's cache slot", async () => {
    // Both the "old" (match-all) and "new" (filtered) fetch must target the
    // SAME page (0), or this test cannot distinguish a correctly-guarded
    // store from a broken one (different cache slots never collide anyway).
    // So the old fetch has to be open()'s own initial page-0 fetch, gated,
    // rather than a page openReady() would have already landed and cached.
    vi.mocked(OpenSource).mockResolvedValueOnce(openResult("handle-gap2"));
    const oldGate = deferred<any>();
    vi.mocked(QueryRows).mockImplementationOnce(() => oldGate.promise);
    const openPromise = explorer.open("gap2.ndjson");
    await flush(); // let open() reach 'ready' and issue its own page-0 fetch (now pending on oldGate)
    expect(get(explorer).status).toBe("ready");

    const newGate = deferred<any>();
    vi.mocked(QueryRows).mockImplementationOnce(() => newGate.promise);
    explorer.setFilter(filterAge18); // bumps gen, cancels+clears inflight, clears cache, starts a new page-0 fetch

    // Resolve the OLD fetch AFTER setFilter, with distinctive rows for page 0.
    oldGate.resolve({
      columns: [], rows: [{ index: 0, cells: [{ kind: "string", str: "OLD-STALE" }] }], offset: 0,
      total: 999, totalExact: true, scanned: 1, truncated: false, elapsedMs: 0,
      columnsTruncated: false, totalPaths: 0,
    } as any);
    await flush();

    expect(explorer.rowAt(0).row).toBeNull(); // the stale old-filter row must never become visible
    expect(get(explorer).total).not.toBe(999);

    newGate.resolve(emptyRowSet());
    await flush();
    await openPromise;
  });

  it("open() resets currentFilter to match-all for the next file", async () => {
    await openReady();
    explorer.setFilter(filterAge18);
    await flush();
    expect(get(explorer).filterActive).toBe(true);

    vi.mocked(OpenSource).mockResolvedValueOnce(openResult("handle-g"));
    vi.mocked(QueryRows).mockResolvedValueOnce(emptyRowSet());
    const callsBefore = vi.mocked(QueryRows).mock.calls.length;
    await explorer.open("file2.ndjson");

    const calls = vi.mocked(QueryRows).mock.calls;
    expect(calls.length).toBeGreaterThan(callsBefore);
    const args = calls[callsBefore][0] as any;
    expect(args.filter).toEqual({ combinator: "and" });
  });
});

// E3 Task 5: on rescan/sqlite/parquet, a filtered QueryRows(wantTotal:false)
// returns Total:-1, so CountMatches is the only eager, exact source -- a full
// residual scan, cancellable via the engine's per-RequestID registry. On the
// memory tier, QueryRows already returns the exact filtered total on page 0,
// so a count there would be a redundant full re-scan -- setFilter must skip
// it. The count carries its own supersession id (countReqId) PLUS the same
// `gen` guard as everything else, because a slow count for one filter must
// never overwrite a different filter's (or the cleared, unfiltered) state.
describe("live filtered count via CountMatches (E3 Task 5)", () => {
  const filterAge18: Filter = {
    combinator: "and",
    conditions: [{ path: "age", op: "gte", value: { kind: "number", num: 18 } }],
  } as unknown as Filter;

  const filterAge21: Filter = {
    combinator: "and",
    conditions: [{ path: "age", op: "gte", value: { kind: "number", num: 21 } }],
  } as unknown as Filter;

  const matchAll = { combinator: "and" } as unknown as Filter;

  const flush = () => new Promise<void>((r) => setTimeout(r, 0));

  async function openReadyTier(tier: string, handle = "handle-count"): Promise<void> {
    vi.mocked(OpenSource).mockResolvedValueOnce(openResult(handle, tier));
    vi.mocked(QueryRows).mockResolvedValueOnce(emptyRowSet());
    await explorer.open("file.ndjson");
  }

  it("rescan tier: setFilter(nonEmpty) calls CountMatches once with the filter and adopts the result", async () => {
    await openReadyTier("rescan");
    const gate = deferred<any>();
    vi.mocked(CountMatches).mockImplementationOnce(() => gate.promise);

    explorer.setFilter(filterAge18);
    expect(vi.mocked(CountMatches).mock.calls.length).toBe(1);
    const args = vi.mocked(CountMatches).mock.calls[0][0] as any;
    expect(args.filter).toEqual(filterAge18);

    gate.resolve({ total: 42, exact: true, elapsedMs: 0 });
    await flush();

    const s = get(explorer);
    expect(s.matchCount).toBe(42);
    expect(s.matchExact).toBe(true);
    expect(s.counting).toBe(false);
    expect(s.total).toBe(42);
    expect(s.totalExact).toBe(true);
  });

  // Mutation-proof: dropping the `s.tier !== "memory"` guard in setFilter
  // makes this fail (CountMatches gets called).
  it("memory tier: setFilter(nonEmpty) does NOT call CountMatches", async () => {
    await openReadyTier("memory");

    explorer.setFilter(filterAge18);
    await flush();

    expect(vi.mocked(CountMatches).mock.calls.length).toBe(0);
  });

  it("counting is true synchronously after setFilter on a rescan tier, and false once it resolves", async () => {
    await openReadyTier("rescan");
    const gate = deferred<any>();
    vi.mocked(CountMatches).mockImplementationOnce(() => gate.promise);

    explorer.setFilter(filterAge18);
    expect(get(explorer).counting).toBe(true);

    gate.resolve({ total: 1, exact: true, elapsedMs: 0 });
    await flush();
    expect(get(explorer).counting).toBe(false);
  });

  // This pins GEN-supersession, not the countReqId guard: setFilter bumps gen
  // per call, so filter A (genAtStart N+1) and filter B (gen N+2) live in
  // different generations, and A's late resolve is rejected by
  // `genAtStart !== gen` on its own. Removing the countReqId disjunct leaves
  // this green -- countReqId's uniquely load-bearing case (a late resolve
  // AFTER cancelCount, which nulls countReqId WITHOUT bumping gen) is pinned
  // separately in the cancelCount test below.
  it("count supersession: a slow filter-A count must not overwrite filter-B's later count", async () => {
    await openReadyTier("rescan");

    const gateA = deferred<any>();
    vi.mocked(CountMatches).mockImplementationOnce(() => gateA.promise);
    explorer.setFilter(filterAge18); // filter A, count A pending

    const gateB = deferred<any>();
    vi.mocked(CountMatches).mockImplementationOnce(() => gateB.promise);
    explorer.setFilter(filterAge21); // filter B, count B pending; supersedes A

    gateB.resolve({ total: 2, exact: true, elapsedMs: 0 });
    await flush();
    expect(get(explorer).matchCount).toBe(2);

    gateA.resolve({ total: 1, exact: true, elapsedMs: 0 }); // A lands late
    await flush();
    expect(get(explorer).matchCount).toBe(2); // must not flicker to / land on 1
  });

  it("cancelCount() while counting calls Cancel(countReqId) and sets counting:false", async () => {
    await openReadyTier("rescan");
    const gate = deferred<any>();
    vi.mocked(CountMatches).mockImplementationOnce(() => gate.promise);

    explorer.setFilter(filterAge18);
    expect(get(explorer).counting).toBe(true);
    const reqId = (vi.mocked(CountMatches).mock.calls[0][0] as any).requestId as string;

    explorer.cancelCount();

    expect(vi.mocked(Cancel)).toHaveBeenCalledWith(reqId);
    expect(get(explorer).counting).toBe(false);

    // The uniquely load-bearing case for the `countReqId` guard: cancelCount
    // nulls countReqId WITHOUT bumping gen, so this late resolve has
    // genAtStart === gen and is rejected SOLELY by countReqId(null) !== reqId.
    // Removing that guard would let a cancelled count land here.
    gate.resolve({ total: 1, exact: true, elapsedMs: 0 });
    await flush();
    expect(get(explorer).matchCount).toBe(-1); // the cancelled count must NOT land
    expect(get(explorer).counting).toBe(false);
  });

  it("setFilter back to an empty filter clears matchCount to -1 and does not start a count", async () => {
    await openReadyTier("rescan");
    vi.mocked(CountMatches).mockResolvedValueOnce({ total: 42, exact: true, elapsedMs: 0 } as any);
    explorer.setFilter(filterAge18);
    await flush();
    expect(get(explorer).matchCount).toBe(42);

    const callsBefore = vi.mocked(CountMatches).mock.calls.length;
    explorer.setFilter(matchAll);
    await flush();

    expect(get(explorer).matchCount).toBe(-1);
    expect(get(explorer).matchExact).toBe(false);
    expect(vi.mocked(CountMatches).mock.calls.length).toBe(callsBefore); // no new count started
  });

  // The regression the plan review flagged as untested: a filter cleared to
  // empty starts no new count, so without nulling countReqId on the clear
  // path a late-resolving stale count from A could still land. NOTE: with
  // countReqId correctly nulled (as setFilter does), that check alone already
  // rejects this specific case -- verified by deliberately removing the
  // `genAtStart !== gen` half of startCount's guard and re-running this test:
  // it still passed. genAtStart is kept as belt-and-suspenders (see its doc
  // comment in store.ts) but is not independently exercised by this test.
  it("cleared-then-late-count: a stale count from filter A must not land after clearing to empty", async () => {
    await openReadyTier("rescan");
    const gateA = deferred<any>();
    vi.mocked(CountMatches).mockImplementationOnce(() => gateA.promise);
    explorer.setFilter(filterAge18); // filter A, count pending

    explorer.setFilter(matchAll); // clear -- no new count starts, countReqId nulled
    await flush();

    gateA.resolve({ total: 1, exact: true, elapsedMs: 0 }); // A's stale count lands late
    await flush();

    const s = get(explorer);
    expect(s.matchCount).toBe(-1);
    expect(s.total).not.toBe(1);
  });
});

// --- E4 Task 8: transform threading ------------------------------------------

describe("setTransform", () => {
  async function openWide(n: number): Promise<Column[]> {
    const cols = wideColumns(n);
    vi.mocked(OpenSource).mockResolvedValue(openResult("h1", "memory", cols));
    await explorer.open("/wide.ndjson");
    return cols;
  }

  it("sends the live transform on every QueryRows, not a hardcoded {}", async () => {
    const cols = await openWide(3);
    const projected = [{ ...cols[0] }];
    const transform = { select: [{ path: "c0", as: "c0" }] } as any;

    vi.mocked(QueryRows).mockClear();
    explorer.setTransform(transform, projected);
    await Promise.resolve();
    await Promise.resolve();

    const sent = vi.mocked(QueryRows).mock.calls.at(-1)?.[0] as any;
    expect(sent.transform).toEqual(transform);
  });

  it("adopts the projected columns SYNCHRONOUSLY, so page arithmetic cannot desync", async () => {
    const cols = await openWide(300); // pageRowsFor(300) === 100
    const openLimit = (vi.mocked(QueryRows).mock.calls.at(-1)?.[0] as any).limit;
    expect(openLimit).toBe(100); // precondition: the wide fixture really does page at 100

    vi.mocked(QueryRows).mockClear();
    explorer.setTransform({ select: [{ path: "c0", as: "c0" }] } as any, [{ ...cols[0] }]);

    // Synchronous, before any fetch resolves.
    expect(get(explorer).columns).toHaveLength(1);
    expect(get(explorer).baseColumns).toHaveLength(300); // base set is untouched
    expect(get(explorer).transformActive).toBe(true);

    await Promise.resolve();
    await Promise.resolve();
    const sent = vi.mocked(QueryRows).mock.calls.at(-1)?.[0] as any;
    // pageRowsFor(1) clamps to PAGE_ROWS_MAX; a stale 300-column count would
    // still be paging at 100.
    expect(sent.limit).toBe(200);
    expect(sent.offset).toBe(0);
  });

  it("does not disturb the row count: a projection changes columns, not matches", async () => {
    // The count must already be established when the projection is applied, so
    // seed it on open()'s own page-0 fetch (page 0 is cached afterwards, and a
    // second ensurePages for the same range is a no-op).
    vi.mocked(QueryRows).mockResolvedValue({ ...emptyRowSet(), total: 999, totalExact: true });
    const cols = await openWide(3);
    expect(get(explorer).total).toBe(999);

    explorer.setTransform({ select: [{ path: "c0", as: "c0" }] } as any, [{ ...cols[0] }]);
    expect(get(explorer).total).toBe(999);
  });

  it("bumps resetToken so the table scrolls back to row 0", async () => {
    const cols = await openWide(3);
    const before = get(explorer).resetToken;
    explorer.setTransform({ select: [{ path: "c0", as: "c0" }] } as any, [{ ...cols[0] }]);
    expect(get(explorer).resetToken).toBe(before + 1);
  });

  it("never lets a pre-transform page land in the new projection's slot", async () => {
    const cols = await openWide(3);
    const stale = deferred<any>();
    vi.mocked(QueryRows).mockReturnValueOnce(stale.promise as any);
    void explorer.ensurePages(0, 0); // page 0 in flight under the OLD projection

    explorer.setTransform({ select: [{ path: "c0", as: "c0" }] } as any, [{ ...cols[0] }]);

    // The old fetch resolves LATE, carrying a distinctive row.
    stale.resolve({
      ...emptyRowSet(), total: 1, rows: [{ index: 0, cells: [{ kind: "string", str: "STALE" }] }],
    });
    await Promise.resolve();
    await Promise.resolve();

    const row = explorer.rowAt(0).row as any;
    expect(row?.cells?.[0]?.str).not.toBe("STALE");
  });

  it("is reset by open(), so the next file starts unprojected", async () => {
    const cols = await openWide(3);
    explorer.setTransform({ select: [{ path: "c0", as: "c0" }] } as any, [{ ...cols[0] }]);

    vi.mocked(QueryRows).mockClear();
    vi.mocked(OpenSource).mockResolvedValue(openResult("h2", "memory", wideColumns(2)));
    await explorer.open("/other.ndjson");

    const sent = vi.mocked(QueryRows).mock.calls.at(-1)?.[0] as any;
    expect(sent.transform).toEqual({});
    expect(get(explorer).transformActive).toBe(false);
  });

  it("leaves an in-flight count alone (countGen, not gen)", async () => {
    // On a rescan tier a filter starts a CountMatches; applying a projection
    // while it is in flight must not strand it at "counting...".
    vi.mocked(OpenSource).mockResolvedValue(openResult("h1", "rescan", wideColumns(3)));
    await explorer.open("/big.ndjson");
    const gate = deferred<any>();
    vi.mocked(CountMatches).mockReturnValueOnce(gate.promise as any);

    explorer.setFilter({ combinator: "and", conditions: [{ path: "c0", op: "notnull" }] } as any);
    expect(get(explorer).counting).toBe(true);

    explorer.setTransform({ select: [{ path: "c0", as: "c0" }] } as any, [wideColumns(1)[0]]);
    gate.resolve({ total: 42, exact: true, elapsedMs: 1 });
    await gate.promise;
    await Promise.resolve();
    await Promise.resolve();

    expect(get(explorer).counting).toBe(false);
    expect(get(explorer).matchCount).toBe(42);
  });
});

// --- E4 Task 8: export lifecycle ---------------------------------------------

describe("runExport", () => {
  async function openReady(): Promise<void> {
    vi.mocked(OpenSource).mockResolvedValue(openResult("h1", "memory", wideColumns(2)));
    await explorer.open("/f.ndjson");
  }

  it("sends the current filter and transform, and records the result", async () => {
    await openReady();
    explorer.setFilter({ combinator: "and", conditions: [{ path: "c0", op: "notnull" }] } as any);
    vi.mocked(ExportQuery).mockResolvedValue({
      outPath: "/out.csv", rowsOut: 7, bytesOut: 42, elapsedMs: 3, warnings: [],
    } as any);

    await explorer.runExport("csv", "/out.csv");

    const sent = vi.mocked(ExportQuery).mock.calls.at(-1)?.[0] as any;
    expect(sent.format).toBe("csv");
    expect(sent.outPath).toBe("/out.csv");
    expect(sent.filter.conditions).toHaveLength(1);
    expect(get(explorer).exportResult?.rowsOut).toBe(7);
    expect(get(explorer).exporting).toBe(false);
  });

  it("moves the progress counter only for ITS OWN requestId", async () => {
    await openReady();
    const gate = deferred<any>();
    vi.mocked(ExportQuery).mockReturnValueOnce(gate.promise as any);
    const done = explorer.runExport("ndjson", "/out.ndjson");

    const sentId = (vi.mocked(ExportQuery).mock.calls.at(-1)?.[0] as any).requestId;
    emitProgress({ requestId: "someone-else", scanned: 999, total: -1 });
    expect(get(explorer).exportRows).toBe(0);

    emitProgress({ requestId: sentId, scanned: 4096, total: -1 });
    expect(get(explorer).exportRows).toBe(4096);

    gate.resolve({ outPath: "/out.ndjson", rowsOut: 5000, bytesOut: 1, elapsedMs: 1 });
    await done;
    expect(get(explorer).exportRows).toBe(5000);
  });

  it("unsubscribes from progress once the export settles", async () => {
    await openReady();
    vi.mocked(ExportQuery).mockResolvedValue({ outPath: "/a", rowsOut: 1, bytesOut: 1, elapsedMs: 1 } as any);
    await explorer.runExport("csv", "/a");
    expect(progressHandlers).toHaveLength(0);

    await explorer.runExport("csv", "/b");
    expect(progressHandlers).toHaveLength(0);
  });

  it("records a failure without leaving `exporting` stuck", async () => {
    await openReady();
    vi.mocked(ExportQuery).mockRejectedValue(new Error("disk full"));
    await explorer.runExport("csv", "/out.csv");
    expect(get(explorer).exporting).toBe(false);
    expect(get(explorer).exportError).toContain("disk full");
  });
});

describe("cancelExport", () => {
  it("writes the terminal state synchronously and ignores the late settle", async () => {
    vi.mocked(OpenSource).mockResolvedValue(openResult("h1", "memory", wideColumns(2)));
    await explorer.open("/f.ndjson");
    const gate = deferred<any>();
    vi.mocked(ExportQuery).mockReturnValueOnce(gate.promise as any);
    const done = explorer.runExport("parquet", "/out.parquet");

    const sentId = (vi.mocked(ExportQuery).mock.calls.at(-1)?.[0] as any).requestId;
    explorer.cancelExport();

    // Synchronous: nulling exportReqId makes runExport's own guards reject its
    // catch/finally, so nothing else will ever write this state.
    expect(get(explorer).exporting).toBe(false);
    expect(get(explorer).exportError).toBe("cancelled");
    expect(vi.mocked(Cancel).mock.calls.some((c) => c[0] === sentId)).toBe(true);

    // The engine's promise settles LATE; it must change nothing.
    gate.resolve({ outPath: "/out.parquet", rowsOut: 12, bytesOut: 1, elapsedMs: 1 });
    await done;
    expect(get(explorer).exportResult).toBeNull();
    expect(get(explorer).exportError).toBe("cancelled");
  });

  it("does nothing when no export is in flight", async () => {
    vi.mocked(OpenSource).mockResolvedValue(openResult("h1", "memory", wideColumns(2)));
    await explorer.open("/f.ndjson");
    vi.mocked(ExportQuery).mockResolvedValue({ outPath: "/a", rowsOut: 3, bytesOut: 1, elapsedMs: 1 } as any);
    await explorer.runExport("csv", "/a");

    explorer.cancelExport(); // a late Esc on a FINISHED dialog
    expect(get(explorer).exportError).toBe("");
    expect(get(explorer).exportResult?.rowsOut).toBe(3);
  });
});
