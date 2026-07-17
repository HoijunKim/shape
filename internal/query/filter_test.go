package query

import (
	"encoding/json"
	"testing"
)

// mustCompile compiles f (with a nil ColumnModel unless the test needs a
// real one) and fails the test on error.
func mustCompile(t *testing.T, f Filter) *CompiledFilter {
	t.Helper()
	cf, err := CompileFilter(f, nil)
	if err != nil {
		t.Fatalf("CompileFilter(%#v) error = %v, want nil", f, err)
	}
	return cf
}

func condFilter(c Condition) Filter {
	return Filter{Conditions: []Condition{c}}
}

// --- eq / ne -----------------------------------------------------------------

func TestFilterEqNe_MatchedType(t *testing.T) {
	rec := map[string]any{"name": "alice"}

	eqAlice := mustCompile(t, condFilter(Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}}))
	if !eqAlice.Match(rec) {
		t.Fatalf("eq alice on {name:alice} = false, want true")
	}
	eqBob := mustCompile(t, condFilter(Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "bob"}}))
	if eqBob.Match(rec) {
		t.Fatalf("eq bob on {name:alice} = true, want false")
	}

	neAlice := mustCompile(t, condFilter(Condition{Path: "name", Op: OpNe, Value: Value{Kind: ValString, Str: "alice"}}))
	if neAlice.Match(rec) {
		t.Fatalf("ne alice on {name:alice} = true, want false")
	}
	neBob := mustCompile(t, condFilter(Condition{Path: "name", Op: OpNe, Value: Value{Kind: ValString, Str: "bob"}}))
	if !neBob.Match(rec) {
		t.Fatalf("ne bob on {name:alice} = false, want true")
	}
}

func TestFilterEqNe_CrossTypeBothFalse(t *testing.T) {
	// string value vs numeric operand: neither eq nor ne matches.
	rec := map[string]any{"name": "alice"}
	eq := mustCompile(t, condFilter(Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValNumber, Num: 5}}))
	ne := mustCompile(t, condFilter(Condition{Path: "name", Op: OpNe, Value: Value{Kind: ValNumber, Num: 5}}))
	if eq.Match(rec) {
		t.Fatalf("eq(number) on string value = true, want false (cross-type)")
	}
	if ne.Match(rec) {
		t.Fatalf("ne(number) on string value = true, want false (cross-type both false)")
	}

	// numeric value vs string operand: same rule, reversed direction.
	rec2 := map[string]any{"age": json.Number("5")}
	eq2 := mustCompile(t, condFilter(Condition{Path: "age", Op: OpEq, Value: Value{Kind: ValString, Str: "5"}}))
	ne2 := mustCompile(t, condFilter(Condition{Path: "age", Op: OpNe, Value: Value{Kind: ValString, Str: "5"}}))
	if eq2.Match(rec2) {
		t.Fatalf("eq(string) on number value = true, want false (cross-type)")
	}
	if ne2.Match(rec2) {
		t.Fatalf("ne(string) on number value = true, want false (cross-type both false)")
	}
}

func TestFilterEq_CaseInsensitive(t *testing.T) {
	rec := map[string]any{"name": "Alice"}
	cf := mustCompile(t, condFilter(Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "ALICE"}, CaseInsensitive: true}))
	if !cf.Match(rec) {
		t.Fatalf("ci eq ALICE on {name:Alice} = false, want true")
	}
	cfCS := mustCompile(t, condFilter(Condition{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "ALICE"}}))
	if cfCS.Match(rec) {
		t.Fatalf("cs eq ALICE on {name:Alice} = true, want false")
	}
}

// --- lt/lte/gt/gte -------------------------------------------------------------

func TestFilterRange_Numeric(t *testing.T) {
	rec := map[string]any{"age": json.Number("18")}
	cases := []struct {
		op   Op
		num  float64
		want bool
	}{
		{OpLt, 19, true}, {OpLt, 18, false}, {OpLt, 17, false},
		{OpLte, 18, true}, {OpLte, 17, false},
		{OpGt, 17, true}, {OpGt, 18, false},
		{OpGte, 18, true}, {OpGte, 19, false},
	}
	for _, tc := range cases {
		cf := mustCompile(t, condFilter(Condition{Path: "age", Op: tc.op, Value: Value{Kind: ValNumber, Num: tc.num}}))
		if got := cf.Match(rec); got != tc.want {
			t.Fatalf("%s %v on age=18: got %v, want %v", tc.op, tc.num, got, tc.want)
		}
	}
}

