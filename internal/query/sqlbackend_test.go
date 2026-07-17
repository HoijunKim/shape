package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
)

// --- fixtures ----------------------------------------------------------------
//
// SQLite has no native BOOLEAN storage class: a TRUE/FALSE literal or an
// INTEGER column round-trips through database/sql + modernc.org/sqlite as
// int64 (-> readers.ToProfileValue -> json.Number), never a Go bool. So
// every fixture below encodes a "flag" as INTEGER 0/1 (tested via OpEq
// against a ValNumber) rather than as a boolean column -- this is a real,
// expected SQLite/JSON representational gap (SQLite itself has no bool
// type), not a bug in sqlBackend; see the task report.

// makeSQLiteFixture creates a fresh SQLite file, running createSQL then each
// of insertSQL against a WRITABLE connection (unlike sqlBackend's own
// read-only connection), and returns the file's path.
func makeSQLiteFixture(t *testing.T, createSQL string, insertSQL ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(createSQL); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, s := range insertSQL {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return path
}

// sqlLiteral renders v (the same value shapes columnDiscoverer/toCell
// understand: nil, json.Number, string, bool) as a SQL literal for an
// inline INSERT statement.
func sqlLiteral(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case json.Number:
		return t.String()
	case string:
		return "'" + strings.ReplaceAll(t, "'", "''") + "'"
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// sqlInsertStatements renders rows (in order) as a batch of multi-row INSERT
// statements (chunked at 500 rows/statement to keep any one SQL string
// reasonably sized), preserving row order exactly -- the same []map[string]any
// slice can also be handed to writeNDJSONFile (rescan_test.go) so a test can
// build a SQLite fixture and an NDJSON fixture from the IDENTICAL logical
// rows, in the IDENTICAL order (needed for the cross-backend equivalence
// test's Row.Index comparison).
func sqlInsertStatements(table string, cols []string, rows []map[string]any) []string {
	const chunk = 500
	var stmts []string
	for start := 0; start < len(rows); start += chunk {
		end := start + chunk
		if end > len(rows) {
			end = len(rows)
		}
		tuples := make([]string, 0, end-start)
		for _, r := range rows[start:end] {
			vals := make([]string, len(cols))
			for i, c := range cols {
				vals[i] = sqlLiteral(r[c])
			}
			tuples = append(tuples, "("+strings.Join(vals, ",")+")")
		}
		stmts = append(stmts, fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", table, strings.Join(cols, ","), strings.Join(tuples, ",")))
	}
	return stmts
}

// sqlNameParityRows returns the same 10-record "name"/index-parity shape as
// memstore_test.go's fixtureRecords/rescan_test.go's evenFilter tests, but
// with "even" encoded as SQLite-safe INTEGER 0/1 instead of a JSON bool
// (see the fixtures doc comment above): even[i] == 1 iff i is even.
func sqlNameParityRows() []map[string]any {
	names := []string{"alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi", "ivan", "judy"}
	recs := make([]map[string]any, len(names))
	for i, name := range names {
		evenVal := 0
		if i%2 == 0 {
			evenVal = 1
		}
		recs[i] = map[string]any{
			"name": name,
			"idx":  json.Number(fmt.Sprintf("%d", i)),
			"even": json.Number(fmt.Sprintf("%d", evenVal)),
		}
	}
	return recs
}

// newTestSQLBackend builds a SQLite fixture from rows (col order = cols) and
// opens it as a *sqlBackend.
func newTestSQLBackend(t *testing.T, table string, cols []string, colTypes string, rows []map[string]any) *sqlBackend {
	t.Helper()
	createSQL := fmt.Sprintf("CREATE TABLE %s (%s)", table, colTypes)
	path := makeSQLiteFixture(t, createSQL, sqlInsertStatements(table, cols, rows)...)
	sb, err := newSQLBackend(path, "")
	if err != nil {
		t.Fatalf("newSQLBackend error = %v, want nil", err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

// sqlLogicalRows returns n records covering string/int/float/null typing --
// the cross-backend equivalence fixture (deliberately no bool column; see
// the fixtures doc comment). "score" always carries a fractional literal
// (e.g. "0.5") so it classifies as KindFloat on BOTH sides (profile.KindOf:
// a json.Number containing "." on the JSON side, and SQLite's REAL -> raw
// float64 on the SQL side, which profile.KindOf always classifies as
// KindFloat regardless of literal text -- see profile/kind.go), and the
// literal is chosen to round-trip exactly through float64 formatting on
// both sides (Cell.Str must agree exactly for the cross-backend invariant).
func sqlLogicalRows(n int) []map[string]any {
	recs := make([]map[string]any, n)
	for i := range recs {
		var tag any = fmt.Sprintf("t%d", i%3)
		if i%5 == 0 {
			tag = nil
		}
		recs[i] = map[string]any{
			"id":    json.Number(fmt.Sprintf("%d", i)),
			"name":  fmt.Sprintf("rec%d", i),
			"score": json.Number(fmt.Sprintf("%d.5", i)),
			"tag":   tag,
		}
	}
	return recs
}

// --- newSQLBackend / Columns / Profile / RowCount ---------------------------

func TestSQLBackend_Columns_SourceOrderHonored(t *testing.T) {
	// Deliberately NON-alphabetical CREATE TABLE column order: if
	// buildColumnModel's sourceOrder hint were ignored, columnDiscoverer's
	// intra-batch tie-break (columns.go's Observe: all three columns are
	// first-seen together in record 0, so ties sort ALPHABETICALLY --
	// "apple","mango","zebra") would produce a DIFFERENT order than the
	// table's real "zebra","apple","mango" -- so this proves sourceOrder is
	// actually threaded through, not a coincidence.
	sb := newTestSQLBackend(t, "t", []string{"zebra", "apple", "mango"}, "zebra INTEGER, apple INTEGER, mango INTEGER",
		[]map[string]any{
			{"zebra": json.Number("1"), "apple": json.Number("2"), "mango": json.Number("3")},
			{"zebra": json.Number("4"), "apple": json.Number("5"), "mango": json.Number("6")},
		})

	cm := sb.Columns()
	if len(cm.Columns) != 3 {
		t.Fatalf("len(Columns) = %d, want 3", len(cm.Columns))
	}
	wantOrder := []string{"zebra", "apple", "mango"}
	for i, want := range wantOrder {
		if cm.Columns[i].Name != want {
			t.Fatalf("Columns[%d].Name = %q, want %q (PRAGMA table_info/CREATE TABLE order must be honored, not alphabetical)", i, cm.Columns[i].Name, want)
		}
	}
}

func TestSQLBackend_ColumnsAndProfile(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)

	if sb.Profile().Records != len(rows) {
		t.Fatalf("Profile().Records = %d, want %d", sb.Profile().Records, len(rows))
	}
	if len(sb.Columns().Columns) != 3 {
		t.Fatalf("len(Columns) = %d, want 3", len(sb.Columns().Columns))
	}
}

func TestSQLBackend_RowCount_Exact(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)

	n, exact := sb.RowCount()
	if n != int64(len(rows)) || !exact {
		t.Fatalf("RowCount() = (%d, %v), want (%d, true)", n, exact, len(rows))
	}
}

// --- Query: empty filter -> SQL LIMIT/OFFSET pushdown, _rowid_ order -------

func TestSQLBackend_Query_EmptyFilter_WindowViaSQL(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)
	cm := sb.Columns()
	p := compilePlan(t, Filter{}, Transform{}, cm)

	rs, err := sb.Query(context.Background(), p, Window{Offset: 2, Limit: 3}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if rs.Total != int64(len(rows)) || !rs.TotalExact {
		t.Fatalf("Total/TotalExact = %d/%v, want %d/true", rs.Total, rs.TotalExact, len(rows))
	}
	if len(rs.Rows) != 3 {
		t.Fatalf("len(Rows) = %d, want 3", len(rs.Rows))
	}
	if rs.Truncated {
		t.Fatalf("Truncated = true, want false (window [2,5) fully within 10 rows)")
	}

	nameIdx := cm.byPath["name"]
	wantNames := []string{"carol", "dave", "erin"} // rows[2:5], _rowid_ order == insertion order
	for i, row := range rs.Rows {
		if row.Index != int64(2+i) {
			t.Fatalf("Rows[%d].Index = %d, want %d (absolute _rowid_-order ordinal)", i, row.Index, 2+i)
		}
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q", i, got, wantNames[i])
		}
	}
}

func TestSQLBackend_Query_EmptyFilter_WindowPastEnd_Truncated(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)
	p := compilePlan(t, Filter{}, Transform{}, sb.Columns())

	rs, err := sb.Query(context.Background(), p, Window{Offset: 8, Limit: 5}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(rs.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2 (only 2 of 10 rows left after offset 8)", len(rs.Rows))
	}
	if !rs.Truncated {
		t.Fatalf("Truncated = false, want true (fewer than Limit rows returned)")
	}
}

// --- Count/RowCount: exact via SELECT COUNT(*) ------------------------------

func TestSQLBackend_Count_EmptyFilter_ExactViaCountStar(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)

	total, exact, err := sb.Count(context.Background(), nil)
	if err != nil {
		t.Fatalf("Count(nil) error = %v, want nil", err)
	}
	if total != int64(len(rows)) || !exact {
		t.Fatalf("Count(nil) = (%d, %v), want (%d, true)", total, exact, len(rows))
	}

	total2, exact2, err := sb.Count(context.Background(), &CompiledFilter{})
	if err != nil {
		t.Fatalf("Count(empty filter) error = %v, want nil", err)
	}
	if total2 != int64(len(rows)) || !exact2 {
		t.Fatalf("Count(empty filter) = (%d, %v), want (%d, true)", total2, exact2, len(rows))
	}
}

// --- Query: filtered, offset-over-matches (the "Go reference" invariant) ---

func TestSQLBackend_Query_Filtered_MatchesGoReference(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)
	cm := sb.Columns()
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpEq, Value: Value{Kind: ValNumber, Num: 1}}}}
	p := compilePlan(t, f, Transform{}, cm)

	// Hand-computed Go reference: matches (even==1) are indices 0,2,4,6,8 ->
	// alice,carol,erin,grace,ivan. offset=3,limit=2 over the MATCH sequence
	// (not raw row position) -> matches[3:5] -> grace, ivan.
	rs, err := sb.Query(context.Background(), p, Window{Offset: 3, Limit: 2}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if rs.Total != 5 || !rs.TotalExact {
		t.Fatalf("Total/TotalExact = %d/%v, want 5/true", rs.Total, rs.TotalExact)
	}
	wantNames := []string{"grace", "ivan"}
	if len(rs.Rows) != len(wantNames) {
		t.Fatalf("len(Rows) = %d, want %d", len(rs.Rows), len(wantNames))
	}
	nameIdx := cm.byPath["name"]
	for i, row := range rs.Rows {
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q", i, got, wantNames[i])
		}
	}
	// The two skipped matches (alice, carol) must never appear.
	for _, row := range rs.Rows {
		if row.Cells[nameIdx].Str == "alice" || row.Cells[nameIdx].Str == "carol" {
			t.Fatalf("Rows contains a pre-window match %q", row.Cells[nameIdx].Str)
		}
	}
}

