import type { Column } from "./types";

export const MIN_W = 80;
export const MAX_W = 320;

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
    return Math.round(Math.min(MAX_W, Math.max(MIN_W, header, natural)));
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
