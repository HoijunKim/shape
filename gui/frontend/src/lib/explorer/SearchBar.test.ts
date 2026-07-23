// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";
import SearchBar from "./SearchBar.svelte";
import { explorer } from "./store";

// The store pulls in the Wails bridge; mock it so importing store.ts under
// jsdom does not reach for window.go / window.runtime. The test spies on the
// REAL explorer.setSearch, so the mocked bindings are never actually called.
vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(), QueryRows: vi.fn(), CloseSource: vi.fn(), Cancel: vi.fn(),
  CountMatches: vi.fn(), Codegen: vi.fn(), ExportQuery: vi.fn(), GetCell: vi.fn(),
}));
vi.mock("../../../wailsjs/runtime", () => ({ EventsOn: vi.fn(() => () => {}) }));

let target: HTMLElement;
let comp: SearchBar | undefined;
let setSearchSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  vi.useFakeTimers();
  target = document.createElement("div");
  document.body.appendChild(target);
  setSearchSpy = vi.spyOn(explorer, "setSearch").mockImplementation(() => {});
  comp = new SearchBar({ target, props: {} });
});

afterEach(() => {
  comp?.$destroy();
  comp = undefined;
  target.remove();
  setSearchSpy.mockRestore();
  vi.useRealTimers();
});

function input(): HTMLInputElement {
  return target.querySelector("input.search-input") as HTMLInputElement;
}

describe("SearchBar", () => {
  it("calls setSearch after the debounce with the typed text", () => {
    const el = input();
    el.value = "london";
    el.dispatchEvent(new Event("input"));
    expect(setSearchSpy).not.toHaveBeenCalled(); // debounced, not immediate
    vi.advanceTimersByTime(260);
    expect(setSearchSpy).toHaveBeenCalledWith("london");
  });

  it("clearing via the ✕ button calls setSearch(\"\")", async () => {
    const el = input();
    el.value = "x";
    el.dispatchEvent(new Event("input"));
    await tick(); // the clear button renders once query is non-empty
    const clearBtn = target.querySelector("button.clear") as HTMLButtonElement;
    expect(clearBtn, "a clear button must appear once there is text").toBeTruthy();
    setSearchSpy.mockClear();
    clearBtn.click();
    vi.advanceTimersByTime(260);
    expect(setSearchSpy).toHaveBeenCalledWith("");
  });

  // Mutation: drop the debounce's onDestroy cancel -> the "late" search fires
  // AFTER unmount, applying against whatever file is open next.
  it("does not fire a stale setSearch after unmount", () => {
    const el = input();
    el.value = "late";
    el.dispatchEvent(new Event("input")); // arm the 250ms debounce
    comp!.$destroy(); // unmount before it elapses
    comp = undefined;
    vi.advanceTimersByTime(500);
    expect(setSearchSpy).not.toHaveBeenCalled();
  });
});