func TestSQLBackend_Query_Filtered_FullMatchSet(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)
	cm := sb.Columns()
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpEq, Value: Value{Kind: ValNumber, Num: 1}}}}
	p := compilePlan(t, f, Transform{}, cm)

	rs, err := sb.Query(context.Background(), p, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	if len(rs.Rows) != len(wantNames) {
		t.Fatalf("len(Rows) = %d, want %d", len(rs.Rows), len(wantNames))
	}
	nameIdx := cm.byPath["name"]
	for i, row := range rs.Rows {
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q (match order must follow _rowid_ order)", i, got, wantNames[i])
		}
	}
}

func TestSQLBackend_Count_Filtered_Exact(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpEq, Value: Value{Kind: ValNumber, Num: 1}}}}
	cf, err := CompileFilter(f, sb.Columns())
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	total, exact, err := sb.Count(context.Background(), cf)
	if err != nil {
		t.Fatalf("Count error = %v, want nil", err)
	}
	if total != 5 || !exact {
		t.Fatalf("Count = (%d, %v), want (5, true)", total, exact)
	}
}

// --- Count: cancellable -----------------------------------------------------

func manySQLRows(n int) []map[string]any {
	recs := make([]map[string]any, n)
	for i := range recs {
		evenVal := 0
		if i%2 == 0 {
			evenVal = 1
		}
		recs[i] = map[string]any{
			"name": fmt.Sprintf("rec%d", i),
			"even": json.Number(fmt.Sprintf("%d", evenVal)),
		}
	}
	return recs
}

