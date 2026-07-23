// Package query's codegen layer (spec §7): turns the SAME Filter/Transform
// the engine executes into the equivalent `jq` expression and SQL query, so a
// user who built a query by clicking can copy it out, run it elsewhere, and
// learn the syntax from their own data.
//
// This file is DISPLAY codegen: literals are inlined and the output is meant
// to be read and pasted. The pushdown planner (sqlpushdown.go) builds a
// parameterised WHERE for the engine to execute instead; the two share this
// file's operator vocabulary but deliberately not its string builder, because
// "readable by a human" and "safe to execute" are different requirements.
package query

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Warnings the generated programs carry when a construct cannot mean exactly
// what the engine means. They are emitted once per output, not per condition.
const (
	warnRegex           = "regex matching differs by target: shape uses Go RE2, jq uses Oniguruma, and SQLite has no REGEXP function unless one is registered"
	warnCaseInsensitive = "case-insensitive matching folds ASCII only in both jq (ascii_downcase) and SQLite (lower()); shape folds full Unicode, so non-ASCII letters can differ"
	warnEmptyIn         = "an empty in-list matches nothing (rendered as false / 1=0)"
	warnIllustrativeSQL = "this source is not a database: the SQL is illustrative, over a flat table named data"
)

// jqIdentRe matches a path segment that can be written as a bare jq field
// (`.name`); anything else must go through the bracket form (`.["odd key"]`).
var jqIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// jqPredicatePath renders segs as a jq path expression for use inside a
// CONDITION, e.g. `.user?.name?` or `.tags?[]?`.
//
// Every segment carries a `?`. That is not defensive noise: `.a.b` against
// {"a":"str"} is a hard jq error ("Cannot index string with b") which aborts
// the ENTIRE stream, while the engine's resolve() (columns.go) simply yields
// no values and the condition evaluates false (spec §5). One sparse record in
// a million-line file would otherwise kill the whole program. Because `?`
// yields EMPTY rather than false, and empty propagates through `not`, callers
// must additionally wrap each condition in `(...) // false` -- see jqCondition.
//
// The root path ("$", no segments) is jq's identity, `.`.
func jqPredicatePath(segs []Seg) string {
	if len(segs) == 0 {
		return "."
	}
	var sb strings.Builder
	for _, s := range segs {
		if s.Elem {
			sb.WriteString("[]?")
			continue
		}
		if jqIdentRe.MatchString(s.Key) {
			sb.WriteString(".")
			sb.WriteString(s.Key)
			sb.WriteString("?")
			continue
		}
		// A key that is not a bare identifier keeps its exact characters,
		// escaped as JSON -- which is precisely what jq's bracket form parses.
		key, _ := json.Marshal(s.Key)
		sb.WriteString(".[")
		sb.Write(key)
		sb.WriteString("]?")
	}
	return sb.String()
}

// jqProjectionPath renders segs for use as a Select output VALUE.
//
// It differs from the predicate form in exactly one way: a path containing an
// array wildcard is wrapped as `[...][0]`. A bare `.tags[]` is a generator --
// `{ "tag": .tags[] }` emits one record PER ELEMENT, fanning one input row
// into N -- whereas CompiledTransform.Project takes the FIRST resolved value
// and emits exactly one row (transform.go). `[...][0]` reproduces that:
// ["x","y"] -> "x", [] -> null, key absent -> null, [false,..] -> false
// (preserved, which `// null` would not do).
func jqProjectionPath(segs []Seg) string {
	path := jqPredicatePath(segs)
	for _, s := range segs {
		if s.Elem {
			return "[" + path + "][0]"
		}
	}
	return path
}

// sqlPathExpr renders a path as a SQL expression, returning the expression and
// any warnings the caller should surface.
//
// The lookup order matters and is not obvious: ColumnModel keys byPath on the
// FULL dotted path (columns.go), so for a JSON/CSV source `user.name` really
// is a column of the flattened model -- and the illustrative `data` table this
// codegen targets for non-SQLite sources is flat by definition. Treating every
// dotted path as nested JSON would generate `json_extract("user",'$.name')`
// against a table that has no `user` column at all, and would equally break a
// REAL SQLite column literally named `a.b`.
//
//  1. the full path is a known column -> one quoted identifier;
//  2. otherwise, a non-SQLite target -> still one quoted identifier (flat);
//  3. otherwise (SQLite, not a column) -> json_extract on the first segment,
//     plus a warning, since shape cannot know the value really is JSON.
func sqlPathExpr(path string, segs []Seg, cols *ColumnModel, sqlite bool) (string, []string) {
	if cols != nil {
		if _, ok := cols.byPath[path]; ok {
			return sqliteQuoteIdent(path), nil
		}
	}
	if !sqlite {
		return sqliteQuoteIdent(path), nil
	}
	if len(segs) == 0 {
		return sqliteQuoteIdent(path), nil
	}
	root := sqliteQuoteIdent(segs[0].Key)
	return fmt.Sprintf("json_extract(%s,'%s')", root, jsonPathOf(segs[1:])),
		[]string{fmt.Sprintf("%q is not a column of this table; the SQL reads it as JSON, which only works if the column really holds JSON", path)}
}

