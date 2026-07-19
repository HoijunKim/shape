import type { RowSet } from "./types";

export const PAGE_ROW_BUDGET = 30000; // rows*columns per fetch; ~1.5 MB of JSON
export const PAGE_ROWS_MIN = 40;
export const PAGE_ROWS_MAX = 200;

/** Rows per fetched page. Wide tables fetch shorter pages: the whole RowSet
 *  crosses the webview bridge as JSON, and 512 columns x 500 rows is ~14 MB. */
export function pageRowsFor(columnCount: number): number {
  const cols = Math.max(1, columnCount | 0);
  const n = Math.floor(PAGE_ROW_BUDGET / cols);
  return Math.min(PAGE_ROWS_MAX, Math.max(PAGE_ROWS_MIN, n));
}

export function pageIndexOf(row: number, pageRows: number): number {
  return Math.floor(row / pageRows);
}

export function pagesForRange(first: number, last: number, pageRows: number): number[] {
  const a = pageIndexOf(Math.max(0, first), pageRows);
  const b = pageIndexOf(Math.max(0, last), pageRows);
  const out: number[] = [];
  for (let i = a; i <= b; i++) out.push(i);
  return out;
}

/** Absolute row index -> (page index, offset within that page's RowSet.rows).
 *  Extracted from store.ts's rowAt so the index arithmetic has its own unit
 *  test independent of the Wails bridge. */
export function rowLocation(index: number, pageRows: number): { page: number; offset: number } {
  const page = pageIndexOf(index, pageRows);
  return { page, offset: index - page * pageRows };
}

export interface EofReconcileInput {
  page: number;             // page index this RowSet answers
  pageRows: number;         // rows requested per page
  rowsLength: number;       // rs.rows.length actually returned
  truncated: boolean;       // rs.truncated
  rsTotal: number;          // rs.total (-1 = backend did not supply one)
  rsTotalExact: boolean;    // rs.totalExact, meaningful only when rsTotal >= 0
  priorTotal: number;       // st.total before this page landed
  priorTotalExact: boolean; // st.totalExact before this page landed
}

/** Reconciles the store's total/totalExact against one landed page.
 *  Extracted from store.ts's ensurePages so the EOF arithmetic (I2) has its
 *  own unit test independent of the Wails bridge.
 *
 *  On the rescan tier `total` starts as fileSize/avgBytes (spec S4) -- an
 *  estimate that misses in both directions and that the backend never
 *  corrects. A landed page is ground truth for its own range: a short page
 *  (truncated) pins the real end; a full page at the current tail proves at
 *  least one more page exists. Without this the scrollbar addresses rows
 *  that do not exist (permanent skeletons) or hides rows that do.
 *
 *  rs.total/rs.totalExact are authoritative whenever the backend supplied
 *  them (rsTotal >= 0). store.ts's page fetches always pass wantTotal:
 *  false, so this is normally a no-op EXCEPT on the memory tier, where
 *  internal/query/memstore.go ignores wantTotal and always returns the exact
 *  count with TotalExact: true on every single page -- so for that tier
 *  rsTotal is >= 0 on every call, not just when wantTotal is requested. */
export function reconcileEof(input: EofReconcileInput): { total: number; totalExact: boolean } {
  const { page, pageRows, rowsLength, truncated, rsTotal, rsTotalExact, priorTotal, priorTotalExact } = input;
  let total = rsTotal >= 0 ? rsTotal : priorTotal;
  let totalExact = rsTotal >= 0 ? rsTotalExact : priorTotalExact;
  const pageEnd = page * pageRows + rowsLength;
  // I2: `!totalExact` guards this branch using the value already reconciled
  // above (backend-authoritative when rsTotal >= 0, else the prior value) --
  // a landed page can only ever IMPROVE the total, never downgrade one the
  // store already holds exactly. Without this guard, an overscan/prefetch
  // page requested past EOF also comes back truncated with 0 rows, and would
  // shrink a correct total into a phantom `pageEnd` and flip totalExact
  // true -> false.
  if (truncated && !totalExact) {
    total = pageEnd;
    // A short NON-EMPTY page (rowsLength > 0) is always exact: the rows it
    // did return prove that many matches exist beyond its offset, and being
    // short proves no more do -- together pinning the total exactly. Page 0
    // returning empty is exact for a different, simpler reason: at offset 0,
    // "0 rows" can only mean the file truly has 0 matches (a match count
    // can't be negative), so there is no ambiguity to resolve.
    //
    // I-A1: an EMPTY page past EOF at page > 0 is neither of those. In
    // isolation it proves only `total <= pageEnd` -- offset could be far
    // past the true end (e.g. a bad rescan-tier fileSize/avgBytes seed let
    // the scrollbar reach somewhere nowhere near reality), so page > 0 alone
    // must NOT be trusted as exact (an earlier version of this comment
    // claimed "the next fetch converges" -- that is false whenever the file
    // is page-aligned: if the true row count is an exact multiple of
    // pageRows, no page is EVER short-and-non-empty, so the `rowsLength > 0`
    // case above never fires and the total was stuck inexact forever).
    //
    // It CAN be trusted when `priorTotal === pageEnd + pageRows`. That exact
    // equality only arises one way: the immediately preceding page landed as
    // a FULL (non-truncated) page whose real, PROVEN end is this page's
    // offset (pageEnd, since rowsLength is 0 here), and its landing pushed
    // the running estimate forward by one more page via the projection in
    // the `else` branch below (`pageEnd + pageRows`) -- i.e. priorTotal
    // encodes that prior page's proven end plus one page width. A full page
    // proves rows [its offset, its offset+pageRows) truly exist (a genuine
    // lower bound, not a guess); THIS page proves nothing exists beyond that
    // same offset. Together they pin the total at exactly `pageEnd`. An
    // unrelated total (e.g. the original fileSize/avgBytes seed, or a
    // scrollbar jump straight to some other far-off empty page) essentially
    // never lands on that exact integer by coincidence, so this is not a
    // blanket "trust every empty page" rule -- it only fires for the
    // contiguous, sequential-scroll case the bug report actually describes.
    totalExact = rowsLength > 0 || page === 0 || priorTotal === pageEnd + pageRows;
  } else if (!totalExact && pageEnd >= total) {
    total = pageEnd + pageRows; // full page at the tail: more to come
  }
  return { total, totalExact };
}

/** LRU cache of fetched pages, so scrolling back is instant and memory stays
 *  bounded regardless of how far the user scrolls. */
export class PageCache {
  private map = new Map<number, RowSet>();
  constructor(private cap = 8) {}
  get size(): number { return this.map.size; }
  has(i: number): boolean { return this.map.has(i); }
  get(i: number): RowSet | undefined {
    const v = this.map.get(i);
    if (v !== undefined) { this.map.delete(i); this.map.set(i, v); } // touch
    return v;
  }
  set(i: number, rs: RowSet): void {
    if (this.map.has(i)) this.map.delete(i);
    this.map.set(i, rs);
    while (this.map.size > this.cap) this.map.delete(this.map.keys().next().value as number);
  }
  clear(): void { this.map.clear(); }
}
