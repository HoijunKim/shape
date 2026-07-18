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
