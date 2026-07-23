package query

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// searchCM builds a small ColumnModel with two top-level columns (name, age)
// and one nested column (user.city, excluded from the top-level SQL search).
func searchCM(t *testing.T) *ColumnModel {
	t.Helper()
	maps := []map[string]any{
		{"name": "alice", "age": json.Number("30"), "user": map[string]any{"city": "london"}},
	}
	disc, prof := discoverAndProfile(maps)
	return buildColumnModel(disc, prof, nil)
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

func sqlStmtLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "SELECT ") {
			return ln
		}
	}
	return ""
}

// --- jq goldens (byte-for-byte on the select line) --------------------------

func TestJQProgram_Search_Goldens(t *testing.T) {
	cm := searchCM(t)
	ageGt := Filter{Conditions: []Condition{{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 18}}}}

	// Query "AdA" must lowercase to "ada" at generation time.
	const searchOnly = `select([.. | select(type=="string" or type=="number" or type=="boolean") | tostring | ascii_downcase] | any(contains("ada")))`
	const filterSearch = `select(([.. | select(type=="string" or type=="number" or type=="boolean") | tostring | ascii_downcase] | any(contains("ada"))) and (((((.age?|type)=="number") and .age? > 18) // false)))`

	t.Run("search only", func(t *testing.T) {
		prog, warnings, err := jqProgram(Filter{}, Transform{}, CodegenContext{Format: "ndjson", Search: "AdA", Cols: cm})
		if err != nil {
			t.Fatalf("jqProgram: %v", err)
		}
		if got := lastLine(prog); got != searchOnly {
			t.Fatalf("jq select line =\n  %s\nwant\n  %s", got, searchOnly)
		}
		if !strings.Contains(prog, "# note: ") || !strings.Contains(prog, "1E+3") {
			t.Fatalf("jq program missing the numeric-canonicalisation # note:\n%s", prog)
		}
		if !containsWarning(warnings, warnCaseInsensitive) || !containsWarning(warnings, warnSearchNumericJQ) {
			t.Fatalf("jq warnings missing a search caveat: %v", warnings)
		}
	})

	t.Run("filter and search combine with and", func(t *testing.T) {
		prog, _, err := jqProgram(ageGt, Transform{}, CodegenContext{Format: "ndjson", Search: "AdA", Cols: cm})
		if err != nil {
			t.Fatalf("jqProgram: %v", err)
		}
		// Mutation: emit the search select WITHOUT `and (<filter>)` -> this fails.
		if got := lastLine(prog); got != filterSearch {
			t.Fatalf("jq filter+search line =\n  %s\nwant\n  %s", got, filterSearch)
		}
	})

	// Review #1: assert the no-op property DIRECTLY (an empty search emits no
	// search clause), not by comparing two Search=="" calls -- a tautology,
	// since both carry the same value. Mutation: drop the `if ctx.Search != ""`
	// guard in jqProgram -> an empty search emits `any(contains(""))` and the
	// absence check fails. The non-empty half keeps it discriminating.
	t.Run("empty search emits no search clause", func(t *testing.T) {
		prog, _, err := jqProgram(ageGt, Transform{}, CodegenContext{Format: "ndjson", Cols: cm})
		if err != nil {
			t.Fatalf("jqProgram: %v", err)
		}
		if strings.Contains(prog, "any(contains(") {
			t.Fatalf("empty search emitted a search clause:\n%s", prog)
		}
		withSearch, _, _ := jqProgram(ageGt, Transform{}, CodegenContext{Format: "ndjson", Search: "x", Cols: cm})
		if !strings.Contains(withSearch, "any(contains(") {
			t.Fatalf("a non-empty search should add a search clause:\n%s", withSearch)
		}
	})
}

// --- SQL goldens (statement line + caveats) ---------------------------------

