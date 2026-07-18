// @vitest-environment jsdom
//
// Regression coverage for the two "real bugs if you skip them" obligations
// carried forward into T8's Explorer shell:
//
//   1. columnPaths must be rebuilt fresh from $explorer.columns every time
//      it changes (in particular across a second open()) -- StructureMap
//      deliberately does not own this set, Explorer does.
//   2. focusToken must be bumped UNCONDITIONALLY on every focus event, from
//      either StructureMap or DataTable, even when the newly-focused path is
//      the SAME path already focused -- Svelte no-ops a same-value prop
//      re-assignment, so re-dispatching an unchanged focusPath alone can
//      never re-trigger TreeNode's re-expand-a-manually-collapsed-ancestor
//      check.
//
// Both mount the REAL Explorer.svelte (which mounts real StructureMap/
// TreeNode/DataTable), driving the real `explorer` store through a mocked
// Wails bridge -- the same pattern store.test.ts and StructureMap.test.ts
// already use -- so a regression shows up as wrong DOM, not just a wrong
// return value from an isolated helper.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { tick } from "svelte";
import Explorer from "./Explorer.svelte";
import { explorer } from "./store";
import { OpenSource, QueryRows, CloseSource } from "../../../wailsjs/go/main/App";

vi.mock("../../../wailsjs/go/main/App", () => ({
  OpenSource: vi.fn(),
  QueryRows: vi.fn(),
  CloseSource: vi.fn(),
  Cancel: vi.fn(),
}));

function makeColumn(path: string): any {
  return { path, name: path, type: "string", nullable: true, presence: 1, distinct: 1, container: false, index: 0 };
}

function makeField(path: string): any {
  return {
    path,
    types: [{ kind: "string", share: 1 }],
    presence: 1,
    nullRate: 0,
    distinct: 1,
    distinctExact: true,
    drift: false,
  };
}

function openResultFor(handle: string, columns: any[], fields: any[]): any {
  return {
    handle,
    format: "ndjson",
    tier: "memory",
    columns,
    profile: { records: columns.length ? 10 : 0, skipped: 0, fields },
    sampled: false,
    rowEstimate: 10,
    rowExact: true,
    warnings: [],
    columnsTruncated: false,
    totalPaths: columns.length,
  };
}

function rowSetFor(columns: any[]): any {
  return {
    columns,
    rows: [{ index: 0, cells: columns.map(() => ({ kind: "string", str: "x" })) }],
    offset: 0,
    total: 10,
    totalExact: true,
    scanned: 1,
    truncated: false,
    elapsedMs: 0,
    columnsTruncated: false,
    totalPaths: columns.length,
  };
}

