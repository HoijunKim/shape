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

// --- V1: virtualization past Blink's element-height cap ---------------------
//
// Blink (WebView2/Chromium) caps any element's height at 2^25 PHYSICAL pixels.
// DataTable sizes its scroll spacer HEADER_H + total*ROW_H; past the cap the
// browser clamps the scrollable area and the file tail becomes unreachable
// (gui/README's virtualization limitation, ~600-800k rows at a typical DPR).
// These pure helpers drive a DPR-aware cap and a SCALED scroll->row mapping the
// component switches to only past the cap; under the cap they leave the
// existing 1:1 path byte-identical (rowWindowFor delegates to rowWindow).

/** Blink's per-element height ceiling, in PHYSICAL pixels (2^25). */
export const BLINK_MAX_PX = 33_554_432;
/** Safety margin kept below the ceiling (sub-pixel rounding + the header). */
export const CAP_MARGIN_PX = 1_000_000;
/** A sane floor so a very large DPR never collapses the cap below a few
 *  viewports. */
export const CAP_FLOOR_PX = 200_000;

/** The CSS-pixel spacer cap for a display's devicePixelRatio. Blink's limit is
 *  physical, and an element's height is specified in CSS px, so the CSS cap is
 *  the physical ceiling divided by dpr, less a margin -- this is what
 *  reproduces the repo's observed ~800k-row ceiling at a typical dpr. */
export function capForDpr(dpr: number): number {
  return Math.max(CAP_FLOOR_PX, Math.floor(BLINK_MAX_PX / Math.max(1, dpr)) - CAP_MARGIN_PX);
}

/** The scroll spacer's height: the natural HEADER_H + total*ROW_H, capped. */
export function contentHeightFor(total: number, rowH: number, headerH: number, cap: number): number {
  return Math.min(headerH + Math.max(0, total) * rowH, cap);
}

/** Whether the natural content height exceeds the cap, i.e. the scaled mapping
 *  is in effect. */
export function isScaled(total: number, rowH: number, headerH: number, cap: number): boolean {
  return headerH + Math.max(0, total) * rowH > cap;
}

/** How many whole rows fit in the band BELOW the sticky header. Subtracting
 *  HEADER_H is load-bearing: without it the scaled fill leaves the last
 *  ceil(HEADER_H/ROW_H) rows clipped under the header at maxScroll (the tail
 *  would be unreachable). Never below 1. */
export function visibleRowCount(clientHeight: number, headerH: number, rowH: number): number {
  return Math.max(1, Math.floor((clientHeight - headerH) / rowH));
}

/** The visible row window for a scroll position. Under the cap it delegates to
 *  rowWindow EXACTLY (so the small-file path is byte-identical). Past the cap it
 *  maps scrollTop by fraction so scrollTop=0 -> row 0 and scrollTop=maxScroll ->
 *  the LAST full page with total-1 seated at the band's bottom edge, widened by
 *  overscan and clamped to [0,total-1]. */
export function rowWindowFor(
  scrollTop: number,
  clientHeight: number,
  total: number,
  rowH: number,
  headerH: number,
  overscan: number,
  contentHeight: number,
): { firstRow: number; lastRow: number } {
  const safeTotal = Math.max(0, total);
  if (safeTotal <= 0) return { firstRow: 0, lastRow: -1 };
  const unscaledHeight = headerH + safeTotal * rowH;
  if (contentHeight >= unscaledHeight) {
    // Not scaled: the exact current behavior, same function, same result.
    return rowWindow(scrollTop, clientHeight, total, rowH, overscan);
  }
  const maxRow = safeTotal - 1;
  const vis = visibleRowCount(clientHeight, headerH, rowH);
  const denom = contentHeight - clientHeight;
  const frac = denom > 0 ? clamp(scrollTop / denom, 0, 1) : 0; // guard denom<=0 (NaN/Inf)
  const firstVisible = Math.round(frac * Math.max(0, safeTotal - vis));
  const firstRow = clamp(firstVisible - overscan, 0, maxRow);
  const lastRow = clamp(firstVisible + vis + overscan, 0, maxRow);
  return { firstRow, lastRow };
}

/** The scrollTop that brings `row` (0-based) to the top of the visible band --
 *  the inverse of rowWindowFor's mapping, for go-to-row. Clamped to the real
 *  scroll range. */
export function scrollTopForRow(
  row: number,
  clientHeight: number,
  total: number,
  rowH: number,
  headerH: number,
  contentHeight: number,
): number {
  const safeTotal = Math.max(0, total);
  const unscaledHeight = headerH + safeTotal * rowH;
  const maxScroll = Math.max(0, contentHeight - clientHeight);
  if (contentHeight >= unscaledHeight) {
    return clamp(Math.max(0, row) * rowH, 0, maxScroll);
  }
  const vis = visibleRowCount(clientHeight, headerH, rowH);
  const denomRows = Math.max(1, safeTotal - vis);
  const fracForRow = clamp(Math.max(0, row) / denomRows, 0, 1);
  return clamp(fracForRow * maxScroll, 0, maxScroll);
}

/** A row's `top` within its layer: absolute i*rowH in the unscaled path (today);
 *  window-relative (i-firstRow)*rowH inside the native-sticky scaled window. */
export function rowTopFor(i: number, firstRow: number, rowH: number, scaled: boolean): number {
  return scaled ? (i - firstRow) * rowH : i * rowH;
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
