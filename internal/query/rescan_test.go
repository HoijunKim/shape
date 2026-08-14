package query

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoijunkim/shape/internal/readers"
)

// manyNumberedRecords returns records with a numeric "n" (shuffled, so sorted
// order != source order) and a float "f", for the E9 sort parity + Row.Index
// tests. Deterministic (fixed seed). Shared by rescan + the T10 parity sweep.
func manyNumberedRecords(nrec int) []map[string]any {
	recs := make([]map[string]any, nrec)
	r := rand.New(rand.NewSource(1))
	perm := r.Perm(nrec)
	for i := 0; i < nrec; i++ {
		recs[i] = map[string]any{"n": perm[i], "f": float64(perm[i]) + 0.5}
	}
	return recs
}

func TestRescanBackend_SortMatchesMemoryTier(t *testing.T) {
	maps := manyNumberedRecords(20000)               // > 1 MiB decoded -> BudgetMB=1 forces the rescan tier
	engMem, hMem, _ := openExportFixture(t, maps, 0) // memory tier
	engRe, hRe, _ := openExportFixture(t, maps, 1)   // rescan tier (fixture fails if not "rescan")
	req := func(h string) QueryRequest {
		return QueryRequest{Handle: h, Offset: 50, Limit: 30, Sort: SortSpec{Path: "n", Desc: true}}
	}
	rsMem, err := engMem.QueryRows(context.Background(), req(hMem))
	if err != nil {
		t.Fatal(err)
	}
	rsRe, err := engRe.QueryRows(context.Background(), req(hRe))
	if err != nil {
		t.Fatal(err)
	}
	if len(rsMem.Rows) != len(rsRe.Rows) || len(rsMem.Rows) == 0 {
		t.Fatalf("window sizes: mem %d vs rescan %d (want equal, non-zero)", len(rsMem.Rows), len(rsRe.Rows))
	}
	for i := range rsMem.Rows {
		if rsMem.Rows[i].Index != rsRe.Rows[i].Index {
			t.Fatalf("row %d: mem Index %d != rescan Index %d (sorted windows must be byte-identical across tiers)", i, rsMem.Rows[i].Index, rsRe.Rows[i].Index)
		}
	}
}

// --- fixtures ----------------------------------------------------------------

// writeNDJSONFile writes maps as NDJSON (one compact JSON object per line, the
// same shape jsonreader.LineMode expects) to a fresh temp file and returns its
// path. rescanBackend needs a REAL, re-openable path -- unlike memBackend it
// never holds decoded records, only ever re-reading from disk (spec §4).
func writeNDJSONFile(t *testing.T, maps []map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.ndjson")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture file: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range maps {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode fixture record: %v", err)
		}
	}
	return path
}

// newTestRescanBackend writes maps to an NDJSON file and wraps it in a
// rescanBackend built the same way OpenSource's ingest pass would on an
// over-budget source: discover columns + profile over the same records (the
// ingest SAMPLE), then construct a rescanBackend pointed at the file with the
// given (test-controlled) avgBytes/fileSize -- letting Total-estimate tests
// pick deliberately distinctive values rather than depending on sizeOf's
// exact heuristic.
func newTestRescanBackend(t *testing.T, maps []map[string]any, avgBytes float64, fileSize int64) (*rescanBackend, *ColumnModel, string) {
	t.Helper()
	path := writeNDJSONFile(t, maps)
	disc, prof := discoverAndProfile(maps)
	cm := buildColumnModel(disc, prof, nil)
	rb := newRescanBackend(path, readers.FormatJSON, "", false, cm, prof, avgBytes, fileSize)
	return rb, cm, path
}

// --- Columns/Profile/RowCount -------------------------------------------------

func TestRescanBackend_ColumnsAndProfile(t *testing.T) {
	maps := fixtureRecords()
	rb, cm, _ := newTestRescanBackend(t, maps, 100, 1000)
	if rb.Columns() != cm {
		t.Fatalf("Columns() did not return the same *ColumnModel passed to newRescanBackend")
	}
	if rb.Profile().Records != len(maps) {
		t.Fatalf("Profile().Records = %d, want %d", rb.Profile().Records, len(maps))
	}
}

