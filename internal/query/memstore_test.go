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
	n, exact := mb.RowCount(context.Background())
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
	el, ok := mb.matchCache[p.FilterKey()]
	mb.mu.Unlock()
	if !ok {
		t.Fatalf("matchCache has no entry for FilterKey() after first Query")
	}
	first := el.Value.(*matchEntry).bs

	// A different window over the SAME CompiledPlan (same FilterKey) must
	// reuse the cached bitset rather than recomputing it: the cache map must
	// still hold exactly the SAME *bitset value (pointer equality), and
	// still have exactly one entry.
	rs2, err := mb.Query(context.Background(), p, Window{Offset: 3, Limit: 2}, true)
	if err != nil {
		t.Fatalf("second Query error = %v, want nil", err)
	}
	mb.mu.Lock()
	el2 := mb.matchCache[p.FilterKey()]
	second := el2.Value.(*matchEntry).bs
	entries := len(mb.matchCache)
	mb.mu.Unlock()
	if first != second {
		t.Fatalf("matchCache bitset pointer changed across re-Query with the same FilterKey: cache was not reused")
	}
	if entries != 1 {
		t.Fatalf("matchCache has %d entries, want 1 (one filter, computed once)", entries)
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

// TestMemBackend_Count_ReusesBitsetAcrossSeparateCompiles is a rewrite of the
// former TestMemBackend_Count_ReusesBitsetForSameFilterPointer, which pinned
// the defective pointer-identity cache semantics (a fresh *CompiledFilter per
// CompileFilter call could never hit another call's cache entry). The
// matchCache is now keyed by CompiledFilter.Key(), a content hash: two
// independently compiled *CompiledFilter values for the same logical Filter
// must share one cache entry.
func TestMemBackend_Count_ReusesBitsetAcrossSeparateCompiles(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}

	cf1, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter(cf1) error = %v, want nil", err)
	}
	cf2, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter(cf2) error = %v, want nil", err)
	}
	if cf1 == cf2 {
		t.Fatalf("fixture invalid: cf1 and cf2 are the same *CompiledFilter pointer")
	}

	total1, exact1, err := mb.Count(context.Background(), cf1)
	if err != nil {
		t.Fatalf("Count(cf1) error = %v, want nil", err)
	}
	total2, exact2, err := mb.Count(context.Background(), cf2)
	if err != nil {
		t.Fatalf("Count(cf2) error = %v, want nil", err)
	}
	if total1 != total2 || !exact1 || !exact2 {
		t.Fatalf("Count(cf1) = (%d,%v), Count(cf2) = (%d,%v), want equal totals, both exact", total1, exact1, total2, exact2)
	}

	mb.mu.Lock()
	entries := len(mb.matchCache)
	mb.mu.Unlock()
	if entries != 1 {
		t.Fatalf("matchCache has %d entries after Count with two separately-compiled CompiledFilters of the same logical Filter, want 1", entries)
	}
}

// TestMemBackend_QueryAndCountShareOneCacheEntry asserts the other half of
// the same fix: Query (keyed by CompiledPlan.FilterKey(), now filter-only)
// and Count (keyed by CompiledFilter.Key()) must land in the SAME cache
// entry when they run over the same logical Filter, not two separate ones.
func TestMemBackend_QueryAndCountShareOneCacheEntry(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	p := compilePlan(t, f, Transform{}, cm)

	if _, err := mb.Query(context.Background(), p, Window{Offset: 0, Limit: 2}, true); err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}

	cf, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}
	if _, _, err := mb.Count(context.Background(), cf); err != nil {
		t.Fatalf("Count error = %v, want nil", err)
	}

	mb.mu.Lock()
	entries := len(mb.matchCache)
	mb.mu.Unlock()
	if entries != 1 {
		t.Fatalf("matchCache has %d entries after Query then Count over the same logical Filter, want 1 (Query and Count share the filter-only key)", entries)
	}
}

