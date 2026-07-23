package query

import (
	"encoding/json"
	"fmt"
	"testing"
)

// pushdownModel is the ONE model the whole matrix runs against. Two entries
// are load-bearing:
//   - "user.name" is a REAL column whose own name contains a dot, so the
//     dotted-path row is rejected by the len(segs)==1 rule rather than by
//     "unknown path" -- otherwise that mutation could not flip anything
//     (buildColumnModel drops Elem paths entirely, so `tags[]` is unknown
//     regardless).
//   - "meta" drifts (int and string), so it types as "mixed".
func pushdownModel(t *testing.T) *ColumnModel {
	t.Helper()
	return codegenModel(t, []map[string]any{
		{
			"id":        json.Number("1"),
			"name":      "ada",
			"score":     json.Number("1.5"),
			"user.name": "ada",
			"meta":      json.Number("1"),
		},
		{
			"id":        json.Number("2"),
			"name":      "bob",
			"score":     json.Number("2.5"),
			"user.name": "bob",
			"meta":      "drifted",
		},
	})
}

func num(f float64) Value { return Value{Kind: ValNumber, Num: f} }
func str(s string) Value  { return Value{Kind: ValString, Str: s} }
func inList(vs ...Value) Value {
	return Value{Kind: ValString, List: vs}
}

func TestSQLPushdown_Pushable(t *testing.T) {
	cols := pushdownModel(t)

	cases := []struct {
		name  string
		f     Filter
		where string
		args  []any
	}{
		{"numeric eq", oneCond(Condition{Path: "id", Op: OpEq, Value: num(1)}),
			`"id" = ?`, []any{float64(1)}},
		{"numeric ne", oneCond(Condition{Path: "id", Op: OpNe, Value: num(1)}),
			`"id" <> ?`, []any{float64(1)}},
		{"numeric gt", oneCond(Condition{Path: "id", Op: OpGt, Value: num(1)}),
			`"id" > ?`, []any{float64(1)}},
		{"numeric lte on a float column", oneCond(Condition{Path: "score", Op: OpLte, Value: num(2)}),
			`"score" <= ?`, []any{float64(2)}},
		// Every string comparison carries COLLATE BINARY on the LEFT operand.
		{"string eq", oneCond(Condition{Path: "name", Op: OpEq, Value: str("ada")}),
			`"name" COLLATE BINARY = ?`, []any{"ada"}},
		{"contains", oneCond(Condition{Path: "name", Op: OpContains, Value: str("ad")}),
			`instr("name",?)>0`, []any{"ad"}},
		{"isnull", oneCond(Condition{Path: "name", Op: OpIsNull}), `"name" IS NULL`, nil},
		{"notnull", oneCond(Condition{Path: "id", Op: OpNotNull}), `"id" IS NOT NULL`, nil},
		{"in strings", oneCond(Condition{Path: "name", Op: OpIn, Value: inList(str("ada"), str("bob"))}),
			`"name" COLLATE BINARY IN (?,?)`, []any{"ada", "bob"}},
		{"in empty", oneCond(Condition{Path: "name", Op: OpIn, Value: Value{Kind: ValString}}), `1=0`, nil},
		{"and group", Filter{Combinator: And, Conditions: []Condition{
			{Path: "id", Op: OpGt, Value: num(1)},
			{Path: "name", Op: OpEq, Value: str("bob")},
		}}, `("id" > ? AND "name" COLLATE BINARY = ?)`, []any{float64(1), "bob"}},
		{"or group", Filter{Combinator: Or, Conditions: []Condition{
			{Path: "id", Op: OpEq, Value: num(1)},
			{Path: "id", Op: OpEq, Value: num(2)},
		}}, `("id" = ? OR "id" = ?)`, []any{float64(1), float64(2)}},
		{"nested groups", Filter{Combinator: And,
			Conditions: []Condition{{Path: "id", Op: OpNotNull}},
			Groups: []Filter{{Combinator: Or, Conditions: []Condition{
				{Path: "name", Op: OpEq, Value: str("ada")},
				{Path: "name", Op: OpEq, Value: str("bob")},
			}}},
		}, `("id" IS NOT NULL AND ("name" COLLATE BINARY = ? OR "name" COLLATE BINARY = ?))`,
			[]any{"ada", "bob"}},
		// 2^52 is comfortably inside the exactly-representable range.
		{"large but exact numeric", oneCond(Condition{Path: "id", Op: OpGt, Value: num(4503599627370496)}),
			`"id" > ?`, []any{float64(4503599627370496)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where, args, exact := sqlPushdown(tc.f, cols, nil)
			if !exact {
				t.Fatalf("exact = false, want true for a pushable filter")
			}
			if where != tc.where {
				t.Fatalf("where =\n  %s\nwant\n  %s", where, tc.where)
			}
			if fmt.Sprint(args) != fmt.Sprint(tc.args) {
				t.Fatalf("args = %#v, want %#v", args, tc.args)
			}
		})
	}
}

