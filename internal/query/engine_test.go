package query

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
)

// evenFilter is the shared "even"==true bool Condition used across these
// tests (see fixtureRecords/manyRecords in memstore_test.go: both fixtures
// carry an index-parity "even" bool field).
func evenFilter() Filter {
	return Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
}

func nameColumnIndex(t *testing.T, cols []Column) int {
	t.Helper()
	for i, c := range cols {
		if c.Name == "name" {
			return i
		}
	}
	t.Fatalf("no \"name\" column found in %#v", cols)
	return -1
}

// --- OpenSource: small NDJSON -> memory tier, exact total, filtered query --

func TestEngine_OpenSource_SmallNDJSON_MemoryTier(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)

	e := NewEngine()
	res, err := e.OpenSource(OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}
	if res.Handle == "" {
		t.Fatalf("Handle = %q, want non-empty", res.Handle)
	}
	if res.Format != string(readers.FormatJSON) {
		t.Fatalf("Format = %q, want %q", res.Format, readers.FormatJSON)
	}
	if res.Tier != "memory" {
		t.Fatalf("Tier = %q, want \"memory\" (a small fixture must fit the default 512 MiB budget)", res.Tier)
	}
	if res.Sampled {
		t.Fatalf("Sampled = true, want false (memory tier is not sampled)")
	}
	if !res.RowExact || res.RowEstimate != int64(len(maps)) {
		t.Fatalf("RowEstimate/RowExact = %d/%v, want %d/true", res.RowEstimate, res.RowExact, len(maps))
	}
	if len(res.Columns) == 0 {
		t.Fatalf("Columns is empty, want the discovered base column set")
	}
	if res.Profile.Records != len(maps) {
		t.Fatalf("Profile.Records = %d, want %d", res.Profile.Records, len(maps))
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("Warnings = %#v, want none (memory tier is not the over-budget case)", res.Warnings)
	}

	rs, err := e.QueryRows(QueryRequest{Handle: res.Handle, Filter: evenFilter(), Offset: 0, Limit: 10, WantTotal: true})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if rs.Total != 5 || !rs.TotalExact {
		t.Fatalf("Total/TotalExact = %d/%v, want 5/true", rs.Total, rs.TotalExact)
	}
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	if len(rs.Rows) != len(wantNames) {
		t.Fatalf("len(Rows) = %d, want %d", len(rs.Rows), len(wantNames))
	}
	nameIdx := nameColumnIndex(t, rs.Columns)
	for i, row := range rs.Rows {
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q", i, got, wantNames[i])
		}
	}
}

// --- OpenSource: a tiny BudgetMB on the SAME file downgrades to rescan,     --
// --- and both tiers return IDENTICAL rows for the same filter+window       --
// --- (the cross-tier invariant, spec §9).                                  --

