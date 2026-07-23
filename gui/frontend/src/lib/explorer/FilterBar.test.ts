// @vitest-environment jsdom
//
// Task 7: FilterBar is the assembly point -- filterModel's draft/buildFilter,
// debounce, and ConditionRow all meet here, wired to the real `explorer`
// store singleton (the same pattern Explorer.test.ts/store.test.ts already
// use: mock the Wails bridge, drive the real store). Fake timers stand in
// for the 250ms debounce window.
//
// The REQUIRED teardown test (review F1) guards the exact bug the plan
// review found: opening file B unmounts FilterBar A (Explorer's
// `{#if status === "ready"}`), and without `onDestroy(() => debouncedApply
// .cancel())`, A's still-armed timer fires `explorer.setFilter` against
// file B's handle after A is already gone. `$destroy()` here simulates that
// unmount directly, without needing a second real open().
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";
import FilterBar from "./FilterBar.svelte";
import { explorer } from "./store";
import type { Column } from "./types";
import { OpenSource, QueryRows, CloseSource } from "../../../wailsjs/go/main/App";

vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
  CountMatches: vi.fn(),
  ExportQuery: vi.fn(),
  // E5: store.ts calls Codegen; a bare vi.fn() resolves undefined and the
  // first property read on the result throws.
  Codegen: vi.fn(() => Promise.resolve({ jq: ".", sql: "SELECT * FROM data;", warnings: [] })),
}));

function makeColumn(path: string, type: string): Column {
  return {
    path, name: path, type, nullable: true, presence: 1, distinct: 1,
    container: false, index: 0,
  } as Column;
}

const ageColumn = makeColumn("age", "int");
const nameColumn = makeColumn("name", "string");

function openResultFor(handle: string, columns: Column[]): any {
  return {
    handle, format: "ndjson", tier: "memory", columns,
    profile: { records: 1, skipped: 0, fields: [] },
    sampled: false, rowEstimate: 1, rowExact: true, warnings: [],
    columnsTruncated: false, totalPaths: columns.length,
  };
}

function rowSetFor(columns: Column[]): any {
  return {
    columns, rows: [{ index: 0, cells: columns.map(() => ({ kind: "string", str: "x" })) }],
    offset: 0, total: 1, totalExact: true, scanned: 1, truncated: false,
    elapsedMs: 0, columnsTruncated: false, totalPaths: columns.length,
  };
}

type Instance = { $destroy: () => void };

