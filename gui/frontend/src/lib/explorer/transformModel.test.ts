import { describe, expect, it } from "vitest";
import {
  buildTransform,
  draftErrors,
  draftFromColumns,
  isIdentityDraft,
  moveColumn,
  projectedColumns,
  type DraftColumn,
} from "./transformModel";
import type { Column } from "./types";

function col(path: string, type = "string", index = 0): Column {
  return {
    path,
    name: path.split(".").pop() ?? path, // engine names base columns by their LEAF
    type,
    nullable: false,
    presence: 1,
    distinct: 1,
    container: false,
    index,
  } as Column;
}

const COLS: Column[] = [col("id", "int", 0), col("user.name", "string", 1), col("meta.name", "string", 2)];

describe("draftFromColumns", () => {
  it("seeds every column visible, in source order, named by its FULL path", () => {
    const draft = draftFromColumns(COLS);
    expect(draft).toEqual([
      { path: "id", name: "id", visible: true },
      { path: "user.name", name: "user.name", visible: true },
      { path: "meta.name", name: "meta.name", visible: true },
    ]);
  });
});

describe("buildTransform", () => {
  it("returns the EMPTY transform for an untouched draft", () => {
    const t = buildTransform(draftFromColumns(COLS), COLS) as any;
    expect(t.select).toBeUndefined();
    expect(t.drop).toBeUndefined();
    expect(Object.keys(t)).toHaveLength(0);
  });

  it("emits an explicit `as` on EVERY spec, never a bare path", () => {
    // Without `as`, the engine names a selected column by its leaf, so
    // user.name and meta.name would BOTH become "name" and collide.
    const draft = draftFromColumns(COLS).filter((d) => d.path !== "id");
    const t = buildTransform(draft, COLS) as any;
    expect(t.select).toEqual([
      { path: "user.name", as: "user.name" },
      { path: "meta.name", as: "meta.name" },
    ]);
    for (const spec of t.select) {
      expect(spec.as).toBeTruthy();
    }
  });

  it("drops hidden columns", () => {
    const draft = draftFromColumns(COLS);
    draft[1].visible = false;
    const t = buildTransform(draft, COLS) as any;
    expect(t.select.map((s: any) => s.path)).toEqual(["id", "meta.name"]);
  });

  it("keeps the draft's order", () => {
    const draft = moveColumn(draftFromColumns(COLS), 2, -1);
    const t = buildTransform(draft, COLS) as any;
    expect(t.select.map((s: any) => s.path)).toEqual(["id", "meta.name", "user.name"]);
  });

  it("carries a rename through as `as`, trimmed", () => {
    const draft = draftFromColumns(COLS);
    draft[1].name = "  Full Name  ";
    const t = buildTransform(draft, COLS) as any;
    expect(t.select[1]).toEqual({ path: "user.name", as: "Full Name" });
  });
});

describe("moveColumn", () => {
  it("swaps neighbours without mutating the input", () => {
    const draft = draftFromColumns(COLS);
    const before = JSON.stringify(draft);
    const moved = moveColumn(draft, 1, -1);
    expect(moved.map((d) => d.path)).toEqual(["user.name", "id", "meta.name"]);
    expect(JSON.stringify(draft)).toBe(before);
  });

  it("is a no-op at either boundary", () => {
    const draft = draftFromColumns(COLS);
    expect(moveColumn(draft, 0, -1)).toBe(draft);
    expect(moveColumn(draft, draft.length - 1, 1)).toBe(draft);
  });
});

describe("isIdentityDraft", () => {
  it("is true only for untouched drafts", () => {
    expect(isIdentityDraft(draftFromColumns(COLS), COLS)).toBe(true);

    const hidden = draftFromColumns(COLS);
    hidden[0].visible = false;
    expect(isIdentityDraft(hidden, COLS)).toBe(false);

    const renamed = draftFromColumns(COLS);
    renamed[0].name = "ID";
    expect(isIdentityDraft(renamed, COLS)).toBe(false);

    expect(isIdentityDraft(moveColumn(draftFromColumns(COLS), 0, 1), COLS)).toBe(false);
  });
});

describe("draftErrors", () => {
  it("returns [] for a valid draft", () => {
    expect(draftErrors(draftFromColumns(COLS))).toEqual([]);
  });

  it("flags a duplicate output name", () => {
    const draft = draftFromColumns(COLS);
    draft[1].name = "name";
    draft[2].name = "name";
    const errors = draftErrors(draft);
    expect(errors).toHaveLength(1);
    expect(errors[0]).toContain("name");
  });

  it("ignores duplicates among HIDDEN columns", () => {
    const draft = draftFromColumns(COLS);
    draft[1].name = "name";
    draft[2].name = "name";
    draft[2].visible = false;
    expect(draftErrors(draft)).toEqual([]);
  });

  it("flags a blank name and an all-hidden draft", () => {
    const blank = draftFromColumns(COLS);
    blank[0].name = "   ";
    expect(draftErrors(blank).join(" ")).toContain("name");

    const none = draftFromColumns(COLS).map((d) => ({ ...d, visible: false }));
    expect(draftErrors(none).join(" ")).toContain("at least one");
  });
});

describe("projectedColumns", () => {
  it("returns the base columns unchanged for an identity draft", () => {
    expect(projectedColumns(draftFromColumns(COLS), COLS)).toBe(COLS);
  });

  it("keeps DISTINCT paths for columns absent from the base (E11 cross-file apply)", () => {
    // A saved reshape applied to a DIFFERENT file: two of its columns are not in
    // this file's base set. Each projected column MUST keep its own (draft) path
    // -- otherwise both get path:undefined and DataTable's keyed {#each (col.path)}
    // collides on a duplicate key and crashes.
    const draft: DraftColumn[] = [
      { path: "gone.a", name: "A", visible: true },
      { path: "gone.b", name: "B", visible: true },
    ];
    const got = projectedColumns(draft, COLS); // COLS has neither gone.a nor gone.b
    // Mutation: fallback `{}` instead of `{ path: d.path }` -> both undefined.
    expect(got.map((c) => c.path)).toEqual(["gone.a", "gone.b"]);
    expect(new Set(got.map((c) => c.path)).size).toBe(2); // distinct keys
  });

  it("renames only `name`, KEEPING the base path, and renumbers index", () => {
    const draft = draftFromColumns(COLS);
    draft[0].visible = false;
    draft[1].name = "Who";
    const got = projectedColumns(draft, COLS);
    expect(got).toHaveLength(2);
    // The header renders `name`, so the rename is visible...
    expect(got[0].name).toBe("Who");
    // ...but `path` stays in BASE space, which is what the sidebar, the focus
    // lookup and the header-click event all join on. Overwriting it here
    // silently disconnects a renamed column from all of them.
    expect(got[0].path).toBe("user.name");
    expect(got[0].index).toBe(0);
    expect(got[0].type).toBe("string"); // metadata inherited from the base column
    expect(got[1].index).toBe(1);
  });
});

describe("whitespace-only renames (E4 branch review, finding 7)", () => {
  it("treats a trailing-space rename as identity", () => {
    // Otherwise buildTransform emits a Select whose `as` trims back to the
    // original name: the engine then reports the projection as un-truncated
    // while the store, comparing paths, sees no change -- and a wide file's
    // "showing 200 of 5,000 columns" silently becomes "200 columns".
    const draft = draftFromColumns(COLS);
    draft[0].name = "id ";
    expect(isIdentityDraft(draft, COLS)).toBe(true);
    expect(buildTransform(draft, COLS)).toEqual({});
  });
});
