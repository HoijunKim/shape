package query

import (
	"fmt"
	"strings"
)

// illustrativeTable is the table name the generated SQL uses when the source
// is NOT a database: the query is illustrative, over an imagined flat table.
const illustrativeTable = "data"

// sqlTargetsSQLite reports whether the generated SQL would actually run
// against the user's own source. Two rules depend on it: the table name, and
// whether COLLATE BINARY is worth emitting (a declared collation can only
// exist in a real database).
func (ctx CodegenContext) sqlTargetsSQLite() bool { return ctx.Format == "sqlite" }

func (ctx CodegenContext) sqlTable() string {
	if ctx.sqlTargetsSQLite() && ctx.Table != "" {
		return sqliteQuoteIdent(ctx.Table)
	}
	return sqliteQuoteIdent(illustrativeTable)
}

// sqlCondition renders one Condition as a SQL boolean expression.
//
// Two rules here exist because SQLite disagrees with the Go predicate unless
// they are applied, both verified against the vendored driver:
//
//   - COLLATE BINARY on the COLUMN operand of every string comparison. A
//     column declared `TEXT COLLATE NOCASE` makes `=` case-insensitive, so a
//     case-SENSITIVE filter would return case-insensitive rows ('Apple' and
//     'apple' both matching 'apple'), while the engine is byte-exact.
//     PRAGMA table_info does not expose collation, so it cannot be detected --
//     only overridden. Placement matters: `x COLLATE BINARY IN (?,?)` works,
//     `x IN (?,?) COLLATE BINARY` binds to the last list element and is a
//     no-op.
//   - `instr(...)>0` rather than LIKE for `contains`, which sidesteps the
//     %/_/ESCAPE foot-guns entirely and is collation-blind (so it needs no
//     COLLATE).
func sqlCondition(c Condition, ctx CodegenContext) (string, []string, error) {
	segs := parsePath(c.Path)
	var warnings []string

	if ctx.Tainted[c.Path] {
		warnings = append(warnings, fmt.Sprintf(
			"column %q holds values SQLite stores differently from what shape shows (a BLOB, or a date the driver converts): this condition may not match the same rows in SQLite", c.Path))
	}

	// An Elem path becomes an EXISTS over json_each, so the comparison is
	// rendered against each element value rather than the column. The path
	// expression is resolved ONLY for the base container here -- resolving the
	// full `tags[]` first (as this used to) produced a spurious "not a column"
	// warning for a path that is a real container, just not a scalar column.
	if hasElemSeg(segs) {
		base, w := sqlPathExpr(elemBasePath(c.Path), elemBaseSegs(segs), ctx.Cols, ctx.sqlTargetsSQLite())
		warnings = append(warnings, w...)
		inner, w2, err := sqlComparison(c, "j.value", false)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, w2...)
		return fmt.Sprintf("EXISTS(SELECT 1 FROM json_each(%s) j WHERE %s)", base, inner), warnings, nil
	}

	pathExpr, w := sqlPathExpr(c.Path, segs, ctx.Cols, ctx.sqlTargetsSQLite())
	warnings = append(warnings, w...)

	expr, w, err := sqlComparison(c, pathExpr, ctx.sqlTargetsSQLite())
	if err != nil {
		return "", nil, err
	}
	return expr, append(warnings, w...), nil
}

// elemBaseSegs drops the trailing Elem segment(s) so json_each gets the
// container itself.
func elemBaseSegs(segs []Seg) []Seg {
	out := make([]Seg, 0, len(segs))
	for _, s := range segs {
		if s.Elem {
			continue
		}
		out = append(out, s)
	}
	return out
}

func elemBasePath(path string) string {
	return strings.ReplaceAll(path, "[]", "")
}

