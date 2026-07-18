// @vitest-environment jsdom
//
// Minor 5: the brief names the `mixed` -> --text-muted fallback as an
// explicit rule (KindChip.svelte:1-6, mirroring FieldCard.svelte:14-16), but
// nothing exercised it. These tests mount the REAL KindChip.svelte and check
// the DOM directly, rather than testing KIND_TOKEN in isolation, so a
// regression in the class/style wiring (not just the lookup table) is
// caught too.
import { describe, it, expect, afterEach } from "vitest";
import { tick } from "svelte";
import KindChip from "./KindChip.svelte";

describe("KindChip", () => {
  let target: HTMLElement;
  let cmp: { $destroy: () => void } | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  it("applies the known-kind class and CSS token for a recognized kind (int folds to the shared 'number' token)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new KindChip({ target, props: { kind: "int" } }) as unknown as { $destroy: () => void };
    await tick();

    const chip = target.querySelector(".kind-chip") as HTMLElement;
    expect(chip).toBeTruthy();
    expect(chip.classList.contains("known")).toBe(true);
    expect(chip.getAttribute("style")).toContain("--chip-color: var(--kind-number);");
    expect(chip.textContent).toBe("int");
  });

  it("has no known-kind class or color token for 'mixed' (falls back to --text-muted styling)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new KindChip({ target, props: { kind: "mixed" } }) as unknown as { $destroy: () => void };
    await tick();

    const chip = target.querySelector(".kind-chip") as HTMLElement;
    expect(chip).toBeTruthy();
    expect(chip.classList.contains("known")).toBe(false);
    expect(chip.getAttribute("style")).toBe(""); // no --chip-color set; CSS's .kind-chip default (--text-muted) applies
    expect(chip.textContent).toBe("mixed");
  });
});
