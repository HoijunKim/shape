import { describe, it, expect } from "vitest";
import { pageRowsFor, pageIndexOf, pagesForRange, PageCache, rowLocation } from "./paging";

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
});
