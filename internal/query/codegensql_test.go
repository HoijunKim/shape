package query

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func sqliteCtx(t *testing.T, records []map[string]any) CodegenContext {
	t.Helper()
	return CodegenContext{Format: "sqlite", Table: "t", Cols: codegenModel(t, records)}
}

func sqlCond(t *testing.T, c Condition, ctx CodegenContext) string {
	t.Helper()
	got, _, err := sqlCondition(c, ctx)
	if err != nil {
		t.Fatalf("sqlCondition error = %v, want nil", err)
	}
	return got
}

// --- goldens ----------------------------------------------------------------

func TestSQLCondition_Goldens(t *testing.T) {
	ctx := sqliteCtx(t, []map[string]any{
		{"age": json.Number("30"), "name": "ada", "tags": []any{"x"}, "ok": true},
	})
	num := Value{Kind: ValNumber, Num: 18}
	str := Value{Kind: ValString, Str: "ada"}

	cases := []struct {
		name string
		cond Condition
		want string
	}{
		// COLLATE BINARY is on the COLUMN operand of every comparison: a
		// column declared COLLATE NOCASE would otherwise make a
		// case-sensitive filter return case-insensitive rows.
		{"eq", Condition{Path: "name", Op: OpEq, Value: str}, `"name" COLLATE BINARY = 'ada'`},
		{"ne", Condition{Path: "name", Op: OpNe, Value: str}, `"name" COLLATE BINARY <> 'ada'`},
		{"lt", Condition{Path: "age", Op: OpLt, Value: num}, `"age" COLLATE BINARY < 18`},
		{"gte", Condition{Path: "age", Op: OpGte, Value: num}, `"age" COLLATE BINARY >= 18`},
		// instr, never LIKE: no %/_/ESCAPE foot-guns, and collation-blind.
		{"contains", Condition{Path: "name", Op: OpContains, Value: str}, `instr("name",'ada')>0`},
		{"contains ci", Condition{Path: "name", Op: OpContains, Value: str, CaseInsensitive: true},
			`instr(lower("name"),lower('ada'))>0`},
		{"eq ci", Condition{Path: "name", Op: OpEq, Value: str, CaseInsensitive: true},
			`lower("name") = lower('ada')`},
		{"regex", Condition{Path: "name", Op: OpRegex, Value: Value{Kind: ValString, Str: "^a"}},
			`"name" REGEXP '^a'`},
		{"isnull", Condition{Path: "name", Op: OpIsNull}, `"name" IS NULL`},
		{"notnull", Condition{Path: "name", Op: OpNotNull}, `"name" IS NOT NULL`},
		{"bool true", Condition{Path: "ok", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}, `"ok" = 1`},
		{"bool false", Condition{Path: "ok", Op: OpBool, Value: Value{Kind: ValBool, Bool: false}}, `"ok" = 0`},
		{"in", Condition{Path: "age", Op: OpIn, Value: Value{Kind: ValNumber, List: []Value{
			{Kind: ValNumber, Num: 1}, {Kind: ValNumber, Num: 2},
		}}}, `"age" COLLATE BINARY IN (1,2)`},
		{"in empty", Condition{Path: "age", Op: OpIn, Value: Value{Kind: ValNumber}}, `1=0`},
		{"array element", Condition{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}},
			`EXISTS(SELECT 1 FROM json_each("tags") j WHERE j.value = 'x')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sqlCond(t, tc.cond, ctx); got != tc.want {
				t.Fatalf("sqlCondition =\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

// TestSQLCondition_CollateOnlyForSQLiteTargets: the illustrative `data` table
// has no declared collation, so the annotation would be noise there.
func TestSQLCondition_CollateOnlyForSQLiteTargets(t *testing.T) {
	records := []map[string]any{{"name": "ada"}}
	ndjson := CodegenContext{Format: "ndjson", Cols: codegenModel(t, records)}
	got := sqlCond(t, Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "ada"}}, ndjson)
	if strings.Contains(got, "COLLATE") {
		t.Fatalf("non-sqlite target emitted COLLATE: %s", got)
	}
}

func TestSQLWhere_GroupsAndZeroTermNodes(t *testing.T) {
	ctx := sqliteCtx(t, []map[string]any{{"a": "x", "b": "y"}})
	c1 := Condition{Path: "a", Op: OpNotNull}
	c2 := Condition{Path: "b", Op: OpNotNull}

	cases := []struct {
		name string
		f    Filter
		want string
	}{
		{"and", Filter{Combinator: And, Conditions: []Condition{c1, c2}},
			`("a" IS NOT NULL AND "b" IS NOT NULL)`},
		{"or", Filter{Combinator: Or, Conditions: []Condition{c1, c2}},
			`("a" IS NOT NULL OR "b" IS NOT NULL)`},
		{"negate", Filter{Combinator: And, Conditions: []Condition{c1}, Negate: true},
			`NOT IFNULL("a" IS NOT NULL,0)`},
		{"childless and", Filter{Combinator: And}, `1=1`},
		{"childless or", Filter{Combinator: Or}, `1=0`},
		// A childless OR must stay "matches nothing". Dropping the empty
		// fragment would make this `("a" IS NOT NULL)` -- silently flipping it
		// to "matches everything the first condition matches".
		{"childless or under an and", Filter{Combinator: And, Conditions: []Condition{c1},
			Groups: []Filter{{Combinator: Or}}}, `("a" IS NOT NULL AND 1=0)`},
		{"childless and under an and", Filter{Combinator: And, Conditions: []Condition{c1},
			Groups: []Filter{{Combinator: And}}}, `("a" IS NOT NULL AND 1=1)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := sqlWhere(tc.f, ctx)
			if err != nil {
				t.Fatalf("sqlWhere error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("sqlWhere =\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

func TestSQLQuery_Shape(t *testing.T) {
	records := []map[string]any{{"a": "x", "b": "y"}}
	ctx := sqliteCtx(t, records)

	t.Run("empty filter omits WHERE entirely", func(t *testing.T) {
		got, _, err := sqlQuery(Filter{}, Transform{}, ctx)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if want := `SELECT * FROM "t";`; got != want {
			t.Fatalf("sqlQuery = %q, want %q", got, want)
		}
	})

	t.Run("select projects aliased columns in order", func(t *testing.T) {
		got, _, err := sqlQuery(Filter{}, Transform{Select: []ColumnSpec{
			{Path: "b", As: "second"}, {Path: "a", As: `he said "hi"`},
		}}, ctx)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		want := `SELECT "b" AS "second", "a" AS "he said ""hi""" FROM "t";`
		if got != want {
			t.Fatalf("sqlQuery =\n  %s\nwant\n  %s", got, want)
		}
	})

	t.Run("drop enumerates the surviving columns", func(t *testing.T) {
		got, _, err := sqlQuery(Filter{}, Transform{Drop: []string{"a"}}, ctx)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if want := `SELECT "b" FROM "t";`; got != want {
			t.Fatalf("sqlQuery = %q, want %q", got, want)
		}
	})

	t.Run("a non-sqlite source is labelled illustrative", func(t *testing.T) {
		nd := CodegenContext{Format: "ndjson", Cols: codegenModel(t, records)}
		got, warnings, err := sqlQuery(Filter{}, Transform{}, nd)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		if !strings.HasPrefix(got, "-- illustrative:") {
			t.Fatalf("missing illustrative prefix:\n%s", got)
		}
		if !strings.Contains(got, `FROM "data"`) {
			t.Fatalf("a non-database source must query the imagined flat table:\n%s", got)
		}
		if !containsWarning(warnings, warnIllustrativeSQL) {
			t.Fatalf("warnings = %v, want the illustrative warning", warnings)
		}
	})

	t.Run("caveats appear exactly once each", func(t *testing.T) {
		f := Filter{Combinator: And, Conditions: []Condition{
			{Path: "a", Op: OpRegex, Value: Value{Kind: ValString, Str: "^x"}},
			{Path: "b", Op: OpRegex, Value: Value{Kind: ValString, Str: "^y"}},
			{Path: "a", Op: OpContains, Value: Value{Kind: ValString, Str: "z"}, CaseInsensitive: true},
			{Path: "b", Op: OpContains, Value: Value{Kind: ValString, Str: "w"}, CaseInsensitive: true},
		}}
		got, _, err := sqlQuery(f, Transform{}, ctx)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		// Mutation that must break it: emit the caveat per condition.
		if n := strings.Count(got, "-- note: REGEXP"); n != 1 {
			t.Fatalf("REGEXP caveat appears %d times, want 1:\n%s", n, got)
		}
		if n := strings.Count(got, "-- note: SQLite lower()"); n != 1 {
			t.Fatalf("lower() caveat appears %d times, want 1:\n%s", n, got)
		}
	})

	t.Run("a tainted column is called out", func(t *testing.T) {
		tainted := ctx
		tainted.Tainted = map[string]bool{"a": true}
		_, warnings, err := sqlQuery(Filter{Combinator: And, Conditions: []Condition{
			{Path: "a", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}},
		}}, Transform{}, tainted)
		if err != nil {
			t.Fatalf("error = %v", err)
		}
		var found bool
		for _, w := range warnings {
			if strings.Contains(w, "BLOB") {
				found = true
			}
		}
		if !found {
			t.Fatalf("warnings = %v, want one naming the storage mismatch", warnings)
		}
	})
}

// --- executability ----------------------------------------------------------

// execFixture seeds an in-memory SQLite table whose columns are literally the
// model's Column.Path values, with a matching row, a non-matching row and a
// NULL row per column -- so a swapped operator cannot survive.
func execFixture(t *testing.T) (*sql.DB, CodegenContext) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// "meta" holds valid JSON (or NULL): a malformed non-NULL value makes
	// json_each raise "malformed JSON" at row-iteration time.
	if _, err := db.Exec(`CREATE TABLE t (name TEXT, age INTEGER, tags TEXT, meta TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows := []struct {
		name string
		age  any
		tags string
		meta any
	}{
		{"ada", 30, `["x","y"]`, `{"note":"hi"}`},
		{"bob", 10, `["z"]`, `{"note":"lo"}`},
		{"", nil, `[]`, nil},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO t (name,age,tags,meta) VALUES (?,?,?,?)`, r.name, r.age, r.tags, r.meta); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	records := []map[string]any{
		{"name": "ada", "age": json.Number("30"), "tags": `["x","y"]`, "meta": `{"note":"hi"}`},
	}
	return db, CodegenContext{Format: "sqlite", Table: "t", Cols: codegenModel(t, records)}
}

// execRowIDs runs a generated statement and returns the matching rowids, so a
// test can assert the EXACT ordered result set rather than "it parsed".
func execRowIDs(t *testing.T, db *sql.DB, where string) []int64 {
	t.Helper()
	q := `SELECT _rowid_ FROM t WHERE ` + where + ` ORDER BY _rowid_`
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("query failed: %v\nSQL: %s", err, q)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate failed: %v\nSQL: %s", err, q)
	}
	return out
}

// TestSQLWhere_ExecutesAndSelectsTheExpectedRows is the difference between
// "the SQL parses" and "the SQL is right": a swapped instr() argument order,
// a < for a >, or 1=0 for 1=1 all parse cleanly.
func TestSQLWhere_ExecutesAndSelectsTheExpectedRows(t *testing.T) {
	db, ctx := execFixture(t)

	cases := []struct {
		name string
		f    Filter
		want []int64
	}{
		{"eq string", oneCond(Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "ada"}}), []int64{1}},
		{"ne string", oneCond(Condition{Path: "name", Op: OpNe, Value: Value{Kind: ValString, Str: "ada"}}), []int64{2, 3}},
		{"gt number", oneCond(Condition{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 20}}), []int64{1}},
		{"lte number", oneCond(Condition{Path: "age", Op: OpLte, Value: Value{Kind: ValNumber, Num: 10}}), []int64{2}},
		{"contains", oneCond(Condition{Path: "name", Op: OpContains, Value: Value{Kind: ValString, Str: "da"}}), []int64{1}},
		{"contains ci", oneCond(Condition{Path: "name", Op: OpContains, Value: Value{Kind: ValString, Str: "DA"}, CaseInsensitive: true}), []int64{1}},
		{"eq ci", oneCond(Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "ADA"}, CaseInsensitive: true}), []int64{1}},
		{"isnull", oneCond(Condition{Path: "age", Op: OpIsNull}), []int64{3}},
		{"notnull", oneCond(Condition{Path: "age", Op: OpNotNull}), []int64{1, 2}},
		{"in", oneCond(Condition{Path: "name", Op: OpIn, Value: Value{Kind: ValString, List: []Value{
			{Kind: ValString, Str: "ada"}, {Kind: ValString, Str: "bob"},
		}}}), []int64{1, 2}},
		{"in empty matches nothing", oneCond(Condition{Path: "name", Op: OpIn, Value: Value{Kind: ValString}}), nil},
		{"array element via json_each", oneCond(Condition{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}}), []int64{1}},
		{"json_extract fallback", oneCond(Condition{Path: "meta.note", Op: OpEq, Value: Value{Kind: ValString, Str: "hi"}}), []int64{1}},
		{"and group", Filter{Combinator: And, Conditions: []Condition{
			{Path: "name", Op: OpNotNull},
			{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 20}},
		}}, []int64{1}},
		{"or group", Filter{Combinator: Or, Conditions: []Condition{
			{Path: "age", Op: OpLt, Value: Value{Kind: ValNumber, Num: 20}},
			{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "ada"}},
		}}, []int64{1, 2}},
		{"childless or matches nothing", Filter{Combinator: And, Groups: []Filter{{Combinator: Or}}}, nil},
		{"negate", Filter{Combinator: And, Negate: true, Conditions: []Condition{
			{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "ada"}},
		}}, []int64{2, 3}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if hasRegexCondition(tc.f) {
				t.Skip("REGEXP needs a user-defined function SQLite does not ship")
			}
			where, _, err := sqlWhere(tc.f, ctx)
			if err != nil {
				t.Fatalf("sqlWhere error = %v", err)
			}
			got := execRowIDs(t, db, where)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("rowids = %v, want %v\nSQL: %s", got, tc.want, where)
			}
		})
	}
}

