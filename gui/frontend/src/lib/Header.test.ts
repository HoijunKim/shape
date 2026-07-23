// @vitest-environment jsdom
import { describe, it, expect, afterEach } from "vitest";
import { tick } from "svelte";
import Header from "./Header.svelte";

let target: HTMLElement;
let header: Header | undefined;

afterEach(() => {
  header?.$destroy();
  header = undefined;
  target?.remove();
});

function mount(props: Record<string, unknown> = {}): Header {
  target = document.createElement("div");
  document.body.appendChild(target);
  header = new Header({ target, props: { path: "/data.ndjson", tier: "memory", format: "ndjson", canExport: true, ...props } });
  return header;
}

function button(label: string): HTMLButtonElement | undefined {
  return Array.from(target.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === label,
  ) as HTMLButtonElement | undefined;
}

describe("Header", () => {
  it("dispatches toggleCode when the Code button is clicked", async () => {
    mount();
    let fired = 0;
    header!.$on("toggleCode", () => (fired += 1));
    button("Code")!.click();
    expect(fired).toBe(1);
  });

  it("reflects codeOpen on the Code button's aria-pressed", async () => {
    mount({ codeOpen: false });
    expect(button("Code")!.getAttribute("aria-pressed")).toBe("false");
    header!.$set({ codeOpen: true });
    await tick();
    expect(button("Code")!.getAttribute("aria-pressed")).toBe("true");
  });

  // The E5 button sits alongside the E4 ones; a regression that dropped any of
  // them would break the corresponding panel with no other signal.
  it("still offers Columns, Filter, Schema and Export", () => {
    mount();
    for (const label of ["Columns", "Filter", "Code", "Schema", "Export"]) {
      expect(button(label), `missing ${label} button`).toBeTruthy();
    }
  });
});