func TestSQLBackend_Count_CancelledContext(t *testing.T) {
	rows := manySQLRows(8000)
	sb := newTestSQLBackend(t, "t", []string{"name", "even"}, "name TEXT, even INTEGER", rows)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpEq, Value: Value{Kind: ValNumber, Num: 1}}}}
	cf, err := CompileFilter(f, sb.Columns())
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := sb.Count(ctx, cf); err == nil {
		t.Fatalf("Count(cancelled ctx) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Count(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
}

func TestSQLBackend_Query_Filtered_CancelledContext(t *testing.T) {
	rows := manySQLRows(8000)
	sb := newTestSQLBackend(t, "t", []string{"name", "even"}, "name TEXT, even INTEGER", rows)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpEq, Value: Value{Kind: ValNumber, Num: 1}}}}
	p := compilePlan(t, f, Transform{}, sb.Columns())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := sb.Query(ctx, p, Window{Offset: 0, Limit: 10}, true); err == nil {
		t.Fatalf("Query(cancelled ctx) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Query(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
}

// --- Export ------------------------------------------------------------------

func TestSQLBackend_Export_StreamsAllMatchingProjectedRows(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpEq, Value: Value{Kind: ValNumber, Num: 1}}}}
	tr := Transform{Select: []ColumnSpec{{Path: "name"}}}
	p := compilePlan(t, f, tr, sb.Columns())

	enc := &collectEncoder{}
	n, err := sb.Export(context.Background(), p, enc)
	if err != nil {
		t.Fatalf("Export error = %v, want nil", err)
	}
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	if n != int64(len(wantNames)) {
		t.Fatalf("Export rows = %d, want %d", n, len(wantNames))
	}
	for i, row := range enc.rows {
		if len(row.Cells) != 1 || row.Cells[0].Str != wantNames[i] {
			t.Fatalf("rows[%d] = %#v, want a single-cell row with name %q", i, row, wantNames[i])
		}
	}
}