// TestSQLQuery_FullStatementExecutes covers the SELECT list too, not just the
// WHERE -- a broken alias or projection would otherwise ship unnoticed.
func TestSQLQuery_FullStatementExecutes(t *testing.T) {
	db, ctx := execFixture(t)
	stmt, _, err := sqlQuery(
		Filter{Combinator: And, Conditions: []Condition{{Path: "age", Op: OpNotNull}}},
		Transform{Select: []ColumnSpec{{Path: "name", As: `he said "hi"`}, {Path: "age", As: "years"}}},
		ctx,
	)
	if err != nil {
		t.Fatalf("sqlQuery error = %v", err)
	}
	rows, err := db.Query(strings.TrimSuffix(stmt, ";"))
	if err != nil {
		t.Fatalf("generated statement failed: %v\nSQL: %s", err, stmt)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	if len(cols) != 2 || cols[0] != `he said "hi"` || cols[1] != "years" {
		t.Fatalf("result columns = %v, want the aliases in Select order", cols)
	}
	n := 0
	for rows.Next() {
		n++
	}
	if n != 2 {
		t.Fatalf("got %d rows, want 2", n)
	}
}

// TestSQLWhere_CollateBinaryDefeatsADeclaredCollation is the reason every
// string comparison carries COLLATE BINARY. Without it a NOCASE column makes
// a case-SENSITIVE filter return case-insensitive rows.
//
// Mutation that must break it: drop the COLLATE, or move it to the right of
// IN (where it binds to the last list element and does nothing).
func TestSQLWhere_CollateBinaryDefeatsADeclaredCollation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (name TEXT COLLATE NOCASE, pad TEXT COLLATE RTRIM)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, r := range [][2]string{{"Apple", "abc   "}, {"apple", "abc"}, {"BANANA", "zz"}} {
		if _, err := db.Exec(`INSERT INTO t VALUES (?,?)`, r[0], r[1]); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	ctx := CodegenContext{Format: "sqlite", Table: "t",
		Cols: codegenModel(t, []map[string]any{{"name": "Apple", "pad": "abc"}})}

	eq, _, err := sqlWhere(oneCond(Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "apple"}}), ctx)
	if err != nil {
		t.Fatalf("sqlWhere error = %v", err)
	}
	if got := execRowIDs(t, db, eq); fmt.Sprint(got) != fmt.Sprint([]int64{2}) {
		t.Fatalf("NOCASE eq matched %v, want only row 2 (the byte-exact 'apple')\nSQL: %s", got, eq)
	}

	in, _, err := sqlWhere(oneCond(Condition{Path: "name", Op: OpIn, Value: Value{Kind: ValString, List: []Value{
		{Kind: ValString, Str: "apple"}, {Kind: ValString, Str: "banana"},
	}}}), ctx)
	if err != nil {
		t.Fatalf("sqlWhere error = %v", err)
	}
	if got := execRowIDs(t, db, in); fmt.Sprint(got) != fmt.Sprint([]int64{2}) {
		t.Fatalf("NOCASE in matched %v, want only row 2\nSQL: %s", got, in)
	}

	rt, _, err := sqlWhere(oneCond(Condition{Path: "pad", Op: OpEq, Value: Value{Kind: ValString, Str: "abc"}}), ctx)
	if err != nil {
		t.Fatalf("sqlWhere error = %v", err)
	}
	if got := execRowIDs(t, db, rt); fmt.Sprint(got) != fmt.Sprint([]int64{2}) {
		t.Fatalf("RTRIM eq matched %v, want only row 2 (the untrimmed value must not match)\nSQL: %s", got, rt)
	}
}