describe("FilterBar", () => {
  let target: HTMLElement;
  let cmp: Instance | null = null;

  beforeEach(async () => {
    vi.mocked(OpenSource).mockReset();
    vi.mocked(QueryRows).mockReset();
    vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
    await explorer.close();
    vi.mocked(CloseSource).mockClear(); // don't count close()'s own no-op call

    // A real, memory-tier "open" file behind the store -- setFilter() reads
    // handle/tier off it, and memory-tier skips Task 5's CountMatches
    // entirely, so this suite never needs to mock that endpoint.
    const columns = [ageColumn, nameColumn];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", columns));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));
    await explorer.open("file.ndjson");

    target = document.createElement("div");
    document.body.appendChild(target);

    vi.useFakeTimers();
  });

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target.remove();
    vi.useRealTimers();
  });

  function mount(columns: Column[], open = true): HTMLElement {
    cmp = new FilterBar({ target, props: { columns, open } }) as unknown as Instance;
    return target;
  }

  it("renders nothing when open is false", async () => {
    const t = mount([ageColumn], false);
    await tick();
    expect(t.textContent?.trim()).toBe("");
  });

  it("adding a condition and typing a complete value calls explorer.setFilter with a non-empty filter after the debounce", async () => {
    const setFilterSpy = vi.spyOn(explorer, "setFilter");
    const t = mount([ageColumn, nameColumn]);
    await tick();

    const addBtn = t.querySelector("button.add-condition") as HTMLButtonElement;
    addBtn.click();
    await tick();

    const valueInput = t.querySelector('input[aria-label="Value"]') as HTMLInputElement;
    expect(valueInput).toBeTruthy();
    valueInput.value = "20";
    valueInput.dispatchEvent(new Event("input"));
    await tick();

    // Debounce window hasn't elapsed yet.
    expect(setFilterSpy).not.toHaveBeenCalled();

    vi.advanceTimersByTime(250);
    await tick();

    expect(setFilterSpy).toHaveBeenCalledTimes(1);
    const filter = setFilterSpy.mock.calls[0][0] as any;
    expect(filter.conditions).toHaveLength(1);
    expect(filter.conditions[0]).toEqual(
      expect.objectContaining({ path: "age", op: "gte", value: { kind: "number", num: 20 } }),
    );

    setFilterSpy.mockRestore();
  });

  it("two conditions with the combinator set to 'or' build a Filter with combinator:'or' and two conditions", async () => {
    const setFilterSpy = vi.spyOn(explorer, "setFilter");
    const t = mount([ageColumn]);
    await tick();

    const addBtn = t.querySelector("button.add-condition") as HTMLButtonElement;
    addBtn.click();
    await tick();
    addBtn.click();
    await tick();

    const valueInputs = t.querySelectorAll('input[aria-label="Value"]');
    expect(valueInputs.length).toBe(2);
    (valueInputs[0] as HTMLInputElement).value = "20";
    valueInputs[0].dispatchEvent(new Event("input"));
    await tick();
    (valueInputs[1] as HTMLInputElement).value = "5";
    valueInputs[1].dispatchEvent(new Event("input"));
    await tick();

    const combinatorSelect = t.querySelector('select[aria-label="Combinator"]') as HTMLSelectElement;
    combinatorSelect.value = "or";
    combinatorSelect.dispatchEvent(new Event("change"));
    await tick();

    vi.advanceTimersByTime(250);
    await tick();

    expect(setFilterSpy).toHaveBeenCalled();
    const filter = setFilterSpy.mock.calls.at(-1)![0] as any;
    expect(filter.combinator).toBe("or");
    expect(filter.conditions).toHaveLength(2);

    setFilterSpy.mockRestore();
  });

  it("Clear calls setFilter with a match-all (empty) filter and empties the rows", async () => {
    const setFilterSpy = vi.spyOn(explorer, "setFilter");
    const t = mount([ageColumn]);
    await tick();

    const addBtn = t.querySelector("button.add-condition") as HTMLButtonElement;
    addBtn.click();
    await tick();

    const valueInput = t.querySelector('input[aria-label="Value"]') as HTMLInputElement;
    valueInput.value = "20";
    valueInput.dispatchEvent(new Event("input"));
    await tick();

    expect(t.querySelectorAll(".condition-row").length).toBe(1);

    const clearBtn = t.querySelector("button.clear") as HTMLButtonElement;
    clearBtn.click();
    await tick();

    expect(t.querySelectorAll(".condition-row").length).toBe(0);

    vi.advanceTimersByTime(250);
    await tick();

    expect(setFilterSpy).toHaveBeenCalled();
    const filter = setFilterSpy.mock.calls.at(-1)![0] as any;
    expect(filter.conditions ?? []).toHaveLength(0);

    setFilterSpy.mockRestore();
  });

  it("a half-typed invalid regex alongside a complete notnull fires setFilter with only the notnull condition", async () => {
    const setFilterSpy = vi.spyOn(explorer, "setFilter");
    const t = mount([nameColumn]);
    await tick();

    const addBtn = t.querySelector("button.add-condition") as HTMLButtonElement;
    addBtn.click(); // row 1: name/string, default op "contains"
    await tick();
    addBtn.click(); // row 2: name/string, default op "contains"
    await tick();

    const opSelects = t.querySelectorAll('select[aria-label="Operator"]');
    expect(opSelects.length).toBe(2);

    // Row 1 -> regex, with an unbalanced-paren (invalid) value.
    (opSelects[0] as HTMLSelectElement).value = "regex";
    opSelects[0].dispatchEvent(new Event("change"));
    await tick();
    const valueInput0 = t.querySelectorAll('input[aria-label="Value"]')[0] as HTMLInputElement;
    valueInput0.value = "(";
    valueInput0.dispatchEvent(new Event("input"));
    await tick();

    // Row 2 -> notnull (arity none, immediately complete, no value needed).
    (opSelects[1] as HTMLSelectElement).value = "notnull";
    opSelects[1].dispatchEvent(new Event("change"));
    await tick();

    vi.advanceTimersByTime(250);
    await tick();

    expect(setFilterSpy).toHaveBeenCalled();
    const filter = setFilterSpy.mock.calls.at(-1)![0] as any;
    // Regression: if buildFilter ever stopped omitting incomplete/invalid
    // conditions, this would be 2 (the invalid regex included too).
    expect(filter.conditions).toHaveLength(1);
    expect(filter.conditions[0].op).toBe("notnull");

    setFilterSpy.mockRestore();
  });

  // REQUIRED (review F1): teardown must cancel the pending debounce.
  it("destroying the component before the debounce elapses cancels the pending setFilter (teardown safety, review F1)", async () => {
    const setFilterSpy = vi.spyOn(explorer, "setFilter");
    const t = mount([ageColumn]);
    await tick();

    const addBtn = t.querySelector("button.add-condition") as HTMLButtonElement;
    addBtn.click();
    await tick();

    const valueInput = t.querySelector('input[aria-label="Value"]') as HTMLInputElement;
    valueInput.value = "20";
    valueInput.dispatchEvent(new Event("input"));
    await tick(); // debounce armed against this filter, not yet fired

    cmp!.$destroy();
    cmp = null;

    vi.advanceTimersByTime(250);
    await tick();

    // The stale filter must never reach the store once this instance is torn
    // down -- a fresh FilterBar for a newly-opened file must not be
    // retroactively filtered by a bar the user already navigated away from.
    expect(setFilterSpy).not.toHaveBeenCalled();

    setFilterSpy.mockRestore();
  });

  it("mounting with a stale seed already present does NOT re-seed (remount after a file switch, review)", async () => {
    // Opening a second file dips the store status ready->opening->ready, which
    // unmounts and remounts FilterBar while Explorer's `seed` local still holds
    // the PRIOR file's funnel click. A fresh bar must NOT append that stale
    // condition. prevSeedNonce initialized to the incoming seed's nonce makes
    // the mount-time reactive run a no-op. Mutation: init prevSeedNonce to -1
    // -> the mount run sees seed.nonce(5) !== -1 -> appends a stale row here.
    cmp = new FilterBar({
      target,
      props: { columns: [ageColumn], open: true, seed: { path: "age", type: "int", nonce: 5 } },
    }) as unknown as Instance;
    await tick();
    expect(target.querySelectorAll(".condition-row").length).toBe(0);
  });
});
