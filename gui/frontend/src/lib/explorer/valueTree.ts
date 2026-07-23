// Pure value->node shaping for the ValueTree overlay (E6 Task 6). The value is
// whatever GetCell returned, JSON-parsed by the Wails bridge: an object, array,
// string, number, boolean, or null. Kept out of the .svelte file so the cap
// arithmetic and kind classification are unit-testable without mounting.

export type ValueKind = "object" | "array" | "string" | "number" | "bool" | "null";

// valueKind classifies a JS value the way CellView classifies a Cell. Note
// numbers arrive as JS numbers (the bridge JSON.parses the response), so a
// 64-bit integer is already float64 by the time it reaches here -- the tree is
// a viewer, not a lossless round-trip of the source literal.
export function valueKind(v: unknown): ValueKind {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  switch (typeof v) {
    case "object":
      return "object";
    case "string":
      return "string";
    case "number":
      return "number";
    case "boolean":
      return "bool";
    default:
      return "null"; // undefined and anything unexpected render as null
  }
}

export function isContainer(v: unknown): boolean {
  const k = valueKind(v);
  return k === "object" || k === "array";
}

// MAX_CHILDREN caps how many children ONE container renders at a time. Without
// it, expanding a 100k-element array would mount 100k rows and freeze the
// webview; the remainder is summarized as an "N more" note instead.
export const MAX_CHILDREN = 100;

export interface ChildEntry {
  key: string; // an object key, or "[i]" for an array index
  value: unknown;
}

export interface ShapedChildren {
  entries: ChildEntry[];
  hidden: number; // children capped away (0 when nothing was hidden)
  total: number; // full child count, before the cap
}

// shapeChildren lists a container's children (object entries or array elements),
// capped at `cap`. A scalar has no children (empty entries, hidden 0).
export function shapeChildren(v: unknown, cap: number = MAX_CHILDREN): ShapedChildren {
  const k = valueKind(v);
  let all: ChildEntry[];
  if (k === "array") {
    all = (v as unknown[]).map((cv, i) => ({ key: `[${i}]`, value: cv }));
  } else if (k === "object") {
    all = Object.entries(v as Record<string, unknown>).map(([key, value]) => ({ key, value }));
  } else {
    return { entries: [], hidden: 0, total: 0 };
  }
  const entries = cap >= 0 && all.length > cap ? all.slice(0, cap) : all;
  return { entries, hidden: all.length - entries.length, total: all.length };
}

// childCount is the container's element/key count for the collapsed badge.
export function childCount(v: unknown): number {
  const k = valueKind(v);
  if (k === "array") return (v as unknown[]).length;
  if (k === "object") return Object.keys(v as Record<string, unknown>).length;
  return 0;
}

// scalarText renders a scalar leaf as display text, matching CellView's idioms
// (strings raw, null as "null", bools as true/false, numbers via String()).
export function scalarText(v: unknown): string {
  switch (valueKind(v)) {
    case "null":
      return "null";
    case "bool":
      return v ? "true" : "false";
    case "string":
      return v as string;
    case "number":
      return String(v);
    default:
      return "";
  }
}
