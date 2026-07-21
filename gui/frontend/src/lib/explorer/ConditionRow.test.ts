// @vitest-environment jsdom
//
// Task 6: ConditionRow is the first component in the app to mount a native
// input/select, and the plan review flagged one CRITICAL correctness rule --
// a column-select change must reset BOTH `op` (to the new type's default)
// AND `type` (to the new column's type), or a stale `type` silently mis-tags
// an `in`-list's elements downstream in buildFilter (numeric elements sent
// as strings, zero-match). These tests mount the REAL ConditionRow.svelte
// (not a mock) and drive it via DOM events, per the house pattern
// (KindChip.test.ts/StatusBar.test.ts): `new ConditionRow({target, props})`,
// `await tick()`, assert on `target.querySelector`, and capture dispatched
// events via `cmp.$on(...)` (StructureMap.test.ts's pattern) rather than
// re-implementing DraftCondition/operator logic in isolation.
import { describe, it, expect, afterEach } from "vitest";
import { tick } from "svelte";
import ConditionRow from "./ConditionRow.svelte";
import type { DraftCondition } from "./filterModel";
import type { Column } from "./types";

type Instance = {
  $set: (props: Record<string, unknown>) => void;
  $on: (event: string, cb: (e: CustomEvent) => void) => void;
  $destroy: () => void;
};

function makeColumn(path: string, type: string): Column {
  return {
    path,
    name: path,
    type,
    nullable: false,
    presence: 1,
    distinct: 1,
    container: false,
    index: 0,
  } as Column;
}

const columns: Column[] = [makeColumn("name", "string"), makeColumn("age", "int")];

function cond(over: Partial<DraftCondition> = {}): DraftCondition {
  return {
    id: 1,
    path: "name",
    type: "string",
    op: "contains",
    text: "",
    num: "",
    bool: false,
    list: [],
    ci: false,
    ...over,
  };
}