func oneCond(c Condition) Filter {
	return Filter{Combinator: And, Conditions: []Condition{c}}
}

// hasRegexCondition reports whether f contains a regex condition anywhere --
// gating on the AST, not on the generated text, because a `contains` on the
// literal value 'REGEXP' would otherwise be skipped by mistake.
func hasRegexCondition(f Filter) bool {
	for _, c := range f.Conditions {
		if c.Op == OpRegex {
			return true
		}
	}
	for _, g := range f.Groups {
		if hasRegexCondition(g) {
			return true
		}
	}
	return false
}

// TestSQLQuery_MinorFidelityFixes covers the display-SQL divergences the
// branch review found -- each executed against real SQLite where a row set is
// at stake.
func TestSQLQuery_MinorFidelityFixes(t *testing.T) {
	t.Run("M2: negate keeps NULL-column rows (NOT IFNULL, not NOT)", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		db.Exec(`CREATE TABLE t (name TEXT)`)
		for _, v := range []any{"ada", "bob", nil} {
			db.Exec(`INSERT INTO t VALUES (?)`, v)
		}
		ctx := CodegenContext{Format: "sqlite", Table: "t",
			Cols: codegenModel(t, []map[string]any{{"name": "ada"}})}
		f := Filter{Combinator: And, Negate: true, Conditions: []Condition{
			{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "ada"}}}}
		where, _, _ := sqlWhere(f, ctx)
		// Engine keeps bob AND the NULL row (both are "not ada").
		got := execRowIDs(t, db, where)
		if fmt.Sprint(got) != fmt.Sprint([]int64{2, 3}) {
			t.Fatalf("negate matched %v, want [2 3] incl. the NULL row\nSQL: %s", got, where)
		}
	})

	t.Run("M3: ne and ordering carry a type-guard note", func(t *testing.T) {
		ctx := sqliteCtx(t, []map[string]any{{"age": json.Number("1"), "name": "x"}})
		gen, _, _ := sqlQuery(Filter{Combinator: And, Conditions: []Condition{
			{Path: "name", Op: OpNe, Value: Value{Kind: ValString, Str: "x"}}}}, Transform{}, ctx)
		if !strings.Contains(gen, "-- note: != and the ordering") {
			t.Fatalf("ne has no type-guard note:\n%s", gen)
		}
		// eq must NOT carry it (eq agrees cross-type).
		genEq, _, _ := sqlQuery(Filter{Combinator: And, Conditions: []Condition{
			{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}}}}, Transform{}, ctx)
		if strings.Contains(genEq, "-- note: != and the ordering") {
			t.Fatalf("eq must not carry the type-guard note:\n%s", genEq)
		}
	})

	t.Run("M6: dropping every column selects nothing, not everything", func(t *testing.T) {
		ctx := sqliteCtx(t, []map[string]any{{"a": "x"}})
		gen, _, _ := sqlQuery(Filter{}, Transform{Drop: []string{"a"}}, ctx)
		if strings.Contains(gen, "SELECT * ") {
			t.Fatalf("dropping the only column fell through to SELECT *:\n%s", gen)
		}
		if !strings.Contains(gen, "SELECT NULL ") {
			t.Fatalf("dropping every column should select NULL:\n%s", gen)
		}
	})

	t.Run("M7: an array-path condition raises no false 'not a column' warning", func(t *testing.T) {
		ctx := sqliteCtx(t, []map[string]any{{"tags": []any{"x"}}})
		_, warnings, _ := sqlQuery(Filter{Combinator: And, Conditions: []Condition{
			{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}}}}, Transform{}, ctx)
		for _, w := range warnings {
			if strings.Contains(w, "is not a column") {
				t.Fatalf("false not-a-column warning for an array path: %q", w)
			}
		}
	})
}