// sqlComparison renders the operator against an already-rendered subject.
// collate says whether the subject is a real column whose declared collation
// must be overridden (false inside json_each, whose values carry none).
func sqlComparison(c Condition, subject string, collate bool) (string, []string, error) {
	var warnings []string

	switch c.Op {
	case OpIsNull:
		return subject + " IS NULL", nil, nil
	case OpNotNull:
		return subject + " IS NOT NULL", nil, nil
	}

	binary := subject
	if collate {
		binary = subject + " COLLATE BINARY"
	}

	switch c.Op {
	case OpBool:
		lit, err := sqlValueLiteral(c.Value)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("%s = %s", subject, lit), nil, nil

	case OpEq, OpNe:
		op := "="
		if c.Op == OpNe {
			op = "<>"
		}
		lit, err := sqlValueLiteral(c.Value)
		if err != nil {
			return "", nil, err
		}
		if c.CaseInsensitive && c.Value.Kind == ValString {
			warnings = append(warnings, warnCaseInsensitive)
			return fmt.Sprintf("lower(%s) %s lower(%s)", subject, op, lit), warnings, nil
		}
		if c.Op == OpNe {
			// eq agrees with the engine cross-type (both false), but ne does
			// not: the display SQL has no type guard, so "name" <> 5 returns
			// text rows the engine's typedEqual rejects.
			warnings = append(warnings, warnTypeGuard)
		}
		return fmt.Sprintf("%s %s %s", binary, op, lit), warnings, nil

	case OpLt, OpLte, OpGt, OpGte:
		// A bool operand never matches an ordering op in the engine
		// (matchesRange has no bool branch); SQL would order it.
		if c.Value.Kind == ValBool {
			return "1=0", nil, nil
		}
		lit, err := sqlValueLiteral(c.Value)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, warnTypeGuard)
		return fmt.Sprintf("%s %s %s", binary, sqlRangeOp(c.Op), lit), warnings, nil

	case OpContains:
		lit, err := sqlValueLiteral(c.Value)
		if err != nil {
			return "", nil, err
		}
		if c.CaseInsensitive {
			warnings = append(warnings, warnCaseInsensitive)
			return fmt.Sprintf("instr(lower(%s),lower(%s))>0", subject, lit), warnings, nil
		}
		return fmt.Sprintf("instr(%s,%s)>0", subject, lit), nil, nil

	case OpRegex:
		warnings = append(warnings, warnRegex)
		if c.CaseInsensitive {
			warnings = append(warnings, warnCaseInsensitive)
		}
		lit, err := sqlValueLiteral(c.Value)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("%s REGEXP %s", subject, lit), warnings, nil

	case OpIn:
		if len(c.Value.List) == 0 {
			return "1=0", []string{warnEmptyIn}, nil
		}
		lits := make([]string, 0, len(c.Value.List))
		for _, item := range c.Value.List {
			// The engine's `in` skips a null candidate (typedEqual is false
			// for null), so a literal NULL in the IN list -- which SQL would
			// never match anyway -- is simply omitted.
			if item.Kind == ValNull {
				continue
			}
			l, err := sqlValueLiteral(item)
			if err != nil {
				return "", nil, err
			}
			lits = append(lits, l)
		}
		if len(lits) == 0 {
			return "1=0", nil, nil
		}
		// COLLATE goes on the LEFT operand: a trailing COLLATE after the list
		// binds to its last element and silently does nothing.
		return fmt.Sprintf("%s IN (%s)", binary, strings.Join(lits, ",")), nil, nil

	default:
		return "", nil, fmt.Errorf("query: codegen: unknown operator %q", c.Op)
	}
}

func sqlRangeOp(op Op) string {
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

// sqlWhere renders a Filter as a SQL boolean expression. A zero-term node
// emits its combinator's identity (1=1 / 1=0) -- never an empty fragment,
// which would be a syntax error inside a group and would silently flip a
// childless OR from "matches nothing" to "matches everything".
func sqlWhere(f Filter, ctx CodegenContext) (string, []string, error) {
	var parts []string
	var warnings []string

	for _, c := range f.Conditions {
		expr, w, err := sqlCondition(c, ctx)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, w...)
		parts = append(parts, expr)
	}
	for _, g := range f.Groups {
		expr, w, err := sqlWhere(g, ctx)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, w...)
		parts = append(parts, expr)
	}

	var body string
	switch {
	case len(parts) == 0:
		body = sqlIdentity(f.Combinator)
	case len(parts) == 1:
		body = parts[0]
	default:
		body = "(" + strings.Join(parts, " "+sqlCombinator(f.Combinator)+" ") + ")"
	}
	if f.Negate {
		// NOT(NULL) is NULL in SQL, so a plain NOT(...) drops rows whose inner
		// comparison was NULL; the engine inverts a two-valued false and keeps
		// them. IFNULL(...,0) collapses the third value first, matching Go's
		// !result.
		body = "NOT IFNULL(" + body + ",0)"
	}
	return body, warnings, nil
}

