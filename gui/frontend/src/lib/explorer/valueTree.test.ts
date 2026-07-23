// @vitest-environment jsdom
//
// Both the pure valueTree.ts shaping AND the ValueTree.svelte component are
// tested here in one file: on a case-insensitive filesystem (Windows) a
// separate ValueTree.test.ts would collide with this name, so they share it.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";
import ValueTree from "./ValueTree.svelte";
import { ClipboardSetText } from "../../../wailsjs/runtime";
import { valueKind, isContainer, shapeChildren, childCount, scalarText, MAX_CHILDREN } from "./valueTree";

// The real runtime module dereferences window.runtime, absent in jsdom; the spy
// is also what the exact-JSON Copy assertion reads.
vi.mock("../../../wailsjs/runtime", () => ({
  ClipboardSetText: vi.fn(() => Promise.resolve(true)),
  EventsOn: vi.fn(() => () => {}),
}));

// --- pure shaping -----------------------------------------------------------

describe("valueKind", () => {
  it("classifies every JSON value shape", () => {
    expect(valueKind(null)).toBe("null");
    expect(valueKind([1, 2])).toBe("array");
    expect(valueKind({ a: 1 })).toBe("object");
    expect(valueKind("s")).toBe("string");
    expect(valueKind(3)).toBe("number");
    expect(valueKind(true)).toBe("bool");
    expect(valueKind(undefined)).toBe("null");
  });
});

describe("isContainer", () => {
  it("is true only for objects and arrays", () => {
    expect(isContainer({})).toBe(true);
    expect(isContainer([])).toBe(true);
    expect(isContainer("s")).toBe(false);
    expect(isContainer(null)).toBe(false);
    expect(isContainer(1)).toBe(false);
  });
});

describe("shapeChildren", () => {
  it("lists object entries by key", () => {
    const s = shapeChildren({ a: 1, b: "x" });
    expect(s.total).toBe(2);
    expect(s.hidden).toBe(0);
    expect(s.entries).toEqual([
      { key: "a", value: 1 },
      { key: "b", value: "x" },
    ]);
  });

  it("lists array elements by [i]", () => {
    const s = shapeChildren(["x", "y"]);
    expect(s.entries).toEqual([
      { key: "[0]", value: "x" },
      { key: "[1]", value: "y" },
    ]);
  });

  it("caps at `cap`, reporting the hidden remainder", () => {
    const big = Array.from({ length: 250 }, (_, i) => i);
    const s = shapeChildren(big, 100);
    expect(s.entries.length).toBe(100);
    expect(s.hidden).toBe(150);
    expect(s.total).toBe(250);
  });

  it("returns no children for a scalar", () => {
    expect(shapeChildren("s")).toEqual({ entries: [], hidden: 0, total: 0 });
    expect(shapeChildren(null)).toEqual({ entries: [], hidden: 0, total: 0 });
  });

  it("defaults the cap to MAX_CHILDREN", () => {
    const big = Array.from({ length: MAX_CHILDREN + 5 }, (_, i) => i);
    const s = shapeChildren(big);
    expect(s.entries.length).toBe(MAX_CHILDREN);
    expect(s.hidden).toBe(5);
  });
});

describe("childCount", () => {
  it("counts keys/elements, 0 for scalars", () => {
    expect(childCount({ a: 1, b: 2 })).toBe(2);
    expect(childCount([1, 2, 3])).toBe(3);
    expect(childCount("s")).toBe(0);
  });
});

describe("scalarText", () => {
  it("renders each scalar kind as display text", () => {
    expect(scalarText("hi")).toBe("hi");
    expect(scalarText(null)).toBe("null");
    expect(scalarText(true)).toBe("true");
    expect(scalarText(false)).toBe("false");
    expect(scalarText(42)).toBe("42");
  });
});

// --- ValueTree.svelte -------------------------------------------------------

let target: HTMLElement;
let comp: ValueTree | undefined;

beforeEach(() => {
  target = document.createElement("div");
  document.body.appendChild(target);
  vi.mocked(ClipboardSetText).mockClear();
});

afterEach(() => {
  comp?.$destroy();
  comp = undefined;
  target.remove();
});

function mount(props: { value: unknown; found?: boolean; name?: string; depth?: number; root?: boolean }): ValueTree {
  comp = new ValueTree({ target, props });
  return comp;
}

function caretButtons(): HTMLButtonElement[] {
  return Array.from(target.querySelectorAll("button.caret-row")) as HTMLButtonElement[];
}

describe("ValueTree component", () => {
  it("renders an object's keys", () => {
    mount({ value: { alpha: 1, beta: "x" } });
    expect(target.textContent).toContain("alpha");
    expect(target.textContent).toContain("beta");
  });

  it("expanding a collapsed nested object reveals its children", async () => {
    // depth 0 (root) and depth 1 auto-expand; the object at depth 2 starts
    // collapsed, so "deep"/"secret" are hidden until its caret is clicked.
    mount({ value: { outer: { inner: { deep: "secret" } } } });
    expect(target.textContent).not.toContain("secret");

    const innerCaret = caretButtons().find((b) => b.textContent?.includes("inner"));
    expect(innerCaret, "an 'inner' caret must exist").toBeTruthy();
    innerCaret!.click();
    await tick();
    expect(target.textContent).toContain("deep");
    expect(target.textContent).toContain("secret");
  });

  it("renders a scalar without an expander", () => {
    mount({ value: "just a string" });
    expect(caretButtons().length).toBe(0);
    expect(target.textContent).toContain("just a string");
  });

  it("Copy puts the EXACT JSON of the value on the clipboard", async () => {
    const value = { n: 42, s: "hi", nested: { a: [1, 2] } };
    mount({ value });
    const copyBtn = target.querySelector("button.copy") as HTMLButtonElement;
    expect(copyBtn).toBeTruthy();
    copyBtn.click();
    await tick();
    // Mutation guard: copying value.toString() would be "[object Object]".
    expect(vi.mocked(ClipboardSetText)).toHaveBeenCalledWith(JSON.stringify(value));
  });

  it("caps a huge array's rendered children and shows an N-more note", () => {
    const big = Array.from({ length: MAX_CHILDREN + 25 }, (_, i) => i);
    mount({ value: big });
    // Only MAX_CHILDREN element rows render (mutation: render all -> wrong count
    // and no note).
    expect(target.querySelectorAll(".row.leaf").length).toBe(MAX_CHILDREN);
    expect(target.textContent).toContain("25 more");
  });

  it("shows an empty state when the path resolved to no value", () => {
    mount({ value: null, found: false });
    expect(target.textContent).toContain("no value");
    expect(target.querySelectorAll(".row").length).toBe(0);
  });
});
