// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { tick } from "svelte";

const h = vi.hoisted(() => ({ getColumnStats: vi.fn() }));
vi.mock("./store", () => ({ explorer: { getColumnStats: h.getColumnStats } }));
// FieldDetail renders the card; stub it so this test targets the panel, not the
// charts (and so a minimal card cannot crash the real FieldDetail).
vi.mock("../FieldDetail.svelte", async () => {
  const Stub = (await import("./__fixtures__/CardStub.svelte")).default;
  return { default: Stub };
});

import FieldStatsPanel from "./FieldStatsPanel.svelte";

let target: HTMLElement;
let cmp: any = null;
afterEach(() => { cmp?.$destroy(); cmp = null; target?.remove(); h.getColumnStats.mockReset(); });

function mount(path: string) {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new FieldStatsPanel({ target, props: { path } });
  return target;
}
const flush = () => new Promise((r) => setTimeout(r, 0));

describe("FieldStatsPanel (E8)", () => {
  it("fetches the path's stats and renders the card", async () => {
    h.getColumnStats.mockResolvedValue({ card: { path: "n" }, found: true });
    const t = mount("n");
    await flush(); await tick();
    expect(h.getColumnStats).toHaveBeenCalledWith("n");
    expect(t.querySelector(".card-stub")?.getAttribute("data-path")).toBe("n");
  });

  it("shows a not-found message when the path is not a source field, and does NOT render the card", async () => {
    h.getColumnStats.mockResolvedValue({ card: { path: "" }, found: false });
    const t = mount("proj");
    await flush(); await tick();
    expect(t.querySelector(".stats-empty")).toBeTruthy();
    // Mutation (Step 5): render the card ignoring `found` -> a .card-stub appears here.
    expect(t.querySelector(".card-stub")).toBeNull();
  });
});
