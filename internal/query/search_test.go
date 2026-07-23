package query

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// --- compileSearch / matchesSearch (unit) -----------------------------------

func TestCompileSearch_EmptyIsNilMatchAll(t *testing.T) {
	if compileSearch("") != nil {
		t.Fatalf("compileSearch(\"\") = non-nil, want nil (an empty search is match-all, decision 6)")
	}
}

func TestMatchesSearch_LeafCases(t *testing.T) {
	deep := map[string]any{"a": map[string]any{"b": "needle-here"}}
	arr := map[string]any{"tags": []any{"x", "found-it", "y"}}
	cases := []struct {
		name  string
		rec   any
		query string
		want  bool
	}{
		{"nested two levels", deep, "needle", true},
		{"inside array element", arr, "found-it", true},
		{"case-insensitive query upper", map[string]any{"v": "xabcy"}, "AbC", true},
		{"case-insensitive value upper", map[string]any{"v": "XABC"}, "abc", true},
		{"unicode fold both ways", map[string]any{"city": "MÜNCHEN"}, "münchen", true},
		{"never matches a key name", map[string]any{"name": "x"}, "nam", false},
		{"number literal", map[string]any{"n": json.Number("1042")}, "42", true},
		{"bool true", map[string]any{"b": true}, "tru", true},
		{"null never matches", map[string]any{"z": nil}, "null", false},
		{"absent needle", deep, "zzz", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pred := compileSearch(tc.query)
			if pred == nil {
				t.Fatalf("compileSearch(%q) = nil, want a predicate", tc.query)
			}
			if got := pred(tc.rec); got != tc.want {
				t.Fatalf("search %q over %v = %v, want %v", tc.query, tc.rec, got, tc.want)
			}
		})
	}
}

// --- engine: search narrows rows AND count, composes with the filter --------

func TestEngine_Search_NarrowsRowsAndCount(t *testing.T) {
	maps := []map[string]any{
		{"name": "alice", "city": "london"},
		{"name": "bob", "city": "paris"},
		{"name": "carol", "city": "london"},
	}
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}

	rs, err := e.QueryRows(context.Background(), QueryRequest{Handle: res.Handle, Search: "london", Limit: 10, WantTotal: true})
	if err != nil {
		t.Fatalf("QueryRows: %v", err)
	}
	if rs.Total != 2 || !rs.TotalExact {
		t.Fatalf("Total/exact = %d/%v, want 2/true (alice, carol)", rs.Total, rs.TotalExact)
	}
	cnt, err := e.CountMatches(context.Background(), CountRequest{Handle: res.Handle, Search: "london"})
	if err != nil {
		t.Fatalf("CountMatches: %v", err)
	}
	if cnt.Total != 2 {
		t.Fatalf("Count = %d, want 2", cnt.Total)
	}
}

func TestEngine_Search_ComposesWithFilter(t *testing.T) {
	maps := []map[string]any{
		{"name": "alice", "city": "london", "even": true},
		{"name": "bob", "city": "london", "even": false},
		{"name": "carol", "city": "paris", "even": true},
	}
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}

	// filter even==true (alice, carol) AND search "london" (alice, bob) => alice.
	rs, err := e.QueryRows(context.Background(), QueryRequest{
		Handle: res.Handle, Filter: evenFilter(), Search: "london", Limit: 10, WantTotal: true,
	})
	if err != nil {
		t.Fatalf("QueryRows: %v", err)
	}
	if rs.Total != 1 {
		t.Fatalf("Total = %d, want 1 (only alice matches filter AND search)", rs.Total)
	}
}

// TestEngine_Search_EmptyIsByteIdenticalToNoSearch pins the ACTUAL contract at
// the compile level (review #3): CompileFilterWithSearch(f, "", cm) is
// byte-identical to CompileFilter(f, cm) -- same canonical key AND src
// preserved (non-nil), so an empty search costs no predicate and never disturbs
// pure-filter pushdown. Comparing two QueryRows calls that both send Search==""
// (the old version) was a tautology: they carry the same value, so any
// empty-search regression would change BOTH identically and stay green. The
// non-empty half keeps this discriminating -- a real search MUST change the key
// and null src.
func TestEngine_Search_EmptyIsByteIdenticalToNoSearch(t *testing.T) {
	maps := fixtureRecords()
	disc, prof := discoverAndProfile(maps)
	cm := buildColumnModel(disc, prof, nil)
	f := evenFilter()

	plain, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter: %v", err)
	}
	withEmpty, err := CompileFilterWithSearch(f, "", cm)
	if err != nil {
		t.Fatalf("CompileFilterWithSearch(\"\"): %v", err)
	}
	if withEmpty.Key() != plain.Key() {
		t.Fatalf("empty-search key %q != plain key %q", withEmpty.Key(), plain.Key())
	}
	if withEmpty.src == nil {
		t.Fatalf("empty search nulled src -- pure-filter pushdown would be lost")
	}

	// Discriminating half: a NON-empty search MUST change the key (so caches
	// never alias it) and null src (so sqlBackend cannot push it away).
	withSearch, err := CompileFilterWithSearch(f, "london", cm)
	if err != nil {
		t.Fatalf("CompileFilterWithSearch(\"london\"): %v", err)
	}
	if withSearch.Key() == plain.Key() {
		t.Fatalf("a non-empty search must change the canonical key")
	}
	if withSearch.src != nil {
		t.Fatalf("a non-empty search must null src (the pushdown fallback)")
	}
}