func TestSQLPushdown_Refused(t *testing.T) {
	cols := pushdownModel(t)

	cases := []struct {
		name string
		f    Filter
		why  string
	}{
		{"regex", oneCond(Condition{Path: "name", Op: OpRegex, Value: str("^a")}),
			"SQLite ships no REGEXP function"},
		{"ci contains", oneCond(Condition{Path: "name", Op: OpContains, Value: str("A"), CaseInsensitive: true}),
			"lower() folds ASCII only"},
		{"ci eq", oneCond(Condition{Path: "name", Op: OpEq, Value: str("A"), CaseInsensitive: true}),
			"lower() folds ASCII only"},
		{"ci ne", oneCond(Condition{Path: "name", Op: OpNe, Value: str("A"), CaseInsensitive: true}),
			"lower() folds ASCII only"},
		{"ci regex", oneCond(Condition{Path: "name", Op: OpRegex, Value: str("a"), CaseInsensitive: true}),
			"both reasons"},
		// A REAL column named "user.name" parses to TWO segments, so it is the
		// len(segs)==1 rule -- not "unknown path" -- that rejects it.
		{"dotted path", oneCond(Condition{Path: "user.name", Op: OpEq, Value: str("ada")}),
			"SQLite rows are flat"},
		{"array path", oneCond(Condition{Path: "tags[]", Op: OpEq, Value: str("x")}),
			"not a column at all"},
		{"unknown path", oneCond(Condition{Path: "nosuch", Op: OpEq, Value: str("x")}),
			"not a column"},
		{"string ordering", oneCond(Condition{Path: "name", Op: OpLt, Value: str("z")}),
			"column affinity converts the bound parameter"},
		{"numeric op on a string column", oneCond(Condition{Path: "name", Op: OpGt, Value: num(1)}),
			"type mismatch is false in Go, ordered in SQLite"},
		{"string op on a numeric column", oneCond(Condition{Path: "id", Op: OpEq, Value: str("1")}),
			"type mismatch"},
		{"contains on a numeric column", oneCond(Condition{Path: "id", Op: OpContains, Value: str("1")}),
			"type mismatch"},
		{"mixed column", oneCond(Condition{Path: "meta", Op: OpEq, Value: str("drifted")}),
			"a drifting column has no single comparison rule"},
		{"operand at 2^53", oneCond(Condition{Path: "id", Op: OpEq, Value: num(9007199254740992)}),
			"float64 aliases with 2^53+1"},
		{"in with an element at 2^53", oneCond(Condition{Path: "id", Op: OpIn,
			Value: inList(num(1), num(9007199254740992))}), "one bad element taints the list"},
		{"in with a mixed-type list", oneCond(Condition{Path: "name", Op: OpIn,
			Value: inList(str("ada"), num(1))}), "type mismatch"},
		{"negate", Filter{Combinator: And, Negate: true, Conditions: []Condition{
			{Path: "id", Op: OpNotNull}}}, "NOT(NULL) is NULL"},
		{"nested negate", Filter{Combinator: And,
			Conditions: []Condition{{Path: "id", Op: OpNotNull}},
			Groups:     []Filter{{Combinator: And, Negate: true, Conditions: []Condition{{Path: "name", Op: OpNotNull}}}},
		}, "a Negate anywhere taints the whole filter"},
		{"or with one non-pushable child", Filter{Combinator: Or, Conditions: []Condition{
			{Path: "id", Op: OpNotNull},
			{Path: "name", Op: OpRegex, Value: str("^a")},
		}}, "an OR cannot be narrowed by a subset"},
		{"zero-term node", Filter{Combinator: And, Groups: []Filter{{Combinator: Or}}},
			"vacuous truth is combinator-dependent"},
		{"empty filter", Filter{}, "nothing to push"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where, args, exact := sqlPushdown(tc.f, cols, nil)
			if exact {
				t.Fatalf("exact = true (where=%q args=%v), want false: %s", where, args, tc.why)
			}
		})
	}
}