func TestSQLQuery_Search_Goldens(t *testing.T) {
	cm := searchCM(t)
	ageGt := Filter{Conditions: []Condition{{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 18}}}}

	t.Run("search only illustrative", func(t *testing.T) {
		sql, warnings, err := sqlQuery(Filter{}, Transform{}, CodegenContext{Format: "ndjson", Search: "AdA", Cols: cm})
		if err != nil {
			t.Fatalf("sqlQuery: %v", err)
		}
		const want = `SELECT * FROM "data" WHERE (instr(lower("age"),lower('AdA'))>0 OR instr(lower("name"),lower('AdA'))>0);`
		if got := sqlStmtLine(sql); got != want {
			t.Fatalf("sql statement =\n  %s\nwant\n  %s", got, want)
		}
		if !containsWarning(warnings, warnCaseInsensitive) || !containsWarning(warnings, warnSearchColumnSQL) {
			t.Fatalf("sql warnings missing a search caveat: %v", warnings)
		}
		if !strings.Contains(sql, "-- note: shape's search scans every leaf value generically") {
			t.Fatalf("sql missing the top-level-only -- note:\n%s", sql)
		}
	})

	t.Run("filter and search combine with AND", func(t *testing.T) {
		sql, _, err := sqlQuery(ageGt, Transform{}, CodegenContext{Format: "sqlite", Table: "t", Search: "AdA", Cols: cm})
		if err != nil {
			t.Fatalf("sqlQuery: %v", err)
		}
		const want = `SELECT * FROM "t" WHERE "age" COLLATE BINARY > 18 AND (instr(lower("age"),lower('AdA'))>0 OR instr(lower("name"),lower('AdA'))>0);`
		// Mutation: drop the search clause from the WHERE -> this fails.
		if got := sqlStmtLine(sql); got != want {
			t.Fatalf("sql filter+search statement =\n  %s\nwant\n  %s", got, want)
		}
	})

	// Review #2: assert the no-op DIRECTLY (empty search emits no instr clause),
	// not by comparing two Search=="" calls. Mutation: drop the guard around
	// sqlSearchClause -> an empty search emits instr(lower(...),lower('')) and
	// the absence check fails.
	t.Run("empty search emits no search clause", func(t *testing.T) {
		sql, _, err := sqlQuery(ageGt, Transform{}, CodegenContext{Format: "sqlite", Table: "t", Cols: cm})
		if err != nil {
			t.Fatalf("sqlQuery: %v", err)
		}
		if strings.Contains(sql, "instr(lower(") {
			t.Fatalf("empty search emitted a search clause:\n%s", sql)
		}
		withSearch, _, _ := sqlQuery(ageGt, Transform{}, CodegenContext{Format: "sqlite", Table: "t", Search: "x", Cols: cm})
		if !strings.Contains(withSearch, "instr(lower(") {
			t.Fatalf("a non-empty search should add a search clause:\n%s", withSearch)
		}
	})

	// Review #4: a source with NO top-level (dot/bracket-free) column cannot
	// represent the search in the illustrative SQL. It must NOT silently emit a
	// filter-only/SELECT-* query that looks search-inclusive -- it must carry a
	// caveat. Mutation: drop the `else { warnings = append(..., warnSearchUnrepSQL) }`
	// branch -> the query has no search clause AND no caveat, and this fails.
	t.Run("search with no top-level column emits the unrepresented caveat", func(t *testing.T) {
		nestedOnly := []map[string]any{{"user": map[string]any{"city": "london", "name": "x"}}}
		disc, prof := discoverAndProfile(nestedOnly)
		cmNested := buildColumnModel(disc, prof, nil)
		sql, warnings, err := sqlQuery(Filter{}, Transform{}, CodegenContext{Format: "ndjson", Search: "lond", Cols: cmNested})
		if err != nil {
			t.Fatalf("sqlQuery: %v", err)
		}
		if strings.Contains(sql, "instr(lower(") {
			t.Fatalf("expected no search clause (no top-level column):\n%s", sql)
		}
		if !containsWarning(warnings, warnSearchUnrepSQL) {
			t.Fatalf("expected the unrepresented-search caveat in the warnings: %v", warnings)
		}
		if !strings.Contains(sql, "-- note: the global search is NOT represented") {
			t.Fatalf("expected the -- note about the unrepresented search:\n%s", sql)
		}
	})
}

// --- real jq: agreement on the common subset, disclosed divergences ---------

