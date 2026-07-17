package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// --- fixtures ----------------------------------------------------------------

// memRecords converts a []map[string]any fixture into the []any shape
// memBackend stores (matching how a decoded record stream is handed in from
// OpenSource's ingest pass -- see spec §4, memBackend holds "records []any").
func memRecords(maps []map[string]any) []any {
	recs := make([]any, len(maps))
	for i, m := range maps {
		recs[i] = m
	}
	return recs
}

// fixtureRecords returns 10 records, in file order, with a deterministic
// "even" predicate (index parity) that both a filter test and a windowing
// test can key off of without recomputing which records matched.
func fixtureRecords() []map[string]any {
	names := []string{"alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi", "ivan", "judy"}
	recs := make([]map[string]any, len(names))
	for i, name := range names {
		recs[i] = map[string]any{
			"name": name,
			"age":  json.Number(fmt.Sprintf("%d", 20+i)),
			"even": i%2 == 0,
		}
	}
	return recs
}

// manyRecords returns n records with the same shape as fixtureRecords (a
// "name" field and an index-parity "even" bool), but sized so that a
// computeMatchBitset scan crosses several cancelCheckStride (4096) boundaries
// -- large enough that a cancellation check partway through the scan (not
// just the very first iteration) would matter, and to make a cold-scan
// cancellation test a faithful stand-in for a large real-world scan.
func manyRecords(n int) []map[string]any {
	recs := make([]map[string]any, n)
	for i := range recs {
		recs[i] = map[string]any{
			"name": fmt.Sprintf("rec%d", i),
			"even": i%2 == 0,
		}
	}
	return recs
}

// newTestMemBackend builds a memBackend the same way OpenSource's ingest
// pass would: discover columns + profile over the same records, then wrap
// them in a memBackend.
func newTestMemBackend(t *testing.T, maps []map[string]any) (*memBackend, *ColumnModel) {
	t.Helper()
	disc, prof := discoverAndProfile(maps)
	cm := buildColumnModel(disc, prof, nil)
	mb := newMemBackend(memRecords(maps), cm, prof)
	return mb, cm
}

func compilePlan(t *testing.T, f Filter, tr Transform, cm *ColumnModel) *CompiledPlan {
	t.Helper()
	p, err := CompilePlan(f, tr, cm)
	if err != nil {
		t.Fatalf("CompilePlan error = %v, want nil", err)
	}
	return p
}

// --- bitset --------------------------------------------------------------

func TestBitset_SetGetCount(t *testing.T) {
	bs := newBitset(130) // > 2 words (64 bits/word) to exercise word-boundary math
	if bs.Count() != 0 {
		t.Fatalf("Count() = %d, want 0 (nothing set)", bs.Count())
	}
	set := []int{0, 1, 63, 64, 65, 129}
	for _, i := range set {
		bs.Set(i)
	}
	if bs.Count() != int64(len(set)) {
		t.Fatalf("Count() = %d, want %d", bs.Count(), len(set))
	}
	for _, i := range set {
		if !bs.Get(i) {
			t.Fatalf("Get(%d) = false, want true (was Set)", i)
		}
	}
	for _, i := range []int{2, 62, 66, 128} {
		if bs.Get(i) {
			t.Fatalf("Get(%d) = true, want false (never Set)", i)
		}
	}
}

func TestBitset_GetOutOfRange(t *testing.T) {
	bs := newBitset(10)
	if bs.Get(-1) {
		t.Fatalf("Get(-1) = true, want false")
	}
	if bs.Get(10) {
		t.Fatalf("Get(10) = true, want false (n=10, valid range [0,10))")
	}
}

// --- memBackend: Columns/Profile/RowCount -------------------------------------

func TestMemBackend_ColumnsAndProfile(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	if mb.Columns() != cm {
		t.Fatalf("Columns() did not return the same *ColumnModel passed to newMemBackend")
	}
	if mb.Profile().Records != len(maps) {
		t.Fatalf("Profile().Records = %d, want %d", mb.Profile().Records, len(maps))
	}
}

func TestMemBackend_RowCount(t *testing.T) {
	maps := fixtureRecords()
	mb, _ := newTestMemBackend(t, maps)
	n, exact := mb.RowCount()
	if n != int64(len(maps)) || !exact {
		t.Fatalf("RowCount() = (%d, %v), want (%d, true)", n, exact, len(maps))
	}
}

// --- memBackend.Query: empty filter --------------------------------------

