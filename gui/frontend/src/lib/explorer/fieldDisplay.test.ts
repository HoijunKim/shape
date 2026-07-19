import { describe, it, expect } from "vitest";
import { dominantKind, nullStatus, formatPercent, formatDistinct, ancestorPaths } from "./fieldDisplay";
import type { FieldDTO } from "./types";

const field = (over: Partial<FieldDTO> = {}): FieldDTO =>
  ({
    path: "x",
    types: [],
    presence: 1,
    nullRate: 0,
    distinct: 0,
    distinctExact: true,
    drift: false,
    ...over,
  } as FieldDTO);

describe("dominantKind", () => {
  it("returns the highest-share type", () => {
    const f = field({ types: [{ kind: "string", share: 0.2 } as any, { kind: "int", share: 0.8 } as any] });
    expect(dominantKind(f)).toBe("int");
  });

  it("keeps the FIRST entry on an exact tie (deterministic, not last-write-wins)", () => {
    // Regresses a `>=` comparison, which would silently flip the winner to
    // the last-seen entry on a tie instead of the first.
    const f = field({ types: [{ kind: "string", share: 0.5 } as any, { kind: "int", share: 0.5 } as any] });
    expect(dominantKind(f)).toBe("string");
  });

  it("returns the literal 'mixed' when field.drift is true, even if one type dominates by share", () => {
    const f = field({ drift: true, types: [{ kind: "int", share: 0.99 } as any, { kind: "string", share: 0.01 } as any] });
    expect(dominantKind(f)).toBe("mixed");
  });

  it("returns '' for a field with no recorded types", () => {
    expect(dominantKind(field({ types: [] }))).toBe("");
  });
});

describe("nullStatus", () => {
  it("is '' below the warn band", () => {
    expect(nullStatus(0)).toBe("");
    expect(nullStatus(0.19)).toBe("");
  });
  it("is 'warning' at and above 0.20, below 0.50", () => {
    expect(nullStatus(0.2)).toBe("warning");
    expect(nullStatus(0.49)).toBe("warning");
  });
  it("is 'serious' at and above 0.50, below 1.0", () => {
    expect(nullStatus(0.5)).toBe("serious");
    expect(nullStatus(0.99)).toBe("serious");
  });
  it("is 'critical' at and above 1.0", () => {
    expect(nullStatus(1.0)).toBe("critical");
  });
  it("treats non-finite input as ''", () => {
    expect(nullStatus(NaN)).toBe("");
  });
});

describe("formatPercent", () => {
  it("rounds to the nearest whole percent", () => {
    expect(formatPercent(0.5)).toBe("50%");
    expect(formatPercent(0.055)).toBe("6%"); // 5.5 rounds up, matches fmtPct's +0.5 truncation
    expect(formatPercent(1)).toBe("100%");
    expect(formatPercent(0)).toBe("0%");
  });

  it("clamps a negative rate to 0 before rounding (Math.max(0, ...) guard)", () => {
    expect(formatPercent(-0.5)).toBe("0%");
  });

  it("treats a non-NaN, non-finite input (Infinity) as 0% via the !Number.isFinite guard", () => {
    expect(formatPercent(Infinity)).toBe("0%");
  });
});

describe("formatDistinct", () => {
  it("comma-groups an exact count with no prefix", () => {
    expect(formatDistinct(48210, true)).toBe("48,210");
  });
  it("prefixes '~' for an inexact (sampled) count", () => {
    expect(formatDistinct(48210, false)).toBe("~48,210");
  });
});

describe("ancestorPaths", () => {
  it("returns every proper ancestor, not the path itself", () => {
    const out = ancestorPaths("a.b.c");
    expect(Array.from(out).sort()).toEqual(["a", "a.b"]);
    expect(out.has("a.b.c")).toBe(false);
  });

  it("returns an empty set for a root-level (undotted) path", () => {
    expect(ancestorPaths("id").size).toBe(0);
  });

  it("returns an empty set for the empty path (no focus)", () => {
    expect(ancestorPaths("").size).toBe(0);
  });

  it("treats an array-element segment as one segment, matching tree.ts", () => {
    // "items[].sku" splits on "." into ["items[]", "sku"] -- same convention
    // as tree.ts's buildTree -- so the only ancestor is "items[]", not
    // "items" and "[]" separately.
    const out = ancestorPaths("items[].sku");
    expect(Array.from(out)).toEqual(["items[]"]);
  });

  it("handles depth >= 3 (regresses an off-by-one in the prefix loop)", () => {
    const out = ancestorPaths("a.b.c.d");
    expect(Array.from(out).sort()).toEqual(["a", "a.b", "a.b.c"]);
  });
});
