package query

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
	"github.com/parquet-go/parquet-go"
)

// --- fixtures ----------------------------------------------------------------
//
// Parquet needs a typed Go schema (struct tags), unlike the []map[string]any
// shape every other backend's fixtures use directly -- these structs mirror
// the SAME logical content as their memstore_test.go/sqlbackend_test.go
// counterparts (parquetNameParityRows <-> fixtureRecords/sqlNameParityRows)
// so cross-referencing expected values across files is straightforward.

// writeParquetFixture writes rows to a fresh temp .parquet file, forcing a
// new row group every maxRowsPerGroup rows (parquet.MaxRowsPerRowGroup) so a
// SINGLE Write call still produces multiple row groups (verified directly
// against parquet-go: a batched Write([]T) call splits across row groups
// exactly like a per-row Write loop does -- see the task report), and
// returns its path. rescanBackend/sqlBackend's test fixtures are similarly
// always generated fresh per test (t.TempDir()) rather than committed under
// testdata/; parquetBackend's follow the same established pattern (see the
// task report for why no internal/query/testdata/ directory was added).
func writeParquetFixture[T any](t *testing.T, rows []T, maxRowsPerGroup int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.parquet")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create parquet fixture: %v", err)
	}
	defer f.Close()

	var opts []parquet.WriterOption
	if maxRowsPerGroup > 0 {
		opts = append(opts, parquet.MaxRowsPerRowGroup(maxRowsPerGroup))
	}
	w := parquet.NewGenericWriter[T](f, opts...)
	if _, err := w.Write(rows); err != nil {
		t.Fatalf("write parquet rows: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close parquet writer: %v", err)
	}
	return path
}

// newTestParquetBackend writes rows to a fresh parquet fixture and opens it
// as a *parquetBackend.
func newTestParquetBackend[T any](t *testing.T, rows []T, maxRowsPerGroup int64) *parquetBackend {
	t.Helper()
	path := writeParquetFixture(t, rows, maxRowsPerGroup)
	pb, err := newParquetBackend(path)
	if err != nil {
		t.Fatalf("newParquetBackend error = %v, want nil", err)
	}
	t.Cleanup(func() { pb.Close() })
	return pb
}

// parquetNameParityRow mirrors fixtureRecords()/sqlNameParityRows()'s "name"/
// index-parity shape (memstore_test.go/sqlbackend_test.go), typed for the
// parquet writer.
type parquetNameParityRow struct {
	Name string `parquet:"name"`
	Age  int64  `parquet:"age"`
	Even bool   `parquet:"even"`
}

func parquetNameParityRows() []parquetNameParityRow {
	names := []string{"alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi", "ivan", "judy"}
	rows := make([]parquetNameParityRow, len(names))
	for i, name := range names {
		rows[i] = parquetNameParityRow{Name: name, Age: int64(20 + i), Even: i%2 == 0}
	}
	return rows
}

// parquetZebraRow has a deliberately NON-alphabetical field order (mirrors
// sqlbackend_test.go's TestSQLBackend_Columns_SourceOrderHonored): if
// buildColumnModel's sourceOrder hint were ignored, columnDiscoverer's
// intra-batch alphabetical tie-break would reorder these to
// "apple","mango","zebra" instead of the schema's real "zebra","apple","mango".
type parquetZebraRow struct {
	Zebra int64 `parquet:"zebra"`
	Apple int64 `parquet:"apple"`
	Mango int64 `parquet:"mango"`
}

// parquetManyRow is a minimal wide-scan fixture shape for cancellation tests
// (mirrors manyRecords/manySQLRows): large enough that a scan crosses several
// cancelCheckStride (4096) boundaries.
type parquetManyRow struct {
	Name string `parquet:"name"`
	Even bool   `parquet:"even"`
}

func manyParquetRows(n int) []parquetManyRow {
	rows := make([]parquetManyRow, n)
	for i := range rows {
		rows[i] = parquetManyRow{Name: fmt.Sprintf("rec%d", i), Even: i%2 == 0}
	}
	return rows
}

