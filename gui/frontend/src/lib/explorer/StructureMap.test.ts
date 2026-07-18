// @vitest-environment jsdom
//
// Wire-up coverage for the two things the T7 brief specifically calls out as
// likely to silently break: the "which paths are focusable" set (columnPaths
// membership, joined by path -- never by index/field-ness) and the
// ancestor-expansion logic (a focusPath change must actually reveal the
// nested row in the DOM, not just satisfy a pure function in isolation).
// This mounts the REAL StructureMap.svelte (which mounts real TreeNode.svelte
// recursively), not a mock -- a regression in either area renders wrong DOM,
// which these assertions catch directly.
import { describe, it, expect, afterEach } from "vitest";
import { tick } from "svelte";
import StructureMap from "./StructureMap.svelte";
import type { FieldDTO } from "./types";

type Instance = {
  $set: (props: Record<string, unknown>) => void;
  $on: (event: string, cb: (e: CustomEvent) => void) => void;
  $destroy: () => void;
};

const f = (path: string, over: Partial<FieldDTO> = {}): FieldDTO =>
  ({
    path,
    types: [{ kind: "string", share: 1 } as any],
    presence: 1,
    nullRate: 0,
    distinct: 1,
    distinctExact: true,
    drift: false,
    ...over,
  } as FieldDTO);

const fields: FieldDTO[] = [
  f("id", { types: [{ kind: "int", share: 1 } as any] }),
  f("user.name"),
  f("user.address.city"),
  f("meta", {
    drift: true,
    types: [{ kind: "string", share: 0.5 } as any, { kind: "object", share: 0.5 } as any],
  }),
];

// "user" and "user.address" are pure interior objects: they have no FieldDTO
// of their own (never staged as a column by buildColumnModel), so they are
// correctly ABSENT from columnPaths -- exactly the real column-model
// behavior this fixture is modeling, not a simplification of it.
const columnPaths = new Set(["id", "user.name", "user.address.city", "meta"]);

function row(target: HTMLElement, path: string): HTMLElement | null {
  return target.querySelector(`.row[data-path="${path}"]`);
}

describe("StructureMap + TreeNode", () => {
  let target: HTMLElement;
  let cmp: Instance | null = null;

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target?.remove();
  });

  it("dims and does not dispatch focus for a path absent from columnPaths (pure interior object)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const userRow = row(target, "user");
    expect(userRow).toBeTruthy();
    expect(userRow!.classList.contains("dimmed")).toBe(true);
    expect(userRow!.getAttribute("tabindex")).toBe("-1");

    let fired = false;
    cmp.$on("focus", () => (fired = true));
    userRow!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    expect(fired).toBe(false); // regression: a click on a non-column row must never focus it
  });

  it("dispatches focus with the clicked path for a real column, and is not dimmed", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const idRow = row(target, "id");
    expect(idRow!.classList.contains("dimmed")).toBe(false);
    expect(idRow!.getAttribute("tabindex")).toBe("0");

    let detail: { path: string } | null = null;
    cmp.$on("focus", (e) => (detail = e.detail));
    idRow!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    expect(detail).toEqual({ path: "id" }); // must be the CLICKED node's path, not e.g. always the first column
  });

  it("activates on Enter and Space for a focusable row (keyboard parity with click)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const metaRow = row(target, "meta");
    let count = 0;
    cmp.$on("focus", () => count++);
    metaRow!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    metaRow!.dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true }));
    await tick();
    expect(count).toBe(2);
  });

  it("does not activate on Enter for a dimmed (non-column) row", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const userRow = row(target, "user");
    let fired = false;
    cmp.$on("focus", () => (fired = true));
    userRow!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await tick();
    expect(fired).toBe(false);
  });

  it("does not render a deeply nested row before it is focused (collapsed by default)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    expect(row(target, "user.address.city")).toBeNull();
  });

  it("auto-expands ancestors and reveals + focuses a nested row when focusPath targets it (bidirectional focus from a DataTable header click)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();
    expect(row(target, "user.address.city")).toBeNull(); // sanity: genuinely collapsed beforehand

    cmp.$set({ focusPath: "user.address.city" });
    await tick();

    const cityRow = row(target, "user.address.city");
    expect(cityRow).toBeTruthy(); // regression: ancestor-expansion logic must actually reveal it, not just claim to
    expect(cityRow!.classList.contains("focused")).toBe(true);

    // The intermediate ancestor "user.address" must also now be visible
    // (it, too, sits on the chain down to the focus).
    expect(row(target, "user.address")).toBeTruthy();
  });

  it("lets a dimmed row's caret still expand/collapse its children (structure browsing is not gated on focusability)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    expect(row(target, "user.name")).toBeNull(); // collapsed initially
    const userRow = row(target, "user")!;
    const caret = userRow.querySelector(".caret") as HTMLElement;
    expect(caret).toBeTruthy();
    caret.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    expect(row(target, "user.name")).toBeTruthy(); // now expanded via the caret alone
  });
});
