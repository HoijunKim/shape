// Pure display-logic helpers for the structure-map sidebar (T7). Kept out of
// StructureMap.svelte/TreeNode.svelte so vitest can reach them directly --
// see widths.ts's doc comment for the same rationale in Task 6's table.
import type { FieldDTO } from "./types";

/** The KindChip's kind for one field: the literal "mixed" when the field's
 *  type distribution drifted (profile.IsTypeDrift on the Go side: "more than
 *  one non-null type observed"), else the highest-share entry in
 *  `field.types`. A tie keeps the first entry encountered -- deterministic
 *  because the engine always emits `types` sorted by Kind (spec §9), not
 *  Go's randomized map order. Returns "" when there is no type information
 *  at all (an all-absent field). */
export function dominantKind(field: FieldDTO): string {
  if (field.drift) return "mixed";
  const types = field.types ?? [];
  if (types.length === 0) return "";
  let best = types[0];
  for (let i = 1; i < types.length; i++) {
    if (types[i].share > best.share) best = types[i];
  }
  return best.kind;
}

/** Buckets a null rate into the Meter/Badge severity bands used elsewhere in
 *  the app (mirrors internal/visual/geometry.go's nullStatus + the
 *  NullWarnBand=0.20 / NullSeriousBand=0.50 constants in types.go, so the
 *  sidebar's coloring matches the FieldCard dashboard's). Below the warn
 *  band returns "" (Meter.svelte's KNOWN_STATUS guard already falls back to
 *  --text-muted for that, matching visual's SevNone = ""). */
export function nullStatus(rate: number): string {
  if (!Number.isFinite(rate)) return "";
  if (rate >= 1.0) return "critical";
  if (rate >= 0.5) return "serious";
  if (rate >= 0.2) return "warning";
  return "";
}

/** Formats a 0..1 rate as a rounded whole-number percent, e.g. 0.5 -> "50%".
 *  Mirrors internal/visual/format.go's fmtPct (int(f*100+0.5)); Math.round
 *  on a non-negative input matches that round-half-up behavior exactly. */
export function formatPercent(rate: number): string {
  if (!Number.isFinite(rate)) return "0%";
  return `${Math.round(Math.max(0, rate) * 100)}%`;
}

/** Formats a distinct count, prefixing "~" when the count is an estimate
 *  rather than exact (mirrors internal/visual/format.go's fmtDistinct). */
export function formatDistinct(n: number, exact: boolean): string {
  const s = Math.max(0, Math.trunc(n)).toLocaleString("en-US");
  return exact ? s : `~${s}`;
}

/** All proper ancestor paths of a dotted path, using "." as the segment
 *  separator (matching tree.ts's own convention: array-element segments like
 *  "items[]" contain no dot, so they are one segment, same as tree.ts's
 *  split). For "a.b.c" this is {"a", "a.b"} -- deliberately NOT including
 *  "a.b.c" itself, so the focused node's own children are not forced open,
 *  only the chain of parents leading down to it. Used to auto-expand the
 *  sidebar to $explorer.focusPath, including when a DataTable header click
 *  (not a sidebar click) set it. */
export function ancestorPaths(path: string): Set<string> {
  const out = new Set<string>();
  if (!path) return out;
  const segs = path.split(".");
  let prefix = "";
  for (let i = 0; i < segs.length - 1; i++) {
    prefix = prefix === "" ? segs[i] : `${prefix}.${segs[i]}`;
    out.add(prefix);
  }
  return out;
}