func TestRescanBackend_RowCount_IsEstimateNotExact(t *testing.T) {
	maps := fixtureRecords() // 10 real records
	// Deliberately distinctive avgBytes/fileSize so the estimate (37) cannot
	// be confused with the true record count (10): RowCount must report
	// exactly the fileSize/avgBytes formula, not the real count.
	rb, _, _ := newTestRescanBackend(t, maps, 10.0, 370)
	n, exact := rb.RowCount(context.Background())
	if exact {
		t.Fatalf("RowCount() exact = true, want false (spec §4: rescanBackend RowCount is always an estimate)")
	}
	if n != 37 {
		t.Fatalf("RowCount() n = %d, want 37 (= round(370/10), the fileSize/avgBytes estimate)", n)
	}
}

func TestRescanBackend_RowCount_UnknownInputsYieldZero(t *testing.T) {
	rb, _, _ := newTestRescanBackend(t, fixtureRecords(), 0, 0)
	n, exact := rb.RowCount(context.Background())
	if n != 0 || exact {
		t.Fatalf("RowCount() = (%d,%v), want (0,false) when avgBytes/fileSize are unknown", n, exact)
	}
}

// --- Query: empty filter / window --------------------------------------------

func TestRescanBackend_Query_EmptyFilter_Window(t *testing.T) {
	maps := fixtureRecords()
	rb, cm, _ := newTestRescanBackend(t, maps, 10.0, 370) // estimate = 37, deliberately != 10
	p := compilePlan(t, Filter{}, Transform{}, cm)

	rs, err := rb.Query(context.Background(), p, Window{Offset: 2, Limit: 3}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if rs.TotalExact {
		t.Fatalf("TotalExact = true, want false (unfiltered rescan Total is always an estimate)")
	}
	if rs.Total != 37 {
		t.Fatalf("Total = %d, want 37 (the fileSize/avgBytes estimate, spec §4)", rs.Total)
	}
	if len(rs.Rows) != 3 {
		t.Fatalf("len(Rows) = %d, want 3", len(rs.Rows))
	}
	if rs.Offset != 2 {
		t.Fatalf("Offset = %d, want 2", rs.Offset)
	}
	if rs.Truncated {
		t.Fatalf("Truncated = true, want false (window [2,5) fully within 10 records)")
	}

	nameIdx := cm.byPath["name"]
	wantNames := []string{"carol", "dave", "erin"} // records[2:5]
	for i, row := range rs.Rows {
		if row.Index != int64(2+i) {
			t.Fatalf("Rows[%d].Index = %d, want %d (absolute record ordinal)", i, row.Index, 2+i)
		}
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q", i, got, wantNames[i])
		}
	}
}

func TestRescanBackend_Query_WindowPastEnd_Truncated(t *testing.T) {
	maps := fixtureRecords()
	rb, _, _ := newTestRescanBackend(t, maps, 10.0, 100)
	p := compilePlan(t, Filter{}, Transform{}, rb.Columns())

	rs, err := rb.Query(context.Background(), p, Window{Offset: 8, Limit: 5}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(rs.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2 (only 2 of 10 records left after offset 8)", len(rs.Rows))
	}
	if !rs.Truncated {
		t.Fatalf("Truncated = false, want true (fewer than Limit rows returned: EOF reached)")
	}

	rs2, err := rb.Query(context.Background(), p, Window{Offset: 20, Limit: 5}, true)
	if err != nil {
		t.Fatalf("Query (entirely past end) error = %v, want nil", err)
	}
	if len(rs2.Rows) != 0 {
		t.Fatalf("len(Rows) = %d, want 0 (offset beyond all records)", len(rs2.Rows))
	}
	if !rs2.Truncated {
		t.Fatalf("Truncated = false, want true (window entirely past end)")
	}
}

// --- Query: filtered, and the offset-skips-MATCHES-not-raw-records rule -----

func TestRescanBackend_Query_Filtered(t *testing.T) {
	maps := fixtureRecords()
	rb, cm, _ := newTestRescanBackend(t, maps, 10.0, 100)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, cm)

	rs, err := rb.Query(context.Background(), p, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	// wantTotal=true never early-stops (spec §4), so the scan reaches real
	// EOF and matched-so-far happens to equal the exact filtered count (5)
	// -- but TotalExact must still read false: Query never asserts
	// exactness for a real filter (that guarantee is Count's alone).
	if rs.Total != 5 {
		t.Fatalf("Total = %d, want 5 (even-index matches)", rs.Total)
	}
	if rs.TotalExact {
		t.Fatalf("TotalExact = true, want false (rescanBackend.Query never asserts an exact filtered total)")
	}
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	if len(rs.Rows) != len(wantNames) {
		t.Fatalf("len(Rows) = %d, want %d", len(rs.Rows), len(wantNames))
	}
	nameIdx := cm.byPath["name"]
	for i, row := range rs.Rows {
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q (match order must follow file order)", i, got, wantNames[i])
		}
	}
}

