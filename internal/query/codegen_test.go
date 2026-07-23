package query

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// codegenModel builds a ColumnModel the way OpenSource would, so path
// resolution is tested against a real model rather than a hand-built map.
func codegenModel(t *testing.T, records []map[string]any) *ColumnModel {
	t.Helper()
	disc, prof := discoverAndProfile(records)
	return buildColumnModel(disc, prof, nil)
}

// --- jq paths ---------------------------------------------------------------

func TestJQPredicatePath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"root", "$", "."},
		{"single identifier", "a", ".a?"},
		{"nested identifiers", "a.b", ".a?.b?"},
		{"array element", "tags[]", ".tags?[]?"},
		{"nested then array", "user.tags[]", ".user?.tags?[]?"},
		{"bracket-quoted dotted key", `["a.b"]`, `.["a.b"]?`},
		{"key needing quotes", `["with space"]`, `.["with space"]?`},
		{"unicode key", "café", `.["café"]?`},
		{"bare root array", "[]", "[]?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jqPredicatePath(parsePath(tc.path)); got != tc.want {
				t.Fatalf("jqPredicatePath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestJQPredicatePath_EverySegmentIsOptional pins the `?` suffixing, which is
// what keeps ONE sparse record from aborting the whole jq stream: `.a.b` on
// {"a":"str"} is a hard error ("Cannot index string with b"), while the Go
// resolve() simply yields no values and the condition is false.
//
// Mutation that must break it: drop the "?" suffix -> the assertion fails.
func TestJQPredicatePath_EverySegmentIsOptional(t *testing.T) {
	got := jqPredicatePath(parsePath("a.b.c"))
	if strings.Count(got, "?") != 3 {
		t.Fatalf("jqPredicatePath = %q, want one ? per segment", got)
	}
}

// TestJQProjectionPath_WrapsElemPaths covers the fan-out trap: a Select naming
// an array leaf must yield ONE value per record (CompiledTransform.Project
// takes values[0]), but a bare `.tags[]` emits one record PER ELEMENT.
//
// Mutation that must break it: return the predicate form unchanged -> the
// array case loses its [...][0] wrapper.
func TestJQProjectionPath_WrapsElemPaths(t *testing.T) {
	if got, want := jqProjectionPath(parsePath("tags[]")), "[.tags?[]?][0]"; got != want {
		t.Fatalf("jqProjectionPath(tags[]) = %q, want %q", got, want)
	}
	// A path with no Elem segment must NOT be wrapped -- the wrapper would be
	// noise, and [.a?][0] is not identical for a value that is itself an array.
	if got, want := jqProjectionPath(parsePath("a.b")), ".a?.b?"; got != want {
		t.Fatalf("jqProjectionPath(a.b) = %q, want %q", got, want)
	}
}

// --- SQL paths --------------------------------------------------------------

func TestSQLPathExpr(t *testing.T) {
	// "user.name" is a REAL column of the flattened model here (ColumnModel
	// keys byPath on the full dotted path), which is exactly why the naive
	// "any dotted path becomes json_extract" rule is wrong.
	cols := codegenModel(t, []map[string]any{
		{"id": json.Number("1"), "user": map[string]any{"name": "ada"}},
	})

	t.Run("known column emits one quoted identifier", func(t *testing.T) {
		got, warns := sqlPathExpr("user.name", parsePath("user.name"), cols, true)
		if want := `"user.name"`; got != want {
			t.Fatalf("sqlPathExpr = %q, want %q (the flattened model HAS this column)", got, want)
		}
		if len(warns) != 0 {
			t.Fatalf("warnings = %v, want none", warns)
		}
	})

	t.Run("unknown path on a non-sqlite target stays a flat identifier", func(t *testing.T) {
		// The illustrative `data` table is flat by definition.
		got, warns := sqlPathExpr("meta.note", parsePath("meta.note"), cols, false)
		if want := `"meta.note"`; got != want {
			t.Fatalf("sqlPathExpr = %q, want %q", got, want)
		}
		if len(warns) != 0 {
			t.Fatalf("warnings = %v, want none", warns)
		}
	})

	t.Run("unknown path on sqlite falls back to json_extract with a warning", func(t *testing.T) {
		got, warns := sqlPathExpr("meta.note", parsePath("meta.note"), cols, true)
		if want := `json_extract("meta",'$.note')`; got != want {
			t.Fatalf("sqlPathExpr = %q, want %q", got, want)
		}
		if len(warns) == 0 {
			t.Fatalf("want a warning that the path is not a real column")
		}
	})

	t.Run("a dotted KEY inside a json path is quoted in JSONPath", func(t *testing.T) {
		// segs = [meta, "a.b"] -- the second segment is a literal key that
		// contains a dot, so '$.a.b' would wrongly select {"a":{"b":..}}.
		got, _ := sqlPathExpr(`meta.["a.b"]`, parsePath(`meta.["a.b"]`), cols, true)
		if want := `json_extract("meta",'$."a.b"')`; got != want {
			t.Fatalf("sqlPathExpr = %q, want %q", got, want)
		}
	})

	t.Run("identifier quoting doubles embedded quotes", func(t *testing.T) {
		if got, want := sqliteQuoteIdent(`he said "hi"`), `"he said ""hi"""`; got != want {
			t.Fatalf("sqliteQuoteIdent = %q, want %q", got, want)
		}
	})
}

// --- literals ---------------------------------------------------------------

// TestSQLNumber pins the formatter shared by jq, the display SQL and the
// pushdown's args. Two failure modes it exists to prevent, both verified:
// 'g' prints 1e6 as "1e+06" (not a valid SQLite numeric literal in context),
// and a bare 'f' prints 1e300 as a 301-character string.
func TestSQLNumber(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1e6, "1000000"},
		{1234567.5, "1234567.5"},
		{0.1, "0.1"},
		{-42, "-42"},
		{1e-7, "1e-7"},
		{1e21, "1e+21"},
		{9007199254740992, "9007199254740992"}, // 2^53, the pushdown boundary
	}
	for _, tc := range cases {
		got, err := sqlNumber(tc.in)
		if err != nil {
			t.Fatalf("sqlNumber(%v) error = %v, want nil", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("sqlNumber(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A non-finite literal must be refused, not rendered: FormatFloat(NaN) yields
// "NaN", which SQLite rejects with `no such column: NaN` -- a generated string
// that cannot execute violates this phase's core constraint.
func TestSQLNumber_RefusesNonFinite(t *testing.T) {
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := sqlNumber(f); err == nil {
			t.Fatalf("sqlNumber(%v) error = nil, want an error", f)
		}
	}
}

func TestJQLiteral(t *testing.T) {
	cases := []struct {
		name string
		in   Value
		want string
	}{
		{"string", Value{Kind: ValString, Str: "hi"}, `"hi"`},
		{"string with quote", Value{Kind: ValString, Str: `a"b`}, `"a\"b"`},
		{"string with newline", Value{Kind: ValString, Str: "a\nb"}, `"a\nb"`},
		{"unicode", Value{Kind: ValString, Str: "café"}, `"café"`},
		{"number", Value{Kind: ValNumber, Num: 18}, "18"},
		{"fraction", Value{Kind: ValNumber, Num: 1.5}, "1.5"},
		{"bool true", Value{Kind: ValBool, Bool: true}, "true"},
		{"bool false", Value{Kind: ValBool, Bool: false}, "false"},
		{"null", Value{Kind: ValNull}, "null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jqLiteral(tc.in)
			if err != nil {
				t.Fatalf("jqLiteral error = %v, want nil", err)
			}
			if got != tc.want {
				t.Fatalf("jqLiteral = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSQLLiteral(t *testing.T) {
	cases := []struct {
		name string
		in   Value
		want string
	}{
		{"string", Value{Kind: ValString, Str: "hi"}, `'hi'`},
		{"apostrophe is doubled", Value{Kind: ValString, Str: "O'Brien"}, `'O''Brien'`},
		{"double quote is literal", Value{Kind: ValString, Str: `a"b`}, `'a"b'`},
		{"number", Value{Kind: ValNumber, Num: 18}, "18"},
		{"bool true is 1", Value{Kind: ValBool, Bool: true}, "1"},
		{"bool false is 0", Value{Kind: ValBool, Bool: false}, "0"},
		{"null", Value{Kind: ValNull}, "NULL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sqlValueLiteral(tc.in)
			if err != nil {
				t.Fatalf("sqlValueLiteral error = %v, want nil", err)
			}
			if got != tc.want {
				t.Fatalf("sqlValueLiteral = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSQLLiteral_RefusesNonFiniteNumber(t *testing.T) {
	if _, err := sqlValueLiteral(Value{Kind: ValNumber, Num: math.Inf(1)}); err == nil {
		t.Fatalf("sqlValueLiteral(+Inf) error = nil, want an error")
	}
}