func TestFilterRange_Lexicographic(t *testing.T) {
	rec := map[string]any{"name": "bob"}
	cases := []struct {
		op   Op
		str  string
		want bool
	}{
		{OpLt, "carl", true}, {OpLt, "bob", false}, {OpLt, "amy", false},
		{OpGt, "amy", true}, {OpGt, "bob", false},
		{OpLte, "bob", true}, {OpGte, "bob", true},
	}
	for _, tc := range cases {
		cf := mustCompile(t, condFilter(Condition{Path: "name", Op: tc.op, Value: Value{Kind: ValString, Str: tc.str}}))
		if got := cf.Match(rec); got != tc.want {
			t.Fatalf("%s %q on name=bob: got %v, want %v", tc.op, tc.str, got, tc.want)
		}
	}
}

func TestFilterRange_TypeMismatchNeverErrors(t *testing.T) {
	// numeric operand against a string value -> false, never a panic/error.
	rec := map[string]any{"name": "bob"}
	cf := mustCompile(t, condFilter(Condition{Path: "name", Op: OpGt, Value: Value{Kind: ValNumber, Num: 5}}))
	if cf.Match(rec) {
		t.Fatalf("gt(number) on string value = true, want false")
	}

	// string operand against a numeric value -> false.
	rec2 := map[string]any{"age": json.Number("18")}
	cf2 := mustCompile(t, condFilter(Condition{Path: "age", Op: OpLt, Value: Value{Kind: ValString, Str: "20"}}))
	if cf2.Match(rec2) {
		t.Fatalf("lt(string) on number value = true, want false")
	}

	// bool operand kind is never valid for a range op -> false.
	rec3 := map[string]any{"age": json.Number("18")}
	cf3 := mustCompile(t, condFilter(Condition{Path: "age", Op: OpLt, Value: Value{Kind: ValBool, Bool: true}}))
	if cf3.Match(rec3) {
		t.Fatalf("lt(bool) on number value = true, want false")
	}
}

// --- contains ------------------------------------------------------------------

func TestFilterContains_CaseSensitiveAndInsensitive(t *testing.T) {
	rec := map[string]any{"bio": "Loves Golang"}

	cs := mustCompile(t, condFilter(Condition{Path: "bio", Op: OpContains, Value: Value{Kind: ValString, Str: "Golang"}}))
	if !cs.Match(rec) {
		t.Fatalf("contains 'Golang' (CS) on 'Loves Golang' = false, want true")
	}
	csMiss := mustCompile(t, condFilter(Condition{Path: "bio", Op: OpContains, Value: Value{Kind: ValString, Str: "golang"}}))
	if csMiss.Match(rec) {
		t.Fatalf("contains 'golang' (CS) on 'Loves Golang' = true, want false")
	}

	ci := mustCompile(t, condFilter(Condition{Path: "bio", Op: OpContains, Value: Value{Kind: ValString, Str: "golang"}, CaseInsensitive: true}))
	if !ci.Match(rec) {
		t.Fatalf("contains 'golang' (CI) on 'Loves Golang' = false, want true")
	}
}

func TestFilterContains_NonStringIgnored(t *testing.T) {
	rec := map[string]any{"age": json.Number("18")}
	cf := mustCompile(t, condFilter(Condition{Path: "age", Op: OpContains, Value: Value{Kind: ValString, Str: "1"}}))
	if cf.Match(rec) {
		t.Fatalf("contains on a number value = true, want false (non-strings ignored)")
	}
}

// --- regex -----------------------------------------------------------------

func TestFilterRegex_CompiledOnce(t *testing.T) {
	rec := map[string]any{"email": "a@example.com"}
	cf := mustCompile(t, condFilter(Condition{Path: "email", Op: OpRegex, Value: Value{Kind: ValString, Str: `^[^@]+@example\.com$`}}))
	if !cf.Match(rec) {
		t.Fatalf("regex match on a@example.com = false, want true")
	}
	rec2 := map[string]any{"email": "a@other.com"}
	if cf.Match(rec2) {
		t.Fatalf("regex match on a@other.com = true, want false")
	}
}