// jsonPathOf renders segs as a SQLite JSONPath body ("$.a.b", "$.a[0]").
// A key that is not a bare identifier is double-quoted, so a literal key
// containing a dot selects {"a.b":..} rather than {"a":{"b":..}}.
func jsonPathOf(segs []Seg) string {
	var sb strings.Builder
	sb.WriteString("$")
	for _, s := range segs {
		if s.Elem {
			// The engine's Elem means "any element"; a JSONPath cannot express
			// that inside json_extract, so the caller (sqlCondition) routes
			// Elem paths through json_each instead. Index 0 keeps this
			// function total for any accidental caller.
			sb.WriteString("[0]")
			continue
		}
		if jqIdentRe.MatchString(s.Key) {
			sb.WriteString(".")
			sb.WriteString(s.Key)
			continue
		}
		sb.WriteString(`."`)
		sb.WriteString(strings.ReplaceAll(s.Key, `"`, `\"`))
		sb.WriteString(`"`)
	}
	return sb.String()
}

// sqlNumber renders a float64 as a numeric literal, with encoding/json's
// formatting rules: plain decimal for ordinary magnitudes, exponent form
// outside [1e-6, 1e21), and an ERROR for NaN/±Inf.
//
// Both halves are load-bearing. 'g' formatting would print 1e6 as "1e+06";
// plain 'f' formatting would print 1e300 as a 301-character literal; and
// FormatFloat(NaN) yields the bare word "NaN", which SQLite parses as an
// identifier and rejects with `no such column: NaN` -- a generated string that
// cannot execute. json.Marshal already implements exactly this policy, so it
// is used rather than reimplemented.
func sqlNumber(f float64) (string, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("query: codegen: %w", err)
	}
	return string(b), nil
}

// jqLiteral renders a Value as a jq literal. jq consumes JSON syntax, so
// json.Marshal is not merely convenient here -- it is the exact grammar.
func jqLiteral(v Value) (string, error) {
	switch v.Kind {
	case ValString:
		b, err := json.Marshal(v.Str)
		if err != nil {
			return "", fmt.Errorf("query: codegen: %w", err)
		}
		return string(b), nil
	case ValNumber:
		return sqlNumber(v.Num)
	case ValBool:
		if v.Bool {
			return "true", nil
		}
		return "false", nil
	case ValNull:
		return "null", nil
	default:
		return "", fmt.Errorf("query: codegen: unknown value kind %q", v.Kind)
	}
}

// sqlValueLiteral renders a Value as a SQL literal: single-quoted with embedded
// quotes doubled, numbers via sqlNumber, booleans as 1/0 (SQLite has no
// boolean storage class), null as NULL.
func sqlValueLiteral(v Value) (string, error) {
	switch v.Kind {
	case ValString:
		return "'" + strings.ReplaceAll(v.Str, "'", "''") + "'", nil
	case ValNumber:
		return sqlNumber(v.Num)
	case ValBool:
		if v.Bool {
			return "1", nil
		}
		return "0", nil
	case ValNull:
		return "NULL", nil
	default:
		return "", fmt.Errorf("query: codegen: unknown value kind %q", v.Kind)
	}
}

// Generated is the codegen result: the two programs plus any caveats a user
// must see before trusting them elsewhere (spec §7).
type Generated struct {
	JQ       string   `json:"jq"`
	SQL      string   `json:"sql"`
	Warnings []string `json:"warnings,omitempty"`
}

// CodegenRequest is the Codegen request DTO (spec §8).
type CodegenRequest struct {
	Handle    string    `json:"handle"`
	Filter    Filter    `json:"filter"`
	Transform Transform `json:"transform"`
}

// Codegen renders f+t as the equivalent jq program and SQL query.
//
// It is PURE: no engine state, no I/O, no scanning. That is what makes it
// golden-testable in isolation, and it is also a promise to the caller -- the
// GUI refreshes this on every filter keystroke, so it must never touch data.
func Codegen(f Filter, t Transform, ctx CodegenContext) (Generated, error) {
	jq, jqWarnings, err := jqProgram(f, t, ctx)
	if err != nil {
		return Generated{}, err
	}
	sqlText, sqlWarnings, err := sqlQuery(f, t, ctx)
	if err != nil {
		return Generated{}, err
	}
	return Generated{
		JQ:       jq,
		SQL:      sqlText,
		Warnings: dedupeWarnings(append(jqWarnings, sqlWarnings...)),
	}, nil
}

// dedupeWarnings keeps the first occurrence of each distinct warning, in
// order: the same caveat reached once per condition would otherwise be
// repeated as many times as the filter has conditions.
func dedupeWarnings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, w := range in {
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

// Codegen renders the jq/SQL equivalent of a request's filter+transform for an
// open handle (spec §8). It looks the handle up only to fill in the source's
// format, table and column model -- it never queries the backend.
func (e *Engine) Codegen(req CodegenRequest) (Generated, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return Generated{}, err
	}
	meta := e.sourceMetaOf(req.Handle)
	ctx := CodegenContext{
		Format: meta.format,
		Table:  meta.table,
		Cols:   backend.Columns(),
	}
	if sb, ok := backend.(*sqlBackend); ok {
		ctx.Tainted = sb.taintedColumns()
	}
	return Codegen(req.Filter, req.Transform, ctx)
}
