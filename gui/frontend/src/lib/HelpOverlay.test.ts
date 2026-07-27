// @vitest-environment jsdom
import { describe, it, expect, afterEach } from "vitest";
import { tick } from "svelte";
import HelpOverlay from "./HelpOverlay.svelte";

let target: HTMLElement;
let cmp: any = null;
afterEach(() => {
  cmp?.$destroy();
  cmp = null;
  target?.remove();
});

function mount(props: Record<string, unknown> = {}): HTMLElement {
  target = document.createElement("div");
  document.body.appendChild(target);
  cmp = new HelpOverlay({ target, props: { open: true, ...props } });
  return target;
}

describe("HelpOverlay (E12)", () => {
  it("renders nothing when closed", () => {
    const t = mount({ open: false });
    expect(t.querySelector(".dialog")).toBeNull();
  });

  it("renders the grouped feature sections when open, over an OPAQUE backdrop", () => {
    const t = mount();
    const headings = [...t.querySelectorAll(".group h3")].map((e) => e.textContent);
    expect(headings).toContain("Shape the query");
    expect(headings).toContain("Reuse & take away");
    // A representative feature is explained.
    expect(t.textContent).toContain("Saved views");
    expect(t.textContent).toContain("Row detail");
    // The backdrop is opaque (not the transparent dropdown scrim).
    expect(t.querySelector(".backdrop.opaque")).toBeTruthy();
  });

  it("Escape and × each dispatch close", async () => {
    const t = mount();
    let closed = 0;
    cmp.$on("close", () => (closed += 1));
    (t.querySelector(".close") as HTMLButtonElement).click();
    // Mutation: drop the Escape branch -> this second close never fires.
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await tick();
    expect(closed).toBe(2);
  });
});
