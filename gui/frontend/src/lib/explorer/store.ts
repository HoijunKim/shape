import { writable, get } from "svelte/store";
import { OpenSource, QueryRows, CloseSource, Cancel, CountMatches, ExportQuery } from "../../../wailsjs/go/main/App";
import { EventsOn } from "../../../wailsjs/runtime";
import type { Column, CountResult, ExportResult, FieldDTO, Filter, OpenResult, RowSet, Transform } from "./types";
import { PageCache, pageRowsFor, pagesForRange, reconcileEof, rowLocation } from "./paging";

export type Status = "idle" | "opening" | "ready" | "error";

// wailsjs/go/models's Filter is a TS *class* (it carries a convertValues()
// decoding method for responses coming FROM Go), so its instance type demands
// that method on anything assigned to it -- a plain object literal can never
// structurally satisfy it. Same `as unknown as Filter` idiom filterModel.ts
// already uses for buildFilter's return.
function matchAllFilter(): Filter {
  return { combinator: "and" } as unknown as Filter;
}

/** The generated `Transform` is a TS *class* (it carries convertValues() for
 *  the Go->JS response path), so a plain object literal can never structurally
 *  satisfy it -- same idiom as matchAllFilter above and filterModel's
 *  buildFilter. The EMPTY object is the identity transform: no select, no drop,
 *  byte-identical to the request the explorer sent before E4. */
function identityTransform(): Transform {
  return {} as unknown as Transform;
}

export interface ExplorerState {
  status: Status;
  error: string;
  path: string;
  handle: string;
  tier: string;
  format: string;
  warnings: string[];
  fields: FieldDTO[];
  // baseColumns is the source's own column set, fixed for as long as the file
  // is open. `columns` is what the TABLE shows, which a transform reprojects.
  // The split matters: a filter runs on the RECORD, before projection, so the
  // filter bar and the structure map must keep addressing base paths even
  // while the table shows renamed/hidden ones.
  baseColumns: Column[];
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
  filterActive: boolean; // true when a non-empty Filter is applied (setFilter) -- lets
                          // the UI/empty-state distinguish "no rows in file" from
                          // "no rows match filter"
  resetToken: number;    // bumped on every filter change so DataTable (which owns
                          // scroll) knows to scroll back to row 0 -- the store cannot
                          // move the viewport itself
  // E3 Task 5: a CountMatches request is in flight (the "counting..." affordance).
  counting: boolean;
  matchCount: number;    // -1 = unknown; the exact filtered count once CountMatches lands
  matchExact: boolean;
  // E4: true when a non-identity transform is applied (columns hidden,
  // reordered or renamed), so the status bar can say "showing M of N columns".
  transformActive: boolean;
  // E4 export lifecycle. exportRows is the running count from the engine's
  // shape:progress events; exportResult/exportError are terminal states the
  // dialog renders until it is dismissed.
  exporting: boolean;
  exportRows: number;
  exportError: string;
  exportResult: ExportResult | null;
  // A5: a mid-scroll page-fetch failure (as opposed to an open()-time
  // failure, which still owns the whole pane via `status`/`error`) must be
  // non-destructive -- it must NOT discard an already-rendered grid or the
  // user's scroll position. "" = no page error outstanding; Explorer.svelte
  // renders this as a dismissible/retryable `role="alert"` bar ABOVE the
  // still-mounted DataTable, never in place of it.
  pageError: string;
}

