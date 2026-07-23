package query

import (
	"fmt"
	"strings"
)

// CodegenContext describes the source a generated program will run against:
// which invocation note to print, which table name the SQL should use, and
// which columns exist (so a path can be told apart from a JSON lookup).
type CodegenContext struct {
	Format string       `json:"format"` // "json"|"ndjson"|"csv"|"parquet"|"sqlite"
	Table  string       `json:"table,omitempty"`
	Cols   *ColumnModel `json:"-"`
}

// jqCondition renders one Condition as a jq boolean expression.
//
// Two rules govern every template here and are easy to get wrong:
//
//  1. PARENTHESES. jq's `|` is the LOWEST-precedence operator and rebinds `.`
//     for everything to its right, so `(.p|type=="string" and (.p|contains(V)))`
//     parses as `.p | (type=="string" and (.p|contains(V)))` -- the inner `.p`
//     then indexes the already-piped STRING and jq dies with "Cannot index
//     string with string". Worse, `and` short-circuits, so it fails precisely
//     on the rows the condition was meant to match. Every `|` a generated
//     expression emits is therefore wrapped in its own parentheses. (This
//     supersedes spec §7, whose templates have the bug; verified against jq
//     1.7.1.)
//
//  2. TYPE GUARDS. The engine's typedEqual/matchesRange (filter.go) make a
//     kind mismatch FALSE, but jq has a total order across types: `.p != 5`
//     selects {"p":"hello"}, and `.age > 18` selects {"age":"unknown"}. So
//     `ne` and the ordering ops carry an explicit guard on the OPERAND's kind
//     -- which is what the Go predicate branches on, not the column's declared
//     type. `eq` needs none: jq's `==` is already type-aware.
//
// The returned expression is a bare boolean; jqFilter wraps it in `(...)
// // false` so a `?`-yielded empty (a sparse record) becomes false rather
// than propagating.
func jqCondition(c Condition, cols *ColumnModel) (string, []string, error) {
	segs := parsePath(c.Path)
	var warnings []string

	// An Elem path is a generator, so the whole condition becomes any(...),
	// whose empty case is already false -- matching the Go rule that a path
	// resolving to no values makes the condition false.
	if hasElemSeg(segs) {
		inner, w, err := jqComparison(c, "", true)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, w...)
		return fmt.Sprintf("any(%s; %s)", jqPredicatePath(segs), inner), warnings, nil
	}

	expr, w, err := jqComparison(c, jqPredicatePath(segs), false)
	if err != nil {
		return "", nil, err
	}
	return expr, append(warnings, w...), nil
}

// jqComparison renders the operator itself. path is the jq path expression
// ("" when the caller is an any() body, where the subject is `.`).
func jqComparison(c Condition, path string, elem bool) (string, []string, error) {
	subject := path
	typeOf := "(" + path + "|type)"
	if elem {
		subject = "."
		typeOf = "(type)"
	}
	var warnings []string

	// isnull/notnull carry NO operand (filter.go), so the literal must be
	// rendered lazily -- eagerly formatting c.Value would fail on their empty
	// Value.Kind.
	switch c.Op {
	case OpIsNull:
		return fmt.Sprintf("(%s == null)", subject), nil, nil
	case OpNotNull:
		return fmt.Sprintf("(%s != null)", subject), nil, nil
	}

	lit, err := jqLiteral(c.Value)
	if err != nil {
		return "", nil, err
	}

	switch c.Op {
	case OpBool:
		return fmt.Sprintf("(%s == %s)", subject, lit), nil, nil

	case OpEq, OpNe:
		if c.CaseInsensitive && c.Value.Kind == ValString {
			warnings = append(warnings, warnCaseInsensitive)
			op := "=="
			if c.Op == OpNe {
				op = "!="
			}
			// The type guard is mandatory, not cosmetic: ascii_downcase
			// ERRORS on a non-string, and an error inside select() aborts the
			// whole program.
			return fmt.Sprintf(`((%s=="string") and ((%s|ascii_downcase) %s (%s|ascii_downcase)))`,
				typeOf, subject, op, lit), warnings, nil
		}
		if c.Op == OpEq {
			// jq's == is type-aware ({"p":"5"} vs .p == 5 selects nothing),
			// so only the null guard is needed.
			return fmt.Sprintf("(%s != null and %s == %s)", subject, subject, lit), nil, nil
		}
		guard, err := jqTypeGuard(typeOf, c.Value)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("(%s and %s != %s)", guard, subject, lit), nil, nil

	case OpLt, OpLte, OpGt, OpGte:
		guard, err := jqTypeGuard(typeOf, c.Value)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("(%s and %s %s %s)", guard, subject, jqRangeOp(c.Op), lit), nil, nil

	case OpContains:
		if c.CaseInsensitive {
			warnings = append(warnings, warnCaseInsensitive)
			return fmt.Sprintf(`((%s=="string") and ((%s|ascii_downcase)|contains((%s|ascii_downcase))))`,
				typeOf, subject, lit), warnings, nil
		}
		return fmt.Sprintf(`((%s=="string") and (%s|contains(%s)))`, typeOf, subject, lit), nil, nil

	case OpRegex:
		warnings = append(warnings, warnRegex)
		flags := ""
		if c.CaseInsensitive {
			warnings = append(warnings, warnCaseInsensitive)
			flags = `;"i"`
		}
		return fmt.Sprintf(`((%s=="string") and (%s|test(%s%s)))`, typeOf, subject, lit, flags), warnings, nil

	case OpIn:
		if len(c.Value.List) == 0 {
			return "false", []string{warnEmptyIn}, nil
		}
		lits := make([]string, 0, len(c.Value.List))
		for _, item := range c.Value.List {
			l, err := jqLiteral(item)
			if err != nil {
				return "", nil, err
			}
			lits = append(lits, l)
		}
		return fmt.Sprintf("(%s as $x|any(%s;.==$x))", subject, strings.Join(lits, ",")), nil, nil

	default:
		return "", nil, fmt.Errorf("query: codegen: unknown operator %q", c.Op)
	}
}