describe("ConditionRow", () => {
  let target: HTMLElement;
  let cmp: Instance | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  function mount(condition: DraftCondition, cols: Column[] = columns): HTMLElement {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new ConditionRow({ target, props: { condition, columns: cols } }) as unknown as Instance;
    return target;
  }

  it("changing the operator select to isnull hides the value input and emits change with op:isnull", async () => {
    const t = mount(cond({ op: "contains", text: "" }));

    // Sanity: a text input is present for the initial `contains` op.
    expect(t.querySelector('input[type="text"]')).toBeTruthy();

    let detail: DraftCondition | null = null;
    cmp!.$on("change", (e) => (detail = e.detail));

    const opSelect = t.querySelectorAll("select")[1] as HTMLSelectElement; // [0]=column, [1]=operator
    opSelect.value = "isnull";
    opSelect.dispatchEvent(new Event("change"));
    await tick();

    expect(t.querySelector('input[type="text"]')).toBeNull();
    expect(detail).toEqual(expect.objectContaining({ op: "isnull" }));
  });

  it("typing in the value input for a contains op emits change with text updated", async () => {
    const t = mount(cond({ op: "contains" }));

    let detail: DraftCondition | null = null;
    cmp!.$on("change", (e) => (detail = e.detail));

    const input = t.querySelector('input[type="text"]') as HTMLInputElement;
    input.value = "hello";
    input.dispatchEvent(new Event("input"));
    await tick();

    expect(detail).toEqual(expect.objectContaining({ text: "hello" }));
  });

  it("changing the column select from a string column to an int column resets op to gte and sets type to int", async () => {
    const t = mount(cond({ path: "name", type: "string", op: "contains", text: "foo" }));

    let detail: DraftCondition | null = null;
    cmp!.$on("change", (e) => (detail = e.detail));

    const colSelect = t.querySelectorAll("select")[0] as HTMLSelectElement;
    colSelect.value = "age";
    colSelect.dispatchEvent(new Event("change"));
    await tick();

    expect(detail).toEqual(
      expect.objectContaining({ path: "age", type: "int", op: "gte", text: "", num: "" })
    );
  });

  it("a regex op with an unbalanced-paren value renders the conditionError text and sets aria-invalid", async () => {
    const t = mount(cond({ op: "regex", text: "(" }));
    await tick();

    const error = t.querySelector(".error") as HTMLElement;
    expect(error).toBeTruthy();
    expect(error.textContent).not.toBe("");

    const input = t.querySelector('input[type="text"]') as HTMLInputElement;
    expect(input.getAttribute("aria-invalid")).toBe("true");
  });

  it("the remove button emits remove with the row's id", async () => {
    const t = mount(cond({ id: 42 }));

    let detail: { id: number } | null = null;
    cmp!.$on("remove", (e) => (detail = e.detail));

    const removeBtn = t.querySelector('button[aria-label="Remove condition"]') as HTMLButtonElement;
    removeBtn.click();
    await tick();

    expect(detail).toEqual({ id: 42 });
  });

  it("renders no ci toggle for a non-ci op (gte)", async () => {
    const t = mount(cond({ type: "int", op: "gte", num: "" }));
    await tick();
    expect(t.querySelector(".ci-toggle")).toBeNull();
  });

  it("renders a ci toggle for contains, and clicking it emits change with ci:true", async () => {
    const t = mount(cond({ op: "contains", ci: false }));
    await tick();

    const toggle = t.querySelector(".ci-toggle") as HTMLButtonElement;
    expect(toggle).toBeTruthy();
    expect(toggle.getAttribute("aria-pressed")).toBe("false");

    let detail: DraftCondition | null = null;
    cmp!.$on("change", (e) => (detail = e.detail));
    toggle.click();
    await tick();

    expect(detail).toEqual(expect.objectContaining({ ci: true }));
  });

  it("renders a two-state true/false select for a bool op", async () => {
    const t = mount(cond({ path: "active", type: "bool", op: "bool", bool: false }), [
      makeColumn("active", "bool"),
    ]);
    await tick();

    const valueSelect = t.querySelectorAll("select")[2] as HTMLSelectElement; // [0]=column, [1]=operator, [2]=value
    expect(Array.from(valueSelect.options).map((o) => o.value)).toEqual(["true", "false"]);
    expect(valueSelect.value).toBe("false");
  });

  it("splits a comma list input into trimmed entries and shows the parsed chip count", async () => {
    const t = mount(cond({ path: "age", type: "int", op: "in", list: [] }), [makeColumn("age", "int")]);
    await tick();

    let detail: DraftCondition | null = null;
    cmp!.$on("change", (e) => (detail = e.detail));

    const input = t.querySelector('input[type="text"]') as HTMLInputElement;
    input.value = "1, 2, 3";
    input.dispatchEvent(new Event("input"));
    await tick();

    expect(detail!.list).toEqual(["1", "2", "3"]);
    expect(t.querySelector(".chip-count")!.textContent).toBe("3 values");
  });

  it("renders no value input at all for isnull (arity none)", async () => {
    const t = mount(cond({ op: "isnull" }));
    await tick();
    expect(t.querySelector("input")).toBeNull();
  });

  // Review of Task 6: eq/ne are the ops the plan singles out as the
  // discriminator between a type-driven arity lookup (correct) and a
  // hardcoded arity-by-op (broken) -- same op id, different arity/ci per
  // column type. A ConditionRow-local regression that stopped calling
  // operatorsForType(condition.type) would pass every other test; these pin it.
  it("string eq renders a text value input with a ci toggle (arity text, ci true)", async () => {
    const t = mount(cond({ path: "name", type: "string", op: "eq" }));
    await tick();
    expect(t.querySelector('input[type="text"]')).toBeTruthy();
    expect(t.querySelector(".ci-toggle")).toBeTruthy();
  });

  it("numeric eq renders a decimal text input and NO ci toggle (arity number, ci false)", async () => {
    const t = mount(cond({ path: "age", type: "int", op: "eq", text: "", num: "" }));
    await tick();
    const input = t.querySelector('input[inputmode="decimal"]') as HTMLInputElement | null;
    expect(input).toBeTruthy();
    // Number arity must be a TEXT input, never type=number -- mid-typing
    // states (a lone "-", "1.") belong to the draft, not the browser.
    expect(input!.getAttribute("type")).toBe("text");
    expect(t.querySelector(".ci-toggle")).toBeNull();
  });
});
