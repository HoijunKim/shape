package query

import (
	"math"
	"strings"
)

// maxExactFloat is the magnitude above which a float64 operand can no longer
// be trusted against SQLite.
//
// Value.Num is a float64 and the Go predicate compares float-to-float, but
// SQLite compares an INTEGER column against a bound REAL EXACTLY. At 2^53 the
// two disagree: rows 9007199254740992 and 9007199254740993 both round to the
// same float64, so the Go predicate matches both while SQLite matches one.
// Snowflake/bigint IDs are the ordinary case for a SQLite source, so this is
// not a theoretical bound. The comparison is strict (`<`), because 2^53 itself
// already aliases with 2^53+1. NaN/±Inf fail it for free.
const maxExactFloat = float64(1 << 53)

// sqlPushdown compiles f into a parameterised WHERE fragment for sqlBackend,
// or reports that it cannot.
//
// EXACT-OR-NOTHING: exact is true only when the returned WHERE selects
// PRECISELY the rows the Go predicate would. Anything less is useless here --
// a superset WHERE would forbid pushing LIMIT/OFFSET/COUNT(*), which is the
// entire win -- so any doubt returns (nil, nil, false) and the caller keeps
// the existing full-scan + Go-predicate path.
//
// The preconditions below are not defensive padding: every one of them was
// added because an experiment showed SQLite returning different rows than the
// engine. See each comment for the case it closes.
func sqlPushdown(f Filter, cols *ColumnModel, noPush map[string]bool) (string, []any, bool) {
	p := &pushdownPlanner{cols: cols, noPush: noPush}
	where, ok := p.filter(f)
	if !ok {
		return "", nil, false
	}
	return where, p.args, true
}

type pushdownPlanner struct {
	cols   *ColumnModel
	noPush map[string]bool
	args   []any
}

func (p *pushdownPlanner) filter(f Filter) (string, bool) {
	// NOT(NULL) is NULL in SQL, so a row the Go engine keeps (inner false ->
	// negate true) is dropped by SQLite. The UI emits no Negate today; this
	// makes sure it cannot start silently.
	if f.Negate {
		return "", false
	}
	// A zero-term node's truth value is combinator-dependent (compileGroup
	// seeds true for AND, false for OR). Rather than encode that here, refuse
	// it: it cannot appear from the UI and the display codegen already has to
	// render it correctly.
	if len(f.Conditions) == 0 && len(f.Groups) == 0 {
		return "", false
	}

	parts := make([]string, 0, len(f.Conditions)+len(f.Groups))
	for _, c := range f.Conditions {
		expr, ok := p.condition(c)
		if !ok {
			return "", false
		}
		parts = append(parts, expr)
	}
	for _, g := range f.Groups {
		expr, ok := p.filter(g)
		if !ok {
			return "", false
		}
		parts = append(parts, expr)
	}
	op := " AND "
	if f.Combinator == Or {
		op = " OR "
	}
	if len(parts) == 1 {
		return parts[0], true
	}
	return "(" + strings.Join(parts, op) + ")", true
}

