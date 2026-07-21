import { describe, it, expect } from "vitest";
import {
  emptyDraft,
  newCondition,
  isConditionComplete,
  conditionError,
  buildFilter,
  type DraftCondition,
  type FilterDraft,
} from "./filterModel";

describe("emptyDraft", () => {
  it("returns an empty and-combined draft", () => {
    expect(emptyDraft()).toEqual({ combinator: "and", conditions: [] });
  });
});

describe("newCondition", () => {
  it("picks the type's default op and zeroed fields", () => {
    const c = newCondition(1, "age", "int");
    expect(c).toEqual({
      id: 1,
      path: "age",
      type: "int",
      op: "gte",
      text: "",
      num: "",
      bool: false,
      list: [],
      ci: false,
    });
  });

  it("defaults a string column to contains", () => {
    expect(newCondition(2, "name", "string").op).toBe("contains");
  });
});

describe("isConditionComplete", () => {
  const base = (overrides: Partial<DraftCondition>): DraftCondition => ({
    id: 1,
    path: "p",
    type: "string",
    op: "contains",
    text: "",
    num: "",
    bool: false,
    list: [],
    ci: false,
    ...overrides,
  });

  it("an isnull condition with empty everything is complete", () => {
    expect(isConditionComplete(base({ op: "isnull" }))).toBe(true);
  });

  it("a contains with text:'' is NOT complete", () => {
    expect(isConditionComplete(base({ op: "contains", text: "" }))).toBe(false);
  });

  it("a gte with num:'' is NOT complete", () => {
    expect(isConditionComplete(base({ type: "int", op: "gte", num: "" }))).toBe(false);
  });

  it("a gte with num:'18' IS complete", () => {
    expect(isConditionComplete(base({ type: "int", op: "gte", num: "18" }))).toBe(true);
  });

  it("a gte with num:'abc' is NOT complete", () => {
    expect(isConditionComplete(base({ type: "int", op: "gte", num: "abc" }))).toBe(false);
  });

  it("a bool op is complete regardless of bool value", () => {
    expect(isConditionComplete(base({ type: "bool", op: "bool", bool: false }))).toBe(true);
    expect(isConditionComplete(base({ type: "bool", op: "bool", bool: true }))).toBe(true);
  });

  it("an in with list:['',''] is NOT complete", () => {
    expect(isConditionComplete(base({ op: "in", list: ["", ""] }))).toBe(false);
  });

  it("an in with list:['a'] IS complete", () => {
    expect(isConditionComplete(base({ op: "in", list: ["a"] }))).toBe(true);
  });
});

describe("buildFilter", () => {
  it("builds the match-all empty Filter from an empty draft", () => {
    const f = buildFilter(emptyDraft());
    expect(f.combinator).toBe("and");
    expect(f.conditions === undefined || f.conditions.length === 0).toBe(true);
  });

  it("omits incomplete conditions", () => {
    const draft: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "deleted_at", type: "string", op: "notnull", text: "", num: "", bool: false, list: [], ci: false },
        { id: 2, path: "name", type: "string", op: "contains", text: "", num: "", bool: false, list: [], ci: false },
      ],
    };
    const f = buildFilter(draft);
    expect(f.conditions?.length).toBe(1);
    expect(f.conditions?.[0]).toEqual({ path: "deleted_at", op: "notnull" });
  });

  it("numeric gte emits value.num as a JS number, not a string", () => {
    const draft: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "age", type: "int", op: "gte", text: "", num: "18", bool: false, list: [], ci: false },
      ],
    };
    const f = buildFilter(draft);
    expect(f.conditions?.[0]).toEqual({ path: "age", op: "gte", value: { kind: "number", num: 18 } });
    expect(typeof f.conditions?.[0].value?.num).toBe("number");
  });

  // The eq/ne op id is shared by numeric and string columns (Task 1's
  // operators.ts callout), but the Value.kind buildFilter emits must be
  // picked from the COLUMN type, not the op id -- a numeric eq that leaked
  // kind:"string" would silently zero-match in the engine (filter.go's
  // OpEq/OpNe branch only reads Value.Num when Kind is ValNumber).
  it("numeric eq emits value:{kind:'number', num} keyed off the column type", () => {
    const draft: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "age", type: "int", op: "eq", text: "", num: "42", bool: false, list: [], ci: false },
      ],
    };
    const f = buildFilter(draft);
    expect(f.conditions?.[0]).toEqual({ path: "age", op: "eq", value: { kind: "number", num: 42 } });
  });

  it("string contains with ci:true includes ci; ci:false omits the key", () => {
    const draftCi: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "name", type: "string", op: "contains", text: "foo", num: "", bool: false, list: [], ci: true },
      ],
    };
    const fCi = buildFilter(draftCi);
    expect(fCi.conditions?.[0]).toEqual({
      path: "name",
      op: "contains",
      value: { kind: "string", str: "foo" },
      ci: true,
    });

    const draftNoCi: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "name", type: "string", op: "contains", text: "foo", num: "", bool: false, list: [], ci: false },
      ],
    };
    const fNoCi = buildFilter(draftNoCi);
    expect(fNoCi.conditions?.[0]).toEqual({
      path: "name",
      op: "contains",
      value: { kind: "string", str: "foo" },
    });
    expect(fNoCi.conditions?.[0]).not.toHaveProperty("ci");
  });

  it("isnull emits no value key", () => {
    const draft: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "email", type: "string", op: "isnull", text: "", num: "", bool: false, list: [], ci: false },
      ],
    };
    const f = buildFilter(draft);
    expect(f.conditions?.[0]).toEqual({ path: "email", op: "isnull" });
    expect(f.conditions?.[0]).not.toHaveProperty("value");
  });

  it("in on a numeric column tags each list element kind:number", () => {
    const draft: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "age", type: "int", op: "in", text: "", num: "", bool: false, list: ["1", "2"], ci: false },
      ],
    };
    const f = buildFilter(draft);
    expect(f.conditions?.[0].value?.list).toEqual([
      { kind: "number", num: 1 },
      { kind: "number", num: 2 },
    ]);
  });

  it("in drops empty list entries and tags string columns kind:string", () => {
    const draft: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "name", type: "string", op: "in", text: "", num: "", bool: false, list: ["a", "", ""], ci: false },
      ],
    };
    const f = buildFilter(draft);
    expect(f.conditions?.[0].value?.list).toEqual([{ kind: "string", str: "a" }]);
  });

  it("bool emits value:{kind:'bool', bool:false} -- false is a real operand", () => {
    const draft: FilterDraft = {
      combinator: "and",
      conditions: [
        { id: 1, path: "active", type: "bool", op: "bool", text: "", num: "", bool: false, list: [], ci: false },
      ],
    };
    const f = buildFilter(draft);
    expect(f.conditions?.[0]).toEqual({ path: "active", op: "bool", value: { kind: "bool", bool: false } });
    expect(f.conditions?.[0]).toHaveProperty("value.bool", false);
  });

  it("sets combinator:'or' from the draft, and never sets groups or negate", () => {
    const draft: FilterDraft = {
      combinator: "or",
      conditions: [
        { id: 1, path: "email", type: "string", op: "isnull", text: "", num: "", bool: false, list: [], ci: false },
      ],
    };
    const f = buildFilter(draft);
    expect(f.combinator).toBe("or");
    expect(f).not.toHaveProperty("groups");
    expect(f).not.toHaveProperty("negate");
  });
});