func TestEngine_OpenSource_BudgetDowngrade_CrossTierInvariant(t *testing.T) {
	maps := manyRecords(20000) // large enough that a 1 MiB budget is exceeded well before EOF
	path := writeNDJSONFile(t, maps)

	e := NewEngine()

	memRes, err := e.OpenSource(OpenRequest{Path: path}) // default (512 MiB) budget
	if err != nil {
		t.Fatalf("OpenSource(default budget) error = %v, want nil", err)
	}
	if memRes.Tier != "memory" {
		t.Fatalf("OpenSource(default budget) Tier = %q, want \"memory\"", memRes.Tier)
	}

	rescanRes, err := e.OpenSource(OpenRequest{Path: path, BudgetMB: 1})
	if err != nil {
		t.Fatalf("OpenSource(BudgetMB=1) error = %v, want nil", err)
	}
	if rescanRes.Tier != "rescan" {
		t.Fatalf("OpenSource(BudgetMB=1) Tier = %q, want \"rescan\" (20000 records must exceed a 1 MiB budget)", rescanRes.Tier)
	}
	if !rescanRes.Sampled {
		t.Fatalf("Sampled = false, want true (rescan tier)")
	}
	if rescanRes.RowExact {
		t.Fatalf("RowExact = true, want false (rescan tier's RowEstimate is never exact)")
	}
	if len(rescanRes.Warnings) == 0 {
		t.Fatalf("Warnings is empty, want a streaming-mode warning for the rescan tier")
	}

	// Both handles must agree on the base column set (same file, same
	// discovery/profiling logic -- only the ingest sample size differs).
	if len(memRes.Columns) != len(rescanRes.Columns) {
		t.Fatalf("Columns differ between tiers: memory=%d rescan=%d", len(memRes.Columns), len(rescanRes.Columns))
	}

	req := func(handle string) QueryRequest {
		return QueryRequest{Handle: handle, Filter: evenFilter(), Offset: 100, Limit: 20, WantTotal: true}
	}
	memRS, err := e.QueryRows(req(memRes.Handle))
	if err != nil {
		t.Fatalf("QueryRows(memory) error = %v, want nil", err)
	}
	rescanRS, err := e.QueryRows(req(rescanRes.Handle))
	if err != nil {
		t.Fatalf("QueryRows(rescan) error = %v, want nil", err)
	}

	if len(memRS.Rows) == 0 {
		t.Fatalf("fixture invalid: memory-tier query returned 0 rows")
	}
	if len(memRS.Rows) != len(rescanRS.Rows) {
		t.Fatalf("cross-tier invariant violated: len(Rows) memory=%d rescan=%d", len(memRS.Rows), len(rescanRS.Rows))
	}
	for i := range memRS.Rows {
		if memRS.Rows[i].Index != rescanRS.Rows[i].Index {
			t.Fatalf("cross-tier invariant violated at row %d: Index memory=%d rescan=%d", i, memRS.Rows[i].Index, rescanRS.Rows[i].Index)
		}
		if len(memRS.Rows[i].Cells) != len(rescanRS.Rows[i].Cells) {
			t.Fatalf("cross-tier invariant violated at row %d: len(Cells) memory=%d rescan=%d", i, len(memRS.Rows[i].Cells), len(rescanRS.Rows[i].Cells))
		}
		for j := range memRS.Rows[i].Cells {
			if memRS.Rows[i].Cells[j] != rescanRS.Rows[i].Cells[j] {
				t.Fatalf("cross-tier invariant violated at row %d, cell %d: memory=%#v rescan=%#v", i, j, memRS.Rows[i].Cells[j], rescanRS.Rows[i].Cells[j])
			}
		}
	}
}

// --- QueryRows after CloseSource: clean error, no panic ---------------------

func TestEngine_QueryRows_AfterCloseSource_ErrorsCleanly(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)

	e := NewEngine()
	res, err := e.OpenSource(OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}
	if err := e.CloseSource(res.Handle); err != nil {
		t.Fatalf("CloseSource error = %v, want nil", err)
	}

	if _, err := e.QueryRows(QueryRequest{Handle: res.Handle, Limit: 10}); err == nil {
		t.Fatalf("QueryRows after CloseSource error = nil, want non-nil")
	}

	// Closing an already-closed/unknown handle must also error cleanly
	// rather than panic.
	if err := e.CloseSource(res.Handle); err == nil {
		t.Fatalf("CloseSource on an already-closed handle error = nil, want non-nil")
	}
}

// --- OpenSource: stdin/empty path rejected (spec §2) ------------------------

func TestEngine_OpenSource_RejectsEmptyAndStdinPath(t *testing.T) {
	e := NewEngine()
	for _, p := range []string{"", "-"} {
		if _, err := e.OpenSource(OpenRequest{Path: p}); err == nil {
			t.Fatalf("OpenSource(Path=%q) error = nil, want non-nil (spec §2: stdin/empty rejected)", p)
		}
	}
}

// --- OpenSource: an empty/invalid file errors for both formats -------------
//
// FormatParquet routing is still stubbed (Task 8, ErrParquetBackendNotImplemented
// in source.go). FormatSQLite is wired to newSQLBackend as of Task 7
// (sqlbackend.go): a real .sqlite fixture is covered by
// TestEngine_OpenSource_SQLite_TierAndColumns/TestCrossBackend_SQLBackendMatchesMemBackend
// in sqlbackend_test.go; this test just confirms an empty/invalid file
// still errors cleanly (not a valid SQLite database) rather than panicking.

func TestEngine_OpenSource_SQLiteAndParquet_EmptyFileErrors(t *testing.T) {
	dir := t.TempDir()
	for _, ext := range []string{"sqlite", "parquet"} {
		path := filepath.Join(dir, "fixture."+ext)
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			t.Fatalf("write dummy %s file: %v", ext, err)
		}
		e := NewEngine()
		if _, err := e.OpenSource(OpenRequest{Path: path}); err == nil {
			t.Fatalf("OpenSource(%s) error = nil, want non-nil (empty file is not a valid %s source; parquet is also still stubbed, Task 8)", ext, ext)
		}
	}
}