// TestJQProgram_Search_MatchesEngineWithKnownDivergences EXECUTES the generated
// search program against real jq and asserts, per query, exactly which records
// jq selects vs what compileSearch selects. It pins BOTH the agreement (string,
// integer, bool leaves) and the two divergences the plan discloses rather than
// hides:
//   - "1e3": the engine matches the source literal; jq's tostring canonicalises
//     the number to "1E+3", so ascii_downcase("1E+3")="1e+3" does not contain
//     "1e3".
//   - "münchen": the engine folds Unicode (MÜNCHEN -> münchen); jq's
//     ascii_downcase leaves Ü, so it does not match.
//
// Mutation: narrow the leaf select to `strings` -> the bool ("true") and
// integer ("42") agreement cases fail (jq stops matching non-string leaves the
// engine still matches).
func TestJQProgram_Search_MatchesEngineWithKnownDivergences(t *testing.T) {
	// NDJSON built by hand so the 1e3 literal survives verbatim.
	input := strings.Join([]string{
		`{"id":1,"s":"london"}`,
		`{"id":2,"n":1042}`,
		`{"id":3,"e":1e3}`,
		`{"id":4,"b":true}`,
		`{"id":5,"city":"MÜNCHEN"}`,
	}, "\n") + "\n"

	records := []map[string]any{
		{"id": json.Number("1"), "s": "london"},
		{"id": json.Number("2"), "n": json.Number("1042")},
		{"id": json.Number("3"), "e": json.Number("1e3")},
		{"id": json.Number("4"), "b": true},
		{"id": json.Number("5"), "city": "MÜNCHEN"},
	}

	cases := []struct {
		name       string
		query      string
		wantEngine []string
		wantJQ     []string // may differ: a disclosed divergence
	}{
		{"string leaf agrees", "lond", []string{"1"}, []string{"1"}},
		{"integer leaf agrees", "42", []string{"2"}, []string{"2"}},
		{"bool leaf agrees", "true", []string{"4"}, []string{"4"}},
		{"exponent number diverges", "1e3", []string{"3"}, nil},
		{"non-ascii fold diverges", "münchen", []string{"5"}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pred := compileSearch(tc.query)
			var engineIDs []string
			for _, r := range records {
				if pred(any(r)) {
					engineIDs = append(engineIDs, string(r["id"].(json.Number)))
				}
			}
			if strings.Join(engineIDs, ",") != strings.Join(tc.wantEngine, ",") {
				t.Fatalf("engine selected %v, want %v", engineIDs, tc.wantEngine)
			}

			prog, _, err := jqProgram(Filter{}, Transform{}, CodegenContext{Format: "ndjson", Search: tc.query})
			if err != nil {
				t.Fatalf("jqProgram: %v", err)
			}
			out := runJQ(t, prog, input) // t.Skip when jq is unavailable
			var jqIDs []string
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if line == "" {
					continue
				}
				var rec map[string]any
				dec := json.NewDecoder(strings.NewReader(line))
				dec.UseNumber()
				if err := dec.Decode(&rec); err != nil {
					t.Fatalf("decode jq output %q: %v", line, err)
				}
				jqIDs = append(jqIDs, rec["id"].(json.Number).String())
			}
			if strings.Join(jqIDs, ",") != strings.Join(tc.wantJQ, ",") {
				t.Fatalf("jq selected %v, want %v", jqIDs, tc.wantJQ)
			}
		})
	}
}

// --- engine wiring (Step 3b) ------------------------------------------------

// TestEngine_Codegen_IncludesSearch guards that Engine.Codegen threads
// req.Search into the CodegenContext. Mutation: drop the Search assignment in
// Codegen -> ctx.Search is empty, no search clause is emitted, and this fails.
func TestEngine_Codegen_IncludesSearch(t *testing.T) {
	maps := []map[string]any{{"name": "alice", "city": "london"}}
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	gen, err := e.Codegen(CodegenRequest{Handle: res.Handle, Search: "lond"})
	if err != nil {
		t.Fatalf("Codegen: %v", err)
	}
	if !strings.Contains(gen.JQ, `any(contains("lond"))`) {
		t.Fatalf("jq missing the search expr:\n%s", gen.JQ)
	}
	if !strings.Contains(gen.SQL, "instr(lower(") || !strings.Contains(gen.SQL, "'lond'") {
		t.Fatalf("sql missing the search clause:\n%s", gen.SQL)
	}
}
