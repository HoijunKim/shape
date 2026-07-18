import { writable, get } from "svelte/store";
import { OpenSource, QueryRows, CloseSource, Cancel } from "../../../wailsjs/go/main/App";
import type { Column, FieldDTO, OpenResult, RowSet } from "./types";
import { PageCache, pageRowsFor, pagesForRange, reconcileEof, rowLocation } from "./paging";

export type Status = "idle" | "opening" | "ready" | "error";

export interface ExplorerState {
  status: Status;
  error: string;
  path: string;
  handle: string;
  tier: string;
  format: string;
  warnings: string[];
  fields: FieldDTO[];
  columns: Column[];
  columnsTruncated: boolean;
  totalPaths: number;
  total: number;        // -1 = unknown
  totalExact: boolean;
  sampled: boolean;
  skipped: number;      // ProfileDTO.skipped: malformed records the reader dropped (T8's zero-columns state)
  focusPath: string;    // sidebar-selected column path, "" = none
  fetching: boolean;
  version: number;      // bumps whenever a page lands, so views re-read the cache
}

const empty: ExplorerState = {
  status: "idle", error: "", path: "", handle: "", tier: "", format: "",
  warnings: [], fields: [], columns: [], columnsTruncated: false, totalPaths: 0,
  total: -1, totalExact: false, sampled: false, skipped: 0, focusPath: "", fetching: false, version: 0,
};