// --- memBackend: matchCache LRU eviction ------------------------------------

// TestMemBackend_MatchCache_EvictsLeastRecentlyUsed drives more distinct
// filters through Count than maxMatchCacheEntries allows and asserts the
// cache stays capped, evicts the least-recently-used (oldest) entry first,
// and that re-Counting an evicted filter is a cache miss (correct answer,
// recomputed) rather than a wrong answer.
func TestMemBackend_MatchCache_EvictsLeastRecentlyUsed(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)

	n := maxMatchCacheEntries + 3
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		age := float64(i)
		if i == 0 {
			// The i==0 filter is the one that will be LRU-evicted first (see
			// below). Give it a DISTINCTIVE, NONZERO expected count: every
			// other candidate age here (1..n-1) matches zero fixtureRecords
			// (ages run 20..29), so re-Counting one of those after eviction
			// would return 0 whether it was correctly recomputed OR wrongly
			// served from some other (also-zero) cached filter's bitset --
			// the assertion couldn't tell the difference. age 25 is
			// fixtureRecords' "frank" (index 5): exactly one match.
			age = 25
		}
		f := Filter{Conditions: []Condition{{Path: "age", Op: OpEq, Value: Value{Kind: ValNumber, Num: age}}}}
		cf, err := CompileFilter(f, cm)
		if err != nil {
			t.Fatalf("CompileFilter(%d) error = %v, want nil", i, err)
		}
		keys[i] = cf.Key()
		if _, _, err := mb.Count(context.Background(), cf); err != nil {
			t.Fatalf("Count(%d) error = %v, want nil", i, err)
		}
	}

	mb.mu.Lock()
	entries := len(mb.matchCache)
	_, firstStillPresent := mb.matchCache[keys[0]]
	mb.mu.Unlock()
	if entries != maxMatchCacheEntries {
		t.Fatalf("len(matchCache) = %d, want %d (LRU cap)", entries, maxMatchCacheEntries)
	}
	if firstStillPresent {
		t.Fatalf("matchCache still has the first (least-recently-used) key after exceeding the cap")
	}

	mb.mu.Lock()
	for i := n - maxMatchCacheEntries; i < n; i++ {
		if _, ok := mb.matchCache[keys[i]]; !ok {
			mb.mu.Unlock()
			t.Fatalf("matchCache missing key for filter %d, want the most recent %d keys all present", i, maxMatchCacheEntries)
		}
	}
	mb.mu.Unlock()

	// Re-Count the evicted (first) filter: a cache miss, never a wrong answer.
	// age==25 matches exactly one fixture record ("frank"), a nonzero count no
	// other filter in this test also produces -- so getting 1 back can only be
	// explained by an actual recompute of THIS filter, not a stale/wrong hit
	// off some other cached (and, for every other filter here, zero-count)
	// bitset.
	fEvicted := Filter{Conditions: []Condition{{Path: "age", Op: OpEq, Value: Value{Kind: ValNumber, Num: 25}}}}
	cfEvicted, err := CompileFilter(fEvicted, cm)
	if err != nil {
		t.Fatalf("CompileFilter(evicted) error = %v, want nil", err)
	}
	if cfEvicted.Key() != keys[0] {
		t.Fatalf("fixture invalid: recompiled evicted filter key %q != original %q", cfEvicted.Key(), keys[0])
	}
	total, exact, err := mb.Count(context.Background(), cfEvicted)
	if err != nil {
		t.Fatalf("Count(evicted) error = %v, want nil", err)
	}
	if !exact {
		t.Fatalf("Count(evicted) exact = false, want true")
	}
	if total != 1 {
		t.Fatalf("Count(evicted) = %d, want 1 (fixtureRecords age 25 -> exactly \"frank\")", total)
	}
}