// jqTypeGuard renders the operand-kind guard the Go predicate implies.
func jqTypeGuard(typeOf string, v Value) (string, error) {
	switch v.Kind {
	case ValNumber:
		return fmt.Sprintf(`(%s=="number")`, typeOf), nil
	case ValString:
		return fmt.Sprintf(`(%s=="string")`, typeOf), nil
	case ValBool:
		return fmt.Sprintf(`(%s=="boolean")`, typeOf), nil
	case ValNull:
		// A null operand can never satisfy a comparison in the Go engine
		// (missing-or-null is false for every comparison op, spec §5).
		return "false", nil
	default:
		return "", fmt.Errorf("query: codegen: unknown value kind %q", v.Kind)
	}
}

func jqRangeOp(op Op) string {
	switch op {
	case OpLt:
		return "<"
	case OpLte:
		return "<="
	case OpGt:
		return ">"
	default:
		return ">="
	}
}

// jqFilter renders a whole Filter as a jq boolean expression.
//
// A node with no conditions and no groups emits its combinator's IDENTITY
// (`true` for and, `false` for or), matching compileGroup (filter.go), so a
// childless OR keeps meaning "matches nothing" and Negate has something
// well-defined to invert. Emitting nothing instead would silently flip that
// to "matches everything".
func jqFilter(f Filter, cols *ColumnModel) (string, []string, error) {
	var parts []string
	var warnings []string

	for _, c := range f.Conditions {
		expr, w, err := jqCondition(c, cols)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, w...)
		// ((...) // false): a `?`-suffixed path yields EMPTY for a sparse
		// record, and empty propagates through `not`, so it is pinned to false
		// here. The OUTER parens are not cosmetic -- jq binds `//` LOOSER than
		// `and`/`or`, so `A // false and B // false` parses as
		// `A // (false and B) // false` and a two-condition AND returns the
		// wrong rows (verified against jq 1.7.1: the unparenthesised form
		// yields true where the parenthesised one correctly yields false).
		parts = append(parts, "("+expr+" // false)")
	}
	for _, g := range f.Groups {
		expr, w, err := jqFilter(g, cols)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, w...)
		parts = append(parts, expr)
	}

	var body string
	switch {
	case len(parts) == 0:
		body = jqIdentity(f.Combinator)
	case len(parts) == 1:
		body = parts[0]
	default:
		body = "(" + strings.Join(parts, " "+jqCombinator(f.Combinator)+" ") + ")"
	}
	if f.Negate {
		body = "(" + body + " | not)"
	}
	return body, warnings, nil
}

func jqCombinator(c Combinator) string {
	if c == Or {
		return "or"
	}
	return "and"
}

func jqIdentity(c Combinator) string {
	if c == Or {
		return "false"
	}
	return "true"
}

// jqProjection renders a Transform as the program's output stage.
func jqProjection(t Transform) (string, error) {
	if len(t.Select) > 0 {
		parts := make([]string, 0, len(t.Select))
		for _, spec := range t.Select {
			segs := parsePath(spec.Path)
			name := spec.As
			if name == "" {
				name = columnName(spec.Path, segs)
			}
			key, err := jqLiteral(Value{Kind: ValString, Str: name})
			if err != nil {
				return "", err
			}
			parts = append(parts, fmt.Sprintf("%s: %s", key, jqProjectionPath(segs)))
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	if len(t.Drop) > 0 {
		paths := make([]string, 0, len(t.Drop))
		for _, d := range t.Drop {
			paths = append(paths, jqPredicatePath(parsePath(d)))
		}
		return "del(" + strings.Join(paths, ",") + ")", nil
	}
	return "", nil
}

// jqInvocationNote is the leading `# ` comment telling the user how to feed
// this source to jq at all -- the program is useless without it for a JSON
// array (which needs `.[] |`) and impossible for the binary formats.
func jqInvocationNote(format string) string {
	switch format {
	case "ndjson":
		return "# jq: one JSON object per line -- run: jq '<program>' file.ndjson"
	case "json":
		return "# jq: the file is one JSON array -- run: jq '.[] | <program>' file.json"
	case "csv", "parquet", "sqlite":
		return "# jq needs JSON input; convert " + format + " first, or use the SQL below"
	default:
		return "# jq: run: jq '<program>' file.json"
	}
}

// jqProgram renders the complete jq program for a filter + transform.
//
// `select` is omitted for an empty filter and the projection stage is omitted
// for an identity transform, so a match-all identity query generates `.` --
// not `select(true) | .`, which would be noise the user has to read past.
func jqProgram(f Filter, t Transform, ctx CodegenContext) (string, []string, error) {
	var warnings []string
	var stages []string

	if !isEmptyFilter(f) {
		expr, w, err := jqFilter(f, ctx.Cols)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, w...)
		stages = append(stages, "select("+expr+")")
	}
	proj, err := jqProjection(t)
	if err != nil {
		return "", nil, err
	}
	if proj != "" {
		stages = append(stages, proj)
	}
	if len(stages) == 0 {
		stages = append(stages, ".")
	}

	return jqInvocationNote(ctx.Format) + "\n" + strings.Join(stages, " | "), warnings, nil
}