func TestMemBackend_Query_EmptyFilter_Window(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	p := compilePlan(t, Filter{}, Transform{}, cm)

	rs, err := mb.Query(context.Background(), p, Window{Offset: 2, Limit: 3}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if rs.Total != int64(len(maps)) || !rs.TotalExact {
		t.Fatalf("Total/TotalExact = %d/%v, want %d/true (empty filter matches everything)", rs.Total, rs.TotalExact, len(maps))
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

// --- memBackend.Query: filtered -------------------------------------------

func TestMemBackend_Query_Filtered(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, cm)

	rs, err := mb.Query(context.Background(), p, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	// even-index records: 0,2,4,6,8 -> 5 matches, out of 10.
	if rs.Total != 5 || !rs.TotalExact {
		t.Fatalf("Total/TotalExact = %d/%v, want 5/true", rs.Total, rs.TotalExact)
	}
	if len(rs.Rows) != 5 {
		t.Fatalf("len(Rows) = %d, want 5 (only matching rows)", len(rs.Rows))
	}

	nameIdx := cm.byPath["name"]
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	for i, row := range rs.Rows {
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q (match order must follow record order)", i, got, wantNames[i])
		}
	}
}

// --- memBackend.Query: bitset cache reuse across windows on the same filter --

func TestMemBackend_Query_ReusesCachedBitsetAcrossWindows(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, cm)

	if _, err := mb.Query(context.Background(), p, Window{Offset: 0, Limit: 2}, true); err != nil {
		t.Fatalf("first Query error = %v, want nil", err)
	}
	mb.mu.Lock()
	first, ok := mb.filterCache[p.FilterKey()]
	mb.mu.Unlock()
	if !ok {
		t.Fatalf("filterCache has no entry for FilterKey() after first Query")
	}

	// A different window over the SAME CompiledPlan (same FilterKey) must
	// reuse the cached bitset rather than recomputing it: the cache map must
	// still hold exactly the SAME *bitset value (pointer equality), and
	// still have exactly one entry.
	rs2, err := mb.Query(context.Background(), p, Window{Offset: 3, Limit: 2}, true)
	if err != nil {
		t.Fatalf("second Query error = %v, want nil", err)
	}
	mb.mu.Lock()
	second := mb.filterCache[p.FilterKey()]
	entries := len(mb.filterCache)
	mb.mu.Unlock()
	if first != second {
		t.Fatalf("filterCache bitset pointer changed across re-Query with the same FilterKey: cache was not reused")
	}
	if entries != 1 {
		t.Fatalf("filterCache has %d entries, want 1 (one filter, computed once)", entries)
	}

	// matches (even indices) = [0,2,4,6,8] -> "alice","carol","erin","grace","ivan"
	// window offset=3,limit=2 over the MATCH sequence -> matches[3:5] -> "grace","ivan"
	nameIdx := cm.byPath["name"]
	wantNames := []string{"grace", "ivan"}
	if len(rs2.Rows) != len(wantNames) {
		t.Fatalf("len(Rows) = %d, want %d", len(rs2.Rows), len(wantNames))
	}
	for i, row := range rs2.Rows {
		if got := row.Cells[nameIdx].Str; got != wantNames[i] {
			t.Fatalf("Rows[%d] name = %q, want %q", i, got, wantNames[i])
		}
	}
}

// --- memBackend.Query: window past end -> Truncated -----------------------

func TestMemBackend_Query_WindowPastEnd_Truncated(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	p := compilePlan(t, Filter{}, Transform{}, cm)

	rs, err := mb.Query(context.Background(), p, Window{Offset: 8, Limit: 5}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(rs.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2 (only 2 of 10 records left after offset 8)", len(rs.Rows))
	}
	if !rs.Truncated {
		t.Fatalf("Truncated = false, want true (fewer than Limit rows returned: EOF reached)")
	}

	rs2, err := mb.Query(context.Background(), p, Window{Offset: 20, Limit: 5}, true)
	if err != nil {
		t.Fatalf("Query (entirely past end) error = %v, want nil", err)
	}
	if len(rs2.Rows) != 0 {
		t.Fatalf("len(Rows) = %d, want 0 (offset beyond all records)", len(rs2.Rows))
	}
	if !rs2.Truncated {
		t.Fatalf("Truncated = false, want true (window entirely past end)")
	}
	if rs2.Total != int64(len(maps)) || !rs2.TotalExact {
		t.Fatalf("Total/TotalExact = %d/%v, want %d/true (Total independent of window position)", rs2.Total, rs2.TotalExact, len(maps))
	}
}

// --- memBackend.Query: rows align to CompiledTransform.Columns() ----------

func TestMemBackend_Query_RowsAlignToTransformColumns(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	tr := Transform{Select: []ColumnSpec{{Path: "name"}, {Path: "age"}}}
	p := compilePlan(t, Filter{}, tr, cm)

	rs, err := mb.Query(context.Background(), p, Window{Offset: 0, Limit: 4}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	wantCols := p.Transform.Columns()
	if len(rs.Columns) != len(wantCols) {
		t.Fatalf("len(RowSet.Columns) = %d, want %d", len(rs.Columns), len(wantCols))
	}
	for i, c := range wantCols {
		if rs.Columns[i] != c {
			t.Fatalf("RowSet.Columns[%d] = %#v, want %#v", i, rs.Columns[i], c)
		}
	}
	for i, row := range rs.Rows {
		if len(row.Cells) != len(wantCols) {
			t.Fatalf("Rows[%d].Cells len = %d, want %d (aligned to RowSet.Columns)", i, len(row.Cells), len(wantCols))
		}
	}
}

// --- memBackend.Query: filter + transform together ------------------------

func TestMemBackend_Query_FilterAndTransform_ProjectedSubset(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	tr := Transform{Select: []ColumnSpec{{Path: "name"}}}
	p := compilePlan(t, f, tr, cm)

	rs, err := mb.Query(context.Background(), p, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(rs.Columns) != 1 || rs.Columns[0].Name != "name" {
		t.Fatalf("Columns = %#v, want a single \"name\" column", rs.Columns)
	}
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	if len(rs.Rows) != len(wantNames) {
		t.Fatalf("len(Rows) = %d, want %d", len(rs.Rows), len(wantNames))
	}
	for i, row := range rs.Rows {
		if len(row.Cells) != 1 {
			t.Fatalf("Rows[%d].Cells len = %d, want 1 (Select projected a single column)", i, len(row.Cells))
		}
		if row.Cells[0].Str != wantNames[i] {
			t.Fatalf("Rows[%d].Cells[0] = %q, want %q", i, row.Cells[0].Str, wantNames[i])
		}
	}
}

// --- memBackend.Count ------------------------------------------------------

func TestMemBackend_Count_Exact(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	cf, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	total, exact, err := mb.Count(context.Background(), cf)
	if err != nil {
		t.Fatalf("Count error = %v, want nil", err)
	}
	if total != 5 || !exact {
		t.Fatalf("Count = (%d, %v), want (5, true)", total, exact)
	}

	emptyTotal, emptyExact, err := mb.Count(context.Background(), &CompiledFilter{})
	if err != nil {
		t.Fatalf("Count(empty filter) error = %v, want nil", err)
	}
	if emptyTotal != int64(len(maps)) || !emptyExact {
		t.Fatalf("Count(empty filter) = (%d, %v), want (%d, true)", emptyTotal, emptyExact, len(maps))
	}
}

func TestMemBackend_Count_ReusesBitsetForSameFilterPointer(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	cf, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	if _, _, err := mb.Count(context.Background(), cf); err != nil {
		t.Fatalf("first Count error = %v, want nil", err)
	}
	mb.mu.Lock()
	first, ok := mb.countCache[cf]
	mb.mu.Unlock()
	if !ok {
		t.Fatalf("countCache has no entry for cf after first Count")
	}

	if _, _, err := mb.Count(context.Background(), cf); err != nil {
		t.Fatalf("second Count error = %v, want nil", err)
	}
	mb.mu.Lock()
	second := mb.countCache[cf]
	mb.mu.Unlock()
	if first != second {
		t.Fatalf("countCache bitset pointer changed for the identical *CompiledFilter: cache was not reused")
	}
}

// --- memBackend.Export -------------------------------------------------------

// collectEncoder is a minimal test double for RowEncoder: it just appends
// every Encode-d Row, in call order, so a test can assert both the count and
// the exact projected content streamed by Export.
type collectEncoder struct {
	rows []Row
}

func (c *collectEncoder) Encode(row Row) error {
	c.rows = append(c.rows, row)
	return nil
}

func TestMemBackend_Export_StreamsAllMatchingProjectedRows(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	tr := Transform{Select: []ColumnSpec{{Path: "name"}}}
	p := compilePlan(t, f, tr, cm)

	enc := &collectEncoder{}
	n, err := mb.Export(context.Background(), p, enc)
	if err != nil {
		t.Fatalf("Export error = %v, want nil", err)
	}
	wantNames := []string{"alice", "carol", "erin", "grace", "ivan"}
	if n != int64(len(wantNames)) {
		t.Fatalf("Export rows = %d, want %d", n, len(wantNames))
	}
	if len(enc.rows) != len(wantNames) {
		t.Fatalf("collected rows = %d, want %d", len(enc.rows), len(wantNames))
	}
	for i, row := range enc.rows {
		if len(row.Cells) != 1 || row.Cells[0].Str != wantNames[i] {
			t.Fatalf("rows[%d] = %#v, want a single-cell row with name %q", i, row, wantNames[i])
		}
		if row.Index != int64(2*i) {
			t.Fatalf("rows[%d].Index = %d, want %d (absolute record ordinal, even indices)", i, row.Index, 2*i)
		}
	}
}

func TestMemBackend_Export_EmptyFilterStreamsEverything(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	p := compilePlan(t, Filter{}, Transform{}, cm)

	enc := &collectEncoder{}
	n, err := mb.Export(context.Background(), p, enc)
	if err != nil {
		t.Fatalf("Export error = %v, want nil", err)
	}
	if n != int64(len(maps)) || len(enc.rows) != len(maps) {
		t.Fatalf("Export rows = %d (%d collected), want %d", n, len(enc.rows), len(maps))
	}
}

// --- memBackend.Close --------------------------------------------------------

func TestMemBackend_Close(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	p := compilePlan(t, Filter{}, Transform{}, cm)

	if _, err := mb.Query(context.Background(), p, Window{Limit: 1}, true); err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(mb.filterCache) == 0 {
		t.Fatalf("fixture invalid: filterCache is empty after a Query")
	}

	if err := mb.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if len(mb.filterCache) != 0 {
		t.Fatalf("filterCache not cleared after Close: %d entries remain", len(mb.filterCache))
	}
	if len(mb.countCache) != 0 {
		t.Fatalf("countCache not cleared after Close: %d entries remain", len(mb.countCache))
	}
}

// --- memBackend: cancellation ------------------------------------------------

// TestMemBackend_Query_CancelledContext_NotCached exercises the Important
// review finding: an already-cancelled ctx must abort the O(records)
// match-bitset scan behind Query's cache-miss path (rather than running it to
// completion, uncancellable, as before the fix), the scan's error must
// surface as ctx.Err() through Query, and -- critically -- nothing may be
// cached for the aborted compute: a later Query with a live ctx over the same
// filter must still (re-)compute and return correct results.
func TestMemBackend_Query_CancelledContext_NotCached(t *testing.T) {
	maps := manyRecords(20000)
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, cm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rs, err := mb.Query(ctx, p, Window{Offset: 0, Limit: 10}, true)
	if err == nil {
		t.Fatalf("Query(cancelled ctx) error = nil, want non-nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Query(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
	if len(rs.Rows) != 0 {
		t.Fatalf("Query(cancelled ctx) returned %d rows, want a zero-value RowSet on error", len(rs.Rows))
	}

	mb.mu.Lock()
	_, cached := mb.filterCache[p.FilterKey()]
	entries := len(mb.filterCache)
	mb.mu.Unlock()
	if cached || entries != 0 {
		t.Fatalf("filterCache has %d entries (cached=%v) after a cancelled Query, want 0/false: a cancelled compute must never be cached", entries, cached)
	}

	// A subsequent Query with a live ctx and the SAME filter/plan must still
	// work (not poisoned by the earlier cancellation) and return correct,
	// complete results.
	rs2, err := mb.Query(context.Background(), p, Window{Offset: 0, Limit: 10000}, true)
	if err != nil {
		t.Fatalf("follow-up Query error = %v, want nil", err)
	}
	if rs2.Total != int64(len(maps))/2 || !rs2.TotalExact {
		t.Fatalf("follow-up Query Total/TotalExact = %d/%v, want %d/true", rs2.Total, rs2.TotalExact, len(maps)/2)
	}

	mb.mu.Lock()
	_, cachedAfter := mb.filterCache[p.FilterKey()]
	mb.mu.Unlock()
	if !cachedAfter {
		t.Fatalf("filterCache has no entry for FilterKey() after a successful follow-up Query")
	}
}

// TestMemBackend_Count_CancelledContext_NotCached is the Count-side twin of
// TestMemBackend_Query_CancelledContext_NotCached: Count's cache-miss path
// goes through the same computeMatchBitset scan via countCache instead of
// filterCache, and must be independently cancellable and independently
// non-caching on cancellation.
func TestMemBackend_Count_CancelledContext_NotCached(t *testing.T) {
	maps := manyRecords(20000)
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	cf, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := mb.Count(ctx, cf); err == nil {
		t.Fatalf("Count(cancelled ctx) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Count(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}

	mb.mu.Lock()
	_, cached := mb.countCache[cf]
	mb.mu.Unlock()
	if cached {
		t.Fatalf("countCache has an entry for cf after a cancelled Count: cancelled compute must never be cached")
	}

	total, exact, err := mb.Count(context.Background(), cf)
	if err != nil {
		t.Fatalf("follow-up Count error = %v, want nil", err)
	}
	if total != int64(len(maps))/2 || !exact {
		t.Fatalf("follow-up Count = (%d, %v), want (%d, true)", total, exact, len(maps)/2)
	}
}

// --- memBackend: different filters do not contaminate each other's results --

// TestMemBackend_Query_DifferentFilters_NoContamination covers a previously
// uncovered gap: two logically different filters queried against the SAME
// memBackend instance must each return their own correct, uncontaminated
// results (not the other filter's matches, and not a merge of both), and the
// filterCache must end up with exactly one entry per distinct FilterKey --
// here, two.
func TestMemBackend_Query_DifferentFilters_NoContamination(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	nameIdx := cm.byPath["name"]

	fEven := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	pEven := compilePlan(t, fEven, Transform{}, cm)

	fCarol := Filter{Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "carol"}}}}
	pCarol := compilePlan(t, fCarol, Transform{}, cm)

	rsEven, err := mb.Query(context.Background(), pEven, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query(even) error = %v, want nil", err)
	}
	rsCarol, err := mb.Query(context.Background(), pCarol, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query(carol) error = %v, want nil", err)
	}

	wantEven := []string{"alice", "carol", "erin", "grace", "ivan"}
	if rsEven.Total != int64(len(wantEven)) || !rsEven.TotalExact {
		t.Fatalf("Query(even) Total/TotalExact = %d/%v, want %d/true", rsEven.Total, rsEven.TotalExact, len(wantEven))
	}
	if len(rsEven.Rows) != len(wantEven) {
		t.Fatalf("Query(even) len(Rows) = %d, want %d", len(rsEven.Rows), len(wantEven))
	}
	for i, row := range rsEven.Rows {
		if got := row.Cells[nameIdx].Str; got != wantEven[i] {
			t.Fatalf("Query(even) Rows[%d] name = %q, want %q", i, got, wantEven[i])
		}
	}

	if rsCarol.Total != 1 || !rsCarol.TotalExact {
		t.Fatalf("Query(carol) Total/TotalExact = %d/%v, want 1/true", rsCarol.Total, rsCarol.TotalExact)
	}
	if len(rsCarol.Rows) != 1 || rsCarol.Rows[0].Cells[nameIdx].Str != "carol" {
		t.Fatalf("Query(carol) Rows = %#v, want a single row for %q", rsCarol.Rows, "carol")
	}

	// Re-running the first filter must still be unaffected by having computed
	// the second in between (no shared/aliased bitset state).
	rsEvenAgain, err := mb.Query(context.Background(), pEven, Window{Offset: 0, Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query(even) re-run error = %v, want nil", err)
	}
	if rsEvenAgain.Total != int64(len(wantEven)) {
		t.Fatalf("Query(even) re-run Total = %d, want %d (unaffected by intervening different-filter Query)", rsEvenAgain.Total, len(wantEven))
	}

	mb.mu.Lock()
	entries := len(mb.filterCache)
	mb.mu.Unlock()
	if entries != 2 {
		t.Fatalf("filterCache has %d entries, want 2 (two distinct filters, each cached once)", entries)
	}
}

// --- memBackend.Count: nil filter (match-all) -------------------------------

// TestMemBackend_Count_NilFilterMatchesAll covers the nil-*CompiledFilter
// match-all path through Count directly (Backend.Count's f may be nil):
// CompiledFilter.Match treats a nil receiver as match-all, so Count(ctx, nil)
// must return the exact, full record count.
func TestMemBackend_Count_NilFilterMatchesAll(t *testing.T) {
	maps := fixtureRecords()
	mb, _ := newTestMemBackend(t, maps)

	total, exact, err := mb.Count(context.Background(), nil)
	if err != nil {
		t.Fatalf("Count(nil) error = %v, want nil", err)
	}
	if total != int64(len(maps)) || !exact {
		t.Fatalf("Count(nil) = (%d, %v), want (%d, true)", total, exact, len(maps))
	}
}
