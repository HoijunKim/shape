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
