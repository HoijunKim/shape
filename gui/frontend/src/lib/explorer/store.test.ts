import { describe, it, expect, vi, beforeEach } from "vitest";
import { get } from "svelte/store";
import { explorer } from "./store";
import { OpenSource, QueryRows, CloseSource } from "../../../wailsjs/go/main/App";

// C1: a slow open() must not clobber a newer file's state, and it must not
// leak the backend handle it opened. Mock the Wails bridge so we can control
// exactly when each OpenSource() call resolves, relative to the other.
vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
}));

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

const openResult = (handle: string): any => ({
  handle, format: "json", tier: "memory", columns: [],
  profile: { records: 0, skipped: 0, fields: [] },
  sampled: false, rowEstimate: 0, rowExact: true, warnings: [],
  columnsTruncated: false, totalPaths: 0,
});

beforeEach(async () => {
  vi.mocked(OpenSource).mockReset();
  vi.mocked(QueryRows).mockReset().mockResolvedValue(emptyRowSet());
  vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
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
