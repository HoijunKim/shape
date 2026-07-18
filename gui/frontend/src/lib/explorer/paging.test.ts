import { describe, it, expect } from "vitest";
import { pageRowsFor, pageIndexOf, pagesForRange, PageCache, rowLocation, reconcileEof } from "./paging";

describe("pageRowsFor", () => {
  it("clamps to the max for narrow tables", () => {
    expect(pageRowsFor(1)).toBe(200);
    expect(pageRowsFor(20)).toBe(200);
  });
  it("shrinks as columns grow", () => {
    expect(pageRowsFor(512)).toBe(Math.floor(30000 / 512));
  });
  it("clamps to the min for very wide tables", () => {
    expect(pageRowsFor(5000)).toBe(40);
  });
  it("never returns 0 for a degenerate column count", () => {
    expect(pageRowsFor(0)).toBe(200);
  });
  // M3: pageRowsFor(0) alone can't fail even with the Math.max(1, ...) guard
  // removed (30000/0 === Infinity clamps to 200 anyway). These two
  // discriminate the guard and the `| 0` truncation instead.
  it("clamps a negative column count the same as the guard would (discriminates the Math.max(1, ...) guard)", () => {
    // With the guard: cols = max(1, -5) = 1 -> floor(30000/1) clamped to 200.
    // Without it: cols = -5 -> floor(30000/-5) = -6000 clamped to 40.
    expect(pageRowsFor(-5)).toBe(200);
  });
  it("truncates a fractional column count (discriminates the `| 0`)", () => {
    // With `| 0`: cols = 200 -> floor(30000/200) = 150.
    // Without truncation: cols = 200.5 -> floor(30000/200.5) = 149.
    expect(pageRowsFor(200.5)).toBe(150);
  });
});

describe("pageIndexOf", () => {
  it("maps rows to their page", () => {
    expect(pageIndexOf(0, 100)).toBe(0);
    expect(pageIndexOf(99, 100)).toBe(0);
    expect(pageIndexOf(100, 100)).toBe(1);
  });
});

describe("pagesForRange", () => {
  it("covers a range spanning three pages", () => {
    expect(pagesForRange(95, 205, 100)).toEqual([0, 1, 2]);
  });
  it("returns one page for a range inside one page", () => {
    expect(pagesForRange(10, 20, 100)).toEqual([0]);
  });
  // M7: negative first, and last < first, were previously untested.
  it("clamps a negative first to 0", () => {
    expect(pagesForRange(-50, 10, 100)).toEqual([0]);
  });
  it("returns no pages when last < first", () => {
    expect(pagesForRange(500, 10, 100)).toEqual([]);
  });
});

describe("rowLocation", () => {
  it("locates a row within its page (store.ts's rowAt arithmetic, extracted)", () => {
    expect(rowLocation(0, 100)).toEqual({ page: 0, offset: 0 });
    expect(rowLocation(99, 100)).toEqual({ page: 0, offset: 99 });
    expect(rowLocation(100, 100)).toEqual({ page: 1, offset: 0 });
    expect(rowLocation(150, 100)).toEqual({ page: 1, offset: 50 });
    expect(rowLocation(250, 37)).toEqual({ page: 6, offset: 28 });
  });
});

describe("PageCache", () => {
  it("evicts least-recently-used beyond its cap", () => {
    const c = new PageCache(2);
    c.set(0, {} as any);
    c.set(1, {} as any);
    c.get(0);              // touch 0 so 1 is now the oldest
    c.set(2, {} as any);
    expect(c.has(0)).toBe(true);
    expect(c.has(1)).toBe(false);
    expect(c.has(2)).toBe(true);
    expect(c.size).toBe(2);
  });
  // M7: set() on an existing key must also refresh recency (paging.ts:48's
  // delete-then-set), not just get(). Without that delete, re-setting key 0
  // would leave it at its original (oldest) position, and the next eviction
  // would wrongly evict 0 instead of 1.
  it("re-setting an existing key refreshes its recency", () => {
    const c = new PageCache(2);
    c.set(0, {} as any);
    c.set(1, {} as any);
    c.set(0, {} as any); // re-set 0 without calling get() -- should become most-recent
    c.set(2, {} as any); // must evict 1 (the true LRU), not 0
    expect(c.has(0)).toBe(true);
    expect(c.has(1)).toBe(false);
    expect(c.has(2)).toBe(true);
  });
  it("clear() empties the cache", () => {
    const c = new PageCache(2);
    c.set(0, {} as any);
    c.set(1, {} as any);
    c.clear();
    expect(c.size).toBe(0);
    expect(c.has(0)).toBe(false);
    expect(c.has(1)).toBe(false);
  });
});

