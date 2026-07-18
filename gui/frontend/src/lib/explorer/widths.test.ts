import { describe, it, expect } from "vitest";
import { columnWidths, prefixSums, MIN_W, MAX_W } from "./widths";
import type { Column } from "./types";

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

describe("columnWidths", () => {
  it("floors every column at MIN_W (no real header/type combination goes below it today)", () => {
    // Shortest possible header (empty name -> 16) against the narrowest
    // natural width (bool -> 90): still above MIN_W (80), so this documents
    // that the floor is a defensive clamp, not currently reachable via the
    // type table -- and confirms every width stays >= MIN_W regardless.
    const widths = columnWidths([
      col({ name: "", type: "bool" }),
      col({ name: "", type: "int" }),
      col({ name: "", type: "string" }),
    ]);
    for (const w of widths) expect(w).toBeGreaterThanOrEqual(MIN_W);
  });

  it("uses the header-text floor when a long name exceeds the type's natural width", () => {
    const longName = "a".repeat(30); // header = 16 + 30*7.2 = 232
    const [w] = columnWidths([col({ name: longName, type: "bool" })]); // natural = 90
    expect(w).toBe(232);
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

  it("is monotonically non-decreasing (each column has non-negative width)", () => {
    const sums = prefixSums([90, 120, 260, 80]);
    for (let i = 1; i < sums.length; i++) {
      expect(sums[i]).toBeGreaterThanOrEqual(sums[i - 1]);
    }
  });

  it("handles a single column", () => {
    expect(prefixSums([150])).toEqual([0, 150]);
  });
});