func sqlCombinator(c Combinator) string {
	if c == Or {
		return "OR"
	}
	return "AND"
}

func sqlIdentity(c Combinator) string {
	if c == Or {
		return "1=0"
	}
	return "1=1"
}

// sqlSelectList renders the projection: an explicit Select becomes aliased
// columns in order, a Drop becomes the enumerated columns that survive it,
// and an identity transform stays "*".
func sqlSelectList(t Transform, ctx CodegenContext) string {
	if len(t.Select) > 0 {
		parts := make([]string, 0, len(t.Select))
		for _, spec := range t.Select {
			segs := parsePath(spec.Path)
			expr, _ := sqlPathExpr(spec.Path, segs, ctx.Cols, ctx.sqlTargetsSQLite())
			name := spec.As
			if name == "" {
				name = columnName(spec.Path, segs)
			}
			parts = append(parts, expr+" AS "+sqliteQuoteIdent(name))
		}
		return strings.Join(parts, ", ")
	}
	if len(t.Drop) > 0 && ctx.Cols != nil {
		var parts []string
		for _, col := range ctx.Cols.Columns {
			if isDropped(col.Path, t.Drop) {
				continue
			}
			expr, _ := sqlPathExpr(col.Path, parsePath(col.Path), ctx.Cols, ctx.sqlTargetsSQLite())
			parts = append(parts, expr)
		}
		// Even when Drop removes every column, the result is "select
		// nothing", not "select everything" -- falling through to "*" would
		// silently re-include the dropped columns. NULL is a valid
		// zero-column stand-in.
		if len(parts) == 0 {
			return "NULL"
		}
		return strings.Join(parts, ", ")
	}
	return "*"
}

// sqlQuery renders the complete SQL statement for a filter + transform.
func sqlQuery(f Filter, t Transform, ctx CodegenContext) (string, []string, error) {
	var warnings []string
	var lines []string

	if !ctx.sqlTargetsSQLite() {
		warnings = append(warnings, warnIllustrativeSQL)
		lines = append(lines, fmt.Sprintf("-- illustrative: this source is %s, not a database; the query assumes a flat table named %s", displayFormat(ctx.Format), illustrativeTable))
	}

	stmt := "SELECT " + sqlSelectList(t, ctx) + " FROM " + ctx.sqlTable()
	if !isEmptyFilter(f) {
		where, w, err := sqlWhere(f, ctx)
		if err != nil {
			return "", nil, err
		}
		warnings = append(warnings, w...)
		stmt += " WHERE " + where
	}
	lines = append(lines, stmt+";")

	// Caveat comments mirror the warnings, once each, so a user reading only
	// the SQL still sees them.
	if containsWarning(warnings, warnRegex) {
		lines = append(lines, "-- note: REGEXP needs a user-defined function in SQLite; shape matches with Go RE2, so this line is illustrative")
	}
	if containsWarning(warnings, warnCaseInsensitive) {
		lines = append(lines, "-- note: SQLite lower() folds ASCII only; shape folds full Unicode, so non-ASCII letters can differ")
	}
	if containsWarning(warnings, warnTypeGuard) {
		lines = append(lines, "-- note: != and the ordering operators have no type guard here; SQL compares across types, so a column holding text can match a numeric operand (and vice versa) where shape would not")
	}
	return strings.Join(lines, "\n"), warnings, nil
}

func containsWarning(warnings []string, want string) bool {
	for _, w := range warnings {
		if w == want {
			return true
		}
	}
	return false
}

// displayFormat renders a format for a human-readable comment.
func displayFormat(format string) string {
	if format == "" {
		return "not a database"
	}
	return format
}
