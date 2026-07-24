// @vitest-environment jsdom
//
// E7 edit toolbar. EditBar reads editedCount off the explorer singleton and
// calls revertAllEdits; the store is mocked to control the count and spy.
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";

const h = vi.hoisted(() => ({
  state: { editedCount: 0 },
  revertAllEdits: vi.fn(),
}));

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (s: any) => void) => {
      run(h.state);
      return () => {};
    },
    revertAllEdits: h.revertAllEdits,
  },
}));

import EditBar from "./EditBar.svelte";

let target: HTMLElement;
let cmp: any = null;

afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
  h.state.editedCount = 0;
  h.revertAllEdits.mockReset();
});

function mount(props: Record<string, unknown> = {}): HTMLElement {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new EditBar({ target, props });
  return target;
}

describe("EditBar (E7 edit toolbar)", () => {
  it("renders nothing when there are no edits", () => {
    h.state.editedCount = 0;
    const t = mount();
    expect(t.querySelector(".edit-bar")).toBeNull();
  });

  it("reports the edited-cell count", () => {
    h.state.editedCount = 3;
    const t = mount();
    expect(t.querySelector(".count")?.textContent).toContain("3 edited cells");
  });

  it("toggles the edited-only view on click", async () => {
    h.state.editedCount = 1;
    const t = mount({ editedOnly: false });
    const toggle = t.querySelector(".toggle") as HTMLButtonElement;
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    toggle.click();
    await tick();
    // Mutation: the click handler does not flip editedOnly -> stays "false".
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(toggle.classList.contains("active")).toBe(true);
  });

  it("reverts all and drops out of the edited-only view", async () => {
    h.state.editedCount = 2; // stays 2 (spy doesn't mutate) so the toolbar stays mounted
    const t = mount({ editedOnly: true });
    const toggle = t.querySelector(".toggle") as HTMLButtonElement;
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    (t.querySelectorAll(".edit-bar button")[1] as HTMLButtonElement).click(); // Revert all
    await tick();
    expect(h.revertAllEdits).toHaveBeenCalledTimes(1);
    // Mutation: revertAll leaves editedOnly true -> aria-pressed stays "true".
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
  });

  it("dispatches save when the Save button is clicked", async () => {
    h.state.editedCount = 1;
    const t = mount();
    const saves: number[] = [];
    cmp.$on("save", () => saves.push(1));
    (t.querySelector(".primary") as HTMLButtonElement).click();
    await tick();
    expect(saves).toHaveLength(1);
  });
});