// TestRescanBackend_Query_SkipsOffsetMatches_WindowOverMatchSequence covers
// the rescan-specific correctness rule from the task brief: Offset/Limit
// apply to the MATCH sequence, not raw record position -- matches before the
// window are scanned (decoded and tested) but never projected into Rows.
func TestRescanBackend_Query_SkipsOffsetMatches_WindowOverMatchSequence(t *testing.T) {
	maps := fixtureRecords()
	rb, cm, _ := newTestRescanBackend(t, maps, 10.0, 100)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, cm)

	// matches (even indices) = [0,2,4,6,8] -> "alice","carol","erin","grace","ivan"
	// window offset=3,limit=2 over the MATCH sequence -> matches[3:5] -> "grace","ivan"
	rs, err := rb.Query(context.Background(), p, Window{Offset: 3, Limit: 2}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
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
	// The two skipped matches ("alice" at raw idx 0, "carol" at raw idx 2)
	// must not appear anywhere in Rows.
	for _, row := range rs.Rows {
		if row.Cells[nameIdx].Str == "alice" || row.Cells[nameIdx].Str == "carol" {
			t.Fatalf("Rows contains a pre-window match %q, want only matches[3:5]", row.Cells[nameIdx].Str)
		}
	}
}

// --- Query: early-stop is conditioned on !wantTotal, not on filter state ----

func TestRescanBackend_Query_EarlyStop_WhenWantTotalFalse(t *testing.T) {
	maps := manyRecords(5000)
	rb, _, _ := newTestRescanBackend(t, maps, 1, 1)
	p := compilePlan(t, Filter{}, Transform{}, rb.Columns())

	rs, err := rb.Query(context.Background(), p, Window{Offset: 0, Limit: 5}, false)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(rs.Rows) != 5 {
		t.Fatalf("len(Rows) = %d, want 5", len(rs.Rows))
	}
	if rs.Total != -1 {
		t.Fatalf("Total = %d, want -1 (wantTotal=false: skipped, spec's Backend.Query doc)", rs.Total)
	}
	if rs.Scanned > 100 {
		t.Fatalf("Scanned = %d, want a small number (<=100): !wantTotal must early-stop once the window (limit=5) is full, not scan all %d records", rs.Scanned, len(maps))
	}
}

