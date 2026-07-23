// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";
import TransformPanel from "./TransformPanel.svelte";
import { explorer } from "./store";
import type { Column } from "./types";

vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
  CountMatches: vi.fn(),
  // E5: store.ts calls Codegen; a bare vi.fn() resolves undefined and the
  // first property read on the result throws.
  Codegen: vi.fn(() => Promise.resolve({ jq: ".", sql: "SELECT * FROM data;", warnings: [] })),
  ExportQuery: vi.fn(),
}));
vi.mock("../../../wailsjs/runtime", () => ({ EventsOn: vi.fn(() => () => {}) }));

const COLS: Column[] = [
  { path: "id", name: "id", type: "int", nullable: false, presence: 1, distinct: 3, container: false, index: 0 },
  { path: "user.name", name: "name", type: "string", nullable: false, presence: 1, distinct: 3, container: false, index: 1 },
  { path: "meta.name", name: "name", type: "string", nullable: true, presence: 0.5, distinct: 2, container: false, index: 2 },
] as Column[];

let target: HTMLElement;
let panel: TransformPanel | undefined;
let setTransform: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  vi.useFakeTimers();
  target = document.createElement("div");
  document.body.appendChild(target);
  setTransform = vi.spyOn(explorer, "setTransform").mockImplementation(() => {});
});

afterEach(() => {
  panel?.$destroy();
  panel = undefined;
  target.remove();
  setTransform.mockRestore();
  vi.useRealTimers();
});

function mount(props: Record<string, unknown> = {}): TransformPanel {
  panel = new TransformPanel({ target, props: { columns: COLS, open: true, ...props } });
  return panel;
}

function rows(): HTMLElement[] {
  return Array.from(target.querySelectorAll(".column-row")) as HTMLElement[];
}

function checkbox(i: number): HTMLInputElement {
  return rows()[i].querySelector('input[type="checkbox"]') as HTMLInputElement;
}

function renameInput(i: number): HTMLInputElement {
  return rows()[i].querySelector(".rename") as HTMLInputElement;
}

describe("TransformPanel", () => {
  it("renders one row per BASE column, all shown, named by full path", () => {
    mount();
    expect(rows()).toHaveLength(3);
    expect(renameInput(1).value).toBe("user.name"); // not the leaf "name"
    expect(checkbox(0).checked).toBe(true);
  });

  it("hiding a column applies a select without it, after the debounce", async () => {
    mount();
    checkbox(1).click();
    await tick();
    expect(setTransform).not.toHaveBeenCalled(); // debounced
    vi.advanceTimersByTime(250);

    expect(setTransform).toHaveBeenCalledTimes(1);
    const [transform, projected] = setTransform.mock.calls[0] as any[];
    expect(transform.select.map((s: any) => s.path)).toEqual(["id", "meta.name"]);
    expect(projected).toHaveLength(2);
  });

  it("moving a column up reorders the emitted select", async () => {
    mount();
    (rows()[2].querySelector('[aria-label="Move meta.name up"]') as HTMLButtonElement).click();
    await tick();
    vi.advanceTimersByTime(250);

    const [transform] = setTransform.mock.calls.at(-1) as any[];
    expect(transform.select.map((s: any) => s.path)).toEqual(["id", "meta.name", "user.name"]);
  });

  it("renaming emits an explicit `as`", async () => {
    mount();
    const input = renameInput(1);
    input.value = "Full Name";
    input.dispatchEvent(new Event("input"));
    await tick();
    vi.advanceTimersByTime(250);

    const [transform] = setTransform.mock.calls.at(-1) as any[];
    expect(transform.select).toContainEqual({ path: "user.name", as: "Full Name" });
  });

  it("a duplicate name shows the error and applies NOTHING", async () => {
    mount();
    const input = renameInput(2);
    input.value = "user.name"; // collides with row 1
    input.dispatchEvent(new Event("input"));
    await tick();
    vi.advanceTimersByTime(250);

    expect(target.querySelector(".error")?.textContent).toContain("user.name");
    // Mutation that must break this: apply the draft regardless of
    // draftErrors -> setTransform is called with a colliding projection.
    expect(setTransform).not.toHaveBeenCalled();
  });

  it("dispatches `errors` on every change, INCLUDING the return to valid", async () => {
    const seen: string[][] = [];
    mount();
    panel!.$on("errors", (e) => seen.push(e.detail as string[]));

    const input = renameInput(2);
    input.value = "user.name";
    input.dispatchEvent(new Event("input"));
    await tick();
    expect(seen.at(-1)).toHaveLength(1);

    input.value = "meta.name"; // back to valid
    input.dispatchEvent(new Event("input"));
    await tick();
    // Mutation that must break this: dispatch only when errors are non-empty
    // -> the export button stays disabled forever after one bad rename.
    expect(seen.at(-1)).toEqual([]);
  });

  it("Reset restores identity, so the emitted transform is empty", async () => {
    mount();
    checkbox(1).click();
    await tick();
    vi.advanceTimersByTime(250);
    setTransform.mockClear();

    (target.querySelector(".reset") as HTMLButtonElement).click();
    await tick();
    vi.advanceTimersByTime(250);

    const [transform] = setTransform.mock.calls.at(-1) as any[];
    expect(transform).toEqual({});
  });

  it("cancels a pending apply when destroyed", async () => {
    mount();
    checkbox(1).click();
    await tick();
    panel!.$destroy();
    panel = undefined;
    vi.advanceTimersByTime(250);

    // Mutation that must break this: drop onDestroy(debouncedApply.cancel())
    // -> file A's projection is applied against file B's handle.
    expect(setTransform).not.toHaveBeenCalled();
  });

  it("renders nothing when closed", () => {
    mount({ open: false });
    expect(rows()).toHaveLength(0);
  });
});

