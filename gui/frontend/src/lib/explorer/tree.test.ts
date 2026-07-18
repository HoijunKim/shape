import { describe, it, expect } from "vitest";
import { buildTree } from "./tree";

const f = (path: string) => ({ path, types: [], presence: 1, nullRate: 0, distinct: 0, distinctExact: true, drift: false } as any);

describe("buildTree", () => {
  it("nests dotted paths under synthetic parents", () => {
    const t = buildTree([f("user.name"), f("user.age"), f("id")]);
    expect(t.map((n) => n.name)).toEqual(["user", "id"]);
    expect(t[0].field).toBe(null);            // synthetic interior node
    expect(t[0].children.map((n) => n.name)).toEqual(["name", "age"]);
    expect(t[1].children).toEqual([]);
  });
  it("keeps a real field that also has children", () => {
    const t = buildTree([f("a"), f("a.b")]);
    expect(t[0].field).not.toBe(null);        // 'a' is a drifting path: field AND parent
    expect(t[0].children.map((n) => n.name)).toEqual(["b"]);
  });
  it("treats array-element segments as one segment", () => {
    const t = buildTree([f("items[].sku")]);
    expect(t[0].name).toBe("items[]");
    expect(t[0].children[0].name).toBe("sku");
  });
  it("preserves input order, which is the profiler's alphabetized order", () => {
    const t = buildTree([f("b"), f("a")]);
    expect(t.map((n) => n.name)).toEqual(["b", "a"]);
  });
  // M7: depth >= 3 was previously uncovered -- the `siblings = node.children`
  // descent (tree.ts:34) is the only non-trivial line in the function, and a
  // bug that attached grandchildren to `roots` instead of their parent would
  // pass every test above.
  it("attaches grandchildren under their parent, not under roots, at depth >= 3", () => {
    const t = buildTree([f("a.b.c"), f("a.b.d"), f("a.e")]);
    expect(t.map((n) => n.name)).toEqual(["a"]); // exactly one root
    const a = t[0];
    expect(a.children.map((n) => n.name)).toEqual(["b", "e"]);
    const b = a.children[0];
    expect(b.field).toBe(null); // synthetic interior node, no field of its own
    expect(b.children.map((n) => n.name)).toEqual(["c", "d"]);
    expect(b.children.map((n) => n.path)).toEqual(["a.b.c", "a.b.d"]);
    expect(a.children[1].children).toEqual([]); // "a.e" is a leaf
  });
});