// TestMemBackend_MatchCache_TouchOnHitPreventsEviction fills the cache, then
// re-Counts the oldest key (a cache HIT, which must move it to the front of
// the LRU list), then adds one more distinct filter: the touched key must
// survive, and the entry that was actually second-oldest is evicted instead.
func TestMemBackend_MatchCache_TouchOnHitPreventsEviction(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)

	countFilter := func(i int) string {
		f := Filter{Conditions: []Condition{{Path: "age", Op: OpEq, Value: Value{Kind: ValNumber, Num: float64(i)}}}}
		cf, err := CompileFilter(f, cm)
		if err != nil {
			t.Fatalf("CompileFilter(%d) error = %v, want nil", i, err)
		}
		if _, _, err := mb.Count(context.Background(), cf); err != nil {
			t.Fatalf("Count(%d) error = %v, want nil", i, err)
		}
		return cf.Key()
	}

	keys := make([]string, maxMatchCacheEntries)
	for i := 0; i < maxMatchCacheEntries; i++ {
		keys[i] = countFilter(i)
	}
	// Cache is now full: keys[0] (oldest/LRU) .. keys[maxMatchCacheEntries-1] (newest).

	// Touch the oldest key: re-Count filter 0, making it the most recently used.
	touched := countFilter(0)
	if touched != keys[0] {
		t.Fatalf("fixture invalid: re-compiled filter 0 key changed: %q != %q", touched, keys[0])
	}

	// Add one more distinct filter. Without the touch, keys[0] would be the
	// LRU victim; keys[1] is now the actual least-recently-used entry.
	countFilter(maxMatchCacheEntries)

	mb.mu.Lock()
	_, key0Present := mb.matchCache[keys[0]]
	_, key1Present := mb.matchCache[keys[1]]
	entries := len(mb.matchCache)
	mb.mu.Unlock()

	if !key0Present {
		t.Fatalf("matchCache evicted the touched (re-Counted) key, want it retained as most-recently-used")
	}
	if key1Present {
		t.Fatalf("matchCache retained key[1], want it evicted as the new least-recently-used")
	}
	if entries != maxMatchCacheEntries {
		t.Fatalf("len(matchCache) = %d, want %d", entries, maxMatchCacheEntries)
	}
}

// cancelAfterNCalls wraps real (typically a genuine compiled predicate
// extracted from CompileFilter's result) so the returned predicate still
// applies real's logic to every record it is asked about, but ALSO calls
// cancel exactly once -- the Nth time it is invoked. Wiring this in as a
// hand-built *CompiledFilter's pred (constructible directly here since this
// test file is in package query, which can see CompiledFilter's unexported
// fields) lets a test land ctx cancellation deterministically in the middle
// of computeMatchBitset's per-record scan: no timer, no goroutine race, no
// "cancel before the call is even made" shortcut that the top-level ctx.Err()
// guards in Query/Count would swallow before matchBitsetFor is ever reached.
// This is the same "no timing races" discipline sqlbackend_test.go's
// countCancelConn applies on the sqlBackend side (see the comment there).
func cancelAfterNCalls(real func(rec any) bool, n int, cancel context.CancelFunc) func(rec any) bool {
	calls := 0
	return func(rec any) bool {
		calls++
		if calls == n {
			cancel()
		}
		return real(rec)
	}
}