// --- E4 branch review regressions -------------------------------------------

describe("TransformPanel error signalling (review findings 3 and 4)", () => {
  // NOTE: the onMount dispatch cannot be observed through this file's harness.
  // `new TransformPanel({ target })` mounts synchronously inside the
  // constructor, so a $on attached afterwards is already too late -- whereas
  // Svelte's compiler emits `new Child(props); child.$on(...); mount_component(...)`
  // for a real parent, which is exactly why onMount reaches it there. The
  // behaviour is covered end-to-end in Explorer.test.ts ("a transform error
  // from one file does not disable Export for the next").

  it("re-announces when the source columns change", async () => {
    mount();
    const seen: string[][] = [];
    panel!.$on("errors", (e) => seen.push(e.detail as string[]));

    panel!.$set({ columns: [COLS[0]] });
    await tick();
    expect(seen.at(-1)).toEqual([]);
  });

  it("does not apply an invalid draft via a debounce armed by the last valid edit", async () => {
    mount();
    const input = renameInput(1);

    // A valid rename arms the 250ms timer...
    input.value = "Full Name";
    input.dispatchEvent(new Event("input"));
    await tick();
    vi.advanceTimersByTime(120);

    // ...then a keystroke makes the draft invalid before it fires. The
    // debounce holds a reference to this very draft array, whose rows are
    // mutated in place, so the armed timer would apply the INVALID state.
    input.value = "";
    input.dispatchEvent(new Event("input"));
    await tick();
    vi.advanceTimersByTime(500);

    // Mutation that must break this: remove debouncedApply.cancel() from the
    // errors branch of apply() -> setTransform fires with a blank output name,
    // which the engine then resolves to the column's LEAF, silently colliding
    // with any sibling that shares it.
    expect(setTransform).not.toHaveBeenCalled();
  });

  it("keeps focus on a reorder button after the row moves", async () => {
    mount();
    const btn = rows()[1].querySelector('[aria-label="Move user.name up"]') as HTMLButtonElement;
    btn.focus();
    btn.click();
    await tick();
    await tick();

    // Mutation that must break this: use `disabled` instead of `aria-disabled`
    // (a disabled element cannot hold focus) or drop the post-move refocus ->
    // focus falls to <body> and keyboard reordering stops after one press.
    expect(document.activeElement).toBe(btn);
  });
});