// --- newParquetBackend / Columns / Profile / RowCount -----------------------

func TestParquetBackend_Columns_SourceOrderHonored(t *testing.T) {
	pb := newTestParquetBackend(t, []parquetZebraRow{{Zebra: 1, Apple: 2, Mango: 3}, {Zebra: 4, Apple: 5, Mango: 6}}, 0)

	cm := pb.Columns()
	if len(cm.Columns) != 3 {
		t.Fatalf("len(Columns) = %d, want 3", len(cm.Columns))
	}
	wantOrder := []string{"zebra", "apple", "mango"}
	for i, want := range wantOrder {
		if cm.Columns[i].Name != want {
			t.Fatalf("Columns[%d].Name = %q, want %q (parquet schema field order must be honored, not alphabetical)", i, cm.Columns[i].Name, want)
		}
	}
}

func TestParquetBackend_ColumnsAndProfile(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 0)

	if pb.Profile().Records != len(rows) {
		t.Fatalf("Profile().Records = %d, want %d", pb.Profile().Records, len(rows))
	}
	if len(pb.Columns().Columns) != 3 {
		t.Fatalf("len(Columns) = %d, want 3", len(pb.Columns().Columns))
	}
}

func TestParquetBackend_RowCount_Exact(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 0)

	n, exact := pb.RowCount()
	if n != int64(len(rows)) || !exact {
		t.Fatalf("RowCount() = (%d, %v), want (%d, true)", n, exact, len(rows))
	}
}

func TestParquetBackend_Total_ExactFromFooter_MultipleRowGroups(t *testing.T) {
	rows := parquetNameParityRows() // 10 rows
	pb := newTestParquetBackend(t, rows, 3)

	if len(pb.pf.RowGroups()) < 2 {
		t.Fatalf("len(RowGroups()) = %d, want >= 2 (fixture must actually exercise multiple row groups)", len(pb.pf.RowGroups()))
	}
	if pb.total != int64(len(rows)) {
		t.Fatalf("total = %d, want %d (sum of every row group's NumRows(), from the footer)", pb.total, len(rows))
	}
	n, exact := pb.RowCount()
	if n != int64(len(rows)) || !exact {
		t.Fatalf("RowCount() = (%d, %v), want (%d, true)", n, exact, len(rows))
	}
}

// --- Query: empty filter -> SeekToRow window, no full scan -----------------

func TestParquetBackend_Query_EmptyFilter_WindowViaSeek(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 3) // >= 2 row groups: the window below spans a row-group boundary
	cm := pb.Columns()
	p := compilePlan(t, Filter{}, Transform{}, cm)

	rs, err := pb.Query(context.Background(), p, Window{Offset: 2, Limit: 3}, true)
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
	wantNames := []string{"carol", "dave", "erin"} // rows[2:5], file/row order
	for i, row := range rs.Rows {
		if row.Index != int64(2+i) {
			t.Fatalf("Rows[%d].Index = %d, want %d (absolute row ordinal)", i, row.Index, 2+i)
		}
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q", i, got, wantNames[i])
		}
	}
}

func TestParquetBackend_Query_EmptyFilter_WindowPastEnd_Truncated(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 0)
	p := compilePlan(t, Filter{}, Transform{}, pb.Columns())

	rs, err := pb.Query(context.Background(), p, Window{Offset: 8, Limit: 5}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(rs.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2 (only 2 of 10 rows left after offset 8)", len(rs.Rows))
	}
	if !rs.Truncated {
		t.Fatalf("Truncated = false, want true (fewer than Limit rows returned)")
	}

	rs2, err := pb.Query(context.Background(), p, Window{Offset: 20, Limit: 5}, true)
	if err != nil {
		t.Fatalf("Query (entirely past end) error = %v, want nil", err)
	}
	if len(rs2.Rows) != 0 {
		t.Fatalf("len(Rows) = %d, want 0 (offset beyond all rows)", len(rs2.Rows))
	}
	if !rs2.Truncated {
		t.Fatalf("Truncated = false, want true (window entirely past end)")
	}
}