// TestSQLPushdown_RefusesTaintedColumns covers the storage-mismatch gate: a
// column that ever yielded a BLOB or a driver-converted time compares
// differently in SQLite than in the engine, so nothing about it may be pushed.
//
// Mutation that must break it: drop the noPush check -> these become pushable.
func TestSQLPushdown_RefusesTaintedColumns(t *testing.T) {
	cols := pushdownModel(t)
	tainted := map[string]bool{"name": true}

	for _, c := range []Condition{
		{Path: "name", Op: OpEq, Value: str("ada")},
		{Path: "name", Op: OpContains, Value: str("ad")},
		{Path: "name", Op: OpIsNull},
		{Path: "name", Op: OpIn, Value: inList(str("ada"))},
	} {
		if _, _, exact := sqlPushdown(oneCond(c), cols, tainted); exact {
			t.Fatalf("op %q on a tainted column was pushed", c.Op)
		}
	}
	// An untainted column in the same model still pushes.
	if _, _, exact := sqlPushdown(oneCond(Condition{Path: "id", Op: OpEq, Value: num(1)}), cols, tainted); !exact {
		t.Fatalf("an untainted column stopped pushing")
	}
}

// TestCompileFilter_RetainsTheSourceAST is the plumbing Task 6 depends on.
// The pointer is what makes a hand-built CompiledFilter safe: nil means
// "unknown", not "match everything".
func TestCompileFilter_RetainsTheSourceAST(t *testing.T) {
	f := Filter{Combinator: And, Conditions: []Condition{{Path: "id", Op: OpNotNull}}}
	cf, err := CompileFilter(f, nil)
	if err != nil {
		t.Fatalf("CompileFilter error = %v", err)
	}
	if cf.src == nil {
		t.Fatalf("CompileFilter did not retain the source AST")
	}
	if len(cf.src.Conditions) != 1 || cf.src.Conditions[0].Path != "id" {
		t.Fatalf("retained AST = %+v, want the compiled filter", cf.src)
	}

	// An empty filter also retains its AST, so a caller can tell "match-all"
	// apart from "unknown".
	empty, err := CompileFilter(Filter{}, nil)
	if err != nil {
		t.Fatalf("CompileFilter(empty) error = %v", err)
	}
	if empty.src == nil {
		t.Fatalf("an empty filter must still retain its AST")
	}

	// A hand-built CompiledFilter (this package's tests decorate predicates
	// this way) has NO source: a planner must treat that as "do not push",
	// never as the zero Filter's match-all.
	hand := &CompiledFilter{pred: func(any) bool { return false }}
	if hand.src != nil {
		t.Fatalf("a hand-built CompiledFilter must not present a source AST")
	}
}

// TestSQLPushdown_RefusesOversizedInLists covers a failure mode the other
// gates do not: too many bound parameters makes SQLite REFUSE the statement,
// so a pushed query errors where the Go path answers normally. Verified
// against the vendored driver: 32766 elements yields "too many SQL variables".
//
// Mutation that must break it: remove the maxPushedParams check -> the huge
// list becomes pushable.
func TestSQLPushdown_RefusesOversizedInLists(t *testing.T) {
	cols := pushdownModel(t)

	small := make([]Value, 0, 100)
	for i := 0; i < 100; i++ {
		small = append(small, num(float64(i)))
	}
	if _, _, exact := sqlPushdown(oneCond(Condition{Path: "id", Op: OpIn,
		Value: Value{Kind: ValNumber, List: small}}), cols, nil); !exact {
		t.Fatalf("an ordinary 100-element in-list must still push")
	}

	huge := make([]Value, 0, maxPushedParams+1)
	for i := 0; i <= maxPushedParams; i++ {
		huge = append(huge, num(float64(i)))
	}
	if _, _, exact := sqlPushdown(oneCond(Condition{Path: "id", Op: OpIn,
		Value: Value{Kind: ValNumber, List: huge}}), cols, nil); exact {
		t.Fatalf("an in-list past the parameter cap was pushed; SQLite would refuse the statement")
	}

	// The cap counts parameters across the WHOLE filter, not per condition.
	half := make([]Value, 0, maxPushedParams/2)
	for i := 0; i < maxPushedParams/2; i++ {
		half = append(half, num(float64(i)))
	}
	two := Filter{Combinator: And, Conditions: []Condition{
		{Path: "id", Op: OpIn, Value: Value{Kind: ValNumber, List: half}},
		{Path: "id", Op: OpIn, Value: Value{Kind: ValNumber, List: half}},
	}}
	if _, _, exact := sqlPushdown(two, cols, nil); exact {
		t.Fatalf("two lists that together exceed the cap were pushed")
	}
}