func TestRescanBackend_Query_NoEarlyStop_WhenWantTotalTrue(t *testing.T) {
	maps := manyRecords(5000)
	rb, _, _ := newTestRescanBackend(t, maps, 1, 1)
	p := compilePlan(t, Filter{}, Transform{}, rb.Columns())

	rs, err := rb.Query(context.Background(), p, Window{Offset: 0, Limit: 5}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if rs.Scanned != int64(len(maps)) {
		t.Fatalf("Scanned = %d, want %d: wantTotal=true must scan to EOF, never early-stop (spec §4)", rs.Scanned, len(maps))
	}
}

// --- Count: full, exact, cancellable scan ------------------------------------

func TestRescanBackend_Count_Exact(t *testing.T) {
	maps := fixtureRecords()
	rb, cm, _ := newTestRescanBackend(t, maps, 10.0, 100)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	cf, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	total, exact, err := rb.Count(context.Background(), cf)
	if err != nil {
		t.Fatalf("Count error = %v, want nil", err)
	}
	if total != 5 || !exact {
		t.Fatalf("Count = (%d, %v), want (5, true)", total, exact)
	}
}

func TestRescanBackend_Count_NilFilterMatchesAll(t *testing.T) {
	maps := fixtureRecords()
	rb, _, _ := newTestRescanBackend(t, maps, 10.0, 100)

	total, exact, err := rb.Count(context.Background(), nil)
	if err != nil {
		t.Fatalf("Count(nil) error = %v, want nil", err)
	}
	if total != int64(len(maps)) || !exact {
		t.Fatalf("Count(nil) = (%d, %v), want (%d, true)", total, exact, len(maps))
	}
}

func TestRescanBackend_Count_CancelledContext(t *testing.T) {
	maps := manyRecords(20000)
	rb, cm, _ := newTestRescanBackend(t, maps, 1, 1)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	cf, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := rb.Count(ctx, cf); err == nil {
		t.Fatalf("Count(cancelled ctx) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Count(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
}

// --- Query: cancellation -----------------------------------------------------

func TestRescanBackend_Query_CancelledContext(t *testing.T) {
	maps := manyRecords(20000)
	rb, cm, _ := newTestRescanBackend(t, maps, 1, 1)
	p := compilePlan(t, Filter{}, Transform{}, cm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rs, err := rb.Query(ctx, p, Window{Offset: 0, Limit: 10}, true)
	if err == nil {
		t.Fatalf("Query(cancelled ctx) error = nil, want non-nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Query(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
	if len(rs.Rows) != 0 {
		t.Fatalf("Query(cancelled ctx) returned %d rows, want a zero-value RowSet on error", len(rs.Rows))
	}
}

// --- Export -------------------------------------------------------------------

func TestRescanBackend_Export_StreamsAllMatchingProjectedRows(t *testing.T) {
	maps := fixtureRecords()
	rb, cm, _ := newTestRescanBackend(t, maps, 10.0, 100)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	tr := Transform{Select: []ColumnSpec{{Path: "name"}}}
	p := compilePlan(t, f, tr, cm)

	enc := &collectEncoder{}
	n, err := rb.Export(context.Background(), p, enc)
	if err != nil {
		t.Fatalf("Export error = %v, want nil", err)
	}
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	if n != int64(len(wantNames)) {
		t.Fatalf("Export rows = %d, want %d", n, len(wantNames))
	}
	for i, row := range enc.rows {
		if len(row.values) != 1 || row.values[0] != any(wantNames[i]) {
			t.Fatalf("rows[%d] = %#v, want a single-value row with name %q", i, row, wantNames[i])
		}
		if row.index != int64(2*i) {
			t.Fatalf("rows[%d].index = %d, want %d (absolute record ordinal, even indices)", i, row.index, 2*i)
		}
	}
}

func TestRescanBackend_Export_EmptyFilterStreamsEverything(t *testing.T) {
	maps := fixtureRecords()
	rb, cm, _ := newTestRescanBackend(t, maps, 10.0, 100)
	p := compilePlan(t, Filter{}, Transform{}, cm)

	enc := &collectEncoder{}
	n, err := rb.Export(context.Background(), p, enc)
	if err != nil {
		t.Fatalf("Export error = %v, want nil", err)
	}
	if n != int64(len(maps)) || len(enc.rows) != len(maps) {
		t.Fatalf("Export rows = %d (%d collected), want %d", n, len(enc.rows), len(maps))
	}
}

// --- Close --------------------------------------------------------------------

func TestRescanBackend_Close_NoError(t *testing.T) {
	rb, _, _ := newTestRescanBackend(t, fixtureRecords(), 10.0, 100)
	if err := rb.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}
