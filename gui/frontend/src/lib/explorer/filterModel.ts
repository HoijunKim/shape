// Filter draft model (E3). The UI edits a FilterDraft -- rows of strings,
// suitable for a Svelte {#each} binding a text/number input mid-typing --
// and buildFilter is the single coercion point that turns a draft into
// exactly the JSON internal/query/filter.go's CompileFilter accepts (spec
// §5). Kept pure and dependency-free so every per-op value-shape rule is a
// direct unit test.
//
// Why a draft layer separate from the engine Filter: a numeric input is a
// string mid-typing, a regex is a string that may be invalid mid-typing, and
// every row carries fields for every possible arity (text/num/bool/list) so
// switching an operator on an existing row keeps whatever the user already
// typed in view. Rows need a stable `id` for Svelte keying, which the engine
// Condition has no use for and does not carry.
import type { OpId } from "./operators";
import { operatorsForType, defaultOpForType } from "./operators";
import type { Filter } from "./types";

// wailsjs/go/models's Filter/Condition/Value are TS *classes* (they carry a
// convertValues() decoding method for responses coming FROM Go), so their
// instance type demands that method on anything assigned to it -- a plain
// object literal (needed here to get exact key presence/absence: no `ci`
// key at all when off, no `value` key at all for isnull/notnull) can never
// structurally satisfy that. These Plain* types describe just the JSON data
// shape those classes serialize to/from; buildFilter builds with these
// throughout and casts to the real Filter type only at its single return
// (the same `as unknown as` pattern already used at component boundaries in
// this codebase's tests), matching engine-visible JSON exactly with no
// undefined-valued keys leaking in from unused class fields.
interface PlainValue {
  kind: string;
  str?: string;
  num?: number;
  bool?: boolean;
  list?: PlainValue[];
}

interface PlainCondition {
  path: string;
  op: OpId;
  value?: PlainValue;
  ci?: boolean;
}

interface PlainFilter {
  combinator: "and" | "or";
  conditions?: PlainCondition[];
}

/** One editable condition row. Carries a field for every arity so switching
 *  `op` on an existing row doesn't lose what the user already typed; only
 *  the fields relevant to the current op's arity are read by buildFilter. */
export interface DraftCondition {
  id: number;
  path: string;
  /** Column.type this row was created against (int|float|string|bool|...) --
   *  carried because eq/ne/in coerce their operand's Value.kind off the
   *  COLUMN type, not the op id (numeric columns emit kind:"number", string
   *  columns emit kind:"string" for the identical "eq" op id). */
  type: string;
  op: OpId;
  text: string;
  num: string;
  bool: boolean;
  list: string[];
  ci: boolean;
}

/** The whole editable filter: flat-only for E3 (no nested groups, no
 *  negate) -- mirrors the engine Filter's shape minus the fields E3 never
 *  sets. */
export interface FilterDraft {
  combinator: "and" | "or";
  conditions: DraftCondition[];
}

/** A fresh, empty, match-all draft. */
export function emptyDraft(): FilterDraft {
  return { combinator: "and", conditions: [] };
}

/** A fresh condition row for a column of the given type, defaulted to that
 *  type's default op (operators.ts) with every value field zeroed. */
export function newCondition(id: number, path: string, colType: string): DraftCondition {
  return {
    id,
    path,
    type: colType,
    op: defaultOpForType(colType),
    text: "",
    num: "",
    bool: false,
    list: [],
    ci: false,
  };
}

/** True when `c` carries enough to compile into a Condition: none-arity ops
 *  (isnull/notnull) are always complete since they take no operand; bool is
 *  always complete since `false` is a real, already-present value; text ops
 *  need non-empty text; number ops need `num` to parse as finite; list ops
 *  need at least one non-empty entry. An op's arity (not the op id alone) is
 *  looked up per the row's column `type`, since eq/ne's arity differs by
 *  column type (operators.ts). */
export function isConditionComplete(c: DraftCondition): boolean {
  const arity = arityFor(c);
  switch (arity) {
    case "none":
    case "bool":
      return true;
    case "text":
      return c.text !== "";
    case "number":
      return c.num !== "" && Number.isFinite(Number(c.num));
    case "list":
      return c.list.some((x) => x !== "");
    default:
      return false;
  }
}

/** Validation message for `c`, or "" when valid -- including when `c` is
 *  simply empty (empty is omittable by buildFilter, not an error to surface
 *  to the user). Only two things are ever genuinely wrong: a regex whose
 *  text doesn't compile as a JS RegExp (RE2 vs JS regex differ, but JS
 *  RegExp already rejects the common syntax errors -- unbalanced parens/
 *  brackets -- that would also fail Go's regexp.Compile, which is the
 *  point: reject the obviously-broken pattern before it ever reaches the
 *  engine), and a number op whose `num` is non-empty but doesn't parse. */
export function conditionError(c: DraftCondition): string {
  if (c.op === "regex") {
    if (c.text === "") return "";
    try {
      new RegExp(c.text);
    } catch {
      return "invalid regex";
    }
    return "";
  }
  const arity = arityFor(c);
  if (arity === "number" && c.num !== "" && !Number.isFinite(Number(c.num))) {
    return "not a number";
  }
  return "";
}

/** Compiles a draft into the engine Filter: incomplete conditions (per
 *  isConditionComplete) are omitted entirely so the engine never sees a
 *  half-typed request. combinator is always set explicitly (an empty string
 *  would default to AND server-side, but E3 never relies on that default).
 *  groups/negate are never set -- E3 is flat-only. */
export function buildFilter(draft: FilterDraft): Filter {
  const conditions = draft.conditions.filter(isConditionComplete).map(buildCondition);
  const filter: PlainFilter = { combinator: draft.combinator };
  if (conditions.length > 0) {
    filter.conditions = conditions;
  }
  return filter as unknown as Filter;
}

// --- internals ---

function arityFor(c: DraftCondition): string {
  const spec = operatorsForType(c.type).find((o) => o.id === c.op);
  return spec ? spec.arity : "none";
}

function isNumericType(colType: string): boolean {
  return colType === "int" || colType === "float";
}

function buildCondition(c: DraftCondition): PlainCondition {
  switch (c.op) {
    case "isnull":
    case "notnull":
      return { path: c.path, op: c.op };

    case "contains":
    case "regex":
      return {
        path: c.path,
        op: c.op,
        value: { kind: "string", str: c.text },
        ...(c.ci ? { ci: true } : {}),
      };

    case "eq":
    case "ne": {
      const value: PlainValue = isNumericType(c.type)
        ? { kind: "number", num: Number(c.num) }
        : { kind: "string", str: c.text };
      return {
        path: c.path,
        op: c.op,
        value,
        ...(!isNumericType(c.type) && c.ci ? { ci: true } : {}),
      };
    }

    case "lt":
    case "lte":
    case "gt":
    case "gte":
      return { path: c.path, op: c.op, value: { kind: "number", num: Number(c.num) } };

    case "in": {
      const list: PlainValue[] = c.list
        .filter((x) => x !== "")
        .map((x) => (isNumericType(c.type) ? { kind: "number", num: Number(x) } : { kind: "string", str: x }));
      return { path: c.path, op: c.op, value: { kind: "string", list } };
    }

    case "bool":
      return { path: c.path, op: c.op, value: { kind: "bool", bool: c.bool } };

    default:
      // Exhaustive per OpId; unreachable for any well-formed DraftCondition.
      return { path: c.path, op: c.op };
  }
}