func TestFilterRegex_NonStringIgnored(t *testing.T) {
	rec := map[string]any{"age": json.Number("18")}
	cf := mustCompile(t, condFilter(Condition{Path: "age", Op: OpRegex, Value: Value{Kind: ValString, Str: `\d+`}}))
	if cf.Match(rec) {
		t.Fatalf("regex on a number value = true, want false (non-strings ignored)")
	}
}

// --- in --------------------------------------------------------------------

func TestFilterIn_TypeMatched(t *testing.T) {
	rec := map[string]any{"status": "active"}
	cf := mustCompile(t, condFilter(Condition{Path: "status", Op: OpIn, Value: Value{
		Kind: ValString, // Kind on the outer Value is unused for `in`; List drives matching
		List: []Value{
			{Kind: ValString, Str: "active"},
			{Kind: ValString, Str: "pending"},
		},
	}}))
	if !cf.Match(rec) {
		t.Fatalf("in [active,pending] on status=active = false, want true")
	}

	recNum := map[string]any{"code": json.Number("2")}
	cfNum := mustCompile(t, condFilter(Condition{Path: "code", Op: OpIn, Value: Value{
		List: []Value{{Kind: ValNumber, Num: 1}, {Kind: ValNumber, Num: 2}},
	}}))
	if !cfNum.Match(recNum) {
		t.Fatalf("in [1,2] on code=2 = false, want true")
	}

	// type-matched: a string list never matches a numeric value even if the
	// literal digits coincide.
	cfMismatch := mustCompile(t, condFilter(Condition{Path: "code", Op: OpIn, Value: Value{
		List: []Value{{Kind: ValString, Str: "2"}},
	}}))
	if cfMismatch.Match(recNum) {
		t.Fatalf("in [\"2\"] (string) on code=2 (number) = true, want false (type-matched)")
	}
}

func TestFilterIn_EmptyListFalse(t *testing.T) {
	rec := map[string]any{"status": "active"}
	cf := mustCompile(t, condFilter(Condition{Path: "status", Op: OpIn, Value: Value{List: nil}}))
	if cf.Match(rec) {
		t.Fatalf("in [] on status=active = true, want false (empty list never matches)")
	}
}

// --- isnull / notnull ------------------------------------------------------

func TestFilterIsNullNotNull(t *testing.T) {
	missing := map[string]any{"other": "x"}
	nullVal := map[string]any{"age": nil}
	present := map[string]any{"age": json.Number("18")}

	isNull := mustCompile(t, condFilter(Condition{Path: "age", Op: OpIsNull}))
	notNull := mustCompile(t, condFilter(Condition{Path: "age", Op: OpNotNull}))

	if !isNull.Match(missing) {
		t.Fatalf("isnull on missing age = false, want true")
	}
	if !isNull.Match(nullVal) {
		t.Fatalf("isnull on null age = false, want true")
	}
	if isNull.Match(present) {
		t.Fatalf("isnull on present age = true, want false")
	}

	if notNull.Match(missing) {
		t.Fatalf("notnull on missing age = true, want false")
	}
	if notNull.Match(nullVal) {
		t.Fatalf("notnull on null age = true, want false")
	}
	if !notNull.Match(present) {
		t.Fatalf("notnull on present age = false, want true")
	}
}

// --- bool --------------------------------------------------------------------

func TestFilterBool(t *testing.T) {
	rec := map[string]any{"active": true}
	isTrue := mustCompile(t, condFilter(Condition{Path: "active", Op: OpBool, Value: Value{Bool: true}}))
	isFalse := mustCompile(t, condFilter(Condition{Path: "active", Op: OpBool, Value: Value{Bool: false}}))
	if !isTrue.Match(rec) {
		t.Fatalf("bool true on active=true = false, want true")
	}
	if isFalse.Match(rec) {
		t.Fatalf("bool false on active=true = true, want false")
	}
}

func TestFilterBool_NonBoolIgnored(t *testing.T) {
	rec := map[string]any{"active": "yes"}
	cf := mustCompile(t, condFilter(Condition{Path: "active", Op: OpBool, Value: Value{Bool: true}}))
	if cf.Match(rec) {
		t.Fatalf("bool true on active=\"yes\" (string) = true, want false (non-bool ignored)")
	}
}

