import { describe, it, expect } from "vitest";
import { columnWidths, prefixSums, columnAt, rowWindow, alignForKind, clamp, MIN_W, MAX_W } from "./widths";
import type { Column } from "./types";
import { CellKind } from "./types";

// Minimal Column factory -- only the fields columnWidths() reads matter for
// these tests, but the full shape keeps this assignable to `Column`.
function col(overrides: Partial<Column>): Column {
  return {
    path: overrides.name ?? "c",
    name: "c",
    type: "string",
    nullable: false,
    presence: 1,
    distinct: 1,
    container: false,
    index: 0,
    ...overrides,
  } as Column;
}

describe("clamp", () => {
  // columnWidths' MIN_W/MAX_W floor and ceiling, and rowWindow's row-index
  // clamping, both delegate to this one function -- test it directly.
  it("floors a value below lo", () => {
    expect(clamp(50, MIN_W, MAX_W)).toBe(MIN_W);
  });

  it("passes a value inside [lo, hi] through unchanged", () => {
    expect(clamp(150, MIN_W, MAX_W)).toBe(150);
  });

  it("ceils a value above hi", () => {
    expect(clamp(500, MIN_W, MAX_W)).toBe(MAX_W);
  });

  it("is inclusive at both boundaries", () => {
    expect(clamp(MIN_W, MIN_W, MAX_W)).toBe(MIN_W);
    expect(clamp(MAX_W, MIN_W, MAX_W)).toBe(MAX_W);
  });
});

describe("columnWidths", () => {
  it("documents that MIN_W is unreachable through columnWidths' own type table: the narrowest natural width in the ternary is 90 (bool), already above MIN_W (80), and the shortest possible header (empty name) is 16 -- max(header, natural) can never fall below 90 for any (name, type, container) combination columnWidths accepts, so its own MIN_W clamp never actually binds. (Genuine floor coverage lives in the `clamp` describe block above, since that -- not columnWidths -- is the only way to exercise a sub-floor input.)", () => {
    const widths = columnWidths([
      col({ name: "", type: "bool" }),
      col({ name: "", type: "int" }),
      col({ name: "", type: "string" }),
    ]);
    expect(widths).toEqual([90, 120, 180]); // every one strictly above MIN_W, by construction
  });

  it("uses the header-text floor when a long name exceeds the type's natural width", () => {
    const longName = "a".repeat(30); // header = 16 + 30*7.2 = 232
    const [w] = columnWidths([col({ name: longName, type: "bool" })]); // natural = 90
    expect(w).toBe(232);
  });

  it("rounds a genuinely fractional header width (29-char name: 16 + 29*7.2 = 224.8 -> 225)", () => {
    const name29 = "a".repeat(29);
    const [w] = columnWidths([col({ name: name29, type: "string" })]); // natural = 180, header wins
    expect(w).toBe(225);
  });

  it("clamps to MAX_W for an extremely long header", () => {
    const longName = "x".repeat(100); // header = 16 + 100*7.2 = 736, way over MAX_W
    const [w] = columnWidths([col({ name: longName, type: "string" })]);
    expect(w).toBe(MAX_W);
  });

  it("gives container columns (object/array) the wide natural width regardless of type", () => {
    const [w] = columnWidths([col({ name: "tags", type: "array", container: true })]);
    // header = 16 + 4*7.2 = 44.8; container natural = 260 -> 260.
    expect(w).toBe(260);
  });

  it("lets a long name's header floor beat even a container's wide natural width", () => {
    const longName = "x".repeat(40); // header = 16 + 40*7.2 = 304
    const [w] = columnWidths([col({ name: longName, type: "array", container: true })]); // natural = 260
    expect(w).toBe(304); // header (304) > container natural (260)
  });

  it("gives int/float columns a narrow natural width", () => {
    const [wInt] = columnWidths([col({ name: "n", type: "int" })]);
    const [wFloat] = columnWidths([col({ name: "n", type: "float" })]);
    expect(wInt).toBe(120);
    expect(wFloat).toBe(120);
  });

  it("gives mixed-type columns a mid natural width", () => {
    const [w] = columnWidths([col({ name: "m", type: "mixed" })]);
    expect(w).toBe(200);
  });

  it("gives a plain string column the default natural width", () => {
    const [w] = columnWidths([col({ name: "email", type: "string" })]);
    // header = 16 + 5*7.2 = 52; natural(string, not container) = 180 -> 180.
    expect(w).toBe(180);
  });

  it("computes one width per column, independent of the others", () => {
    const cols = [
      col({ name: "id", type: "int" }),
      col({ name: "email", type: "string" }),
      col({ name: "tags", type: "array", container: true }),
    ];
    expect(columnWidths(cols)).toEqual([120, 180, 260]);
  });

  it("returns an empty array for no columns", () => {
    expect(columnWidths([])).toEqual([]);
  });
});