describe("conditionError", () => {
  it("regex with an unbalanced pattern returns a non-empty message", () => {
    const c: DraftCondition = {
      id: 1,
      path: "name",
      type: "string",
      op: "regex",
      text: "(",
      num: "",
      bool: false,
      list: [],
      ci: false,
    };
    expect(conditionError(c)).not.toBe("");
  });

  it("regex with a valid pattern returns ''", () => {
    const c: DraftCondition = {
      id: 1,
      path: "name",
      type: "string",
      op: "regex",
      text: "^a.*",
      num: "",
      bool: false,
      list: [],
      ci: false,
    };
    expect(conditionError(c)).toBe("");
  });

  it("gte with num:'abc' returns 'not a number'", () => {
    const c: DraftCondition = {
      id: 1,
      path: "age",
      type: "int",
      op: "gte",
      text: "",
      num: "abc",
      bool: false,
      list: [],
      ci: false,
    };
    expect(conditionError(c)).toBe("not a number");
  });

  it("contains with text:'' returns '' -- empty is omittable, not an error", () => {
    const c: DraftCondition = {
      id: 1,
      path: "name",
      type: "string",
      op: "contains",
      text: "",
      num: "",
      bool: false,
      list: [],
      ci: false,
    };
    expect(conditionError(c)).toBe("");
  });
});

describe("buildFilter -- coverage added in review of Task 2", () => {
  const cond = (over: Partial<DraftCondition>): DraftCondition => ({
    id: 1, path: "x", type: "string", op: "eq", text: "", num: "", bool: false, list: [], ci: false, ...over,
  });
  const draft = (conditions: DraftCondition[], combinator: "and" | "or" = "and"): FilterDraft => ({ combinator, conditions });

  it("string eq emits {kind:'string', str} off the type-forked else-branch (no num read)", () => {
    const f = buildFilter(draft([cond({ path: "name", type: "string", op: "eq", text: "foo" })])) as any;
    expect(f.conditions[0].value).toEqual({ kind: "string", str: "foo" });
    expect(f.conditions[0]).not.toHaveProperty("ci"); // ci off => no key
  });

  it("string ne with ci:true emits {kind:'string', str} plus ci:true", () => {
    const f = buildFilter(draft([cond({ path: "name", type: "string", op: "ne", text: "foo", ci: true })])) as any;
    expect(f.conditions[0].value).toEqual({ kind: "string", str: "foo" });
    expect(f.conditions[0].ci).toBe(true);
  });

  it("omits a complete-but-invalid regex (unbalanced paren), keeping a valid sibling", () => {
    const f = buildFilter(draft([
      cond({ path: "name", type: "string", op: "regex", text: "(" }),
      cond({ path: "email", type: "string", op: "notnull" }),
    ])) as any;
    expect(f.conditions).toHaveLength(1);
    expect(f.conditions[0].op).toBe("notnull");
  });

  it("drops a non-finite entry from a numeric `in` list (would coerce to num:NaN -> 0)", () => {
    const f = buildFilter(draft([cond({ path: "age", type: "int", op: "in", list: ["1", "abc", "2"] })])) as any;
    expect(f.conditions[0].value.list).toEqual([{ kind: "number", num: 1 }, { kind: "number", num: 2 }]);
  });

  it("treats an all-non-numeric `in` list on a numeric column as incomplete (omitted)", () => {
    expect(isConditionComplete(cond({ type: "int", op: "in", list: ["abc", ""] }))).toBe(false);
    const f = buildFilter(draft([cond({ path: "age", type: "int", op: "in", list: ["abc"] })])) as any;
    expect(f.conditions === undefined || f.conditions.length === 0).toBe(true);
  });
});
