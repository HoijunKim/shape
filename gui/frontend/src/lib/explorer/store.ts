import { writable, get } from "svelte/store";
import { OpenSource, QueryRows, CloseSource, Cancel, CountMatches, ExportQuery, Codegen, GetCell, SaveEdits, ColumnStats } from "../../../wailsjs/go/main/App";
import { EventsOn } from "../../../wailsjs/runtime";
import type { Column, CountResult, ExportResult, FieldCard, FieldDTO, Filter, Generated, OpenResult, Row, RowSet, SaveResult, SortSpec, Transform } from "./types";

// E7: one edited cell's value carried as kind + literal (a number keeps its
// exact source text -- a JS number would round a >2^53 integer, so it is never
// routed through Number()) plus a display string.
export interface EditValue {
  kind: string;    // string | int | float | bool | null (the cell's CellKind)
  literal: string; // the value's text: a number literal, the string, "true"/"false", "" for null
  display: string; // what the cell renders
}

// E7: one entry in the edit overlay: the new value, the ORIGINAL (recorded the
// first time a cell is edited, for revert-to-original + the title), and a
// snapshot of the on-screen Row (so the edited-only view can render it without
// the absolute-index virtualization band).
export interface EditEntry {
  value: EditValue;
  original: EditValue;
  snapshot: Row;
}
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
  // E6: the global search term currently applied (setSearch), "" = none. Kept
  // in state (not just the module-level currentSearch) so the search box can
  // reflect it and Explorer's empty state can say "no rows match your search"
  // distinct from "no rows in file". searchActive is `search !== ""`.
  search: string;
  // E9: the active column sort (path "" = none). Exposed in state so the
  // DataTable header can render the ▲/▼ direction indicator.
  sort: SortSpec;
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
  // E5: the jq/SQL equivalent of the CURRENT filter+transform, refreshed
  // whenever either changes. null until the first refresh lands.
  codegen: Generated | null;
  codegenError: string;
  // A5: a mid-scroll page-fetch failure (as opposed to an open()-time
  // failure, which still owns the whole pane via `status`/`error`) must be
  // non-destructive -- it must NOT discard an already-rendered grid or the
  // user's scroll position. "" = no page error outstanding; Explorer.svelte
  // renders this as a dismissible/retryable `role="alert"` bar ABOVE the
  // still-mounted DataTable, never in place of it.
  pageError: string;
  // E7: the edit overlay -- edits[index][sourcePath] = EditEntry. Keyed by the
  // ABSOLUTE Row.Index + source path, so it survives filter/search/transform/
  // scroll and is cleared only by revertAll or open()/close(). editedCount is
  // the total edited-cell count (recomputed on every change).
  edits: Record<number, Record<string, EditEntry>>;
  editedCount: number;
  // E7 save lifecycle (mirrors the export one).
  saving: boolean;
  saveRows: number;
  saveError: string;
  saveResult: SaveResult | null;
}