// TestElemNullOps_MatchTheEngine (branch-review M1) covers isnull/notnull over
// an ARRAY path, which the engine treats as "empty-or-any-null" (isnull) and
// "non-empty-and-all-non-null" (notnull) -- NOT the existential any()/EXISTS
// the value ops use. Runs the generated SQL against real SQLite and asserts
// the exact row set matches CompileFilter().Match.
func TestElemNullOps_MatchTheEngine(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (tags TEXT)`); err != nil {
		t.Fatal(err)
	}
	// json text per row; the reader would deliver these as []any/scalar/nil.
	rowsJSON := []any{`[1,2]`, `[1,null]`, `[]`, nil, `"scalar"`}
	for _, v := range rowsJSON {
		if _, err := db.Exec(`INSERT INTO t VALUES (?)`, v); err != nil {
			t.Fatal(err)
		}
	}
	// The engine sees decoded values, not the json text.
	records := []map[string]any{
		{"tags": []any{json.Number("1"), json.Number("2")}},
		{"tags": []any{json.Number("1"), nil}},
		{"tags": []any{}},
		{},
		{"tags": "scalar"},
	}
	ctx := CodegenContext{Format: "sqlite", Table: "t",
		Cols: codegenModel(t, records)}

	for _, op := range []Op{OpIsNull, OpNotNull} {
		t.Run(string(op), func(t *testing.T) {
			f := Filter{Combinator: And, Conditions: []Condition{{Path: "tags[]", Op: op}}}

			cf, err := CompileFilter(f, ctx.Cols)
			if err != nil {
				t.Fatalf("CompileFilter: %v", err)
			}
			var want []int64
			for i, r := range records {
				if cf.Match(any(r)) {
					want = append(want, int64(i+1))
				}
			}

			where, _, err := sqlWhere(f, ctx)
			if err != nil {
				t.Fatalf("sqlWhere: %v", err)
			}
			got := execRowIDs(t, db, where)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("%s: SQL rowids %v, engine %v\nSQL: %s", op, got, want, where)
			}
		})
	}
}
