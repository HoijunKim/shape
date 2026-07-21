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

  it("existing unfiltered callers (no filterActive/counting/matchCount/matchExact passed at all) are unaffected by the new branch", () => {
    // Sanity that the new filtered branch is purely additive: omitting the
    // new optional fields entirely must behave exactly as every fixture
    // above already pins.
    expect(formatRowCount({ total: 1234, totalExact: true, rowsLoaded: true })).toBe("1,234 rows");
  });
});

// E3 Task 8: the filtered-count branch. filterActive gates a SEPARATE
// rendering path from the unfiltered one above -- it must never regress the
// byte-identical unfiltered strings pinned above (none of those fixtures
// pass filterActive, so they exercise the untouched original branch).
describe("formatRowCount — filtered (E3 Task 8)", () => {
  it("renders counting… while a filtered count is in flight, even over a stale exact total/matchCount", () => {
    const text = formatRowCount({
      total: 999, totalExact: true, rowsLoaded: true,
      filterActive: true, counting: true, matchCount: 5, matchExact: true,
    });
    expect(text).toContain("counting");
  });

  it("renders a known exact filtered match count plainly, no tilde", () => {
    expect(
      formatRowCount({
        total: -1, totalExact: false, rowsLoaded: true,
        filterActive: true, counting: false, matchCount: 42, matchExact: true,
      }),
    ).toBe("42 rows");
  });

  it("renders a known inexact filtered match count with a leading tilde", () => {
    expect(
      formatRowCount({
        total: -1, totalExact: false, rowsLoaded: true,
        filterActive: true, counting: false, matchCount: 42, matchExact: false,
      }),
    ).toBe("~42 rows");
  });

  it("falls back to total/totalExact when matchCount is unknown (memory tier: CountMatches never runs, but filtered page 0 already wrote the exact filtered total there)", () => {
    expect(
      formatRowCount({
        total: 7, totalExact: true, rowsLoaded: true,
        filterActive: true, counting: false, matchCount: -1, matchExact: false,
      }),
    ).toBe("7 rows");
  });

  it("renders counting… when filtered and neither matchCount nor total is known yet", () => {
    expect(
      formatRowCount({
        total: -1, totalExact: false, rowsLoaded: false,
        filterActive: true, counting: false, matchCount: -1, matchExact: false,
      }),
    ).toBe("counting…");
  });
});