// --- Query: projection through BOTH the seek path and the scan path --------

func TestParquetBackend_Query_Projection_OnlyRequestedColumns(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 3)
	tr := Transform{Select: []ColumnSpec{{Path: "name"}}}

	// Empty filter: exercises readWindow (the seek path).
	p := compilePlan(t, Filter{}, tr, pb.Columns())
	rs, err := pb.Query(context.Background(), p, Window{Offset: 1, Limit: 2}, false)
	if err != nil {
		t.Fatalf("Query (seek path) error = %v, want nil", err)
	}
	if len(rs.Columns) != 1 || rs.Columns[0].Name != "name" {
		t.Fatalf("Columns = %#v, want a single \"name\" column", rs.Columns)
	}
	for _, row := range rs.Rows {
		if len(row.Cells) != 1 {
			t.Fatalf("row Cells = %#v, want exactly 1 cell (projected)", row.Cells)
		}
	}

	// Filtered: exercises the full scan path.
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p2 := compilePlan(t, f, tr, pb.Columns())
	rs2, err := pb.Query(context.Background(), p2, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query (scan path) error = %v, want nil", err)
	}
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	if len(rs2.Rows) != len(wantNames) {
		t.Fatalf("len(Rows) = %d, want %d", len(rs2.Rows), len(wantNames))
	}
	for i, row := range rs2.Rows {
		if len(row.Cells) != 1 || row.Cells[0].Str != wantNames[i] {
			t.Fatalf("Rows[%d] = %#v, want a single-cell row with name %q", i, row, wantNames[i])
		}
	}
}

// --- Count/RowCount: exact via footer / Go scan ------------------------------

func TestParquetBackend_Count_EmptyFilter_ExactFromFooter(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 0)

	total, exact, err := pb.Count(context.Background(), nil)
	if err != nil {
		t.Fatalf("Count(nil) error = %v, want nil", err)
	}
	if total != int64(len(rows)) || !exact {
		t.Fatalf("Count(nil) = (%d, %v), want (%d, true)", total, exact, len(rows))
	}

	total2, exact2, err := pb.Count(context.Background(), &CompiledFilter{})
	if err != nil {
		t.Fatalf("Count(empty filter) error = %v, want nil", err)
	}
	if total2 != int64(len(rows)) || !exact2 {
		t.Fatalf("Count(empty filter) = (%d, %v), want (%d, true)", total2, exact2, len(rows))
	}
}

// --- Query: filtered, offset-over-matches (the "Go reference" invariant) ---

func TestParquetBackend_Query_Filtered_MatchesGoReference(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 3)
	cm := pb.Columns()
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, cm)

	// Hand-computed Go reference: matches (even==true) are indices
	// 0,2,4,6,8 -> alice,carol,erin,grace,ivan. offset=3,limit=2 over the
	// MATCH sequence (not raw row position) -> matches[3:5] -> grace, ivan.
	rs, err := pb.Query(context.Background(), p, Window{Offset: 3, Limit: 2}, true)
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
	for _, row := range rs.Rows {
		if row.Cells[nameIdx].Str == "alice" || row.Cells[nameIdx].Str == "carol" {
			t.Fatalf("Rows contains a pre-window match %q", row.Cells[nameIdx].Str)
		}
	}
}

func TestParquetBackend_Query_Filtered_FullMatchSet(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 3)
	cm := pb.Columns()
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, cm)

	rs, err := pb.Query(context.Background(), p, Window{Offset: 0, Limit: 10}, true)
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
			t.Fatalf("Rows[%d] name = %q, want %q (match order must follow file/row order)", i, got, wantNames[i])
		}
	}
}