// --- Close --------------------------------------------------------------------

func TestSQLBackend_Close_NoError(t *testing.T) {
	rows := sqlNameParityRows()
	sb := newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", rows)
	if err := sb.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}

// --- WITHOUT ROWID: no crash, hasRowID=false, exact count still works ------

func TestSQLBackend_WithoutRowIDTable(t *testing.T) {
	path := makeSQLiteFixture(t,
		"CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT) WITHOUT ROWID",
		"INSERT INTO t (id, val) VALUES (1,'a'),(2,'b'),(3,'c')",
	)
	sb, err := newSQLBackend(path, "")
	if err != nil {
		t.Fatalf("newSQLBackend error = %v, want nil", err)
	}
	defer sb.Close()

	if sb.hasRowID {
		t.Fatalf("hasRowID = true, want false for a WITHOUT ROWID table")
	}
	n, exact := sb.RowCount()
	if n != 3 || !exact {
		t.Fatalf("RowCount() = (%d, %v), want (3, true)", n, exact)
	}

	p := compilePlan(t, Filter{}, Transform{}, sb.Columns())
	rs, err := sb.Query(context.Background(), p, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(rs.Rows) != 3 {
		t.Fatalf("len(Rows) = %d, want 3", len(rs.Rows))
	}
}

// --- Engine wiring: OpenSource routes FormatSQLite to sqlBackend, Tier "sqlite" --

func TestEngine_OpenSource_SQLite_TierAndColumns(t *testing.T) {
	rows := sqlNameParityRows()
	path := makeSQLiteFixture(t,
		"CREATE TABLE t (name TEXT, idx INTEGER, even INTEGER)",
		sqlInsertStatements("t", []string{"name", "idx", "even"}, rows)...,
	)

	e := NewEngine()
	res, err := e.OpenSource(OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}
	// Windows cannot remove an open file: t.TempDir()'s cleanup would
	// otherwise fail with "used by another process" unless the sqlBackend's
	// underlying *sql.DB connection is closed first.
	t.Cleanup(func() { e.CloseSource(res.Handle) })
	if res.Format != string(readers.FormatSQLite) {
		t.Fatalf("Format = %q, want %q", res.Format, readers.FormatSQLite)
	}
	if res.Tier != "sqlite" {
		t.Fatalf("Tier = %q, want \"sqlite\"", res.Tier)
	}
	if !res.RowExact || res.RowEstimate != int64(len(rows)) {
		t.Fatalf("RowEstimate/RowExact = %d/%v, want %d/true", res.RowEstimate, res.RowExact, len(rows))
	}
	if len(res.Columns) != 3 || res.Columns[0].Name != "name" || res.Columns[1].Name != "idx" || res.Columns[2].Name != "even" {
		t.Fatalf("Columns = %#v, want [name idx even] in CREATE TABLE order", res.Columns)
	}

	rs, err := e.QueryRows(QueryRequest{Handle: res.Handle, Filter: Filter{Conditions: []Condition{{Path: "even", Op: OpEq, Value: Value{Kind: ValNumber, Num: 1}}}}, Offset: 0, Limit: 10, WantTotal: true})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if rs.Total != 5 || !rs.TotalExact {
		t.Fatalf("Total/TotalExact = %d/%v, want 5/true", rs.Total, rs.TotalExact)
	}
}

// --- Cross-backend equivalence: sqlBackend vs memBackend on the SAME logical --
// --- rows (spec §9's headline invariant) ------------------------------------

func TestCrossBackend_SQLBackendMatchesMemBackend(t *testing.T) {
	rows := sqlLogicalRows(37) // odd count so windows land unevenly across boundaries
	ndjsonPath := writeNDJSONFile(t, rows)
	sqlitePath := makeSQLiteFixture(t,
		"CREATE TABLE t (id INTEGER, name TEXT, score REAL, tag TEXT)",
		sqlInsertStatements("t", []string{"id", "name", "score", "tag"}, rows)...,
	)

	e := NewEngine()
	memRes, err := e.OpenSource(OpenRequest{Path: ndjsonPath})
	if err != nil {
		t.Fatalf("OpenSource(ndjson) error = %v, want nil", err)
	}
	if memRes.Tier != "memory" {
		t.Fatalf("OpenSource(ndjson) Tier = %q, want \"memory\"", memRes.Tier)
	}
	sqlRes, err := e.OpenSource(OpenRequest{Path: sqlitePath})
	if err != nil {
		t.Fatalf("OpenSource(sqlite) error = %v, want nil", err)
	}
	// Windows cannot remove an open file: t.TempDir()'s cleanup would
	// otherwise fail with "used by another process" unless the sqlBackend's
	// underlying *sql.DB connection is closed first.
	t.Cleanup(func() { e.CloseSource(sqlRes.Handle) })
	if sqlRes.Tier != "sqlite" {
		t.Fatalf("OpenSource(sqlite) Tier = %q, want \"sqlite\"", sqlRes.Tier)
	}

	// An explicit Select fixes output column order identically on both
	// backends -- the base ColumnModel order legitimately differs (mem:
	// first-seen/alphabetical-tiebreak order; sql: PRAGMA table_info order)
	// even though the underlying DATA is identical, so this isolates the
	// invariant this test actually cares about: same filter+window => same
	// Cells, not "same base column discovery order".
	tr := Transform{Select: []ColumnSpec{{Path: "id"}, {Path: "name"}, {Path: "score"}, {Path: "tag"}}}

	cases := []struct {
		name   string
		f      Filter
		offset int64
		limit  int
	}{
		{"empty_filter_window", Filter{}, 5, 10},
		{"empty_filter_window_tail", Filter{}, 30, 20},
		{"score_gt", Filter{Conditions: []Condition{{Path: "score", Op: OpGt, Value: Value{Kind: ValNumber, Num: 10}}}}, 0, 50},
		{"score_gt_windowed", Filter{Conditions: []Condition{{Path: "score", Op: OpGt, Value: Value{Kind: ValNumber, Num: 10}}}}, 2, 4},
		{"tag_isnull", Filter{Conditions: []Condition{{Path: "tag", Op: OpIsNull}}}, 0, 50},
		{"tag_notnull", Filter{Conditions: []Condition{{Path: "tag", Op: OpNotNull}}}, 1, 5},
		{"name_contains", Filter{Conditions: []Condition{{Path: "name", Op: OpContains, Value: Value{Kind: ValString, Str: "rec1"}}}}, 1, 3},
		{"id_eq", Filter{Conditions: []Condition{{Path: "id", Op: OpEq, Value: Value{Kind: ValNumber, Num: 5}}}}, 0, 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := func(handle string) QueryRequest {
				return QueryRequest{Handle: handle, Filter: tc.f, Transform: tr, Offset: tc.offset, Limit: tc.limit, WantTotal: true}
			}
			memRS, err := e.QueryRows(req(memRes.Handle))
			if err != nil {
				t.Fatalf("QueryRows(mem) error = %v, want nil", err)
			}
			sqlRS, err := e.QueryRows(req(sqlRes.Handle))
			if err != nil {
				t.Fatalf("QueryRows(sql) error = %v, want nil", err)
			}

			if memRS.Total != sqlRS.Total {
				t.Fatalf("Total mismatch: mem=%d sql=%d", memRS.Total, sqlRS.Total)
			}
			if !memRS.TotalExact || !sqlRS.TotalExact {
				t.Fatalf("TotalExact mismatch: mem=%v sql=%v, want both true", memRS.TotalExact, sqlRS.TotalExact)
			}
			if len(memRS.Rows) != len(sqlRS.Rows) {
				t.Fatalf("len(Rows) mismatch: mem=%d sql=%d", len(memRS.Rows), len(sqlRS.Rows))
			}
			if len(memRS.Rows) == 0 {
				t.Fatalf("fixture invalid: 0 rows returned for case %q", tc.name)
			}
			for i := range memRS.Rows {
				if memRS.Rows[i].Index != sqlRS.Rows[i].Index {
					t.Fatalf("row %d Index mismatch: mem=%d sql=%d", i, memRS.Rows[i].Index, sqlRS.Rows[i].Index)
				}
				if len(memRS.Rows[i].Cells) != len(sqlRS.Rows[i].Cells) {
					t.Fatalf("row %d len(Cells) mismatch: mem=%d sql=%d", i, len(memRS.Rows[i].Cells), len(sqlRS.Rows[i].Cells))
				}
				for j := range memRS.Rows[i].Cells {
					if memRS.Rows[i].Cells[j] != sqlRS.Rows[i].Cells[j] {
						t.Fatalf("row %d cell %d mismatch: mem=%#v sql=%#v", i, j, memRS.Rows[i].Cells[j], sqlRS.Rows[i].Cells[j])
					}
				}
			}
		})
	}
}