describe("prefixSums", () => {
  it("returns [0] for an empty widths array", () => {
    expect(prefixSums([])).toEqual([0]);
  });

  it("accumulates offsets with the last element as the total width", () => {
    expect(prefixSums([100, 80, 120])).toEqual([0, 100, 180, 300]);
  });

  it("has length widths.length + 1", () => {
    const widths = [10, 20, 30, 40];
    expect(prefixSums(widths).length).toBe(widths.length + 1);
  });

  it("computes exact offsets for a four-column input (not just a monotonic trend: a monotonicity-only check can't tell a correct implementation from a buggy one that still produces increasing values for non-negative widths, e.g. doubling every width)", () => {
    expect(prefixSums([90, 120, 260, 80])).toEqual([0, 90, 210, 470, 550]);
  });

  it("handles a single column", () => {
    expect(prefixSums([150])).toEqual([0, 150]);
  });
});

describe("columnAt", () => {
  it("returns 0 for an empty prefix array (no columns)", () => {
    expect(columnAt(0, [0])).toBe(0);
    expect(columnAt(500, [0])).toBe(0);
  });

  it("always returns 0 for a single column, regardless of x", () => {
    const prefix = prefixSums([150]); // [0, 150]
    expect(columnAt(0, prefix)).toBe(0);
    expect(columnAt(149, prefix)).toBe(0);
    expect(columnAt(150, prefix)).toBe(0); // at/after the column's own end
    expect(columnAt(999999, prefix)).toBe(0);
  });

  it("finds the containing column for an x strictly inside its span", () => {
    const prefix = prefixSums([100, 80, 120]); // [0, 100, 180, 300]
    expect(columnAt(0, prefix)).toBe(0);
    expect(columnAt(50, prefix)).toBe(0);
    expect(columnAt(100, prefix)).toBe(1); // exactly at column 1's start
    expect(columnAt(150, prefix)).toBe(1);
    expect(columnAt(180, prefix)).toBe(2); // exactly at column 2's start
    expect(columnAt(299, prefix)).toBe(2);
  });

  it("clamps to the last column when x is past the total width", () => {
    const prefix = prefixSums([100, 80, 120]); // total width 300
    expect(columnAt(300, prefix)).toBe(2);
    expect(columnAt(100000, prefix)).toBe(2);
  });

  it("resolves every column boundary exactly across 512 columns", () => {
    const widths = new Array(512).fill(100);
    const prefix = prefixSums(widths); // prefix[i] = i*100, length 513

    expect(columnAt(0, prefix)).toBe(0);
    expect(columnAt(99, prefix)).toBe(0);
    expect(columnAt(prefix[300], prefix)).toBe(300); // exact boundary
    expect(columnAt(prefix[300] + 50, prefix)).toBe(300); // mid-span
    expect(columnAt(prefix[511], prefix)).toBe(511); // last column's start
    expect(columnAt(prefix[511] + 99, prefix)).toBe(511); // last column's end
    expect(columnAt(prefix[511] + 100000, prefix)).toBe(511); // past the end, clamps
  });
});

describe("rowWindow", () => {
  const ROW_H = 10;
  const OVERSCAN = 2;

  it("clamps firstRow to 0 at the very top (row 0)", () => {
    const { firstRow, lastRow } = rowWindow(0, 100, 1000, ROW_H, OVERSCAN);
    expect(firstRow).toBe(0);
    expect(lastRow).toBe(12); // ceil(100/10) + 2
  });

  it("clamps both bounds to the last row when scrolled past the end", () => {
    const { firstRow, lastRow } = rowWindow(100000, 100, 50, ROW_H, OVERSCAN);
    expect(firstRow).toBe(49);
    expect(lastRow).toBe(49);
  });

  it("returns the empty range {firstRow: 0, lastRow: -1} when total === 0", () => {
    expect(rowWindow(500, 300, 0, ROW_H, OVERSCAN)).toEqual({ firstRow: 0, lastRow: -1 });
  });

  it("treats total === -1 (unknown) the same as total === 0: the empty range, never negative indices", () => {
    expect(rowWindow(500, 300, -1, ROW_H, OVERSCAN)).toEqual({ firstRow: 0, lastRow: -1 });
  });

  it("clamps to a total that shrinks below the current window -- finding (a)'s regression case: a landed page can shrink `total` (paging.ts's reconcileEof, rescan tier) after the window was computed against a larger, optimistic estimate. Calling rowWindow again with the SAME scrollTop but the new, smaller total must clamp both bounds to it, never leaving a stale window pointing past the new end", () => {
    const scrollTop = 25000;
    const clientHeight = 500;
    const rowH = 28;
    const overscan = 8;

    const deep = rowWindow(scrollTop, clientHeight, 1000, rowH, overscan);
    expect(deep.firstRow).toBeGreaterThan(800); // sanity: this really is a deep-scroll window
    expect(deep.lastRow).toBeGreaterThan(deep.firstRow);

    const shrunk = rowWindow(scrollTop, clientHeight, 10, rowH, overscan);
    expect(shrunk.firstRow).toBeLessThanOrEqual(9);
    expect(shrunk.lastRow).toBeLessThanOrEqual(9);
    expect(shrunk.firstRow).toBeGreaterThanOrEqual(0);
  });

  it("handles a viewport taller than the whole table", () => {
    const { firstRow, lastRow } = rowWindow(0, 10000, 5, ROW_H, OVERSCAN);
    expect(firstRow).toBe(0);
    expect(lastRow).toBe(4); // total-1
  });
});

