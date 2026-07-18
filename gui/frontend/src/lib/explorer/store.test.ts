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
