import type { FieldDTO } from "./types";

export interface TreeNode {
  name: string;         // this segment, e.g. "user" or "items[]"
  path: string;         // full dotted path from the root, matches Column.path
  children: TreeNode[];
  field: FieldDTO | null; // null for a synthetic interior node with no profile of its own
}

/** Splits dotted paths into a tree. Order is input order (the profiler's
 *  alphabetized field order), NOT the table's first-seen column order -- the
 *  two legitimately differ and are matched by path, never by index.
 *
 *  Known limitation: profile.Flatten cannot distinguish a literal dot inside a
 *  key from a nesting separator (carried forward from E1 Task 1's WATCH item);
 *  this tree inherits that ambiguity and it is not this task's to fix. */
export function buildTree(fields: FieldDTO[]): TreeNode[] {
  const roots: TreeNode[] = [];
  const byPath = new Map<string, TreeNode>();

  for (const f of fields) {
    const segs = f.path.split(".");
    let prefix = "";
    let siblings = roots;
    let node: TreeNode | undefined;
    for (const seg of segs) {
      prefix = prefix === "" ? seg : prefix + "." + seg;
      node = byPath.get(prefix);
      if (node === undefined) {
        node = { name: seg, path: prefix, children: [], field: null };
        byPath.set(prefix, node);
        siblings.push(node);
      }
      siblings = node.children;
    }
    if (node) node.field = f;
  }
  return roots;
}