describe("reconcileEof (I2)", () => {
  it("never downgrades an already-exact total when a later page comes back truncated past EOF", () => {
    // A tier already knows the exact total (e.g. established at open() time,
    // or by a prior page's authoritative rs.total). An overscan/prefetch
    // request for a page past EOF returns 0 rows, truncated: true, and (per
    // store.ts's page fetches always passing wantTotal: false on a tier that
    // is NOT the memory tier) rs.total === -1. This landed page carries no
    // new information and must not touch a total the store already has
    // exactly.
    const out = reconcileEof({
      page: 3, pageRows: 40, rowsLength: 0, truncated: true,
      rsTotal: -1, rsTotalExact: false,
      priorTotal: 100, priorTotalExact: true,
    });
    expect(out.total).toBe(100);       // must stay 100, not collapse to pageEnd (120)
    expect(out.totalExact).toBe(true); // must stay exact, not flip to an estimate
  });

  it("still pins the real end when the prior total was only an estimate", () => {
    // Regression guard: the I2 fix must not disable the legitimate case it
    // was designed to preserve -- an inexact estimate DOES get corrected by
    // a short/empty landed page.
    const out = reconcileEof({
      page: 2, pageRows: 40, rowsLength: 15, truncated: true,
      rsTotal: -1, rsTotalExact: false,
      priorTotal: 1000, priorTotalExact: false,
    });
    expect(out.total).toBe(95);        // 2*40 + 15
    expect(out.totalExact).toBe(true); // rowsLength > 0
  });

  it("still projects a full page at the tail forward when the total is inexact", () => {
    const out = reconcileEof({
      page: 1, pageRows: 40, rowsLength: 40, truncated: false,
      rsTotal: -1, rsTotalExact: false,
      priorTotal: 80, priorTotalExact: false,
    });
    expect(out.total).toBe(120);       // pageEnd(80) + pageRows(40)
    expect(out.totalExact).toBe(false);
  });

  it("still trusts the backend's authoritative total when rs.total >= 0 (memory tier: always exact, even under wantTotal: false)", () => {
    const out = reconcileEof({
      page: 0, pageRows: 40, rowsLength: 40, truncated: false,
      rsTotal: 100, rsTotalExact: true,
      priorTotal: 999, priorTotalExact: false,
    });
    expect(out.total).toBe(100);
    expect(out.totalExact).toBe(true);
  });

  // I-A1: a page-aligned rescan-tier file (true row count an exact multiple
  // of pageRows) never produces a short-non-empty page -- the tail page is
  // always full, and the page after it is always entirely empty. Before this
  // fix, `totalExact` could only ever become true via `rowsLength > 0` or
  // `page === 0`, neither of which ever fires here, so the store converged
  // on the right NUMBER (500,000) but rendered `~500,000` forever. This is
  // the concrete T=500,000 P=200 scenario from the bug report: page 2499
  // (the true last page, [499800, 500000)) landed FULL and pushed the
  // running total to 500200 via the sibling `else if` branch's
  // `pageEnd + pageRows` projection; page 2500 ([500000, 500200)) then lands
  // empty.
  it("closes the tilde for a page-aligned exact-multiple file once the immediately-following empty page confirms the prior full page's proven end (I-A1)", () => {
    const out = reconcileEof({
      page: 2500, pageRows: 200, rowsLength: 0, truncated: true,
      rsTotal: -1, rsTotalExact: false,
      priorTotal: 500200, priorTotalExact: false, // set by page 2499's full-page projection
    });
    expect(out.total).toBe(500000); // pageEnd: page*pageRows + 0
    expect(out.totalExact).toBe(true); // MUST turn the tilde off, not stay stuck forever
  });

  // Guardrail for the fix above: it must not regress into "trust ANY empty
  // page at page > 0", which would be unsound -- an empty page far past a
  // wildly-wrong estimate (e.g. a bad rescan-tier fileSize/avgBytes seed
  // that let the scrollbar jump way past the true end) only proves
  // `total <= pageEnd`, not `total === pageEnd`. Here priorTotal (an
  // unrelated seed estimate) does NOT satisfy `pageEnd + pageRows`, so the
  // page must be treated the same as before this fix: an upper bound only.
  it("does NOT trust an empty page at page > 0 when priorTotal does not match the proven-full-page signature (guards against over-trusting every empty page)", () => {
    const out = reconcileEof({
      page: 500, pageRows: 200, rowsLength: 0, truncated: true,
      rsTotal: -1, rsTotalExact: false,
      priorTotal: 100000, // unrelated to pageEnd(100000) + pageRows(200) = 100200
      priorTotalExact: false,
    });
    expect(out.total).toBe(100000); // still snaps to the upper bound
    expect(out.totalExact).toBe(false); // but must NOT claim exactness
  });
});
