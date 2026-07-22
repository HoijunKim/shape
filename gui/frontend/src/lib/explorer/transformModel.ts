import type { Column, Transform } from "./types";

/** One row of the columns panel: a base column plus the two things the user can
 *  change about it -- whether it is shown, and what it is called on the way out.
 *
 *  `path` is the BASE column path and never changes: it is what the engine
 *  resolves against each record. `name` is the OUTPUT name (defaulting to the
 *  path, not the leaf -- see buildTransform). Reordering is the array's own
 *  order. */
export interface DraftColumn {
  path: string;
  name: string;
  visible: boolean;
}

/** The plain shape buildTransform actually builds. The generated `Transform` is
 *  a TS *class* (it carries a convertValues() decoding method for responses
 *  coming FROM Go) with a required `flattenObjects`, so no object literal can
 *  structurally satisfy it -- same reason store.ts's matchAllFilter and
 *  filterModel.ts's buildFilter cast. */
interface PlainTransform {
  select?: { path: string; as: string }[];
  drop?: string[];
}

/** Seeds a draft from the source's base columns: everything visible, in the
 *  source's own order, named by its full path. */
export function draftFromColumns(cols: Column[]): DraftColumn[] {
  return cols.map((c) => ({ path: c.path, name: c.path, visible: true }));
}

/** Moves one row up (-1) or down (+1), returning a NEW array. Out-of-range
 *  moves are no-ops, so the caller can wire ↑/↓ buttons without bounds checks. */
export function moveColumn(draft: DraftColumn[], index: number, delta: -1 | 1): DraftColumn[] {
  const target = index + delta;
  if (index < 0 || index >= draft.length || target < 0 || target >= draft.length) {
    return draft;
  }
  const out = draft.slice();
  const [moved] = out.splice(index, 1);
  out.splice(target, 0, moved);
  return out;
}

/** True when the draft changes nothing about the base column set: every column
 *  visible, in the original order, under its original name. */
export function isIdentityDraft(draft: DraftColumn[], cols: Column[]): boolean {
  if (draft.length !== cols.length) return false;
  return draft.every(
    (d, i) => d.visible && d.path === cols[i].path && d.name.trim() === cols[i].path,
  );
}

/** Everything that would make an export fail or lose data, checked in the UI's
 *  own terms so the Export button can be disabled with a reason instead of the
 *  engine rejecting the request after the user picked a file.
 *
 *  The duplicate check is case-SENSITIVE and keyed on the output name, which is
 *  exactly the key space the engine validates (`Column.Path`) and the encoders
 *  write (JSON keys, the CSV header, the Parquet schema). */
export function draftErrors(draft: DraftColumn[]): string[] {
  const errors: string[] = [];
  const visible = draft.filter((d) => d.visible);
  if (visible.length === 0) {
    errors.push("Select at least one column.");
  }
  if (visible.some((d) => d.name.trim() === "")) {
    errors.push("Every selected column needs a name.");
  }
  const seen = new Set<string>();
  const dupes = new Set<string>();
  for (const d of visible) {
    const name = d.name.trim();
    if (name === "") continue;
    if (seen.has(name)) dupes.add(name);
    seen.add(name);
  }
  for (const name of dupes) {
    errors.push(`Two columns are both named "${name}".`);
  }
  return errors;
}

/** Compiles a draft into the engine `Transform`.
 *
 *  An untouched draft returns the EMPTY transform, not a full Select of every
 *  column: identity must stay byte-identical to the request the explorer sent
 *  before E4, and the engine keys its wide-data truncation reporting on
 *  "Select is empty" (engine.go's QueryRows), which an explicit full Select
 *  would silently switch off.
 *
 *  Every emitted spec carries an explicit `as`. Without it the engine names a
 *  selected column by its LEAF (transform.go's compileSelect), so selecting
 *  `user.name` would silently rename the column to `name` -- and collide with
 *  `meta.name`. */
export function buildTransform(draft: DraftColumn[], cols: Column[]): Transform {
  if (isIdentityDraft(draft, cols)) {
    return {} as unknown as Transform;
  }
  const select = draft
    .filter((d) => d.visible)
    .map((d) => ({ path: d.path, as: d.name.trim() }));
  return { select } as unknown as PlainTransform as unknown as Transform;
}

/** The column set the table will show once this draft is applied -- what the
 *  store must adopt SYNCHRONOUSLY with the transform, because page arithmetic
 *  (pageRowsFor) reads the column count before the first projected page lands.
 *
 *  `name` becomes the output name (that is what the table header renders) and
 *  `index` is renumbered, but `path` deliberately KEEPS the base column's
 *  path, unlike the engine's own Select output which overwrites both. The
 *  whole app joins the table to the sidebar by path -- DataTable's focus
 *  lookup, its `focused` class, the header click it dispatches, Explorer's
 *  columnPaths set, the store's focusPath seed -- all of which live in BASE
 *  path space. Renaming a column into the projected space would silently
 *  disconnect every one of those, so a renamed column could no longer be
 *  focused from the sidebar or highlight when clicked. Base paths are unique,
 *  so the keyed {#each} stays well-formed, and cells are matched positionally
 *  anyway. The wire Transform comes from buildTransform, not from here. */
export function projectedColumns(draft: DraftColumn[], cols: Column[]): Column[] {
  if (isIdentityDraft(draft, cols)) return cols;
  const byPath = new Map(cols.map((c) => [c.path, c]));
  return draft
    .filter((d) => d.visible)
    .map((d, i) => {
      const base = byPath.get(d.path);
      const name = d.name.trim();
      return { ...(base ?? ({} as Column)), name, index: i } as Column;
    });
}
