package query

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// --- golden templates --------------------------------------------------------

func jqCond(t *testing.T, c Condition) string {
	t.Helper()
	got, _, err := jqCondition(c, nil)
	if err != nil {
		t.Fatalf("jqCondition error = %v, want nil", err)
	}
	return got
}

func TestJQCondition_Goldens(t *testing.T) {
	num := Value{Kind: ValNumber, Num: 18}
	str := Value{Kind: ValString, Str: "ada"}

	cases := []struct {
		name string
		cond Condition
		want string
	}{
		{"eq number", Condition{Path: "age", Op: OpEq, Value: num},
			`(.age? != null and .age? == 18)`},
		{"eq string", Condition{Path: "name", Op: OpEq, Value: str},
			`(.name? != null and .name? == "ada")`},
		// ne and the ordering ops guard on the OPERAND's kind: jq has a total
		// order across types, so an unguarded `.p != 5` selects {"p":"hello"}
		// while the Go predicate returns false.
		{"ne number is type-guarded", Condition{Path: "age", Op: OpNe, Value: num},
			`(((.age?|type)=="number") and .age? != 18)`},
		{"ne string is type-guarded", Condition{Path: "name", Op: OpNe, Value: str},
			`(((.name?|type)=="string") and .name? != "ada")`},
		{"gte is type-guarded", Condition{Path: "age", Op: OpGte, Value: num},
			`(((.age?|type)=="number") and .age? >= 18)`},
		{"lt is type-guarded", Condition{Path: "age", Op: OpLt, Value: num},
			`(((.age?|type)=="number") and .age? < 18)`},
		{"contains", Condition{Path: "name", Op: OpContains, Value: str},
			`(((.name?|type)=="string") and (.name?|contains("ada")))`},
		{"contains ci", Condition{Path: "name", Op: OpContains, Value: str, CaseInsensitive: true},
			`(((.name?|type)=="string") and ((.name?|ascii_downcase)|contains(("ada"|ascii_downcase))))`},
		{"eq ci", Condition{Path: "name", Op: OpEq, Value: str, CaseInsensitive: true},
			`(((.name?|type)=="string") and ((.name?|ascii_downcase) == ("ada"|ascii_downcase)))`},
		{"ne ci", Condition{Path: "name", Op: OpNe, Value: str, CaseInsensitive: true},
			`(((.name?|type)=="string") and ((.name?|ascii_downcase) != ("ada"|ascii_downcase)))`},
		{"regex", Condition{Path: "name", Op: OpRegex, Value: Value{Kind: ValString, Str: "^a"}},
			`(((.name?|type)=="string") and (.name?|test("^a")))`},
		{"regex ci", Condition{Path: "name", Op: OpRegex, Value: Value{Kind: ValString, Str: "^a"}, CaseInsensitive: true},
			`(((.name?|type)=="string") and (.name?|test("^a";"i")))`},
		{"isnull", Condition{Path: "meta", Op: OpIsNull}, `([.meta?][0] == null)`},
		{"notnull", Condition{Path: "meta", Op: OpNotNull}, `([.meta?][0] != null)`},
		{"bool true", Condition{Path: "ok", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}},
			`(.ok? == true)`},
		{"bool false", Condition{Path: "ok", Op: OpBool, Value: Value{Kind: ValBool, Bool: false}},
			`(.ok? == false)`},
		{"in", Condition{Path: "id", Op: OpIn, Value: Value{Kind: ValNumber, List: []Value{
			{Kind: ValNumber, Num: 1}, {Kind: ValNumber, Num: 2},
		}}}, `(.id? as $x|any(1,2;.==$x))`},
		{"in empty", Condition{Path: "id", Op: OpIn, Value: Value{Kind: ValNumber}}, `false`},
		{"array element", Condition{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}},
			`any(.tags?[]?; (. != null and . == "x"))`},
		{"nested path", Condition{Path: "user.name", Op: OpEq, Value: str},
			`(.user?.name? != null and .user?.name? == "ada")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jqCond(t, tc.cond); got != tc.want {
				t.Fatalf("jqCondition =\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

func TestJQFilter_GroupsAndZeroTermNodes(t *testing.T) {
	c1 := Condition{Path: "a", Op: OpNotNull}
	c2 := Condition{Path: "b", Op: OpNotNull}

	cases := []struct {
		name string
		f    Filter
		want string
	}{
		{"two conditions and", Filter{Combinator: And, Conditions: []Condition{c1, c2}},
			`((([.a?][0] != null) // false) and (([.b?][0] != null) // false))`},
		{"two conditions or", Filter{Combinator: Or, Conditions: []Condition{c1, c2}},
			`((([.a?][0] != null) // false) or (([.b?][0] != null) // false))`},
		{"negate", Filter{Combinator: And, Conditions: []Condition{c1}, Negate: true},
			`((([.a?][0] != null) // false) | not)`},
		// A zero-term node emits its combinator's IDENTITY, matching
		// compileGroup: a childless AND matches everything, a childless OR
		// matches NOTHING. Emitting nothing at all would flip the OR case.
		{"childless and", Filter{Combinator: And}, `true`},
		{"childless or", Filter{Combinator: Or}, `false`},
		{"childless or nested under an and", Filter{Combinator: And,
			Conditions: []Condition{c1}, Groups: []Filter{{Combinator: Or}}},
			`((([.a?][0] != null) // false) and false)`},
		{"childless and nested under an and", Filter{Combinator: And,
			Conditions: []Condition{c1}, Groups: []Filter{{Combinator: And}}},
			`((([.a?][0] != null) // false) and true)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := jqFilter(tc.f, nil)
			if err != nil {
				t.Fatalf("jqFilter error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("jqFilter =\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

// TestJQProjection_PreservesSelectOrder uses a REVERSE-alphabetical fixture on
// purpose: with an already-sorted fixture, a map-built projection emits the
// identical string and the ordering bug is invisible.
//
// Mutation that must break it: build the object from a map[string]string and
// emit its keys sorted -> "alpha" comes first and this fails.
func TestJQProjection_PreservesSelectOrder(t *testing.T) {
	got, err := jqProjection(Transform{Select: []ColumnSpec{
		{Path: "zeta", As: "zeta"}, {Path: "alpha", As: "alpha"},
	}})
	if err != nil {
		t.Fatalf("jqProjection error = %v", err)
	}
	if want := `{"zeta": [.zeta?][0], "alpha": [.alpha?][0]}`; got != want {
		t.Fatalf("jqProjection = %s, want %s", got, want)
	}
}

func TestJQProjection_DropAndAliasEscaping(t *testing.T) {
	got, err := jqProjection(Transform{Drop: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if want := "del(.a?,.b?)"; got != want {
		t.Fatalf("jqProjection(drop) = %s, want %s", got, want)
	}

	got, err = jqProjection(Transform{Select: []ColumnSpec{{Path: "a", As: `he said "hi"`}}})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if want := `{"he said \"hi\"": [.a?][0]}`; got != want {
		t.Fatalf("jqProjection(quoted alias) = %s, want %s", got, want)
	}

	// An Elem path in a Select must be wrapped so one record yields one record.
	got, err = jqProjection(Transform{Select: []ColumnSpec{{Path: "tags[]", As: "tag"}}})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if want := `{"tag": [.tags?[]?][0]}`; got != want {
		t.Fatalf("jqProjection(elem) = %s, want %s", got, want)
	}
}

func TestJQProgram_OmitsEmptyStages(t *testing.T) {
	prog, _, err := jqProgram(Filter{}, Transform{}, CodegenContext{Format: "ndjson"})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	body := programBody(prog)
	// Mutation that must break it: emit select(true) for an empty filter.
	if body != "." {
		t.Fatalf("match-all identity program = %q, want %q", body, ".")
	}
	if !strings.HasPrefix(prog, "# jq: one JSON object per line") {
		t.Fatalf("program has no ndjson invocation note:\n%s", prog)
	}

	prog, _, err = jqProgram(Filter{}, Transform{}, CodegenContext{Format: "json"})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(prog, ".[] |") {
		t.Fatalf("a JSON-array source must document the .[] prefix:\n%s", prog)
	}
}

func TestJQProgram_WarnsOnceForRepeatedConstructs(t *testing.T) {
	f := Filter{Combinator: And, Conditions: []Condition{
		{Path: "a", Op: OpContains, Value: Value{Kind: ValString, Str: "x"}, CaseInsensitive: true},
		{Path: "b", Op: OpContains, Value: Value{Kind: ValString, Str: "y"}, CaseInsensitive: true},
	}}
	_, warnings, err := jqProgram(f, Transform{}, CodegenContext{Format: "ndjson"})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	n := 0
	for _, w := range warnings {
		if strings.Contains(w, "case-insensitive") {
			n++
		}
	}
	// jqProgram returns raw warnings; Codegen dedupes. Assert the CI warning
	// is present at all here, and that dedup is Task 4's job.
	if n == 0 {
		t.Fatalf("no case-insensitive warning for a ci condition: %v", warnings)
	}
}

func programBody(prog string) string {
	_, body, _ := strings.Cut(prog, "\n")
	return body
}

// --- real jq execution -------------------------------------------------------

// runJQ executes prog over the given NDJSON input with the real jq binary,
// skipping when jq is unavailable. CI runs `go test ./...` on ubuntu-latest,
// whose runner image ships jq, so these execute there even though a Windows
// dev box may not have it.
func runJQ(t *testing.T, prog, input string) string {
	t.Helper()
	bin, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not on PATH; the generated programs are executed on CI (ubuntu-latest ships jq)")
	}
	cmd := exec.Command(bin, "-c", programBody(prog))
	cmd.Stdin = strings.NewReader(input)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("jq failed: %v\nprogram: %s\nstderr: %s", err, programBody(prog), errb.String())
	}
	if errb.Len() != 0 {
		t.Fatalf("jq wrote to stderr: %s\nprogram: %s", errb.String(), programBody(prog))
	}
	return out.String()
}

// TestJQProgram_MatchesTheGoPredicate is the real proof: for each operator,
// the generated program is EXECUTED over a fixture containing a value of every
// wrong kind, and its output must equal exactly what CompileFilter().Match
// selects over the same records.
//
// Mutations that must break it: revert contains/regex to the spec §7
// parenthesisation (jq errors on the rows it should match); drop the ne/
// ordering type guards (jq emits records Go rejects).
func TestJQProgram_MatchesTheGoPredicate(t *testing.T) {
	records := []map[string]any{
		{"id": json.Number("1"), "p": "hello"},
		{"id": json.Number("2"), "p": json.Number("5")},
		{"id": json.Number("3"), "p": true},
		{"id": json.Number("4"), "p": nil},
		{"id": json.Number("5")}, // p missing
		{"id": json.Number("6"), "p": []any{"x"}},
		{"id": json.Number("7"), "p": map[string]any{"k": "v"}},
		{"id": json.Number("8"), "p": "HELLO"},
	}
	var input strings.Builder
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		input.Write(b)
		input.WriteByte('\n')
	}

	cases := []struct {
		name string
		cond Condition
	}{
		{"eq string", Condition{Path: "p", Op: OpEq, Value: Value{Kind: ValString, Str: "hello"}}},
		{"eq number", Condition{Path: "p", Op: OpEq, Value: Value{Kind: ValNumber, Num: 5}}},
		{"ne string", Condition{Path: "p", Op: OpNe, Value: Value{Kind: ValString, Str: "hello"}}},
		{"ne number", Condition{Path: "p", Op: OpNe, Value: Value{Kind: ValNumber, Num: 5}}},
		{"gt number", Condition{Path: "p", Op: OpGt, Value: Value{Kind: ValNumber, Num: 1}}},
		{"lt string", Condition{Path: "p", Op: OpLt, Value: Value{Kind: ValString, Str: "z"}}},
		{"contains", Condition{Path: "p", Op: OpContains, Value: Value{Kind: ValString, Str: "ell"}}},
		{"contains ci", Condition{Path: "p", Op: OpContains, Value: Value{Kind: ValString, Str: "ELL"}, CaseInsensitive: true}},
		{"eq ci", Condition{Path: "p", Op: OpEq, Value: Value{Kind: ValString, Str: "hello"}, CaseInsensitive: true}},
		{"regex", Condition{Path: "p", Op: OpRegex, Value: Value{Kind: ValString, Str: "^h"}}},
		{"isnull", Condition{Path: "p", Op: OpIsNull}},
		{"notnull", Condition{Path: "p", Op: OpNotNull}},
		{"bool", Condition{Path: "p", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}},
		{"in", Condition{Path: "p", Op: OpIn, Value: Value{Kind: ValString, List: []Value{
			{Kind: ValString, Str: "hello"}, {Kind: ValString, Str: "nope"},
		}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Filter{Combinator: And, Conditions: []Condition{tc.cond}}

			// What the ENGINE selects.
			cf, err := CompileFilter(f, nil)
			if err != nil {
				t.Fatalf("CompileFilter error = %v", err)
			}
			var wantIDs []string
			for _, r := range records {
				if cf.Match(any(r)) {
					wantIDs = append(wantIDs, string(r["id"].(json.Number)))
				}
			}

			// What jq selects.
			prog, _, err := jqProgram(f, Transform{}, CodegenContext{Format: "ndjson"})
			if err != nil {
				t.Fatalf("jqProgram error = %v", err)
			}
			out := runJQ(t, prog, input.String())
			var gotIDs []string
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if line == "" {
					continue
				}
				var rec map[string]any
				dec := json.NewDecoder(strings.NewReader(line))
				dec.UseNumber() // or an id decodes as float64 and the assertion panics
				if err := dec.Decode(&rec); err != nil {
					t.Fatalf("jq emitted invalid JSON %q: %v", line, err)
				}
				gotIDs = append(gotIDs, string(rec["id"].(json.Number)))
			}

			if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
				t.Fatalf("jq selected %v, engine selected %v\nprogram: %s",
					gotIDs, wantIDs, programBody(prog))
			}
		})
	}
}

// TestJQProgram_SparseRecordsDoNotAbortTheStream covers the failure mode that
// kills an ENTIRE run rather than one row: a record whose path is missing or
// scalar makes an unguarded jq path a hard error.
//
// Mutation that must break it: drop the `?` suffixes (or the `// false`
// wrapper) -> jq exits non-zero and the later records are never emitted.
func TestJQProgram_SparseRecordsDoNotAbortTheStream(t *testing.T) {
	input := `{"id":1,"tags":["x"]}
{"id":2,"other":1}
{"id":3,"tags":["x"]}
{"id":4,"tags":"scalar"}
`
	f := Filter{Combinator: And, Conditions: []Condition{
		{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "x"}},
	}}
	prog, _, err := jqProgram(f, Transform{}, CodegenContext{Format: "ndjson"})
	if err != nil {
		t.Fatalf("jqProgram error = %v", err)
	}
	out := runJQ(t, prog, input)
	// Both id 1 AND id 3 must survive: the un-`?` form errors on record 2 and
	// never reaches record 3.
	if !strings.Contains(out, `"id":1`) || !strings.Contains(out, `"id":3`) {
		t.Fatalf("output lost records after a sparse one:\n%s", out)
	}
	if strings.Contains(out, `"id":2`) || strings.Contains(out, `"id":4`) {
		t.Fatalf("output selected a non-matching record:\n%s", out)
	}
}

// A nested dotted path over a scalar ancestor is the same trap without arrays.
func TestJQProgram_ScalarAncestorDoesNotAbortTheStream(t *testing.T) {
	input := `{"id":1,"a":{"b":1}}
{"id":2,"a":"str"}
{"id":3,"a":{"b":1}}
`
	f := Filter{Combinator: And, Conditions: []Condition{
		{Path: "a.b", Op: OpEq, Value: Value{Kind: ValNumber, Num: 1}},
	}}
	prog, _, err := jqProgram(f, Transform{}, CodegenContext{Format: "ndjson"})
	if err != nil {
		t.Fatalf("jqProgram error = %v", err)
	}
	out := runJQ(t, prog, input)
	if !strings.Contains(out, `"id":1`) || !strings.Contains(out, `"id":3`) {
		t.Fatalf("output lost records after a scalar-ancestor record:\n%s", out)
	}
}

// TestJQProgram_SelectOverAnArrayPathDoesNotFanOut pins the projection
// wrapper end to end: 3 records in, 3 records out.
func TestJQProgram_SelectOverAnArrayPathDoesNotFanOut(t *testing.T) {
	input := `{"tags":["x","y"]}
{"tags":[]}
{"other":1}
`
	prog, _, err := jqProgram(Filter{}, Transform{Select: []ColumnSpec{{Path: "tags[]", As: "tag"}}},
		CodegenContext{Format: "ndjson"})
	if err != nil {
		t.Fatalf("jqProgram error = %v", err)
	}
	// jq terminates lines with \r\n on Windows; compare the payloads only.
	out := strings.TrimSpace(strings.ReplaceAll(runJQ(t, prog, input), "\r", ""))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d records out for 3 in (a bare .tags[] fans out):\n%s", len(lines), out)
	}
	if lines[0] != `{"tag":"x"}` || lines[1] != `{"tag":null}` || lines[2] != `{"tag":null}` {
		t.Fatalf("unexpected projection output:\n%s", out)
	}
}

// TestJQProgram_ProjectionDoesNotDropRecords is the C1 regression: a Select
// over a plain nested path must emit one output record per INPUT record. A
// bare `{ "B": .a?.b? }` annihilates the object whenever an ancestor is a
// scalar or array, silently dropping the row -- breaking the panel's one
// promise ("the same query").
//
// Mutation that must break it: make jqProjectionPath skip the [...][0] wrapper
// for a non-Elem path -> only 2 of 3 records survive.
func TestJQProgram_ProjectionDoesNotDropRecords(t *testing.T) {
	input := `{"a":{"b":1}}
{"a":"scalar"}
{"a":{"b":2}}
`
	prog, _, err := jqProgram(Filter{}, Transform{Select: []ColumnSpec{{Path: "a.b", As: "B"}}},
		CodegenContext{Format: "ndjson"})
	if err != nil {
		t.Fatalf("jqProgram error = %v", err)
	}
	out := strings.TrimSpace(strings.ReplaceAll(runJQ(t, prog, input), "\r", ""))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d records out for 3 in -- a scalar ancestor dropped a row:\n%s", len(lines), out)
	}
	if lines[0] != `{"B":1}` || lines[1] != `{"B":null}` || lines[2] != `{"B":2}` {
		t.Fatalf("unexpected projection output:\n%s", out)
	}
}

// TestJQProgram_IsNullOverAScalarAncestorMatchesTheEngine is the I2 regression:
// Go treats an empty resolve as null, so `a.b is null` selects a record whose
// `a` is a scalar. A bare `.a?.b? == null` yields empty there, which jqFilter
// pins to false -- dropping the record.
//
// Mutation that must break it: render isnull as `(subject == null)` instead of
// `([subject][0] == null)` -> the scalar/array/missing rows are dropped.
func TestJQProgram_IsNullOverAScalarAncestorMatchesTheEngine(t *testing.T) {
	records := []map[string]any{
		{"id": json.Number("1"), "a": map[string]any{"b": json.Number("1")}},
		{"id": json.Number("2"), "a": "scalar"},
		{"id": json.Number("3"), "a": []any{json.Number("1")}},
		{"id": json.Number("4")},                                // a missing
		{"id": json.Number("5"), "a": map[string]any{"b": nil}}, // a.b explicitly null
	}
	var input strings.Builder
	for _, r := range records {
		b, _ := json.Marshal(r)
		input.Write(b)
		input.WriteByte('\n')
	}
	for _, op := range []Op{OpIsNull, OpNotNull} {
		t.Run(string(op), func(t *testing.T) {
			f := Filter{Combinator: And, Conditions: []Condition{{Path: "a.b", Op: op}}}
			cf, err := CompileFilter(f, nil)
			if err != nil {
				t.Fatalf("CompileFilter: %v", err)
			}
			var want []string
			for _, r := range records {
				if cf.Match(any(r)) {
					want = append(want, string(r["id"].(json.Number)))
				}
			}
			prog, _, err := jqProgram(f, Transform{}, CodegenContext{Format: "ndjson"})
			if err != nil {
				t.Fatalf("jqProgram: %v", err)
			}
			out := strings.TrimSpace(strings.ReplaceAll(runJQ(t, prog, input.String()), "\r", ""))
			var got []string
			for _, line := range strings.Split(out, "\n") {
				if line == "" {
					continue
				}
				var rec map[string]any
				dec := json.NewDecoder(strings.NewReader(line))
				dec.UseNumber()
				if err := dec.Decode(&rec); err != nil {
					t.Fatalf("bad jq output %q: %v", line, err)
				}
				got = append(got, string(rec["id"].(json.Number)))
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("%s: jq selected %v, engine selected %v\nprogram: %s", op, got, want, programBody(prog))
			}
		})
	}
}

// TestJQProgram_ElemNullOpsMatchTheEngine is the jq half of branch-review M1:
// isnull/notnull over an array path are not existential.
func TestJQProgram_ElemNullOpsMatchTheEngine(t *testing.T) {
	records := []map[string]any{
		{"id": json.Number("1"), "tags": []any{json.Number("1"), json.Number("2")}},
		{"id": json.Number("2"), "tags": []any{json.Number("1"), nil}},
		{"id": json.Number("3"), "tags": []any{}},
		{"id": json.Number("4")},
		{"id": json.Number("5"), "tags": "scalar"},
	}
	var input strings.Builder
	for _, r := range records {
		b, _ := json.Marshal(r)
		input.Write(b)
		input.WriteByte('\n')
	}
	for _, op := range []Op{OpIsNull, OpNotNull} {
		t.Run(string(op), func(t *testing.T) {
			f := Filter{Combinator: And, Conditions: []Condition{{Path: "tags[]", Op: op}}}
			cf, err := CompileFilter(f, nil)
			if err != nil {
				t.Fatalf("CompileFilter: %v", err)
			}
			var want []string
			for _, r := range records {
				if cf.Match(any(r)) {
					want = append(want, string(r["id"].(json.Number)))
				}
			}
			prog, _, err := jqProgram(f, Transform{}, CodegenContext{Format: "ndjson"})
			if err != nil {
				t.Fatalf("jqProgram: %v", err)
			}
			out := strings.TrimSpace(strings.ReplaceAll(runJQ(t, prog, input.String()), "\r", ""))
			var got []string
			for _, line := range strings.Split(out, "\n") {
				if line == "" {
					continue
				}
				var rec map[string]any
				dec := json.NewDecoder(strings.NewReader(line))
				dec.UseNumber()
				if err := dec.Decode(&rec); err != nil {
					t.Fatalf("bad jq output %q: %v", line, err)
				}
				got = append(got, string(rec["id"].(json.Number)))
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("%s: jq %v, engine %v\nprogram: %s", op, got, want, programBody(prog))
			}
		})
	}
}