const empty: ExplorerState = {
  status: "idle", error: "", path: "", handle: "", tier: "", format: "",
  warnings: [], fields: [], baseColumns: [], columns: [], columnsTruncated: false, totalPaths: 0,
  total: -1, totalExact: false, sampled: false, skipped: 0, focusPath: "", fetching: false, version: 0,
  pageError: "", filterActive: false, resetToken: 0,
  counting: false, matchCount: -1, matchExact: false,
  transformActive: false,
  exporting: false, exportRows: 0, exportError: "", exportResult: null,
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
  // E3: the filter currently applied to QueryRows, set via setFilter(). A new
  // file always starts unfiltered -- open()/close() reset this to match-all.
  let currentFilter: Filter = matchAllFilter();
  // E4: the projection currently applied to QueryRows, set via setTransform().
  let currentTransform: Transform = identityTransform();
  // E3 Task 5: the requestId of the CountMatches call currently in flight, or
  // null when none is. Its own supersession id -- separate from `gen` -- so a
  // count for filter A can be told apart from filter B's even when both are
  // in flight in the same generation's window; see startCount's guard.
  let countReqId: string | null = null;
  // E4: the count's OWN supersession generation, deliberately separate from
  // `gen`. startCount's guards reject a landing count whose generation moved,
  // and they return BEFORE writing counting:false -- so if the count keyed on
  // `gen`, setTransform's ++gen (which must supersede in-flight PAGES) would
  // strand a perfectly good count at "counting..." forever, with no writer left
  // to clear it. A projection changes which columns are shown, never which
  // records match, so it must not disturb a count at all. countGen is bumped
  // exactly where countReqId is reset: open(), close() and setFilter().
  let countGen = 0;
  // E4: the requestId of the export currently in flight, or null.
  let exportReqId: string | null = null;
  // A5: the most recent range passed to ensurePages(), so retryPageError()
  // can re-request the SAME range a failed fetch belonged to without
  // Explorer.svelte (which has no idea what row range DataTable last
  // scrolled to) needing to supply one.
  let lastFirst = 0;
  let lastLast = 0;
  // T10 fix: OpenSource's own rowEstimate/rowExact (the file-level total --
  // exact on memory/sqlite/parquet, a sampled estimate on rescan), kept
  // separately from the mutable `total`/`totalExact` state so setFilter can
  // restore it when a filter is CLEARED back to empty. Without this, clearing
  // a filter on a non-memory tier left `total` stuck at whatever tiny
  // page-based guess reconcileEof produced from the post-clear page-0 refetch
  // (e.g. "~400 rows" surviving a clear on a 726,181-row rescan-tier file) --
  // QueryRows is always called with wantTotal:false (ensurePages), and every
  // non-memory Backend.Query returns Total:-1 for that case (rescan.go/
  // sqlbackend.go/parquetbackend.go), so reconcileEof's page-boundary
  // fallback was the ONLY source left for `total`, and it never climbs back
  // to the true estimate on its own. On the memory tier this is a no-op:
  // memBackend.Query always returns the exact count regardless of wantTotal,
  // so the very next page-0 fetch overwrites whatever we restore here anyway.
  let baseTotal = -1;
  let baseTotalExact = false;

  async function open(path: string): Promise<void> {
    const prev = get({ subscribe });
    // Cancel BEFORE closing: cancelling ends the scan before its rename, while
    // closing first would kill the backend mid-export (sqlite/parquet) or --
    // worse, on the memory and rescan tiers, where Close is a no-op -- let it
    // run to completion and rename a file the user is no longer watching.
    if (exportReqId) { void Cancel(exportReqId).catch(() => {}); exportReqId = null; }
    if (prev.handle) { void CloseSource(prev.handle).catch(() => {}); }
    const myGen = ++gen; // C1: a fetch (here, this whole open()) from an older gen must not touch state
    cache.clear();
    inflight.clear();
    currentFilter = matchAllFilter(); // a new file always starts unfiltered
    currentTransform = identityTransform(); // ...and unprojected
    countReqId = null;
    countGen++;
    exportReqId = null;
    baseTotal = -1;
    baseTotalExact = false;
    set({ ...empty, status: "opening", path });
    try {
      // I-1: send a per-open unique, monotonically-increasing requestId
      // (`open${myGen}`) rather than "". Wails runs every binding call in its
      // own goroutine, so two overlapping OpenSource calls can race to reach
      // Go's own ordering bookkeeping in EITHER order -- completion order
      // alone (which the openSeq guard already handles) isn't the only place
      // that race can hide; the goroutines can also race for which one's
      // bookkeeping runs first, independent of which one JS actually issued
      // first. myGen is already correctly ordered (JS is single-threaded, so
      // it's incremented in real call order); encoding it into requestId
      // lets Go key its own ordering off that authoritative value instead of
      // off Go-side arrival order, closing that race at the source rather
      // than trying to out-guess goroutine scheduling. See gui/app.go's
      // resolveOpenSeq for the Go side of this fix.
      const res: OpenResult = await OpenSource({ path, format: "", table: "", csvRaw: false, budgetMB: 0, requestId: `open${myGen}` } as any);
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
      baseTotal = res.rowEstimate;
      baseTotalExact = res.rowExact;
      update((s) => ({
        ...s, status: "ready", handle: res.handle, tier: res.tier, format: res.format,
        warnings: res.warnings ?? [], fields: res.profile?.fields ?? [],
        baseColumns: res.columns ?? [], columns: res.columns ?? [],
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
    lastFirst = first;
    lastLast = last;
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
          requestId: reqId, handle: s.handle, filter: currentFilter, transform: currentTransform,
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
        //
        // A5: this is a MID-SCROLL page fetch, not the open()-time one -- by
        // the time we get here `status` is already "ready" and a grid may
        // already be fully rendered with the user scrolled deep into it.
        // Flipping `status` to "error" (as this used to) would have
        // Explorer.svelte replace the WHOLE pane with the full-screen error
        // state, discarding that rendered grid and the scroll position for
        // one failed page. Record the failure in `pageError` instead --
        // status/handle/columns/cache are all left exactly as they were, so
        // the table stays mounted and everything already fetched stays
        // visible; Explorer.svelte renders this as a dismissible/retryable
        // bar above it, never in place of it.
        if (myGen !== gen || inflight.get(page) !== reqId) return;
        update((st) => ({ ...st, pageError: String(e) }));
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

  /** A5: dismisses a mid-scroll page-fetch error bar without retrying. The
   *  page that failed is not cached and not in flight (the `finally` above
   *  always clears it), so it stays an unresolved skeleton until the user
   *  scrolls it back into view (which re-requests it the normal way) or
   *  calls retryPageError(). */
  function dismissPageError(): void {
    update((s) => ({ ...s, pageError: "" }));
  }

  /** A5: clears the error bar and re-requests the same row range the failed
   *  fetch belonged to (lastFirst/lastLast), so "Retry" tries again
   *  immediately rather than waiting for the user to scroll. Returns the
   *  underlying ensurePages() promise (rather than firing-and-forgetting it)
   *  so a caller that cares when the retry actually lands -- e.g. a test --
   *  can await it; Explorer.svelte's Retry button does not need to. */
  function retryPageError(): Promise<void> {
    update((s) => ({ ...s, pageError: "" }));
    return ensurePages(lastFirst, lastLast);
  }

  async function close(): Promise<void> {
    const s = get({ subscribe });
    if (exportReqId) { void Cancel(exportReqId).catch(() => {}); exportReqId = null; }
    if (s.handle) { await CloseSource(s.handle).catch(() => {}); }
    gen++;
    cache.clear(); inflight.clear();
    currentFilter = matchAllFilter();
    currentTransform = identityTransform();
    countReqId = null;
    countGen++;
    exportReqId = null;
    baseTotal = -1;
    baseTotalExact = false;
    set({ ...empty });
  }

  /** E3 Task 5: runs one CountMatches to finality for `filter`, superseded-safe
   *  via BOTH `countReqId` (a newer count, e.g. filter B's, must win over this
   *  one even within the same `gen`) and `genAtStart` (belt-and-suspenders: every
   *  gen-bumping call site -- open()/close()/setFilter(), including the
   *  cleared-to-empty path -- also resets `countReqId` synchronously in the same
   *  call, so `countReqId` alone already rejects every stale count reachable
   *  today; `genAtStart` guards the invariant itself, in case a future
   *  gen-bumping path is ever added that forgets to touch `countReqId`). The
   *  memory-tier skip is decided by the caller (setFilter), not here. */
  async function startCount(handle: string, filter: Filter, genAtStart: number): Promise<void> {
    const reqId = `c${++seq}`;
    countReqId = reqId;
    update((s) => ({ ...s, counting: true }));
    try {
      const res: CountResult = await CountMatches({ requestId: reqId, handle, filter } as any);
      if (countReqId !== reqId || genAtStart !== countGen) return; // superseded by a newer filter
      update((s) => ({
        ...s, matchCount: res.total, matchExact: res.exact, counting: false,
        total: res.total, totalExact: res.exact,
      }));
    } catch (e) {
      if (countReqId !== reqId || genAtStart !== countGen) return;
      // A cancelled or failed count is not a page error; just stop counting.
      update((s) => ({ ...s, counting: false }));
    } finally {
      if (countReqId === reqId) countReqId = null;
    }
  }

  /** E3 Task 5: the Cancel button behind the "counting..." affordance. */
  function cancelCount(): void {
    if (countReqId) { void Cancel(countReqId).catch(() => {}); countReqId = null; }
    countGen++;
    update((s) => ({ ...s, counting: false }));
  }

  /** E3: applies a live filter to the current file. Recon GAPs 2/3/9 -- see
   *  the inline comments below for why each step is required. */
  function setFilter(f: Filter): void {
    currentFilter = f;
    const active = !!(f.conditions && f.conditions.length > 0);
    // Bump gen so any in-flight OLD-filter QueryRows is superseded and cannot
    // cache.set() into the NEW filter's page slot (recon GAP 2). Cancel and
    // clear inflight the same way a superseding scroll does, and clear the
    // page cache so no old-filter rows survive.
    ++gen;
    for (const [, reqId] of inflight) { void Cancel(reqId).catch(() => {}); }
    inflight.clear();
    cache.clear();
    // E3 Task 5: cancel and null out any prior count the same way open()/
    // close() do. Do this even when no new count is about to start below (the
    // cleared-to-empty path) -- otherwise a still-set countReqId would let a
    // late-resolving stale count's own reqId match countReqId again, and only
    // startCount's genAtStart guard would be left to reject it.
    if (countReqId) { void Cancel(countReqId).catch(() => {}); countReqId = null; }
    countGen++; // E4: keep the invariant "countGen moves wherever countReqId is reset"
    update((s) => ({
      ...s,
      filterActive: active,
      // Reset the total so the stale unfiltered count is not shown as the
      // filtered count (recon GAP 3). On the memory tier page 0 immediately
      // re-fills it exactly; on other tiers it stays -1 (=> "counting...")
      // until Task 5's CountMatches finalizes it.
      //
      // T10 fix: a CLEARED filter (active === false) is not "counting"
      // anything -- it is just the file's own rows again -- so restore the
      // file-level baseTotal/baseTotalExact captured at open() rather than
      // -1. Without this, a non-memory tier's total got stuck at whatever
      // tiny page-based guess the post-clear page-0 refetch produced (every
      // non-memory Backend.Query returns Total:-1 for QueryRows'
      // wantTotal:false, so reconcileEof's page-boundary fallback was the
      // only thing left feeding `total`, and it never climbs back to the
      // true estimate on its own -- e.g. clearing a filter on a 726,181-row
      // rescan-tier file left the status bar reading "~400 rows" forever).
      total: active ? -1 : baseTotal,
      totalExact: active ? false : baseTotalExact,
      version: 0,
      resetToken: s.resetToken + 1, // DataTable scrolls to row 0 (recon GAP 9)
      pageError: "",
      matchCount: -1,
      matchExact: false,
      counting: false,
    }));
    void ensurePages(0, 0);
    const s = get({ subscribe });
    // E3 Task 5: CountMatches is the eager exact source only where QueryRows
    // can't already give one -- on the memory tier, filtered page 0 already
    // returns the exact total, so counting there would be a redundant full
    // re-scan. An empty filter needs no count at all (page 0/the unfiltered
    // seed already has it).
    if (active && s.tier !== "memory") {
      void startCount(s.handle, f, countGen);
    }
  }


  /** E4: applies a column projection. `projected` is what the table will show
   *  (transformModel.projectedColumns) and MUST be adopted synchronously here:
   *  ensurePages reads pageRowsFor(columns.length) BEFORE its fetch, so waiting
   *  for the first projected page to land would page the new projection with
   *  the old page size and desync every cached page from rowAt's arithmetic.
   *
   *  Structurally this is setFilter's supersede dance (bump gen, cancel and
   *  clear in-flight pages, drop the cache) minus everything about MATCHING: a
   *  projection changes which columns are shown, never which records match, so
   *  total/matchCount/counting are deliberately left alone. See countGen for
   *  why that is safe even with a count in flight. */
  function setTransform(t: Transform, projected: Column[]): void {
    currentTransform = t;
    // Key "active" on the artifact actually sent -- exactly the condition the
    // engine branches on for its truncation reporting (a non-empty Select).
    // Comparing projected-vs-base paths instead would call a pure rename, or a
    // reorder-plus-rename that restores the original path list, "inactive"
    // while a Select was still sent, and the status bar would quietly go back
    // to claiming an untruncated column count.
    const t2 = t as unknown as { select?: unknown[]; drop?: unknown[] };
    const active = (t2.select?.length ?? 0) > 0 || (t2.drop?.length ?? 0) > 0;
    ++gen;
    for (const [, reqId] of inflight) { void Cancel(reqId).catch(() => {}); }
    inflight.clear();
    cache.clear();
    update((s) => ({
      ...s,
      columns: projected,
      transformActive: active,
      version: 0,
      resetToken: s.resetToken + 1, // DataTable scrolls back to row 0
      pageError: "",
    }));
    void ensurePages(0, 0);
  }

  /** E4: exports the CURRENT filter + transform to outPath. The engine streams
   *  a fresh full pass, so this is never capped by the interactive tier.
   *
   *  The shape:progress subscription is per-export and torn down in the same
   *  finally that clears `exporting`. It must NOT be a module-level EventsOn:
   *  wailsjs/runtime reaches for window.runtime, which does not exist in the
   *  node test environment (store.test.ts) or in jsdom, so a module-scope call
   *  would throw while store.ts is being evaluated and take every test that
   *  imports it down with it. */
  async function runExport(format: string, outPath: string): Promise<void> {
    const s0 = get({ subscribe });
    if (!s0.handle) return;
    const reqId = `x${++seq}`;
    exportReqId = reqId;
    update((s) => ({ ...s, exporting: true, exportRows: 0, exportError: "", exportResult: null }));

    const off = EventsOn("shape:progress", (p: any) => {
      // A foreign or stale event must not move this export's counter.
      if (!p || p.requestId !== exportReqId) return;
      update((s) => (s.exporting ? { ...s, exportRows: Number(p.scanned) || 0 } : s));
    });

    try {
      const res: ExportResult = await ExportQuery({
        requestId: reqId, handle: s0.handle,
        filter: currentFilter, transform: currentTransform,
        format, outPath,
      } as any);
      // Guarded on exportReqId ALONE, deliberately not on `gen`. An export
      // owns a complete snapshot of its request (filter/transform were
      // serialised above), so a filter or projection change while it runs
      // does not invalidate the file it just wrote -- and a debounced
      // setFilter/setTransform can fire at any moment, including from a timer
      // the modal backdrop cannot block. Guarding on `gen` made both terminal
      // writes return early, leaving `exporting` true forever with no writer
      // left: a permanently stuck modal over an opaque backdrop, its result
      // silently discarded. exportReqId is the complete supersession key --
      // open(), close(), cancelExport() and a newer runExport all reset it.
      if (exportReqId !== reqId) return;
      update((s) => ({ ...s, exporting: false, exportResult: res, exportRows: res.rowsOut }));
    } catch (e) {
      if (exportReqId !== reqId) return;
      update((s) => ({ ...s, exporting: false, exportError: String(e) }));
    } finally {
      off();
      if (exportReqId === reqId) {
        exportReqId = null;
        // Belt-and-braces: whatever path we left by, this export is over, so
        // the spinner must not outlive it.
        update((s) => (s.exporting ? { ...s, exporting: false } : s));
      }
    }
  }

  /** E4: stops an in-flight export. The terminal state is written HERE and
   *  synchronously -- nulling exportReqId makes runExport's own guards reject
   *  its catch/finally, so waiting for the rejected promise would leave the
   *  dialog stuck on "exporting..." forever. Mirrors cancelCount above.
   *
   *  The early return matters: without it a late Esc would flip an
   *  already-finished dialog into a failed one. */
  function cancelExport(): void {
    if (!exportReqId) return;
    void Cancel(exportReqId).catch(() => {});
    exportReqId = null;
    update((s) => ({ ...s, exporting: false, exportError: "cancelled" }));
  }

  /** E4: clears a finished export's terminal state (the dialog's dismiss). */
  function dismissExport(): void {
    update((s) => ({ ...s, exportResult: null, exportError: "", exportRows: 0 }));
  }

  return {
    subscribe, open, ensurePages, rowAt, focus, close, dismissPageError, retryPageError,
    setFilter, cancelCount, setTransform, runExport, cancelExport, dismissExport,
  };
}

export const explorer = createExplorer();