describe("alignForKind", () => {
  it("right-aligns int and float", () => {
    expect(alignForKind(CellKind.INT)).toBe("right");
    expect(alignForKind(CellKind.FLOAT)).toBe("right");
  });

  it("left-aligns every other kind", () => {
    expect(alignForKind(CellKind.STRING)).toBe("left");
    expect(alignForKind(CellKind.BOOL)).toBe("left");
    expect(alignForKind(CellKind.NULL)).toBe("left");
    expect(alignForKind(CellKind.MISSING)).toBe("left");
    expect(alignForKind(CellKind.OBJECT)).toBe("left");
    expect(alignForKind(CellKind.ARRAY)).toBe("left");
  });
});

// --- V1: virtualization past the height cap ---------------------------------

import {
  capForDpr, contentHeightFor, isScaled, visibleRowCount, rowWindowFor,
  scrollTopForRow, rowTopFor, BLINK_MAX_PX, CAP_MARGIN_PX, CAP_FLOOR_PX,
} from "./widths";

describe("capForDpr", () => {
  it("shrinks as dpr grows and stays margin-guarded", () => {
    expect(capForDpr(1)).toBe(BLINK_MAX_PX - CAP_MARGIN_PX);
    expect(capForDpr(2)).toBe(Math.floor(BLINK_MAX_PX / 2) - CAP_MARGIN_PX);
    expect(capForDpr(2)).toBeLessThan(capForDpr(1));
    // Mutation: drop the `- CAP_MARGIN_PX` -> a dpr=1 element sized exactly at
    // 2^25 is not kept clear of the ceiling.
    expect(capForDpr(1)).toBeLessThan(BLINK_MAX_PX);
  });

  it("never collapses below the floor for a huge dpr", () => {
    expect(capForDpr(1000)).toBe(CAP_FLOOR_PX);
  });
});

describe("contentHeightFor / isScaled", () => {
  const ROW_H = 28, HEADER_H = 32, CAP = 100_000;
  it("is exact under the cap and capped over it", () => {
    expect(contentHeightFor(100, ROW_H, HEADER_H, CAP)).toBe(HEADER_H + 100 * ROW_H);
    expect(contentHeightFor(1_000_000, ROW_H, HEADER_H, CAP)).toBe(CAP);
  });
  it("flips isScaled exactly at the boundary", () => {
    // rows that fit exactly: HEADER_H + n*ROW_H <= CAP
    const n = Math.floor((CAP - HEADER_H) / ROW_H);
    expect(isScaled(n, ROW_H, HEADER_H, CAP)).toBe(false);
    expect(isScaled(n + 1, ROW_H, HEADER_H, CAP)).toBe(true);
  });
});

describe("visibleRowCount", () => {
  it("subtracts the header (not ceil of the whole viewport)", () => {
    // Mutation: use ceil(clientHeight/ROW_H) -> 29 here; the header-blind count
    // is what hid the tail (review V16).
    expect(visibleRowCount(800, 32, 28)).toBe(Math.floor((800 - 32) / 28)); // 27
    expect(visibleRowCount(800, 32, 28)).not.toBe(Math.ceil(800 / 28)); // != 29
  });
  it("never drops below 1, even when the viewport is shorter than the header", () => {
    expect(visibleRowCount(20, 32, 28)).toBe(1);
  });
});