// --- array membership (existential resolve) ---------------------------------

func TestFilterArrayMembership(t *testing.T) {
	rec := map[string]any{"tags": []any{"x", "y", "z"}}
	cf := mustCompile(t, condFilter(Condition{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}}))
	if !cf.Match(rec) {
		t.Fatalf("tags[] eq x on tags=[x,y,z] = false, want true")
	}
	cfMiss := mustCompile(t, condFilter(Condition{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "w"}}))
	if cfMiss.Match(rec) {
		t.Fatalf("tags[] eq w on tags=[x,y,z] = true, want false")
	}

	recEmpty := map[string]any{"tags": []any{}}
	if cf.Match(recEmpty) {
		t.Fatalf("tags[] eq x on empty tags = true, want false")
	}
}

// --- the decisive SQL-native null rule ---------------------------------------

func TestFilterNullRule_MissingExcludesComparisonOps(t *testing.T) {
	rec := map[string]any{"name": "alice"} // no "age" key at all

	gt := mustCompile(t, condFilter(Condition{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 18}}))
	ne := mustCompile(t, condFilter(Condition{Path: "age", Op: OpNe, Value: Value{Kind: ValNumber, Num: 18}}))
	eq := mustCompile(t, condFilter(Condition{Path: "age", Op: OpEq, Value: Value{Kind: ValNumber, Num: 18}}))

	if gt.Match(rec) {
		t.Fatalf("age>18 on record missing age = true, want false (SQL-native null rule)")
	}
	if ne.Match(rec) {
		t.Fatalf("age!=18 on record missing age = true, want false (SQL-native null rule)")
	}
	if eq.Match(rec) {
		t.Fatalf("age=18 on record missing age = true, want false")
	}
}

func TestFilterNullRule_ExplicitNullExcludesComparisonOps(t *testing.T) {
	rec := map[string]any{"age": nil}

	gt := mustCompile(t, condFilter(Condition{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 18}}))
	ne := mustCompile(t, condFilter(Condition{Path: "age", Op: OpNe, Value: Value{Kind: ValNumber, Num: 18}}))
	eq := mustCompile(t, condFilter(Condition{Path: "age", Op: OpEq, Value: Value{Kind: ValNumber, Num: 18}}))
	contains := mustCompile(t, condFilter(Condition{Path: "age", Op: OpContains, Value: Value{Kind: ValString, Str: "1"}}))
	boolOp := mustCompile(t, condFilter(Condition{Path: "age", Op: OpBool, Value: Value{Bool: true}}))
	inOp := mustCompile(t, condFilter(Condition{Path: "age", Op: OpIn, Value: Value{List: []Value{{Kind: ValNumber, Num: 18}}}}))

	for name, f := range map[string]*CompiledFilter{"gt": gt, "ne": ne, "eq": eq, "contains": contains, "bool": boolOp, "in": inOp} {
		if f.Match(rec) {
			t.Fatalf("%s on explicit-null age = true, want false (SQL-native null rule)", name)
		}
	}
}

// --- combinators: AND / OR / Negate / nested groups --------------------------

func TestFilterCombinators_And(t *testing.T) {
	rec := map[string]any{"name": "alice", "age": json.Number("30")}
	f := Filter{
		Combinator: And,
		Conditions: []Condition{
			{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}},
			{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 18}},
		},
	}
	cf := mustCompile(t, f)
	if !cf.Match(rec) {
		t.Fatalf("AND(both true) = false, want true")
	}

	f2 := f
	f2.Conditions[1].Value.Num = 40 // age>40 now false
	cf2 := mustCompile(t, f2)
	if cf2.Match(rec) {
		t.Fatalf("AND(one false) = true, want false")
	}
}

func TestFilterCombinators_Or(t *testing.T) {
	rec := map[string]any{"name": "bob", "age": json.Number("15")}
	f := Filter{
		Combinator: Or,
		Conditions: []Condition{
			{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}},
			{Path: "age", Op: OpLt, Value: Value{Kind: ValNumber, Num: 18}},
		},
	}
	cf := mustCompile(t, f)
	if !cf.Match(rec) {
		t.Fatalf("OR(one true) = false, want true")
	}

	f2 := Filter{
		Combinator: Or,
		Conditions: []Condition{
			{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}},
			{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 100}},
		},
	}
	cf2 := mustCompile(t, f2)
	if cf2.Match(rec) {
		t.Fatalf("OR(both false) = true, want false")
	}
}

