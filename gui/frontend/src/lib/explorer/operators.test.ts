import { describe, it, expect } from "vitest";
import { operatorsForType, defaultOpForType, OP_LABELS } from "./operators";

describe("operatorsForType", () => {
  it("returns the 9 numeric op ids in order for int and float", () => {
    const numericIds = ["eq", "ne", "lt", "lte", "gt", "gte", "in", "isnull", "notnull"];
    expect(operatorsForType("int").map((o) => o.id)).toEqual(numericIds);
    expect(operatorsForType("float").map((o) => o.id)).toEqual(numericIds);
  });

  it("gives numeric ops the right arity", () => {
    const ops = operatorsForType("int");
    const byId = Object.fromEntries(ops.map((o) => [o.id, o]));
    expect(byId["in"].arity).toBe("list");
    expect(byId["gte"].arity).toBe("number");
    expect(byId["isnull"].arity).toBe("none");
  });

  it("returns the 7 string op ids in order", () => {
    expect(operatorsForType("string").map((o) => o.id)).toEqual([
      "eq",
      "ne",
      "contains",
      "regex",
      "in",
      "isnull",
      "notnull",
    ]);
  });

  it("gives string ops the right arity and case-insensitive flag", () => {
    const ops = operatorsForType("string");
    const byId = Object.fromEntries(ops.map((o) => [o.id, o]));
    expect(byId["contains"].arity).toBe("text");
    expect(byId["contains"].ci).toBe(true);
    expect(byId["regex"].ci).toBe(true);
    expect(byId["isnull"].ci).toBe(false);
  });

  it("returns exactly bool, isnull, notnull for bool columns", () => {
    expect(operatorsForType("bool").map((o) => o.id)).toEqual(["bool", "isnull", "notnull"]);
    const ops = operatorsForType("bool");
    const byId = Object.fromEntries(ops.map((o) => [o.id, o]));
    expect(byId["bool"].arity).toBe("bool");
  });

  it("returns only isnull/notnull for container and mixed types", () => {
    for (const t of ["object", "array", "mixed", "null"]) {
      expect(operatorsForType(t).map((o) => o.id)).toEqual(["isnull", "notnull"]);
    }
  });

  it("falls back to isnull/notnull for an unrecognized column type", () => {
    expect(operatorsForType("wat").map((o) => o.id)).toEqual(["isnull", "notnull"]);
  });
});

describe("defaultOpForType", () => {
  it("picks a sensible default op per type", () => {
    expect(defaultOpForType("int")).toBe("gte");
    expect(defaultOpForType("string")).toBe("contains");
    expect(defaultOpForType("bool")).toBe("bool");
    expect(defaultOpForType("object")).toBe("isnull");
  });
});

describe("OP_LABELS", () => {
  it("has a label for all 12 OpIds (guards against label-less ops shipping)", () => {
    expect(Object.keys(OP_LABELS).length).toBe(12);
  });

  it("has no orphan op: every id returned by operatorsForType is a key of OP_LABELS", () => {
    const allTypes = ["int", "float", "string", "bool", "object", "array", "null", "mixed", "wat"];
    for (const t of allTypes) {
      for (const op of operatorsForType(t)) {
        expect(OP_LABELS).toHaveProperty(op.id);
      }
    }
  });
});