// TestMemBackend_MatchCache_CancelledComputeNotCached cancels ctx from
// INSIDE computeMatchBitset's scan (via cancelAfterNCalls, wired in as the
// compiled predicate) so the cancellation genuinely lands mid-scan, at the
// cancelCheckStride check (memstore.go) -- not before Query is ever called.
// A pre-cancelled ctx would be caught by Query's top-level ctx.Err() guard
// before matchBitsetFor/computeMatchBitset run at all, proving nothing about
// "never cache a cancelled compute" (matchBitsetFor's real invariant under
// test here).
func TestMemBackend_MatchCache_CancelledComputeNotCached(t *testing.T) {
	maps := manyRecords(20000) // >> cancelCheckStride (4096): plenty of room for a mid-scan cancel to land well before the scan would otherwise finish
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	realCF, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// 100 < cancelCheckStride (4096): the cancel fires partway through the
	// FIRST stride, so the check at the i=4096 boundary is computeMatchBitset's
	// first opportunity to observe it -- genuinely mid-scan.
	hooked := &CompiledFilter{pred: cancelAfterNCalls(realCF.pred, 100, cancel), key: realCF.Key()}
	p := &CompiledPlan{Filter: hooked, Transform: &CompiledTransform{}, Columns: cm, filterKey: hooked.Key()}

	if _, err := mb.Query(ctx, p, Window{Offset: 0, Limit: 10}, true); err == nil {
		t.Fatalf("Query(cancelled mid-scan) error = nil, want non-nil")
	}

	mb.mu.Lock()
	entries := len(mb.matchCache)
	mb.mu.Unlock()
	if entries != 0 {
		t.Fatalf("matchCache has %d entries after a cancelled compute, want 0: a cancelled compute must never be cached", entries)
	}
}

// --- memBackend.Export -------------------------------------------------------

// collectEncoder is a minimal test double for RowEncoder: it appends every
// Encode-d row, in call order, so a test can assert both the count and the
// exact projected values streamed by Export.
//
// It COPIES values on the way in, which is not politeness but the interface's
// contract (backend.go): Export projects every record into ONE reused scratch
// buffer, so an encoder that retained the slice would end up with every
// collected row aliasing the last record's values.
type collectEncoder struct {
	rows []collectedRow
}

type collectedRow struct {
	index  int64
	values []any
}

func (c *collectEncoder) Encode(index int64, values []any) error {
	c.rows = append(c.rows, collectedRow{index: index, values: append([]any(nil), values...)})
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
		if len(row.values) != 1 || row.values[0] != any(wantNames[i]) {
			t.Fatalf("rows[%d] = %#v, want a single-value row with name %q", i, row, wantNames[i])
		}
		if row.index != int64(2*i) {
			t.Fatalf("rows[%d].index = %d, want %d (absolute record ordinal, even indices)", i, row.index, 2*i)
		}
	}
}