describe("rowWindowFor", () => {
  const ROW_H = 28, HEADER_H = 32, OVERSCAN = 8;

  it("delegates to rowWindow byte-identically under the cap", () => {
    const total = 1000;
    const contentHeight = HEADER_H + total * ROW_H; // not scaled
    for (const scrollTop of [0, 137, 5000, 27000, 1e9]) {
      const got = rowWindowFor(scrollTop, 500, total, ROW_H, HEADER_H, OVERSCAN, contentHeight);
      const want = rowWindow(scrollTop, 500, total, ROW_H, OVERSCAN);
      // Mutation: an off-by-one in the unscaled delegation (floor->ceil) breaks this.
      expect(got).toEqual(want);
    }
  });

  it("scaled: scrollTop=0 -> firstRow 0; scrollTop=maxScroll -> tail seated at total-1", () => {
    const total = 40_000_000;
    const clientHeight = 800;
    const cap = capForDpr(1);
    const contentHeight = contentHeightFor(total, ROW_H, HEADER_H, cap);
    expect(isScaled(total, ROW_H, HEADER_H, cap)).toBe(true);

    const top = rowWindowFor(0, clientHeight, total, ROW_H, HEADER_H, OVERSCAN, contentHeight);
    expect(top.firstRow).toBe(0);

    const maxScroll = contentHeight - clientHeight;
    const bottom = rowWindowFor(maxScroll, clientHeight, total, ROW_H, HEADER_H, OVERSCAN, contentHeight);
    // Mutation: use floor(scrollTop/ROW_H) in the scaled branch -> firstRow is
    // tiny and lastRow never reaches total-1 (the tail is unreachable).
    expect(bottom.lastRow).toBe(total - 1);
    const vis = visibleRowCount(clientHeight, HEADER_H, ROW_H);
    // The top visible row is total-vis (no above-overscan in scaled mode), so
    // rows [total-vis .. total-1] fill the band with total-1 at its bottom.
    expect(bottom.firstRow).toBe(total - vis);
  });

  it("scaled: guards denom===0 (0/0 -> NaN) and denom<0, never past total-1", () => {
    const total = 500;
    // denom === 0 AND scrollTop === 0 is the exact 0/0 -> NaN case the guard
    // exists for (a mere negative denom is already neutralised by clamp).
    const zero = rowWindowFor(0, 5000, total, ROW_H, HEADER_H, OVERSCAN, 5000);
    // Mutation: remove the `denom > 0 ?` guard -> frac = 0/0 = NaN -> NaN bounds.
    expect(Number.isFinite(zero.firstRow)).toBe(true);
    expect(Number.isFinite(zero.lastRow)).toBe(true);
    expect(zero.firstRow).toBe(0);

    // A viewport taller than the capped content (denom < 0) also stays sane.
    const neg = rowWindowFor(100, 6000, total, ROW_H, HEADER_H, OVERSCAN, 5000);
    expect(neg.firstRow).toBe(0);
    expect(neg.lastRow).toBeLessThanOrEqual(total - 1);
  });
});

describe("scrollTopForRow", () => {
  const ROW_H = 28, HEADER_H = 32, OVERSCAN = 8;

  it("round-trips a mid row through rowWindowFor in scaled mode", () => {
    const total = 40_000_000;
    const clientHeight = 800;
    const cap = capForDpr(1);
    const contentHeight = contentHeightFor(total, ROW_H, HEADER_H, cap);
    for (const row of [0, 10_000_000, 25_000_000, total - 1]) {
      const st = scrollTopForRow(row, clientHeight, total, ROW_H, HEADER_H, contentHeight);
      const w = rowWindowFor(st, clientHeight, total, ROW_H, HEADER_H, OVERSCAN, contentHeight);
      // Mutation: use row*ROW_H unconditionally -> on a scaled fixture the row
      // is nowhere near the returned window.
      expect(row).toBeGreaterThanOrEqual(w.firstRow);
      expect(row).toBeLessThanOrEqual(w.lastRow);
    }
  });

  it("unscaled scrollTopForRow is row*rowH, clamped", () => {
    const total = 1000;
    const contentHeight = HEADER_H + total * ROW_H;
    expect(scrollTopForRow(50, 500, total, ROW_H, HEADER_H, contentHeight)).toBe(50 * ROW_H);
  });
});

describe("rowTopFor", () => {
  it("is absolute i*rowH unscaled, window-relative (i-firstRow)*rowH scaled", () => {
    expect(rowTopFor(100, 90, 28, false)).toBe(100 * 28);
    expect(rowTopFor(100, 90, 28, true)).toBe(10 * 28);
  });
});
