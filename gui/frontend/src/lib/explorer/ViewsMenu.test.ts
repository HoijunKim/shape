// @vitest-environment jsdom
//
// E11 saved-views menu. ViewsMenu reads $explorer.views + calls saveView/
// applyView/deleteView; the store is mocked to control the list + spy.
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";

const h = vi.hoisted(() => ({
  state: { status: "ready", views: [{ name: "v1" }, { name: "v2" }] as any[] },
  saveView: vi.fn(),
  applyView: vi.fn(),
  deleteView: vi.fn(),
}));

vi.mock("./store", () => ({
  explorer: {
    subscribe: (run: (s: any) => void) => {
      run(h.state);
      return () => {};
    },
    saveView: h.saveView,
    applyView: h.applyView,
    deleteView: h.deleteView,
  },
}));

import ViewsMenu from "./ViewsMenu.svelte";

let target: HTMLElement;
let cmp: any = null;
afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
  h.state = { status: "ready", views: [{ name: "v1" }, { name: "v2" }] };
  h.saveView.mockReset();
  h.applyView.mockReset();
  h.deleteView.mockReset();
});

function mount(props: Record<string, unknown> = {}): HTMLElement {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new ViewsMenu({ target, props: { open: true, ...props } });
  return target;
}

describe("ViewsMenu (E11)", () => {
  it("renders nothing when closed", () => {
    const t = mount({ open: false });
    expect(t.querySelector(".menu")).toBeNull();
  });

  it("saving a named view calls saveView(name)", async () => {
    const t = mount();
    const input = t.querySelector('[aria-label="View name"]') as HTMLInputElement;
    input.value = "my view";
    input.dispatchEvent(new Event("input"));
    await tick();
    (t.querySelector(".save-row .primary") as HTMLButtonElement).click();
    // Mutation: Save calls applyView instead of saveView -> this fails.
    expect(h.saveView).toHaveBeenCalledWith("my view");
  });

  it("Save is disabled when the name is blank", () => {
    const t = mount();
    expect((t.querySelector(".save-row .primary") as HTMLButtonElement).disabled).toBe(true);
  });

  it("clicking a view row applies THAT view", async () => {
    const t = mount();
    const rows = t.querySelectorAll(".view-row .apply");
    (rows[1] as HTMLButtonElement).click(); // v2
    // Mutation: apply passes the wrong name (e.g. always v.name[0]) -> fails.
    expect(h.applyView).toHaveBeenCalledWith("v2");
  });

  it("the × deletes THAT view", async () => {
    const t = mount();
    const dels = t.querySelectorAll(".view-row .del");
    (dels[0] as HTMLButtonElement).click(); // v1
    expect(h.deleteView).toHaveBeenCalledWith("v1");
  });

  it("shows an empty state when there are no views", () => {
    h.state = { status: "ready", views: [] };
    const t = mount();
    expect(t.querySelector(".empty")?.textContent).toContain("No saved views");
  });
});