// TestMemBackend_Export_ContainerValuesAreNotTruncated is E4's reason for
// streaming raw values instead of Rows: toCell caps a container's compact-JSON
// preview at previewCap (200 bytes) and sets HasMore, which is right for a
// table cell and silently lossy in an exported file.
//
// Mutation that must break it: in ProjectValues (transform.go), write
// toCell(values[0]).Str instead of the raw value -- same signature, same call
// sites, still compiles -- and the exported value arrives as a 200-byte
// preview string rather than the whole object.
func TestMemBackend_Export_ContainerValuesAreNotTruncated(t *testing.T) {
	rec, meta := bigNestedRecord()
	mb, cm := newTestMemBackend(t, []map[string]any{rec})
	p := compilePlan(t, Filter{}, Transform{Select: []ColumnSpec{{Path: "meta", As: "meta"}}}, cm)

	enc := &collectEncoder{}
	n, err := mb.Export(context.Background(), p, enc)
	if err != nil {
		t.Fatalf("Export error = %v, want nil", err)
	}
	if n != 1 || len(enc.rows) != 1 {
		t.Fatalf("Export rows = %d (%d collected), want 1", n, len(enc.rows))
	}
	got, ok := enc.rows[0].values[0].(map[string]any)
	if !ok {
		t.Fatalf("exported value = %#v (%T), want the raw map[string]any -- a string here means the export went through a Cell preview", enc.rows[0].values[0], enc.rows[0].values[0])
	}
	if len(got) != len(meta) {
		t.Fatalf("exported object has %d keys, want %d (the value was truncated on its way out)", len(got), len(meta))
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

// TestMemBackend_Close_ClearsMatchCache is a rewrite of the former
// TestMemBackend_Close, which asserted the separate filterCache/countCache
// were both cleared: there is now a single matchCache to clear.
func TestMemBackend_Close_ClearsMatchCache(t *testing.T) {
	maps := fixtureRecords()
	mb, cm := newTestMemBackend(t, maps)
	p := compilePlan(t, Filter{}, Transform{}, cm)

	if _, err := mb.Query(context.Background(), p, Window{Limit: 1}, true); err != nil {
		t.Fatalf("Query error = %v, want nil", err)
	}
	if len(mb.matchCache) == 0 {
		t.Fatalf("fixture invalid: matchCache is empty after a Query")
	}

	if err := mb.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if len(mb.matchCache) != 0 {
		t.Fatalf("matchCache not cleared after Close: %d entries remain", len(mb.matchCache))
	}
}

// --- memBackend: cancellation ------------------------------------------------

// TestMemBackend_Query_CancelledContext_NotCached exercises the Important
// review finding: a ctx cancelled mid-scan (via cancelAfterNCalls, landing
// the cancel inside computeMatchBitset's loop -- see
// TestMemBackend_MatchCache_CancelledComputeNotCached's doc comment for why a
// PRE-cancelled ctx would prove nothing here) must abort the O(records)
// match-bitset scan behind Query's cache-miss path (rather than running it to
// completion, uncancellable, as before the fix), the scan's error must
// surface as ctx.Err() through Query, and -- critically -- nothing may be
// cached for the aborted compute: a later Query with a live ctx over the same
// filter must still (re-)compute and return correct results.
func TestMemBackend_Query_CancelledContext_NotCached(t *testing.T) {
	maps := manyRecords(20000)
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	realCF, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	hooked := &CompiledFilter{pred: cancelAfterNCalls(realCF.pred, 100, cancel), key: realCF.Key()}
	p := &CompiledPlan{Filter: hooked, Transform: &CompiledTransform{}, Columns: cm, filterKey: hooked.Key()}

	rs, err := mb.Query(ctx, p, Window{Offset: 0, Limit: 10}, true)
	if err == nil {
		t.Fatalf("Query(cancelled mid-scan) error = nil, want non-nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Query(cancelled mid-scan) error = %v, want errors.Is(err, context.Canceled)", err)
	}
	if len(rs.Rows) != 0 {
		t.Fatalf("Query(cancelled mid-scan) returned %d rows, want a zero-value RowSet on error", len(rs.Rows))
	}

	mb.mu.Lock()
	_, cached := mb.matchCache[p.FilterKey()]
	entries := len(mb.matchCache)
	mb.mu.Unlock()
	if cached || entries != 0 {
		t.Fatalf("matchCache has %d entries (cached=%v) after a cancelled Query, want 0/false: a cancelled compute must never be cached", entries, cached)
	}

	// A follow-up Query over the SAME logical Filter (a fresh, unhooked
	// CompiledPlan sharing the cancelled plan's FilterKey) with a live ctx
	// must still work (not poisoned by the earlier cancellation) and return
	// correct, complete results.
	p2 := compilePlan(t, f, Transform{}, cm)
	if p2.FilterKey() != p.FilterKey() {
		t.Fatalf("fixture invalid: follow-up plan's FilterKey() %q != cancelled plan's %q", p2.FilterKey(), p.FilterKey())
	}
	rs2, err := mb.Query(context.Background(), p2, Window{Offset: 0, Limit: 10000}, true)
	if err != nil {
		t.Fatalf("follow-up Query error = %v, want nil", err)
	}
	if rs2.Total != int64(len(maps))/2 || !rs2.TotalExact {
		t.Fatalf("follow-up Query Total/TotalExact = %d/%v, want %d/true", rs2.Total, rs2.TotalExact, len(maps)/2)
	}

	mb.mu.Lock()
	_, cachedAfter := mb.matchCache[p2.FilterKey()]
	mb.mu.Unlock()
	if !cachedAfter {
		t.Fatalf("matchCache has no entry for FilterKey() after a successful follow-up Query")
	}
}

// TestMemBackend_Count_CancelledContext_NotCached is the Count-side twin of
// TestMemBackend_Query_CancelledContext_NotCached: Count's cache-miss path
// goes through the same computeMatchBitset scan via the same shared
// matchCache, and must be independently cancellable and independently
// non-caching on a cancellation landing mid-scan (via cancelAfterNCalls; see
// TestMemBackend_MatchCache_CancelledComputeNotCached's doc comment for why a
// PRE-cancelled ctx would not exercise this).
func TestMemBackend_Count_CancelledContext_NotCached(t *testing.T) {
	maps := manyRecords(20000)
	mb, cm := newTestMemBackend(t, maps)
	f := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	realCF, err := CompileFilter(f, cm)
	if err != nil {
		t.Fatalf("CompileFilter error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	hooked := &CompiledFilter{pred: cancelAfterNCalls(realCF.pred, 100, cancel), key: realCF.Key()}

	if _, _, err := mb.Count(ctx, hooked); err == nil {
		t.Fatalf("Count(cancelled mid-scan) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Count(cancelled mid-scan) error = %v, want errors.Is(err, context.Canceled)", err)
	}

	mb.mu.Lock()
	_, cached := mb.matchCache[hooked.Key()]
	mb.mu.Unlock()
	if cached {
		t.Fatalf("matchCache has an entry for cf.Key() after a cancelled Count: cancelled compute must never be cached")
	}

	// A follow-up Count over the SAME logical Filter (the original, unhooked
	// realCF, sharing hooked's key) with a live ctx must still work and
	// return correct, complete results.
	total, exact, err := mb.Count(context.Background(), realCF)
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
// matchCache must end up with exactly one entry per distinct FilterKey --
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
	entries := len(mb.matchCache)
	mb.mu.Unlock()
	if entries != 2 {
		t.Fatalf("matchCache has %d entries, want 2 (two distinct filters, each cached once)", entries)
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

func TestMemBackend_SortsByColumnKeepingAbsoluteIndex(t *testing.T) {
	// records: n = [3,1,2] at absolute indices [0,1,2].
	eng, handle := openMemFixtureForSort(t)
	rs, err := eng.QueryRows(context.Background(), QueryRequest{Handle: handle, Limit: 100, Sort: SortSpec{Path: "n"}})
	if err != nil {
		t.Fatal(err)
	}
	// Ascending by n -> display order is n=1,2,3 -> absolute indices 1,2,0. The
	// Row.Index MUST be the absolute ordinal, NOT 0,1,2 (the display rank).
	got := []int64{rs.Rows[0].Index, rs.Rows[1].Index, rs.Rows[2].Index}
	if got[0] != 1 || got[1] != 2 || got[2] != 0 {
		t.Fatalf("sorted Row.Index = %v, want [1 2 0] (absolute ordinals in sorted order)", got)
	}
}

func TestMemBackend_NoSortIsSourceOrder(t *testing.T) {
	eng, handle := openMemFixtureForSort(t)
	rs, err := eng.QueryRows(context.Background(), QueryRequest{Handle: handle, Limit: 100, Sort: SortSpec{Path: ""}})
	if err != nil {
		t.Fatal(err)
	}
	got := []int64{rs.Rows[0].Index, rs.Rows[1].Index, rs.Rows[2].Index}
	if got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("unsorted Row.Index = %v, want [0 1 2] (source order)", got)
	}
}

func TestMemBackend_SortOffsetPastEndIsEmpty(t *testing.T) {
	eng, handle := openMemFixtureForSort(t)
	rs, err := eng.QueryRows(context.Background(), QueryRequest{Handle: handle, Offset: 1000, Limit: 10, Sort: SortSpec{Path: "n"}})
	if err != nil {
		t.Fatalf("an offset past the end must not panic: %v", err)
	}
	if len(rs.Rows) != 0 {
		t.Fatalf("offset past end returned %d rows, want 0", len(rs.Rows))
	}
}
