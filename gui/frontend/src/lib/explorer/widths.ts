import type { Column } from "./types";
import { CellKind } from "./types";

export const MIN_W = 80;
export const MAX_W = 320;

/** Clamps v into [lo, hi]. Shared by columnWidths' MIN_W/MAX_W floor/ceiling
 *  and rowWindow's row-index clamping -- one clamp implementation, so a test
 *  of this function directly covers both call sites. */
export function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, v));
}

/** Width heuristic: header text sets the floor, the column's type sets the
 *  natural width (numbers and bools are narrow, containers and strings wide). */
export function columnWidths(columns: Column[]): number[] {
  return columns.map((c) => {
    const header = 16 + c.name.length * 7.2;
    const natural =
      c.container ? 260 :
      c.type === "bool" ? 90 :
      c.type === "int" || c.type === "float" ? 120 :
      c.type === "mixed" ? 200 : 180;
    return Math.round(clamp(Math.max(header, natural), MIN_W, MAX_W));
  });
}

/** prefixSums(widths)[i] is the x offset of column i; the last element is the
 *  total width. Used for horizontal virtualization by binary search. */
export function prefixSums(widths: number[]): number[] {
  const out = new Array<number>(widths.length + 1);
  out[0] = 0;
  for (let i = 0; i < widths.length; i++) out[i + 1] = out[i] + widths[i];
  return out;
}

/** Binary search over a prefix-sum array (as returned by prefixSums) for the
 *  column whose span contains x: the greatest i such that prefix[i] <= x.
 *  Hoisted out of DataTable.svelte (where vitest cannot reach it) because
 *  this hand-rolled binary search is the highest boundary-risk arithmetic in
 *  the table -- single-column and 512-column edges in particular. */
export function columnAt(x: number, prefix: number[]): number {
  const n = prefix.length - 1; // number of columns; prefix has length n+1
  if (n <= 0) return 0;
  let lo = 0;
  let hi = n - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (prefix[mid] <= x) lo = mid; else hi = mid - 1;
  }
  return lo;
}

/** The visible row window for a given scroll position, viewport height, row
 *  height and overscan -- clamped to [0, total-1], or the empty range
 *  {firstRow: 0, lastRow: -1} when total <= 0. Pure and total-driven rather
 *  than incrementally stateful: calling it fresh with a new `total` (even
 *  with every other argument unchanged) always yields a window clamped to
 *  THAT total, never one left over from a previous, larger total -- see
 *  widths.test.ts's "total shrinks below the current window" case, and
 *  DataTable.svelte's `$: safeTotal, recomputeRange()` trigger that calls
 *  this on every `total` change, not only on scroll/mount/resize. */
export function rowWindow(
  scrollTop: number,
  clientHeight: number,
  total: number,
  rowH: number,
  overscan: number
): { firstRow: number; lastRow: number } {
  const safeTotal = Math.max(0, total);
  if (safeTotal <= 0) return { firstRow: 0, lastRow: -1 };
  const maxRow = safeTotal - 1;
  const firstRow = clamp(Math.floor(scrollTop / rowH) - overscan, 0, maxRow);
  const lastRow = clamp(Math.ceil((scrollTop + clientHeight) / rowH) + overscan, 0, maxRow);
  return { firstRow, lastRow };
}

/** Cell-kind, not column-type, decides alignment (spec §3's table is keyed
 *  on kind: a `null` inside an otherwise-numeric column still renders left-
 *  aligned, so this can't be precomputed once per column). Hoisted here
 *  alongside columnAt/rowWindow: CellKind is a plain generated TS enum with
 *  no Svelte-specific dependency, so nothing forces this to stay local to
 *  DataTable.svelte. */
export function alignForKind(kind: CellKind): "left" | "right" {
  return kind === CellKind.INT || kind === CellKind.FLOAT ? "right" : "left";
}
