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
  // "items[]" and "items[].sku": the REAL dimming case (internal/query/
  // columns.go:570 and its unconditional hasElemSeg exclusion), and the
  // common one -- unlike "user"/"user.address" below, these two DO have
  // their own FieldDTO (profile.Flatten emits a KindObject observation for
  // the interior array-element object, and a leaf observation for "sku"),
  // yet neither is ever staged as a column: array-element paths are previews
  // only, dropped before the pure-interior-object check even runs. A fixture
  // that only ever dims field===null nodes cannot tell this rule apart from
  // `isColumn = node.field !== null` -- these two nodes close that gap.
  f("items[]", { types: [{ kind: "object", share: 1 } as any] }),
  f("items[].sku", { types: [{ kind: "string", share: 1 } as any] }),
];

// "user" and "user.address" are pure interior objects: they have no FieldDTO
// of their own (never staged as a column by buildColumnModel), so they are
// correctly ABSENT from columnPaths. But this is only HALF of the real
// column-model behavior, and the less common half: "items[]" and
// "items[].sku" above DO have a FieldDTO each and are STILL absent from
// columnPaths (array-element paths are dropped unconditionally, and a pure
// interior object with deeper columns is dropped even when profiled) -- see
// internal/query/columns.go's hasElemSeg/pure-interior-object rules. Any
// test that only exercises field-less nodes cannot distinguish the real rule
// (columnPaths membership) from the wrong one (`node.field !== null`).
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
    // "user" has children ("name", "address"), so it stays in the tab order
    // for BROWSING even though it is not a column (Finding 3): tabindex is
    // gated on (isColumn || hasChildren), not on isColumn alone.
    expect(userRow!.getAttribute("tabindex")).toBe("0");

    let fired = false;
    cmp.$on("focus", () => (fired = true));
    userRow!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    expect(fired).toBe(false); // regression: a click on a non-column row must never focus it
  });

  it("dims a node that HAS a FieldDTO but is absent from columnPaths, and it still renders a KindChip (proving the dimming cannot be explained by field === null)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const itemsRow = row(target, "items[]");
    expect(itemsRow).toBeTruthy();
    expect(itemsRow!.classList.contains("dimmed")).toBe(true);
    expect(itemsRow!.querySelector(".kind-chip")).toBeTruthy(); // has a field -> renders a chip despite being dimmed

    // Expand it (via the caret) to reach "items[].sku", which is also
    // profiled (has a FieldDTO) but is likewise never a column (Elem-segment
    // paths are previews only, per internal/query/columns.go's hasElemSeg
    // check).
    const caret = itemsRow!.querySelector(".caret") as HTMLElement;
    caret.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();

    const skuRow = row(target, "items[].sku");
    expect(skuRow).toBeTruthy();
    expect(skuRow!.classList.contains("dimmed")).toBe(true);
    expect(skuRow!.querySelector(".kind-chip")).toBeTruthy();
    // A dimmed LEAF has nothing to browse into and nothing to focus, so
    // unlike "items[]" (which has children) it correctly drops out of the
    // tab order (Finding 3: tabindex is (isColumn || hasChildren), and sku
    // is neither).
    expect(skuRow!.getAttribute("tabindex")).toBe("-1");
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

  it("forwards a focus click from a DEEPLY nested row (depth >= 2) all the way up through the recursive svelte:self chain to StructureMap's consumer", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    // Mount with the deep path already focused so its ancestors are
    // pre-expanded (same mechanism test 6 exercises), then click it.
    cmp = new StructureMap({
      target,
      props: { fields, focusPath: "user.address.city", columnPaths },
    }) as unknown as Instance;
    await tick();

    const cityRow = row(target, "user.address.city");
    expect(cityRow).toBeTruthy(); // sanity: it is genuinely revealed at depth 2

    let detail: { path: string } | null = null;
    cmp.$on("focus", (e) => (detail = e.detail));
    cityRow!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    // Regression: TreeNode.svelte's recursive <svelte:self ... on:focus />
    // forward (depth 2 -> depth 1 -> depth 0 -> StructureMap) is the ONLY
    // path a nested row's click can reach the consumer through, since Svelte
    // component events do not bubble on their own. Every other click/keydown
    // test in this file targets a root-level (depth 0) row, so this is the
    // only one that would catch a deleted forward.
    expect(detail).toEqual({ path: "user.address.city" });
  });

  it("expands and collapses via ArrowRight/ArrowLeft on the row, including a DIMMED parent (keyboard structure-browsing is not gated on isColumn)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const userRow = row(target, "user")!;
    expect(userRow.classList.contains("dimmed")).toBe(true); // not a column...
    expect(userRow.getAttribute("tabindex")).toBe("0"); // ...but still keyboard-reachable
    expect(userRow.getAttribute("aria-expanded")).toBe("false");
    expect(row(target, "user.name")).toBeNull();

    userRow.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    await tick();
    expect(row(target, "user.name")).toBeTruthy(); // expanded via keyboard alone
    expect(userRow.getAttribute("aria-expanded")).toBe("true");

    userRow.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }));
    await tick();
    expect(row(target, "user.name")).toBeNull(); // collapsed via keyboard alone
    expect(userRow.getAttribute("aria-expanded")).toBe("false");
  });

  it("omits aria-expanded on a leaf row (nothing to expand)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const idRow = row(target, "id")!;
    expect(idRow.hasAttribute("aria-expanded")).toBe(false);
  });

  it("re-reveals a manually-collapsed ancestor of the CURRENT focus when the consumer bumps focusToken, even though focusPath itself is unchanged (Minor 4 escape hatch)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({
      target,
      props: { fields, focusPath: "user.address.city", columnPaths, focusToken: 0 },
    }) as unknown as Instance;
    await tick();
    expect(row(target, "user.address.city")).toBeTruthy(); // revealed by the initial focus

    // The user manually collapses "user" (e.g. clicked its caret).
    const userRow = row(target, "user")!;
    const caret = userRow.querySelector(".caret") as HTMLElement;
    caret.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    expect(row(target, "user.address.city")).toBeNull(); // sanity: genuinely collapsed

    // The consumer re-focuses the SAME already-focused path (e.g. the user
    // clicks the same DataTable header again). Svelte treats re-assigning a
    // prop to an unchanged value as a no-op, so focusPath alone cannot signal
    // this -- the consumer instead bumps focusToken (e.g. to a store
    // revision counter) WITHOUT writing back to focusPath.
    cmp.$set({ focusToken: 1 });
    await tick();

    expect(row(target, "user.address.city")).toBeTruthy(); // re-revealed
  });

  // E3 Task 9 (click-to-seed, the second "wow"): a small funnel button per
  // column row that seeds a FilterBar condition without also focusing/
  // scrolling the DataTable column -- the two actions must not both fire off
  // a single click.
  it("renders a seed button only on column rows, not on a column-less (dimmed) row", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const idRow = row(target, "id")!; // a real column
    expect(idRow.querySelector("button.seed")).toBeTruthy();

    const userRow = row(target, "user")!; // pure interior object, not a column
    expect(userRow.querySelector("button.seed")).toBeNull();
  });

  it("clicking a column row's seed button emits seedFilter with {path, type} and does NOT also emit focus (stopPropagation)", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({ target, props: { fields, focusPath: "", columnPaths } }) as unknown as Instance;
    await tick();

    const idRow = row(target, "id")!; // types: [{kind:"int", share:1}]
    const seedBtn = idRow.querySelector("button.seed") as HTMLButtonElement;
    expect(seedBtn).toBeTruthy();

    let seedDetail: { path: string; type: string } | null = null;
    let focusFired = false;
    cmp.$on("seedFilter", (e) => (seedDetail = e.detail));
    cmp.$on("focus", () => (focusFired = true));

    seedBtn.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();

    expect(seedDetail).toEqual({ path: "id", type: "int" });
    // Mutation: removing TreeNode's e.stopPropagation() on the seed button
    // lets this click also bubble to the row's own on:click handler, which
    // would dispatch `focus` -- the exact double-fire a plan review flagged.
    expect(focusFired).toBe(false);
  });

  it("forwards seedFilter from a deeply nested row up through the recursive svelte:self chain", async () => {
    target = document.createElement("div");
    document.body.appendChild(target);
    cmp = new StructureMap({
      target,
      props: { fields, focusPath: "user.address.city", columnPaths },
    }) as unknown as Instance;
    await tick();

    const cityRow = row(target, "user.address.city")!;
    const seedBtn = cityRow.querySelector("button.seed") as HTMLButtonElement;
    expect(seedBtn).toBeTruthy();

    let seedDetail: { path: string; type: string } | null = null;
    cmp.$on("seedFilter", (e) => (seedDetail = e.detail));
    seedBtn.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();

    expect(seedDetail).toEqual({ path: "user.address.city", type: "string" });
  });
});