func TestParquetBackend_Count_Filtered_Exact(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 0)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	cf, err := CompileFilter(f, pb.Columns())
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	total, exact, err := pb.Count(context.Background(), cf)
	if err != nil {
		t.Fatalf("Count error = %v, want nil", err)
	}
	if total != 5 || !exact {
		t.Fatalf("Count = (%d, %v), want (5, true)", total, exact)
	}
}

// --- Cancellation ------------------------------------------------------------

func TestParquetBackend_Count_CancelledContext(t *testing.T) {
	rows := manyParquetRows(8000)
	pb := newTestParquetBackend(t, rows, 0)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	cf, err := CompileFilter(f, pb.Columns())
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := pb.Count(ctx, cf); err == nil {
		t.Fatalf("Count(cancelled ctx) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Count(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
}

func TestParquetBackend_Query_Filtered_CancelledContext(t *testing.T) {
	rows := manyParquetRows(8000)
	pb := newTestParquetBackend(t, rows, 0)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, pb.Columns())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := pb.Query(ctx, p, Window{Offset: 0, Limit: 10}, true); err == nil {
		t.Fatalf("Query(cancelled ctx) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Query(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
}

func TestParquetBackend_Query_EmptyFilter_CancelledContext(t *testing.T) {
	rows := manyParquetRows(8000)
	pb := newTestParquetBackend(t, rows, 0)
	p := compilePlan(t, Filter{}, Transform{}, pb.Columns())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := pb.Query(ctx, p, Window{Offset: 0, Limit: 10}, true); err == nil {
		t.Fatalf("Query(cancelled ctx) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Query(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
}

// --- Export ------------------------------------------------------------------

func TestParquetBackend_Export_StreamsAllMatchingProjectedRows(t *testing.T) {
	rows := parquetNameParityRows()
	pb := newTestParquetBackend(t, rows, 3)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	tr := Transform{Select: []ColumnSpec{{Path: "name"}}}
	p := compilePlan(t, f, tr, pb.Columns())

	enc := &collectEncoder{}
	n, err := pb.Export(context.Background(), p, enc)
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

func TestParquetBackend_Close_NoError(t *testing.T) {
	pb := newTestParquetBackend(t, parquetNameParityRows(), 0)
	if err := pb.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}

// --- newParquetBackend: stdin/invalid-file rejection ------------------------

func TestParquetBackend_NewParquetBackend_RejectsEmptyPath(t *testing.T) {
	if _, err := newParquetBackend(""); err == nil {
		t.Fatalf("newParquetBackend(\"\") error = nil, want non-nil (stdin rejected)")
	}
}

func TestParquetBackend_NewParquetBackend_InvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-parquet-file.parquet")
	if err := os.WriteFile(path, []byte("definitely not parquet"), 0o644); err != nil {
		t.Fatalf("write invalid fixture: %v", err)
	}
	if _, err := newParquetBackend(path); err == nil {
		t.Fatalf("newParquetBackend(invalid file) error = nil, want non-nil")
	}
}

// --- Engine wiring: OpenSource routes FormatParquet to parquetBackend, Tier "parquet" --

func TestEngine_OpenSource_Parquet_TierAndColumns(t *testing.T) {
	rows := parquetNameParityRows()
	path := writeParquetFixture(t, rows, 3)

	e := NewEngine()
	res, err := e.OpenSource(OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}
	// Windows cannot remove an open file: t.TempDir()'s cleanup would
	// otherwise fail with "used by another process" unless the
	// parquetBackend's underlying *os.File is closed first.
	t.Cleanup(func() { e.CloseSource(res.Handle) })
	if res.Format != string(readers.FormatParquet) {
		t.Fatalf("Format = %q, want %q", res.Format, readers.FormatParquet)
	}
	if res.Tier != "parquet" {
		t.Fatalf("Tier = %q, want \"parquet\"", res.Tier)
	}
	if !res.RowExact || res.RowEstimate != int64(len(rows)) {
		t.Fatalf("RowEstimate/RowExact = %d/%v, want %d/true", res.RowEstimate, res.RowExact, len(rows))
	}
	if len(res.Columns) != 3 || res.Columns[0].Name != "name" || res.Columns[1].Name != "age" || res.Columns[2].Name != "even" {
		t.Fatalf("Columns = %#v, want [name age even] in schema order", res.Columns)
	}

	rs, err := e.QueryRows(QueryRequest{Handle: res.Handle, Filter: Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}, Offset: 0, Limit: 10, WantTotal: true})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if rs.Total != 5 || !rs.TotalExact {
		t.Fatalf("Total/TotalExact = %d/%v, want 5/true", rs.Total, rs.TotalExact)
	}
}

// --- Cross-backend invariant helpers -----------------------------------------

// assertRowsMatch asserts want and got are identical row-by-row: same Index,
// same Cells (full equality, spec §9's "byte-identical" row invariant).
func assertRowsMatch(t *testing.T, name string, want, got []Row) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("[%s] len(Rows) = %d, want %d", name, len(got), len(want))
	}
	for i := range want {
		if want[i].Index != got[i].Index {
			t.Fatalf("[%s] row %d Index = %d, want %d", name, i, got[i].Index, want[i].Index)
		}
		if len(want[i].Cells) != len(got[i].Cells) {
			t.Fatalf("[%s] row %d len(Cells) = %d, want %d", name, i, len(got[i].Cells), len(want[i].Cells))
		}
		for j := range want[i].Cells {
			if want[i].Cells[j] != got[i].Cells[j] {
				t.Fatalf("[%s] row %d cell %d = %#v, want %#v", name, i, j, got[i].Cells[j], want[i].Cells[j])
			}
		}
	}
}

// assertTotalMatches asserts rs is an EXACT backend (TotalExact == true)
// reporting the same Total as want.
func assertTotalMatches(t *testing.T, name string, want int64, rs RowSet) {
	t.Helper()
	if !rs.TotalExact {
		t.Fatalf("[%s] TotalExact = false, want true", name)
	}
	if rs.Total != want {
		t.Fatalf("[%s] Total = %d, want %d", name, rs.Total, want)
	}
}

// writeCSVFile writes header+rows as CSV to a fresh temp file and returns its
// path, rendering each cell the same way csvreader.inferValue would need to
// parse it back (json.Number verbatim, bool as "true"/"false", nil as an
// empty cell -- csvreader.go: cell=="" -> nil).
func writeCSVFile(t *testing.T, header []string, rows []map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create csv fixture: %v", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		t.Fatalf("write csv header: %v", err)
	}
	for _, r := range rows {
		rec := make([]string, len(header))
		for i, col := range header {
			rec[i] = csvCellLiteral(r[col])
		}
		if err := w.Write(rec); err != nil {
			t.Fatalf("write csv row: %v", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatalf("flush csv: %v", err)
	}
	return path
}

// csvCellLiteral renders v (the same value shapes columnDiscoverer/toCell
// understand) as a CSV cell string.
func csvCellLiteral(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case json.Number:
		return t.String()
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// crossFormatParquetRow is the typed parquet counterpart of
// sqlLogicalRows(n)'s map rows (sqlbackend_test.go): same id/name/score/tag
// shape, Tag nullable via a pointer (Parquet's real optional/null
// representation).
type crossFormatParquetRow struct {
	ID    int64   `parquet:"id"`
	Name  string  `parquet:"name"`
	Score float64 `parquet:"score"`
	Tag   *string `parquet:"tag,optional"`
}

// toParquetRows converts sqlLogicalRows(n)'s []map[string]any into typed
// parquet rows, preserving order and null/non-null "tag" exactly.
func toParquetRows(rows []map[string]any) []crossFormatParquetRow {
	out := make([]crossFormatParquetRow, len(rows))
	for i, r := range rows {
		idNum := r["id"].(json.Number)
		id, _ := idNum.Int64()
		scoreNum := r["score"].(json.Number)
		score, _ := scoreNum.Float64()
		var tag *string
		if s, ok := r["tag"].(string); ok {
			tag = &s
		}
		out[i] = crossFormatParquetRow{ID: id, Name: r["name"].(string), Score: score, Tag: tag}
	}
	return out
}

// --- THE HEADLINE TEST: all four backends agree on identical rows (spec §9) -

// TestCrossBackend_AllFourBackendsMatch is E1's headline guarantee (spec §9):
// the SAME logical rows, encoded as ndjson (memBackend AND, via a tiny
// avgBytes/fileSize, rescanBackend), csv (a bonus fifth memBackend instance
// exercising the CSV ingest path), sqlite (sqlBackend), and a >=2-row-group
// parquet file (parquetBackend), must return byte-identical Rows for an
// identical (filter, transform, window) -- extending
// TestCrossBackend_SQLBackendMatchesMemBackend (sqlbackend_test.go) to all
// four Backend implementations.
//
// Float-parity note (see the task report): "score" always carries a
// CANONICAL float literal (e.g. "5.5", never "5.50"): toCell's Str for a raw
// float64 (sqlite/parquet) is strconv.FormatFloat(f,'g',-1,64) -- the
// shortest round-trip decimal -- while a JSON/CSV json.Number's Str is the
// verbatim source literal; these agree byte-for-byte ONLY when the literal
// IS already that canonical shortest form, which sqlLogicalRows guarantees
// (fmt.Sprintf("%d.5", i) is always exact/canonical for float64).
func TestCrossBackend_AllFourBackendsMatch(t *testing.T) {
	rows := sqlLogicalRows(37) // odd count so windows land unevenly across boundaries

	ndjsonPath := writeNDJSONFile(t, rows)
	csvPath := writeCSVFile(t, []string{"id", "name", "score", "tag"}, rows)
	sqlitePath := makeSQLiteFixture(t,
		"CREATE TABLE t (id INTEGER, name TEXT, score REAL, tag TEXT)",
		sqlInsertStatements("t", []string{"id", "name", "score", "tag"}, rows)...,
	)
	parquetPath := writeParquetFixture(t, toParquetRows(rows), 10) // 37 rows / 10-per-group -> 4 row groups

	e := NewEngine()

	memRes, err := e.OpenSource(OpenRequest{Path: ndjsonPath})
	if err != nil {
		t.Fatalf("OpenSource(ndjson) error = %v, want nil", err)
	}
	if memRes.Tier != "memory" {
		t.Fatalf("OpenSource(ndjson) Tier = %q, want \"memory\"", memRes.Tier)
	}

	// rescanBackend is constructed directly (bypassing OpenSource's
	// MB-granularity BudgetMB, which cannot force a downgrade on a fixture
	// this small): the SAME direct-construction pattern rescan_test.go's
	// newTestRescanBackend uses. avgBytes=fileSize=1 makes RowCount/the
	// unfiltered Total a deliberately tiny, inexact estimate -- irrelevant to
	// row correctness, since every filtered/windowed Query still runs the
	// identical shared Go predicate over the real file (see rescan.go).
	disc, prof := discoverAndProfile(rows)
	rescanCM := buildColumnModel(disc, prof, nil)
	rb := newRescanBackend(ndjsonPath, readers.FormatJSON, "", false, rescanCM, prof, 1, 1)
	rescanHandle := e.register(rb)

	csvRes, err := e.OpenSource(OpenRequest{Path: csvPath})
	if err != nil {
		t.Fatalf("OpenSource(csv) error = %v, want nil", err)
	}
	if csvRes.Tier != "memory" {
		t.Fatalf("OpenSource(csv) Tier = %q, want \"memory\"", csvRes.Tier)
	}

	sqlRes, err := e.OpenSource(OpenRequest{Path: sqlitePath})
	if err != nil {
		t.Fatalf("OpenSource(sqlite) error = %v, want nil", err)
	}
	if sqlRes.Tier != "sqlite" {
		t.Fatalf("OpenSource(sqlite) Tier = %q, want \"sqlite\"", sqlRes.Tier)
	}

	parquetRes, err := e.OpenSource(OpenRequest{Path: parquetPath})
	if err != nil {
		t.Fatalf("OpenSource(parquet) error = %v, want nil", err)
	}
	if parquetRes.Tier != "parquet" {
		t.Fatalf("OpenSource(parquet) Tier = %q, want \"parquet\"", parquetRes.Tier)
	}

	// Windows cannot remove an open file: t.TempDir()'s cleanup would
	// otherwise fail with "used by another process" unless the sqlite/
	// parquet backends' underlying handles are closed first.
	t.Cleanup(func() {
		e.CloseSource(memRes.Handle)
		e.CloseSource(rescanHandle)
		e.CloseSource(csvRes.Handle)
		e.CloseSource(sqlRes.Handle)
		e.CloseSource(parquetRes.Handle)
	})

	// An explicit Select fixes output column order identically across every
	// backend -- the base ColumnModel order legitimately differs (mem/rescan:
	// first-seen/alphabetical tie-break; csv: header order; sql: PRAGMA
	// table_info order; parquet: struct field order) even though the
	// underlying DATA is identical, so this isolates the invariant this test
	// actually cares about: same filter+window => same Cells (mirrors
	// TestCrossBackend_SQLBackendMatchesMemBackend, sqlbackend_test.go).
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
			rescanRS, err := e.QueryRows(req(rescanHandle))
			if err != nil {
				t.Fatalf("QueryRows(rescan) error = %v, want nil", err)
			}
			csvRS, err := e.QueryRows(req(csvRes.Handle))
			if err != nil {
				t.Fatalf("QueryRows(csv) error = %v, want nil", err)
			}
			sqlRS, err := e.QueryRows(req(sqlRes.Handle))
			if err != nil {
				t.Fatalf("QueryRows(sql) error = %v, want nil", err)
			}
			parquetRS, err := e.QueryRows(req(parquetRes.Handle))
			if err != nil {
				t.Fatalf("QueryRows(parquet) error = %v, want nil", err)
			}

			if len(memRS.Rows) == 0 {
				t.Fatalf("fixture invalid: 0 rows returned for case %q", tc.name)
			}

			assertRowsMatch(t, "rescan", memRS.Rows, rescanRS.Rows)
			assertRowsMatch(t, "csv", memRS.Rows, csvRS.Rows)
			assertRowsMatch(t, "sql", memRS.Rows, sqlRS.Rows)
			assertRowsMatch(t, "parquet", memRS.Rows, parquetRS.Rows)

			// Total: mem/csv/sql/parquet are all EXACT backends and must
			// agree exactly. rescanBackend.Query's Total/TotalExact is
			// deliberately an estimate/lower-bound by design (rescan.go's
			// doc comment) -- with avgBytes=fileSize=1 above it is not even
			// a plausible estimate of the real total, so it is intentionally
			// excluded from this comparison; the Rows assertion above is
			// rescanBackend's half of the invariant.
			if !memRS.TotalExact {
				t.Fatalf("mem TotalExact = false, want true")
			}
			assertTotalMatches(t, "csv", memRS.Total, csvRS)
			assertTotalMatches(t, "sql", memRS.Total, sqlRS)
			assertTotalMatches(t, "parquet", memRS.Total, parquetRS)
		})
	}
}

// --- Parquet nesting: array membership + real BOOL type (mem vs parquet) ---
//
// CSV/SQLite cannot faithfully represent a nested array column (SQLite has
// no bool type either -- see sqlbackend_test.go's fixtures doc comment) so
// this invariant is checked between mem(ndjson) and parquet only, per the
// task brief's array-membership/BOOL callouts: "Parquet has a real BOOL
// type (unlike SQLite), so bool -> CellBool should match JSON."

// parquetNestedFixtureRow carries a real Parquet LIST column (Tags) and a
// real Parquet BOOL column (Active).
type parquetNestedFixtureRow struct {
	ID     int64    `parquet:"id"`
	Name   string   `parquet:"name"`
	Active bool     `parquet:"active"`
	Tags   []string `parquet:"tags"`
}

// parquetNestedLogicalRows returns the SAME logical rows in both shapes: the
// []map[string]any ndjson needs, and the typed struct slice parquet needs.
func parquetNestedLogicalRows(n int) ([]map[string]any, []parquetNestedFixtureRow) {
	palette := [][]string{{"blue"}, {"red", "blue"}, {"green"}, {"blue", "green", "red"}, {"red"}}
	maps := make([]map[string]any, n)
	structs := make([]parquetNestedFixtureRow, n)
	for i := range maps {
		tags := palette[i%len(palette)]
		tagsAny := make([]any, len(tags))
		for j, tg := range tags {
			tagsAny[j] = tg
		}
		active := i%2 == 0
		maps[i] = map[string]any{
			"id":     json.Number(fmt.Sprintf("%d", i)),
			"name":   fmt.Sprintf("rec%d", i),
			"active": active,
			"tags":   tagsAny,
		}
		structs[i] = parquetNestedFixtureRow{
			ID:     int64(i),
			Name:   fmt.Sprintf("rec%d", i),
			Active: active,
			Tags:   append([]string(nil), tags...),
		}
	}
	return maps, structs
}

func TestCrossBackend_ParquetNested_ArrayMembershipAndBool(t *testing.T) {
	maps, structs := parquetNestedLogicalRows(23)
	ndjsonPath := writeNDJSONFile(t, maps)
	parquetPath := writeParquetFixture(t, structs, 7) // 23 rows / 7-per-group -> 4 row groups

	e := NewEngine()
	memRes, err := e.OpenSource(OpenRequest{Path: ndjsonPath})
	if err != nil {
		t.Fatalf("OpenSource(ndjson) error = %v, want nil", err)
	}
	parquetRes, err := e.OpenSource(OpenRequest{Path: parquetPath})
	if err != nil {
		t.Fatalf("OpenSource(parquet) error = %v, want nil", err)
	}
	if parquetRes.Tier != "parquet" {
		t.Fatalf("Tier = %q, want \"parquet\"", parquetRes.Tier)
	}
	t.Cleanup(func() {
		e.CloseSource(memRes.Handle)
		e.CloseSource(parquetRes.Handle)
	})

	tr := Transform{Select: []ColumnSpec{{Path: "id"}, {Path: "name"}, {Path: "active"}, {Path: "tags"}}}

	cases := []struct {
		name   string
		f      Filter
		offset int64
		limit  int
	}{
		{"tags_membership_blue", Filter{Conditions: []Condition{{Path: "tags[]", Op: OpEq, Value: Value{Kind: ValString, Str: "blue"}}}}, 0, 50},
		{"active_true", Filter{Conditions: []Condition{{Path: "active", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}, 1, 5},
		{"empty_filter_all", Filter{}, 0, 30},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := func(h string) QueryRequest {
				return QueryRequest{Handle: h, Filter: tc.f, Transform: tr, Offset: tc.offset, Limit: tc.limit, WantTotal: true}
			}
			memRS, err := e.QueryRows(req(memRes.Handle))
			if err != nil {
				t.Fatalf("QueryRows(mem) error = %v, want nil", err)
			}
			parquetRS, err := e.QueryRows(req(parquetRes.Handle))
			if err != nil {
				t.Fatalf("QueryRows(parquet) error = %v, want nil", err)
			}
			if len(memRS.Rows) == 0 {
				t.Fatalf("fixture invalid: 0 rows for case %q", tc.name)
			}
			assertRowsMatch(t, "parquet", memRS.Rows, parquetRS.Rows)
			assertTotalMatches(t, "parquet", memRS.Total, parquetRS)
		})
	}
}