func (p *pushdownPlanner) condition(c Condition) (string, bool) {
	// A regex needs a REGEXP function SQLite does not ship, and shape
	// registers none.
	if c.Op == OpRegex {
		return "", false
	}
	// SQLite's lower() folds ASCII only; the engine folds full Unicode. A
	// Turkish dotted I would silently match differently.
	if c.CaseInsensitive {
		return "", false
	}

	col, ok := p.column(c.Path)
	if !ok {
		return "", false
	}
	// A column whose stored value differs from what the engine sees (a BLOB,
	// or a date the driver rewrites to RFC3339) can never be compared the same
	// way twice. Refuse every op on it, including isnull -- which would in
	// fact be safe -- because one rule is easier to keep true than two.
	if p.noPush[col.Path] {
		return "", false
	}

	quoted := sqliteQuoteIdent(col.Path)

	switch c.Op {
	case OpIsNull:
		return quoted + " IS NULL", true
	case OpNotNull:
		return quoted + " IS NOT NULL", true
	}

	switch c.Op {
	case OpEq, OpNe:
		arg, ok := p.operand(col, c.Value)
		if !ok {
			return "", false
		}
		op := " = "
		if c.Op == OpNe {
			op = " <> "
		}
		p.args = append(p.args, arg)
		return p.collated(quoted, col) + op + "?", true

	case OpLt, OpLte, OpGt, OpGte:
		// A STRING operand is never safe for an ordering comparison: SQLite
		// applies the COLUMN's affinity to the bound parameter, so on an
		// INTEGER-affinity column holding text, `x < '2'` converts '2' to 2
		// and storage-class ordering (INTEGER below TEXT) inverts the result.
		// COLLATE does not fix it -- affinity and collation are different
		// mechanisms.
		if c.Value.Kind != ValNumber {
			return "", false
		}
		arg, ok := p.operand(col, c.Value)
		if !ok {
			return "", false
		}
		p.args = append(p.args, arg)
		return p.collated(quoted, col) + " " + sqlRangeOp(c.Op) + " ?", true

	case OpContains:
		if c.Value.Kind != ValString || col.Type != "string" {
			return "", false
		}
		p.args = append(p.args, c.Value.Str)
		// instr is collation-blind, so no COLLATE is needed here.
		return "instr(" + quoted + ",?)>0", true

	case OpIn:
		if len(c.Value.List) == 0 {
			return "1=0", true
		}
		placeholders := make([]string, 0, len(c.Value.List))
		for _, item := range c.Value.List {
			arg, ok := p.operand(col, item)
			if !ok {
				return "", false
			}
			p.args = append(p.args, arg)
			placeholders = append(placeholders, "?")
		}
		// COLLATE binds to the LEFT operand: `x IN (?,?) COLLATE BINARY`
		// attaches to the last list element and is a verified no-op.
		return p.collated(quoted, col) + " IN (" + strings.Join(placeholders, ",") + ")", true

	default:
		// OpBool included: Column.Type can never be "bool" for a SQLite
		// source (the driver returns int64, which profiles as "int"), so
		// there is no column this could correctly apply to.
		return "", false
	}
}

// collated annotates a string column so the comparison is byte-exact.
//
// A column declared `TEXT COLLATE NOCASE` (or RTRIM) makes =, <>, <, <=, >,
// >= and IN use that collation, so a case-SENSITIVE filter would return
// case-insensitive rows -- and PRAGMA table_info does not expose collation, so
// this cannot be detected, only overridden. On a numeric column the annotation
// is a verified no-op, so it needs no type branch, but restricting it to
// string columns keeps the generated SQL readable.
func (p *pushdownPlanner) collated(quoted string, col Column) string {
	if col.Type == "string" {
		return quoted + " COLLATE BINARY"
	}
	return quoted
}

// column resolves a path to a REAL top-level column of the model. SQLite rows
// are flat, so a dotted or array path is not a column at all -- and a model
// column whose own name contains a dot IS one, which is why the lookup is by
// full path rather than by segment count alone.
func (p *pushdownPlanner) column(path string) (Column, bool) {
	if p.cols == nil {
		return Column{}, false
	}
	idx, ok := p.cols.byPath[path]
	if !ok {
		return Column{}, false
	}
	segs := p.cols.segs[idx]
	if len(segs) != 1 || segs[0].Elem {
		return Column{}, false
	}
	return p.cols.Columns[idx], true
}

// operand converts a Value into a bound argument, refusing anything whose
// type does not match the column's or whose magnitude SQLite would compare
// differently.
//
// The type gate exists because SQLite's cross-type comparison follows storage
// class ordering (INTEGER < TEXT), whereas the Go predicate returns FALSE on a
// type mismatch: the same query would select different rows.
func (p *pushdownPlanner) operand(col Column, v Value) (any, bool) {
	switch v.Kind {
	case ValNumber:
		if col.Type != "int" && col.Type != "float" {
			return nil, false
		}
		if !(math.Abs(v.Num) < maxExactFloat) { // also rejects NaN/±Inf
			return nil, false
		}
		// Always float64, never int64(v.Num): int64 truncates 1.5 to 1 and
		// overflows past 9.22e18. With the magnitude gate above the two are
		// equivalent to SQLite, so float64 is pinned for deterministic args.
		return v.Num, true
	case ValString:
		if col.Type != "string" {
			return nil, false
		}
		return v.Str, true
	case ValBool:
		// Unreachable for sqlBackend (no column profiles as "bool"), and a
		// bool operand against an int column would need a 1/0 convention the
		// Go predicate does not share.
		return nil, false
	default:
		return nil, false
	}
}