func TestEngine_Search_DistinctSearchesDoNotShareCachedCount(t *testing.T) {
	maps := []map[string]any{
		{"city": "london"},
		{"city": "paris"},
		{"city": "london"},
	}
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	c1, err := e.CountMatches(context.Background(), CountRequest{Handle: res.Handle, Search: "london"})
	if err != nil {
		t.Fatalf("CountMatches london: %v", err)
	}
	c2, err := e.CountMatches(context.Background(), CountRequest{Handle: res.Handle, Search: "paris"})
	if err != nil {
		t.Fatalf("CountMatches paris: %v", err)
	}
	if c1.Total != 2 {
		t.Fatalf("london count = %d, want 2", c1.Total)
	}
	// A search dropped from the canonical key makes "paris" alias "london"'s
	// cached bitset/count and return 2.
	if c2.Total != 1 {
		t.Fatalf("paris count = %d, want 1 (a shared cache key would return london's 2)", c2.Total)
	}
}

// --- Step 1b: the CRITICAL sqlBackend-tier guard ----------------------------
//
// A search folded into CompiledFilter.pred alone is silently dropped on the
// SQLite tier, because sqlBackend's pushdown gate reads cf.src (the filter AST)
// not cf.pred. CompileFilterWithSearch must null src when search != "".

func TestSearch_SQLBackendTier_FilterAndSearchNotPushedAway(t *testing.T) {
	// n=0..9, label "keep" on even n, "skip" on odd. Dense rowids 1..10.
	rows := make([]map[string]any, 10)
	for i := 0; i < 10; i++ {
		label := "skip"
		if i%2 == 0 {
			label = "keep"
		}
		rows[i] = map[string]any{"n": json.Number(fmt.Sprintf("%d", i)), "label": label}
	}
	sb := newTestSQLBackend(t, "t", []string{"n", "label"}, "n INTEGER, label TEXT", rows)

	// The pushed path is the one that WOULD run without the fix -- require it.
	if !sb.hasRowID || !sb.denseRowIDs {
		t.Fatalf("fixture must be dense-rowid (hasRowID=%v dense=%v)", sb.hasRowID, sb.denseRowIDs)
	}

	// filter n>4 (exact-pushable) => {5..9}; search "keep" (even n) => {6,8}.
	f := Filter{Conditions: []Condition{{Path: "n", Op: OpGt, Value: Value{Kind: ValNumber, Num: 4}}}}
	tr := Transform{Select: []ColumnSpec{{Path: "n"}}}
	plan, err := CompilePlanWithSearch(f, "keep", tr, sb.Columns())
	if err != nil {
		t.Fatalf("CompilePlanWithSearch: %v", err)
	}
	wantN := []string{"6", "8"}

	// Count must be 2, not the pushed WHERE n>4's 5.
	total, exact, err := sb.Count(context.Background(), plan.Filter)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total != 2 || !exact {
		t.Fatalf("Count = %d (exact %v), want 2 -- the search must not be pushed away", total, exact)
	}

	// Query rows must be exactly {6,8}.
	rs, err := sb.Query(context.Background(), plan, Window{Offset: 0, Limit: 100}, true)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if rs.Total != 2 || len(rs.Rows) != 2 {
		t.Fatalf("Query Total/rows = %d/%d, want 2/2", rs.Total, len(rs.Rows))
	}
	for i, row := range rs.Rows {
		if row.Cells[0].Str != wantN[i] {
			t.Fatalf("Query row %d n = %q, want %q", i, row.Cells[0].Str, wantN[i])
		}
	}

	// Export must stream exactly {6,8}.
	enc := &collectEncoder{}
	n, err := sb.Export(context.Background(), plan, enc)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if n != 2 || len(enc.rows) != 2 {
		t.Fatalf("Export rows = %d/%d, want 2/2 -- the search must not be pushed away", n, len(enc.rows))
	}
}