func TestFilterCombinators_Negate(t *testing.T) {
	rec := map[string]any{"name": "alice"}
	f := Filter{
		Negate:     true,
		Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}}},
	}
	cf := mustCompile(t, f)
	if cf.Match(rec) {
		t.Fatalf("NOT(eq alice) on name=alice = true, want false")
	}
	cf2 := mustCompile(t, Filter{
		Negate:     true,
		Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "bob"}}},
	})
	if !cf2.Match(rec) {
		t.Fatalf("NOT(eq bob) on name=alice = false, want true")
	}
}

func TestFilterCombinators_NestedGroups(t *testing.T) {
	// (name == alice) AND ( (age < 18) OR (age > 60) )
	rec := map[string]any{"name": "alice", "age": json.Number("70")}
	f := Filter{
		Combinator: And,
		Conditions: []Condition{
			{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}},
		},
		Groups: []Filter{
			{
				Combinator: Or,
				Conditions: []Condition{
					{Path: "age", Op: OpLt, Value: Value{Kind: ValNumber, Num: 18}},
					{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 60}},
				},
			},
		},
	}
	cf := mustCompile(t, f)
	if !cf.Match(rec) {
		t.Fatalf("nested AND/OR group on matching record = false, want true")
	}

	rec2 := map[string]any{"name": "alice", "age": json.Number("30")}
	if cf.Match(rec2) {
		t.Fatalf("nested AND/OR group on non-matching age = true, want false")
	}
}

// --- empty Filter ------------------------------------------------------------

func TestFilterEmpty_MatchesEverything(t *testing.T) {
	cf := mustCompile(t, Filter{})
	recs := []any{
		map[string]any{"a": 1},
		map[string]any{},
		nil,
		"scalar",
	}
	for _, r := range recs {
		if !cf.Match(r) {
			t.Fatalf("empty Filter did not match %#v, want match-all", r)
		}
	}
}

func TestFilterMatch_NilCompiledFilter(t *testing.T) {
	var cf *CompiledFilter
	if !cf.Match(map[string]any{"a": 1}) {
		t.Fatalf("nil *CompiledFilter.Match = false, want true (match-all)")
	}
}

// --- CompileFilter errors ------------------------------------------------------

func TestCompileFilter_ErrorsOnBadRegex(t *testing.T) {
	_, err := CompileFilter(condFilter(Condition{Path: "email", Op: OpRegex, Value: Value{Kind: ValString, Str: "(unterminated"}}), nil)
	if err == nil {
		t.Fatalf("CompileFilter with bad regex = nil error, want an error")
	}
}

func TestCompileFilter_ErrorsOnBadPath(t *testing.T) {
	// '[' not followed by ']' or '"' is not valid path grammar (see
	// parsePath's Elem/bracket-quoted-key forms); CompileFilter must reject
	// it at compile time rather than let parsePath loop or fabricate a segment.
	_, err := CompileFilter(condFilter(Condition{Path: "tags[x]", Op: OpEq, Value: Value{Kind: ValString, Str: "a"}}), nil)
	if err == nil {
		t.Fatalf("CompileFilter with bad path = nil error, want an error")
	}

	_, err2 := CompileFilter(condFilter(Condition{Path: `["unterminated`, Op: OpEq, Value: Value{Kind: ValString, Str: "a"}}), nil)
	if err2 == nil {
		t.Fatalf("CompileFilter with unterminated bracket-quoted key = nil error, want an error")
	}
}

// --- determinism: predicate purity, no per-call allocation surprises --------

func TestFilterMatch_DeterministicAcrossCalls(t *testing.T) {
	cf := mustCompile(t, condFilter(Condition{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}}))
	rec := map[string]any{"tags": []any{"x", "y"}}
	for i := 0; i < 5; i++ {
		if !cf.Match(rec) {
			t.Fatalf("iteration %d: Match = false, want true (deterministic)", i)
		}
	}
}
