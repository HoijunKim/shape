// Filter operator vocabulary (E3). This module is the single source of
// truth for which operators a column offers and what value shape each op
// needs -- E3-owned logic (not in the engine spec verbatim), and the thing
// most likely to silently emit an invalid or zero-matching condition if the
// type->operator mapping drifts. Kept as pure logic so every rule is a
// direct unit test, no Svelte/store involved.
//
// The 12 OpId values below are the literal `op` strings the query engine
// reads off a Condition (internal/query/filter.go:15-28) -- they must match
// exactly.
export type OpId =
  | "eq"
  | "ne"
  | "lt"
  | "lte"
  | "gt"
  | "gte"
  | "contains"
  | "regex"
  | "in"
  | "isnull"
  | "notnull"
  | "bool";

// What shape of value input an op needs from the condition builder UI.
export type ValueArity = "none" | "text" | "number" | "bool" | "list";

export interface OpSpec {
  id: OpId;
  label: string;
  arity: ValueArity;
  ci: boolean; // whether a case-insensitive toggle is offered for this op
}

// Single source of truth: every known op, its label, its value arity, and
// whether it supports a case-insensitive toggle. operatorsForType and
// OP_LABELS both derive from this map so the two can never drift apart.
const OPS: Record<OpId, OpSpec> = {
  eq: { id: "eq", label: "=", arity: "text", ci: true },
  ne: { id: "ne", label: "≠", arity: "text", ci: true },
  lt: { id: "lt", label: "<", arity: "number", ci: false },
  lte: { id: "lte", label: "≤", arity: "number", ci: false },
  gt: { id: "gt", label: ">", arity: "number", ci: false },
  gte: { id: "gte", label: "≥", arity: "number", ci: false },
  contains: { id: "contains", label: "contains", arity: "text", ci: true },
  regex: { id: "regex", label: "matches regex", arity: "text", ci: true },
  in: { id: "in", label: "in list", arity: "list", ci: false },
  isnull: { id: "isnull", label: "is null", arity: "none", ci: false },
  notnull: { id: "notnull", label: "is not null", arity: "none", ci: false },
  bool: { id: "bool", label: "is", arity: "bool", ci: false },
};

// eq/ne are text-arity by default in OPS (shared with string columns' ci
// toggle), but for numeric columns they take a plain number operand with no
// case-insensitive toggle. Build numeric-specific variants rather than
// mutating the shared OPS entries.
const NUMERIC_EQ: OpSpec = { id: "eq", label: OPS.eq.label, arity: "number", ci: false };
const NUMERIC_NE: OpSpec = { id: "ne", label: OPS.ne.label, arity: "number", ci: false };

const NUMERIC_OPS: OpSpec[] = [
  NUMERIC_EQ,
  NUMERIC_NE,
  OPS.lt,
  OPS.lte,
  OPS.gt,
  OPS.gte,
  OPS.in,
  OPS.isnull,
  OPS.notnull,
];

const STRING_OPS: OpSpec[] = [OPS.eq, OPS.ne, OPS.contains, OPS.regex, OPS.in, OPS.isnull, OPS.notnull];

const BOOL_OPS: OpSpec[] = [OPS.bool, OPS.isnull, OPS.notnull];

// Container types (object/array/null) and "mixed" only ever offer the two
// null-checks: comparison ops skip non-scalar/null values in the engine
// (internal/query/filter.go:344-368), so nothing else is meaningful. This is
// also the safe fallback for any column type this module doesn't recognize,
// since isnull/notnull are valid for any column regardless of type.
const NULL_ONLY_OPS: OpSpec[] = [OPS.isnull, OPS.notnull];

/** Returns the ordered list of operators offered for a column of the given
 *  `Column.type` (int|float|string|bool|object|array|null|"mixed"). Falls
 *  back to isnull/notnull for any unrecognized type. */
export function operatorsForType(colType: string): OpSpec[] {
  switch (colType) {
    case "int":
    case "float":
      return NUMERIC_OPS;
    case "string":
      return STRING_OPS;
    case "bool":
      return BOOL_OPS;
    case "object":
    case "array":
    case "null":
    case "mixed":
      return NULL_ONLY_OPS;
    default:
      return NULL_ONLY_OPS;
  }
}

/** Returns the operator a fresh condition row should start on for a column
 *  of the given type. */
export function defaultOpForType(colType: string): OpId {
  switch (colType) {
    case "int":
    case "float":
      return "gte";
    case "string":
      return "contains";
    case "bool":
      return "bool";
    default:
      return "isnull";
  }
}

/** Human-readable label for every OpId, derived from OPS so it can never
 *  drift out of sync with the vocabulary (see operators.test.ts's
 *  Object.keys(OP_LABELS).length === 12 exhaustiveness guard). */
export const OP_LABELS: Record<OpId, string> = Object.fromEntries(
  (Object.keys(OPS) as OpId[]).map((id) => [id, OPS[id].label])
) as Record<OpId, string>;
