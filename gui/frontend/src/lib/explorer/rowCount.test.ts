import { describe, it, expect } from "vitest";
import { formatRowCount } from "./rowCount";

describe("formatRowCount", () => {
  it("renders an unknown total (-1) as counting, regardless of the other flags", () => {
    expect(formatRowCount({ total: -1, totalExact: false, rowsLoaded: false })).toBe("counting…");
    expect(formatRowCount({ total: -1, totalExact: true, rowsLoaded: true })).toBe("counting…");
  });

  it("renders an exact total plainly, with thousands separators, never a '~'", () => {
    expect(formatRowCount({ total: 1234, totalExact: true, rowsLoaded: true })).toBe("1,234 rows");
    expect(formatRowCount({ total: 0, totalExact: true, rowsLoaded: false })).toBe("0 rows");
  });

  it("renders a known-but-inexact total with a leading '~'", () => {
    expect(formatRowCount({ total: 1234, totalExact: false, rowsLoaded: true })).toBe("~1,234 rows");
  });

  it("defers an unconfirmed rescan-tier zero estimate to counting until a page lands", () => {
    // total===0 && !totalExact is the pre-reconciliation rescan-tier estimate
    // (fileSize/avgBytes floored to 0); showing "No rows"/"~0 rows" here
    // would present an unconfirmed guess as a real state.
    expect(formatRowCount({ total: 0, totalExact: false, rowsLoaded: false })).toBe("counting…");
  });

  it("trusts a confirmed-but-still-inexact zero once a page has landed", () => {
    expect(formatRowCount({ total: 0, totalExact: false, rowsLoaded: true })).toBe("~0 rows");
  });

  it("never presents an estimate as exact: totalExact always wins over a plain zero-ish total", () => {
    // Regression guard against a naive `if (total === 0) return "0 rows"`
    // ahead of the totalExact check.
    const inexactZero = formatRowCount({ total: 0, totalExact: false, rowsLoaded: true });
    expect(inexactZero).not.toBe("0 rows");
    expect(inexactZero.startsWith("~")).toBe(true);
  });
});