const empty: ExplorerState = {
  status: "idle", error: "", path: "", handle: "", tier: "", format: "",
  warnings: [], fields: [], baseColumns: [], columns: [], columnsTruncated: false, totalPaths: 0,
  total: -1, totalExact: false, sampled: false, skipped: 0, focusPath: "", fetching: false, version: 0,
  pageError: "", filterActive: false, search: "", sort: { path: "", desc: false } as SortSpec, resetToken: 0,
  counting: false, matchCount: -1, matchExact: false,
  transformActive: false,
  exporting: false, exportRows: 0, exportError: "", exportResult: null,
  codegen: null, codegenError: "",
  edits: {}, editedCount: 0,
  saving: false, saveRows: 0, saveError: "", saveResult: null,
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
  // E6: the global search term applied to QueryRows/CountMatches/Codegen/
  // Export, set via setSearch(). Threaded alongside currentFilter -- the engine
  // ANDs the two into one compiled predicate. A new file always starts
  // unsearched -- open()/close() reset this to "".
  let currentSearch = "";
  // E9: the active column sort (path "" = none), threaded into QueryRows. Reset
  // to none on open()/close(), like currentSearch.
  let currentSort: SortSpec = { path: "", desc: false } as SortSpec;
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
  // E7: the requestId of the in-flight SaveEdits, or null.
  let saveReqId: string | null = null;
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
    currentSearch = ""; // ...and unsearched
    currentSort = { path: "", desc: false } as SortSpec; // ...and unsorted
    currentTransform = identityTransform(); // ...and unprojected
    countReqId = null;
    countGen++;
    exportReqId = null;
    saveReqId = null;
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
      void refreshCodegen();
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
          requestId: reqId, handle: s.handle, filter: currentFilter, search: currentSearch, transform: currentTransform,
          sort: currentSort,
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
    currentSearch = "";
    currentSort = { path: "", desc: false } as SortSpec;
    currentTransform = identityTransform();
    countReqId = null;
    countGen++;
    exportReqId = null;
    saveReqId = null;
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
      const res: CountResult = await CountMatches({ requestId: reqId, handle, filter, search: currentSearch } as any);
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

  /** E3/E6: the shared supersede+reset+recount dance behind BOTH setFilter and
   *  setSearch. It bumps gen so any in-flight OLD QueryRows is superseded and
   *  cannot cache.set() into the NEW page slot (recon GAP 2); cancels+clears
   *  inflight and drops the page cache; cancels any running count (E3 Task 5);
   *  resets the total (recon GAP 3); refetches page 0; re-counts on a non-memory
   *  tier; and refreshes codegen.
   *
   *  E6 §7: the total-reset and the re-count key on filter-OR-search being
   *  active, NOT the filter alone. Clearing the filter while a search is still
   *  live must keep the searched (narrowed) count, not snap `total` back to the
   *  file-level baseline; symmetrically for clearing the search under a live
   *  filter. Only when BOTH are empty does `total` return to the baseTotal/
   *  baseTotalExact captured at open() (the T10 restore) and the re-count get
   *  skipped -- the store analogue of the engine's empty-search no-op. Without
   *  this, clearing a filter under an active search on a non-memory tier would
   *  restore the whole-file estimate (e.g. "~726,181 rows") while the table
   *  shows only the search hits. `filterActive` itself stays keyed on the
   *  filter alone: it drives the filter bar's own affordance. */
  function requery(extraPatch: Partial<ExplorerState>): void {
    const filterActive = !!(currentFilter.conditions && currentFilter.conditions.length > 0);
    const anyActive = filterActive || currentSearch !== "";
    ++gen;
    for (const [, reqId] of inflight) { void Cancel(reqId).catch(() => {}); }
    inflight.clear();
    cache.clear();
    if (countReqId) { void Cancel(countReqId).catch(() => {}); countReqId = null; }
    countGen++; // keep the invariant "countGen moves wherever countReqId is reset"
    update((s) => ({
      ...s,
      ...extraPatch,
      filterActive,
      total: anyActive ? -1 : baseTotal,
      totalExact: anyActive ? false : baseTotalExact,
      version: 0,
      resetToken: s.resetToken + 1, // DataTable scrolls to row 0 (recon GAP 9)
      pageError: "",
      matchCount: -1,
      matchExact: false,
      counting: false,
    }));
    void ensurePages(0, 0);
    const s = get({ subscribe });
    // CountMatches is the eager exact source only where QueryRows can't already
    // give one -- on the memory tier, filtered/searched page 0 already returns
    // the exact total, so counting there would be a redundant full re-scan.
    // Neither an empty filter nor an empty search needs a count.
    if (anyActive && s.tier !== "memory") {
      void startCount(s.handle, currentFilter, countGen);
    }
    void refreshCodegen();
  }

  /** E3: applies a live filter to the current file (via requery). */
  function setFilter(f: Filter): void {
    currentFilter = f;
    requery({});
  }

  /** E6: applies a live global search to the current file. Same supersede/
   *  reset/recount contract as setFilter; the caller (the search box) debounces
   *  before calling this, exactly as the filter bar does for setFilter. */
  function setSearch(q: string): void {
    currentSearch = q;
    requery({ search: q });
  }

  /** E9: applies a column sort (path "" = none). Same supersede/reset contract
   *  as setFilter/setSearch (via requery), but a PURE sort does not recount --
   *  requery's `anyActive` keys on filter/search only, so total stays baseTotal.
   *  The overlay/getCell/stats are unaffected: Row.Index stays the absolute
   *  ordinal, so an edit set before a sort is still addressable by the same
   *  index. `sort` is threaded into every QueryRows payload. */
  function setSort(spec: SortSpec): void {
    currentSort = spec;
    requery({ sort: spec });
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
    void refreshCodegen();
  }

  /** E5: re-renders the jq/SQL equivalent of the current filter+transform.
   *
   *  Guarded on `gen`, not on `handle`: none of the three triggers
   *  (open/setFilter/setTransform) changes the handle, all of them bump gen
   *  first, and Wails runs every binding call in its own goroutine -- so
   *  resolution order is not call order and a stale render could otherwise
   *  overwrite a newer one.
   *
   *  A failure leaves the LAST GOOD output in place and only sets
   *  codegenError: blanking on every failed keystroke would make the panel
   *  flicker, and a stale-but-labelled program is more useful than an empty
   *  box. There is deliberately no loading flag -- the call is pure and
   *  instant (Engine.Codegen never touches data). */
  async function refreshCodegen(): Promise<void> {
    const s = get({ subscribe });
    if (!s.handle) return;
    const myGen = gen;
    try {
      const res: Generated = await Codegen({
        handle: s.handle, filter: currentFilter, search: currentSearch, transform: currentTransform,
      } as any);
      if (myGen !== gen) return;
      update((st) => ({ ...st, codegen: res, codegenError: "" }));
    } catch (e) {
      if (myGen !== gen) return;
      update((st) => ({ ...st, codegenError: String(e) }));
    }
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
        filter: currentFilter, search: currentSearch, transform: currentTransform,
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

  // --- E7: the edit overlay ------------------------------------------------

  function sameValue(a: EditValue, b: EditValue): boolean {
    return a.kind === b.kind && a.literal === b.literal;
  }

  function countEdits(edits: Record<number, Record<string, EditEntry>>): number {
    let n = 0;
    for (const idx of Object.keys(edits)) n += Object.keys(edits[Number(idx)]).length;
    return n;
  }

  /** E7: records/updates one cell edit. `original` is the cell's ORIGINAL value
   *  (recorded the FIRST time this cell is edited and kept thereafter, so
   *  editing a cell twice never mistakes the intermediate value for the
   *  original); `snapshot` is the on-screen Row (for the edited-only view).
   *  Editing a cell back to its original REMOVES the entry -- that is not an
   *  edit. Keyed by absolute index + source path, so it survives filter/search/
   *  transform/scroll. */
  function setEdit(index: number, path: string, value: EditValue, original: EditValue, snapshot: Row): void {
    update((s) => {
      const edits = { ...s.edits };
      const row = { ...(edits[index] ?? {}) };
      const existing = row[path];
      const orig = existing ? existing.original : original;
      if (sameValue(value, orig)) {
        delete row[path];
      } else {
        row[path] = { value, original: orig, snapshot };
      }
      if (Object.keys(row).length === 0) delete edits[index];
      else edits[index] = row;
      return { ...s, edits, editedCount: countEdits(edits) };
    });
  }

  /** E7: the overlay entry for a cell, or undefined. Used by the table/tree to
   *  render the edited value + highlight. */
  function editFor(index: number, path: string): EditEntry | undefined {
    return get({ subscribe }).edits[index]?.[path];
  }

  /** E7: drops one cell's edit (revert to original). */
  function revertCell(index: number, path: string): void {
    update((s) => {
      const edits = { ...s.edits };
      const row = { ...(edits[index] ?? {}) };
      delete row[path];
      if (Object.keys(row).length === 0) delete edits[index];
      else edits[index] = row;
      return { ...s, edits, editedCount: countEdits(edits) };
    });
  }

  /** E7: clears every edit. */
  function revertAllEdits(): void {
    update((s) => ({ ...s, edits: {}, editedCount: 0 }));
  }

  /** E7: the sorted absolute indices that carry an edit -- the edited-only view
   *  renders these rows from their snapshots. */
  function editedIndices(): number[] {
    return Object.keys(get({ subscribe }).edits).map(Number).sort((a, b) => a - b);
  }

  /** E7: writes a COPY of the source with the overlay applied (Engine.SaveEdits),
   *  as JSON/NDJSON, to outPath. It does NOT thread the filter/search/transform
   *  (save writes the complete file, not a view). Mirrors runExport's request-
   *  id/progress/supersede discipline. The overlay is NOT cleared on success --
   *  the open file is unchanged (this was a copy), so the edits still apply. */
  async function saveEdits(format: string, outPath: string): Promise<void> {
    const s0 = get({ subscribe });
    if (!s0.handle) return;
    const editsList: { index: number; path: string; kind: string; literal: string }[] = [];
    for (const [idx, row] of Object.entries(s0.edits)) {
      for (const [path, entry] of Object.entries(row)) {
        editsList.push({ index: Number(idx), path, kind: entry.value.kind, literal: entry.value.literal });
      }
    }
    const reqId = `save${++seq}`;
    saveReqId = reqId;
    update((s) => ({ ...s, saving: true, saveRows: 0, saveError: "", saveResult: null }));

    const off = EventsOn("shape:progress", (p: any) => {
      if (!p || p.requestId !== saveReqId) return;
      update((s) => (s.saving ? { ...s, saveRows: Number(p.scanned) || 0 } : s));
    });

    try {
      const res: SaveResult = await SaveEdits({
        requestId: reqId, handle: s0.handle, format, outPath, edits: editsList,
      } as any);
      if (saveReqId !== reqId) return;
      update((s) => ({ ...s, saving: false, saveResult: res, saveRows: res.rowsOut }));
    } catch (e) {
      if (saveReqId !== reqId) return;
      update((s) => ({ ...s, saving: false, saveError: String(e) }));
    } finally {
      off();
      if (saveReqId === reqId) {
        saveReqId = null;
        update((s) => (s.saving ? { ...s, saving: false } : s));
      }
    }
  }

  /** E7: clears a finished save's terminal state (the dialog's dismiss). */
  function dismissSave(): void {
    update((s) => ({ ...s, saveResult: null, saveError: "", saveRows: 0 }));
  }

  /** E6: fetches the FULL, untruncated value of one cell (Backend.GetCell) --
   *  the tree-view overlay's data source. index is a Row.Index the table
   *  rendered, path a column path. A thin async wrapper: it owns no store state
   *  (the overlay owns its own open/loading/error), and a failed fetch rejects
   *  so the caller can show an error without disturbing the table. Value comes
   *  back as raw JSON the binding types as number[] but Go marshals as the real
   *  JSON value, so it is surfaced as `unknown`. */
  async function getCell(index: number, path: string): Promise<{ value: unknown; found: boolean }> {
    const s = get({ subscribe });
    if (!s.handle) throw new Error("no source open");
    const res = await GetCell({ handle: s.handle, index, path } as any);
    return { value: (res as any).value as unknown, found: (res as any).found as boolean };
  }

  /** E8: fetches one column's rich profile (visual FieldCard) for the sidebar's
   *  expandable stats view. Thin async wrapper, owning no store state (the panel
   *  owns its own loading/error), rejecting on failure. `found` is false when the
   *  path is not a source field (e.g. a projected column). Sibling of getCell. */
  async function getColumnStats(path: string): Promise<{ card: FieldCard; found: boolean }> {
    const s = get({ subscribe });
    if (!s.handle) throw new Error("no source open");
    const res = await ColumnStats({ handle: s.handle, path } as any);
    return { card: (res as any).card as FieldCard, found: (res as any).found as boolean };
  }

  return {
    subscribe, open, ensurePages, rowAt, focus, close, dismissPageError, retryPageError,
    setFilter, setSearch, setSort, cancelCount, setTransform, runExport, cancelExport, dismissExport,
    refreshCodegen, getCell, getColumnStats,
    setEdit, editFor, revertCell, revertAllEdits, editedIndices, saveEdits, dismissSave,
  };
}

export const explorer = createExplorer();