function createExplorer() {
  const { subscribe, set, update } = writable<ExplorerState>({ ...empty });
  // M5: both open() and close() reset these the same way (mutate via .clear(),
  // never reassign) so there is exactly one PageCache/Map instance for the
  // store's lifetime and no asymmetry between the two reset paths to trip on.
  const cache = new PageCache(8);
  const inflight = new Map<number, string>(); // page index -> requestId
  let seq = 0;
  let gen = 0; // bumps on open()/close(); a fetch from an older gen must not touch state

  async function open(path: string): Promise<void> {
    const prev = get({ subscribe });
    if (prev.handle) { void CloseSource(prev.handle).catch(() => {}); }
    const myGen = ++gen; // C1: a fetch (here, this whole open()) from an older gen must not touch state
    cache.clear();
    inflight.clear();
    set({ ...empty, status: "opening", path });
    try {
      const res: OpenResult = await OpenSource({ path, format: "", table: "", csvRaw: false, budgetMB: 0, requestId: "" } as any);
      if (myGen !== gen) {
        // A newer open()/close() landed while this OpenSource() call was in
        // flight -- e.g. the user opened file B before file A finished
        // opening. This result belongs to a file the user has already
        // navigated away from and must not overwrite B's state (nor `path`,
        // which already reads B). The backend handle this open() produced is
        // otherwise never closed -- the next open() only closes the CURRENT
        // state's handle, which by now belongs to someone else -- so close
        // it here or it leaks (up to a 512MiB in-memory store, or a live
        // sqlite connection).
        void CloseSource(res.handle).catch(() => {});
        return;
      }
      update((s) => ({
        ...s, status: "ready", handle: res.handle, tier: res.tier, format: res.format,
        warnings: res.warnings ?? [], fields: res.profile?.fields ?? [], columns: res.columns ?? [],
        columnsTruncated: res.columnsTruncated, totalPaths: res.totalPaths,
        total: res.rowEstimate, totalExact: res.rowExact, sampled: res.sampled,
        skipped: res.profile?.skipped ?? 0,
        focusPath: (res.columns && res.columns.length > 0) ? res.columns[0].path : "",
      }));
      await ensurePages(0, 0);
    } catch (e) {
      if (myGen !== gen) return; // superseded: a healthy newer file must not be marked errored by this one's failure
      update((s) => ({ ...s, status: "error", error: String(e) }));
    }
  }

  /** Fetches every page covering rows [first, last] that is not already cached
   *  or in flight. Pages already in flight are left alone; pages no longer
   *  needed are cancelled, so a fast scroll does not queue dead work. */
  async function ensurePages(first: number, last: number): Promise<void> {
    const s = get({ subscribe });
    if (s.status !== "ready" || !s.handle) return;
    const myGen = gen;
    const pageRows = pageRowsFor(s.columns.length);
    const wanted = pagesForRange(first, last, pageRows);

    for (const [page, reqId] of inflight) {
      if (!wanted.includes(page)) { void Cancel(reqId).catch(() => {}); inflight.delete(page); }
    }

    const todo = wanted.filter((p) => !cache.has(p) && !inflight.has(p));
    if (todo.length === 0) {
      // The cancellation loop above may have just drained `inflight` to empty
      // (every in-flight page was superseded and none of the wanted pages
      // needed a new fetch). Those cancelled requests' promises will settle
      // later and each checks `inflight.size === 0` in its own `finally`, but
      // that check is written against inflight AT THAT FUTURE TIME -- by then
      // a newer ensurePages() call could have added fresh entries, so it is
      // not guaranteed to catch this case. Without this, `fetching` can stay
      // stuck true past the point where nothing is actually outstanding.
      if (inflight.size === 0) update((st) => ({ ...st, fetching: false }));
      return;
    }
    update((st) => ({ ...st, fetching: true }));

    await Promise.all(todo.map(async (page) => {
      const reqId = `q${++seq}`;
      inflight.set(page, reqId);
      try {
        const rs: RowSet = await QueryRows({
          requestId: reqId, handle: s.handle, filter: {} as any, transform: {} as any,
          offset: page * pageRows, limit: pageRows, wantTotal: false,
        } as any);
        if (myGen !== gen || inflight.get(page) !== reqId) return; // superseded or stale file
        cache.set(page, rs);

        // EOF reconciliation (I2): a landed page can only ever IMPROVE the
        // store's total, never downgrade one the backend already gave us
        // exactly -- see reconcileEof's doc comment in paging.ts for why (an
        // overscan/prefetch past EOF also comes back truncated, and treating
        // that as new information would shrink a correct total into a
        // phantom one).
        update((st) => {
          const { total, totalExact } = reconcileEof({
            page, pageRows, rowsLength: rs.rows.length, truncated: rs.truncated,
            rsTotal: rs.total, rsTotalExact: rs.totalExact,
            priorTotal: st.total, priorTotalExact: st.totalExact,
          });
          return {
            ...st, total, totalExact,
            columnsTruncated: rs.columnsTruncated, totalPaths: rs.totalPaths,
            version: st.version + 1,
          };
        });
      } catch (e) {
        // A fetch belonging to a file the user has already navigated away from
        // must never write to the new file's state. CloseSource does NOT cancel
        // in-flight queries (engine.go: the handle is simply deleted), so such a
        // fetch fails with "unknown handle" or a backend-closed error, never
        // "context canceled" -- so a sentinel matching that string would never
        // catch the supersede case it might look like it's for (M6: removed;
        // this file used to have one). A genuine error here belongs to the
        // still-current file (the gen/reqId guard above already returned for
        // anything superseded), so surface it rather than swallowing it --
        // silently discarding it would leave this page's rows as permanent,
        // unexplained, unretriable skeletons.
        if (myGen !== gen || inflight.get(page) !== reqId) return;
        update((st) => ({ ...st, status: "error", error: String(e) }));
      } finally {
        if (inflight.get(page) === reqId) inflight.delete(page);
        if (myGen === gen && inflight.size === 0) update((st) => ({ ...st, fetching: false })); // M4
      }
    }));
  }

  function rowAt(index: number): { row: RowSet["rows"][number] | null } {
    const s = get({ subscribe });
    const pageRows = pageRowsFor(s.columns.length);
    const { page, offset } = rowLocation(index, pageRows);
    const rs = cache.get(page);
    if (!rs) return { row: null };
    return { row: rs.rows[offset] ?? null };
  }

  function focus(path: string): void { update((s) => ({ ...s, focusPath: path })); }

  async function close(): Promise<void> {
    const s = get({ subscribe });
    if (s.handle) { await CloseSource(s.handle).catch(() => {}); }
    gen++;
    cache.clear(); inflight.clear();
    set({ ...empty });
  }

  return { subscribe, open, ensurePages, rowAt, focus, close };
}

export const explorer = createExplorer();