describe("Explorer", () => {
  let target: HTMLElement;
  let cmp: { $destroy: () => void } | null = null;

  beforeEach(async () => {
    vi.mocked(OpenSource).mockReset();
    vi.mocked(QueryRows).mockReset();
    vi.mocked(CloseSource).mockReset().mockResolvedValue(undefined as any);
    await explorer.close();
    vi.mocked(CloseSource).mockClear(); // don't count close()'s own no-op call
    target = document.createElement("div");
    document.body.appendChild(target);
  });

  afterEach(() => {
    cmp?.$destroy();
    cmp = null;
    target.remove();
  });

  it("bumps focusToken on every focus event, independent of any store change, so re-focusing an already-focused, manually-collapsed path re-reveals it", async () => {
    // Isolating focusToken's OWN contribution matters here: `fields` and
    // `columnPaths` are array/Set-typed props, and neither StructureMap nor
    // TreeNode opts into `<svelte:options immutable>`, so Svelte 3's default
    // `safe_not_equal` treats ANY re-pass of an object-typed prop as
    // "changed" regardless of reference/content equality (see
    // svelte/internal's `safe_not_equal`: for objects it unconditionally
    // ORs in `true`). That means a bare `explorer.focus()` call -- which
    // reassigns the WHOLE `$explorer` store object even when only
    // `focusPath` logically changes -- already forces `fields`/`columnPaths`
    // to be re-passed and thus treated as dirty everywhere downstream,
    // which by itself re-triggers TreeNode's re-expand check REGARDLESS of
    // focusToken. A naive version of this test that drives a real
    // `explorer.focus()` call cannot tell a correct unconditional bump apart
    // from the exact anti-pattern the brief warns against (bumping only when
    // the path changed) -- both "pass" once, because `explorer.focus()`
    // alone already saturates the cascade. Stubbing `explorer.focus` to a
    // no-op freezes the store completely, so on the second click the ONLY
    // thing that can possibly cause a re-expand is focusToken itself.
    const columns = [makeColumn("user.address.city"), makeColumn("id")];
    const fields = [makeField("user.address.city"), makeField("id")];
    vi.mocked(OpenSource).mockResolvedValue(openResultFor("h1", columns, fields));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columns));

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    // "user.address.city" is columns[0].path, so store.open() itself
    // defaults focusPath to it -- no explicit explorer.focus() call needed
    // to establish the initial focus/auto-expand.
    await explorer.open("file.ndjson");
    await tick();
    await tick();

    expect(target.querySelector('.row[data-path="user.address.city"]')).toBeTruthy(); // sanity: auto-expanded from the initial focus

    // User manually collapses the "user" ancestor via its caret.
    const userRow = target.querySelector('.row[data-path="user"]') as HTMLElement;
    expect(userRow).toBeTruthy();
    const caret = userRow.querySelector(".caret") as HTMLElement;
    caret.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await tick();
    expect(target.querySelector('.row[data-path="user.address.city"]')).toBeNull(); // sanity: genuinely collapsed

    const focusSpy = vi.spyOn(explorer, "focus").mockImplementation(() => {});

    const viewportEl = target.querySelector(".viewport") as HTMLElement;
    Object.defineProperty(viewportEl, "clientHeight", { value: 500, configurable: true });
    Object.defineProperty(viewportEl, "clientWidth", { value: 600, configurable: true });
    const header = Array.from(target.querySelectorAll(".header-cell")).find(
      (el) => (el as HTMLElement).title === "user.address.city",
    ) as HTMLElement;
    expect(header).toBeTruthy();

    // Click the SAME already-focused header again. `explorer.focus` is
    // stubbed to a no-op, so $explorer does not change at all -- if this
    // re-reveals the row, it can only be because Explorer bumped focusToken.
    header.click();
    await tick();

    expect(focusSpy).toHaveBeenCalledWith("user.address.city");
    expect(target.querySelector('.row[data-path="user.address.city"]')).toBeTruthy(); // regression: without an unconditional focusToken bump, this stays null

    focusSpy.mockRestore();
  });

  it("recomputes columnPaths fresh on every open, so a stale column from a previously-open file is never treated as a real column on the new one", async () => {
    const columnsA = [makeColumn("a")];
    // "dropped" carries a FieldDTO on file A but is not staged as a column.
    const fieldsA = [makeField("a"), makeField("dropped")];
    vi.mocked(OpenSource).mockResolvedValueOnce(openResultFor("h1", columnsA, fieldsA));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columnsA));

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("fileA.ndjson");
    await tick();

    let droppedRow = target.querySelector('.row[data-path="dropped"]') as HTMLElement;
    expect(droppedRow).toBeTruthy();
    expect(droppedRow.classList.contains("dimmed")).toBe(true); // not a column on file A

    // File B: "dropped" IS a real column this time.
    const columnsB = [makeColumn("dropped")];
    const fieldsB = [makeField("dropped")];
    vi.mocked(OpenSource).mockResolvedValueOnce(openResultFor("h2", columnsB, fieldsB));
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor(columnsB));
    await explorer.open("fileB.ndjson");
    await tick();

    droppedRow = target.querySelector('.row[data-path="dropped"]') as HTMLElement;
    expect(droppedRow).toBeTruthy();
    // Regression: a columnPaths Set computed once (e.g. at mount, or memoized
    // outside of $explorer.columns) would still hold file A's "a" only, and
    // "dropped" would incorrectly stay dimmed here.
    expect(droppedRow.classList.contains("dimmed")).toBe(false);
  });

  it("renders the zero-columns empty state, including the skipped count, without a blank frame", async () => {
    vi.mocked(OpenSource).mockResolvedValueOnce(
      (() => {
        const r = openResultFor("h3", [], []);
        r.profile.skipped = 7;
        return r;
      })(),
    );
    // ensurePages(0, 0) still runs unconditionally after a successful open()
    // even with zero columns -- must resolve to a valid RowSet or the fetch
    // itself throws and status flips to "error" instead of staying "ready".
    vi.mocked(QueryRows).mockResolvedValue(rowSetFor([]));

    cmp = new Explorer({ target, props: {} }) as unknown as { $destroy: () => void };
    await explorer.open("empty.csv");
    await tick();

    expect(target.textContent).toContain("No columns detected");
    expect(target.textContent).toContain("7");
  });
});
