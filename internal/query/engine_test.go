package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers"
)

// evenFilter is the shared "even"==true bool Condition used across these
// tests (see fixtureRecords/manyRecords in memstore_test.go: both fixtures
// carry an index-parity "even" bool field).
func evenFilter() Filter {
	return Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
}

// notEvenFilter is evenFilter's complement: the same "even" bool path with
// the opposite operand, so its CompiledFilter.Key() always differs from
// evenFilter()'s (see TestEngine_Cancel_SupersedesDuplicateRequestID's CQ-3
// fix, which relies on that to keep two concurrent requests from ever
// colliding on the same memBackend.matchCache entry).
func notEvenFilter() Filter {
	return Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: false}}}}
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
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
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

	rs, err := e.QueryRows(context.Background(), QueryRequest{Handle: res.Handle, Filter: evenFilter(), Offset: 0, Limit: 10, WantTotal: true})
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

	memRes, err := e.OpenSource(context.Background(), OpenRequest{Path: path}) // default (512 MiB) budget
	if err != nil {
		t.Fatalf("OpenSource(default budget) error = %v, want nil", err)
	}
	if memRes.Tier != "memory" {
		t.Fatalf("OpenSource(default budget) Tier = %q, want \"memory\"", memRes.Tier)
	}

	rescanRes, err := e.OpenSource(context.Background(), OpenRequest{Path: path, BudgetMB: 1})
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
	memRS, err := e.QueryRows(context.Background(), req(memRes.Handle))
	if err != nil {
		t.Fatalf("QueryRows(memory) error = %v, want nil", err)
	}
	rescanRS, err := e.QueryRows(context.Background(), req(rescanRes.Handle))
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

// --- OpenSource: rescan tier's streaming-mode warning matches byte-for-byte -

// TestEngine_OpenSource_RescanTier_StreamingWarningExact (A4) pins the exact
// warning string engine.go emits for a rescan-tier OpenSource, byte for byte
// -- including the em dash (U+2014), not a hyphen or en dash. Several earlier
// task briefs asserted a Go test already covered this ("a Go test matches
// this byte-for-byte"); that claim was checked during T9 and found false --
// grep over every *_test.go in this package finds no exact-string assertion
// anywhere, only TestEngine_OpenSource_BudgetDowngrade_CrossTierInvariant's
// `len(rescanRes.Warnings) == 0` check, which would pass just as happily if
// the string were reworded, mis-punctuated, or had its dash swapped for a
// plain "-". The frontend (StatusBar.svelte) renders this string verbatim
// with no reformatting, so a silent wording drift here would ship straight
// to the status bar.
func TestEngine_OpenSource_RescanTier_StreamingWarningExact(t *testing.T) {
	maps := manyRecords(20000) // large enough that a 1 MiB budget forces the rescan tier
	path := writeNDJSONFile(t, maps)

	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path, BudgetMB: 1})
	if err != nil {
		t.Fatalf("OpenSource(BudgetMB=1) error = %v, want nil", err)
	}
	if res.Tier != "rescan" {
		t.Fatalf("Tier = %q, want \"rescan\" (20000 records must exceed a 1 MiB budget)", res.Tier)
	}

	const want = "large file — streaming mode (totals are estimates)" // U+2014 EM DASH, not '-' or U+2013
	if len(res.Warnings) != 1 {
		t.Fatalf("Warnings = %#v, want exactly one warning", res.Warnings)
	}
	if res.Warnings[0] != want {
		t.Fatalf("Warnings[0] = %q, want %q byte-for-byte", res.Warnings[0], want)
	}
}

// --- QueryRows after CloseSource: clean error, no panic ---------------------

func TestEngine_QueryRows_AfterCloseSource_ErrorsCleanly(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)

	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}
	if err := e.CloseSource(res.Handle); err != nil {
		t.Fatalf("CloseSource error = %v, want nil", err)
	}

	if _, err := e.QueryRows(context.Background(), QueryRequest{Handle: res.Handle, Limit: 10}); err == nil {
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
		if _, err := e.OpenSource(context.Background(), OpenRequest{Path: p}); err == nil {
			t.Fatalf("OpenSource(Path=%q) error = nil, want non-nil (spec §2: stdin/empty rejected)", p)
		}
	}
}

// --- OpenSource: an empty/invalid file errors for both formats -------------
//
// Both FormatSQLite (newSQLBackend, sqlbackend.go, Task 7) and FormatParquet
// (newParquetBackend, parquetbackend.go, Task 8) are real backends now; a
// real fixture for each is covered elsewhere
// (TestEngine_OpenSource_SQLite_TierAndColumns/TestCrossBackend_SQLBackendMatchesMemBackend
// in sqlbackend_test.go, TestEngine_OpenSource_Parquet_TierAndColumns/the
// cross-backend tests in parquetbackend_test.go). This test just confirms an
// empty/invalid file still errors cleanly for both formats (not a valid
// SQLite database / not a valid Parquet file -- Parquet's footer alone
// requires more bytes than an empty file has) rather than panicking.

// --- adaptProfile: non-finite Min/Max sanitized at the DTO boundary (I1) ----
//
// internal/profile's accumulator already excludes non-finite values from
// Min/Max when it builds a FieldProfile normally (see accumulator.go's
// AddValue: a NaN/Inf observation is counted but skipped for min/max), so
// this is defense-in-depth at the query/DTO boundary (adaptProfile) for any
// FieldProfile assembled some other way: a non-finite Min/Max must never
// reach encoding/json.Marshal, since Marshal errors on a non-finite float64
// -- pre-fix, this would fail OpenResult's marshal and the file wouldn't
// open at all.

func TestAdaptProfile_NonFiniteMinMax_Sanitized(t *testing.T) {
	inf := math.Inf(1)
	nan := math.NaN()
	fine := 42.0
	pr := profile.ProfileResult{
		Records: 3,
		Fields: []profile.FieldProfile{
			{Path: "bad", Min: &inf, Max: &nan},
			{Path: "good", Min: &fine, Max: &fine},
			{Path: "nilptr", Min: nil, Max: nil},
		},
	}

	dto := adaptProfile(pr)
	if len(dto.Fields) != 3 {
		t.Fatalf("len(Fields) = %d, want 3", len(dto.Fields))
	}
	if dto.Fields[0].Min != nil || dto.Fields[0].Max != nil {
		t.Fatalf("Fields[0] (non-finite) Min/Max = %v/%v, want nil/nil (omitted)", dto.Fields[0].Min, dto.Fields[0].Max)
	}
	if dto.Fields[1].Min == nil || *dto.Fields[1].Min != fine || dto.Fields[1].Max == nil || *dto.Fields[1].Max != fine {
		t.Fatalf("Fields[1] (finite) Min/Max = %v/%v, want %v/%v unchanged", dto.Fields[1].Min, dto.Fields[1].Max, fine, fine)
	}
	if dto.Fields[2].Min != nil || dto.Fields[2].Max != nil {
		t.Fatalf("Fields[2] (already nil) Min/Max = %v/%v, want nil/nil unchanged", dto.Fields[2].Min, dto.Fields[2].Max)
	}

	if _, err := json.Marshal(dto); err != nil {
		t.Fatalf("json.Marshal(ProfileDTO) error = %v, want nil (the OpenResult crash this fix closes)", err)
	}
}

// TestOpenResult_MarshalsWithNonFiniteProfileExtremes is the literal
// OpenResult-level regression named in the finding: "the file won't open at
// all" if a profiled field's Min/Max is non-finite.
func TestOpenResult_MarshalsWithNonFiniteProfileExtremes(t *testing.T) {
	inf := math.Inf(1)
	res := OpenResult{
		Handle: "h1",
		Format: "parquet",
		Tier:   "parquet",
		Profile: adaptProfile(profile.ProfileResult{
			Records: 1,
			Fields:  []profile.FieldProfile{{Path: "amount", Min: &inf, Max: &inf}},
		}),
	}
	if _, err := json.Marshal(res); err != nil {
		t.Fatalf("json.Marshal(OpenResult) error = %v, want nil (non-finite profile extreme must not crash OpenSource's response)", err)
	}
}

func TestEngine_OpenSource_SQLiteAndParquet_EmptyFileErrors(t *testing.T) {
	dir := t.TempDir()
	for _, ext := range []string{"sqlite", "parquet"} {
		path := filepath.Join(dir, "fixture."+ext)
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			t.Fatalf("write dummy %s file: %v", ext, err)
		}
		e := NewEngine()
		if _, err := e.OpenSource(context.Background(), OpenRequest{Path: path}); err == nil {
			t.Fatalf("OpenSource(%s) error = nil, want non-nil (empty file is not a valid %s source; parquet is also still stubbed, Task 8)", ext, ext)
		}
	}
}

// --- E2 Task 2: ctx threading + the Cancel registry (I3, spec §8) ----------

// TestEngine_QueryRows_HonorsCancelledContext passes an already-cancelled
// ctx: the top-level "if err := ctx.Err()" guard every Backend.Query
// implementation already has (memstore.go/rescan.go/sqlbackend.go/
// parquetbackend.go) must surface as context.Canceled through QueryRows,
// proving ctx genuinely reaches the backend rather than QueryRows silently
// swapping in context.Background() (engine.go:206, pre-fix).
func TestEngine_QueryRows_HonorsCancelledContext(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.QueryRows(ctx, QueryRequest{Handle: res.Handle, Limit: 10}); err == nil {
		t.Fatalf("QueryRows(cancelled ctx) error = nil, want non-nil")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("QueryRows(cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", err)
	}
}

// waitForInFlight spins on e.inFlightCount() (a mutex-guarded map length
// read, not a fixed timer) until it reports n, or fails the test after a
// generous deadline. This is deliberately NOT a sleep-based synchronization:
// it polls the engine's REAL registry state, so it succeeds as soon as the
// other goroutine has actually registered (or fails loudly on a genuine
// hang) rather than gambling on a guessed delay.
func waitForInFlight(t *testing.T, e *Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e.inFlightCount() == n {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("inFlightCount() never reached %d within the 5s deadline (got %d)", n, e.inFlightCount())
}

// gatedCountingQueryBackend wraps a real Backend, and on Query substitutes a
// decorator around the compiled Filter's predicate that (1) counts calls and
// (2) on its FIRST call, closes started and then blocks on <-gate --
// mirroring fakeRecordStream's onCall hook above, but at the per-record
// predicate-call granularity memBackend.computeMatchBitset scans at (rather
// than the record-stream granularity openIngestBackend scans at). This lets
// a test deterministically pin a cancellation to land strictly AFTER the
// scan has genuinely entered computeMatchBitset's loop (proven by started
// firing) and strictly BEFORE the scan is allowed to advance any further
// (held at the gate) -- removing the race the original version of
// TestEngine_Cancel_CancelsInFlightQuery had between "Cancel() has been
// called" and "the scan has actually started", which (measured empirically
// while fixing MINOR-3: -count=5 failed 4/5 times) let the ctx die before a
// single predicate call most of the time, silently proving nothing about
// "mid-scan" at all. This is registered directly into Engine.backends (this
// test file is package query, so the unexported field is reachable), not
// through OpenSource, so it needs no seam in production code.
type gatedCountingQueryBackend struct {
	Backend
	calls   int64
	started chan struct{}
	gate    chan struct{}
}

func (g *gatedCountingQueryBackend) Query(ctx context.Context, p *CompiledPlan, w Window, wantTotal bool) (RowSet, error) {
	real := p.Filter
	hooked := &CompiledFilter{
		pred: func(rec any) bool {
			if atomic.AddInt64(&g.calls, 1) == 1 {
				close(g.started)
				<-g.gate
			}
			return real.Match(rec)
		},
		key: real.Key(),
	}
	hp := &CompiledPlan{Filter: hooked, Transform: p.Transform, Columns: p.Columns, filterKey: hooked.Key()}
	return g.Backend.Query(ctx, hp, w, wantTotal)
}

// TestEngine_Cancel_CancelsInFlightQuery is the mid-flight case (as opposed
// to TestEngine_QueryRows_HonorsCancelledContext's pre-cancelled short-
// circuit, which never exercises a backend's stride check): it starts
// QueryRows on a large mem source in a goroutine under RequestID "r1", waits
// (waitForInFlight) until the engine actually reports "r1" registered, then
// calls Cancel("r1") from the main goroutine -- proving Cancel can interrupt
// a scan already in progress, not just a request that hasn't started yet.
//
// MINOR-3 review fix: errors.Is(qerr, context.Canceled) alone does not pin
// WHERE the cancellation landed. gatedCountingQueryBackend closes that gap
// deterministically (no sleep, no scheduling-luck timing margin):
// gb.started is only closed from INSIDE computeMatchBitset's loop, on its
// very first predicate call, and that same call blocks on gb.gate until the
// test releases it -- so waiting on gb.started before calling Cancel("r1"),
// and only closing gb.gate afterward, guarantees the ctx is already
// cancelled by the time the scan is allowed to advance past record 0. Since
// computeMatchBitset only rechecks ctx.Err() at i%cancelCheckStride==0, the
// scan then runs uninterrupted through i=1..cancelCheckStride-1 (calling the
// predicate cancelCheckStride-1 more times) before its next check (i =
// cancelCheckStride) observes the cancellation and aborts -- so gb.calls
// must land at EXACTLY cancelCheckStride on every run, mirroring
// TestOpenIngestBackend_CancelsMidIngest's stream.calls == cancelCheckStride
// assertion. calls == cancelCheckStride is only possible if the scan (a)
// left Query's own entry guard (or it would be 0) and (b) did not run to
// completion (or it would be len(maps)/2, the "even" match count, with no
// error at all) -- i.e., it proves the cancellation was caught genuinely
// mid-scan.
func TestEngine_Cancel_CancelsInFlightQuery(t *testing.T) {
	maps := manyRecords(50000)
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	e.mu.Lock()
	real := e.backends[res.Handle]
	gb := &gatedCountingQueryBackend{Backend: real, started: make(chan struct{}), gate: make(chan struct{})}
	e.backends[res.Handle] = gb
	e.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, qerr := e.QueryRows(context.Background(), QueryRequest{RequestID: "r1", Handle: res.Handle, Filter: evenFilter(), Offset: 0, Limit: 10, WantTotal: true})
		done <- qerr
	}()

	waitForInFlight(t, e, 1)
	<-gb.started // the scan has genuinely entered computeMatchBitset's loop, blocked at record 0
	if err := e.Cancel("r1"); err != nil {
		t.Fatalf("Cancel(r1) error = %v, want nil (r1 must be registered)", err)
	}
	close(gb.gate) // release the scan only now that ctx is already cancelled

	select {
	case qerr := <-done:
		if qerr == nil {
			t.Fatalf("QueryRows(cancelled mid-flight) error = nil, want non-nil")
		}
		if !errors.Is(qerr, context.Canceled) {
			t.Fatalf("QueryRows(cancelled mid-flight) error = %v, want errors.Is(err, context.Canceled)", qerr)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("QueryRows never returned after Cancel(r1)")
	}

	if calls := atomic.LoadInt64(&gb.calls); calls != cancelCheckStride {
		t.Fatalf("predicate calls = %d, want exactly %d (the cancellation must have been caught at computeMatchBitset's very next stride check after the gate released it, proving it landed mid-scan rather than at Query's entry guard or not at all)", calls, cancelCheckStride)
	}
}

// TestEngine_Cancel_UnknownRequestID covers Cancel's "normal race" error
// path (spec §8): cancelling an id nothing is registered under must error
// (mentioning the id, for a useful log/response) rather than panic.
func TestEngine_Cancel_UnknownRequestID(t *testing.T) {
	e := NewEngine()
	err := e.Cancel("no-such-request")
	if err == nil {
		t.Fatalf("Cancel(unknown) error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "no-such-request") {
		t.Fatalf("Cancel(unknown) error = %q, want it to mention the requestID", err.Error())
	}
}

// twoPhaseGateBackend wraps a real Backend for
// TestEngine_Cancel_SupersedesDuplicateRequestID. Its Query method
// distinguishes the FIRST call (the soon-to-be-superseded request) from the
// SECOND (the superseding one) by a simple atomic call counter -- safe here
// specifically because the test never starts the second QueryRows goroutine
// until it has confirmed (via firstStarted) that the first one's Query call
// has already begun, so there is no ambiguity about which logical request
// "call #1" vs "call #2" is:
//   - call #1 (first) just signals firstStarted and runs straight through
//     unmodified -- it must complete via a genuine mid-scan cancellation
//     (the supersede), not be held up by anything this wrapper does.
//   - call #2 (second) signals secondStarted and then blocks its very first
//     predicate invocation on <-gate, so the test can wait for that signal
//     and then read e.inFlightCount() with an ARBITRARILY WIDE window
//     (however slow the scheduler or a GC pause is) instead of the ~10x
//     margin (~0.2ms vs ~2.4ms) the original sleep-free-but-still-
//     wall-clock-dependent version relied on.
type twoPhaseGateBackend struct {
	Backend
	calls         int64
	firstStarted  chan struct{}
	secondStarted chan struct{}
	gate          chan struct{}
}

func (b *twoPhaseGateBackend) Query(ctx context.Context, p *CompiledPlan, w Window, wantTotal bool) (RowSet, error) {
	switch atomic.AddInt64(&b.calls, 1) {
	case 1:
		close(b.firstStarted)
		return b.Backend.Query(ctx, p, w, wantTotal)
	case 2:
		real := p.Filter
		var predCalls int64
		hooked := &CompiledFilter{
			pred: func(rec any) bool {
				// Gate only the FIRST predicate call (record 0): computeMatchBitset
				// calls this once per record, but secondStarted must close exactly
				// once, and only the very first call needs to block -- gate is
				// closed by the test afterward, so every later <-b.gate receive
				// (this call and, if the gate opened mid-scan, none of the rest,
				// since it never blocks past the closed channel) is a no-op.
				if atomic.AddInt64(&predCalls, 1) == 1 {
					close(b.secondStarted)
					<-b.gate
				}
				return real.Match(rec)
			},
			key: real.Key(),
		}
		hp := &CompiledPlan{Filter: hooked, Transform: p.Transform, Columns: p.Columns, filterKey: hooked.Key()}
		return b.Backend.Query(ctx, hp, w, wantTotal)
	default:
		return b.Backend.Query(ctx, p, w, wantTotal)
	}
}

// TestEngine_Cancel_SupersedesDuplicateRequestID: a second QueryRows
// registering under the SAME RequestID as a still-running first one
// supersedes it (begin's documented "reused id means supersede" behavior,
// engine.go) -- the first must be cancelled, the second must complete
// normally, and (the generation-token invariant) the first's own deferred
// release must NOT delete the second's now-current registry entry.
//
// The last point needs its own mid-flight check, not just an end-of-test
// inFlightCount()==0: a NAIVE release (unconditionally deleting e.inflight
// [requestID], no generation check) would ALSO end up at inFlightCount()==0
// once both requests finish -- second's own release would simply find
// nothing left to delete. The only observable difference the generation
// token makes is DURING the overlap: with the token, second's entry must
// still be there the instant first (superseded) finishes, while second is
// still genuinely running. So this test deliberately reads inFlightCount()
// in that exact window, between "first is confirmed done" and "second is
// confirmed done" -- where a naive release would have already (wrongly)
// dropped to 0, but the generation-checked one has not.
//
// MINOR-1 review fix: that window used to be a real wall-clock race (~0.2ms
// for first to abort vs ~2.4ms for second's full scan -- a ~10x margin, not
// the large one originally claimed), fragile under a GC pause or scheduler
// stall, especially under this suite's -count=2 no-flakes gate on Windows.
// twoPhaseGateBackend removes the timing dependency entirely: second's scan
// is held at record 0 (via <-gate) until the test has already read
// inFlightCount(), so that read can never race with second's completion --
// it is now impossible for second to have finished by the time it happens,
// no matter how slow the machine is.
//
// CQ-3 review fix: pre-fix, both requests were built from the SAME req()
// closure -- same RequestID ("dup") AND the same evenFilter() -- so their
// CompiledFilter.Key()s were identical too. twoPhaseGateBackend deliberately
// lets the first request's scan run ungated; if that scan won the race and
// finished (populating memBackend.matchCache under that shared key) before
// the second request's Query call ran, the second became a cache HIT:
// computeMatchBitset never ran, gb.secondStarted never closed, and the test
// blocked on the 30s backstop below -- an empirically ~1-in-12 flake. The
// test's actual subject (begin's generation token, keyed on RequestID) is
// completely indifferent to which filter each request carries, so giving the
// second request a DIFFERENT filter (notEvenFilter) makes its
// CompiledFilter.Key() always differ from the first's: matchBitsetFor always
// misses for it, computeMatchBitset always runs, gb.secondStarted always
// closes. The race is gone with no new synchronization machinery, and the
// backstop below is now dead code on the happy path -- kept at 5s (down from
// 30s) purely as a hang detector, matching this test's other backstops.
func TestEngine_Cancel_SupersedesDuplicateRequestID(t *testing.T) {
	maps := manyRecords(50000)
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}
	req := func(f Filter) QueryRequest {
		return QueryRequest{RequestID: "dup", Handle: res.Handle, Filter: f, Offset: 0, Limit: 10, WantTotal: true}
	}

	e.mu.Lock()
	real := e.backends[res.Handle]
	gb := &twoPhaseGateBackend{Backend: real, firstStarted: make(chan struct{}), secondStarted: make(chan struct{}), gate: make(chan struct{})}
	e.backends[res.Handle] = gb
	e.mu.Unlock()

	firstDone := make(chan error, 1)
	go func() {
		_, qerr := e.QueryRows(context.Background(), req(evenFilter()))
		firstDone <- qerr
	}()
	waitForInFlight(t, e, 1)
	<-gb.firstStarted // first's Query call has genuinely begun -- so the goroutine started below is unambiguously "call #2"

	secondDone := make(chan error, 1)
	go func() {
		_, qerr := e.QueryRows(context.Background(), req(notEvenFilter()))
		secondDone <- qerr
	}()
	// second's scan has genuinely begun, blocked at record 0 -- it cannot have
	// finished by the time inFlightCount() is read below. This is now a pure
	// happens-before guarantee, not a race: second uses notEvenFilter (see
	// above), whose CompiledFilter.Key() always differs from first's
	// evenFilter(), so second's Query can never become a memBackend.matchCache
	// hit regardless of how the first (ungated) request's scan and this one
	// interleave. The 5s wait below is a pure backstop against a genuine hang.
	select {
	case <-gb.secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("gb.secondStarted never closed within 5s: second's Query call never reached computeMatchBitset")
	}

	var firstErr error
	select {
	case firstErr = <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("first QueryRows never returned after being superseded")
	}
	if firstErr == nil {
		t.Fatalf("QueryRows(first, superseded) error = nil, want non-nil (should have been cancelled by the second registration)")
	}
	if !errors.Is(firstErr, context.Canceled) {
		t.Fatalf("QueryRows(first, superseded) error = %v, want errors.Is(err, context.Canceled)", firstErr)
	}

	// THE generation-token assertion: immediately after the superseded first
	// request has finished (and run its own deferred release), the registry
	// must still show ONE in-flight request -- the second's, which is
	// guaranteed (via gb.secondStarted, above) to still be blocked at record
	// 0. A naive (non-generation-checked) release would have already deleted
	// it here, wrongly reporting 0.
	if n := e.inFlightCount(); n != 1 {
		t.Fatalf("inFlightCount() = %d immediately after the superseded first request finished, want 1 (second's registry entry must survive first's release -- the generation-token invariant)", n)
	}
	close(gb.gate) // release second's scan; only now may it proceed to completion

	var secondErr error
	select {
	case secondErr = <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("second QueryRows never returned")
	}
	if secondErr != nil {
		t.Fatalf("QueryRows(second, superseding) error = %v, want nil", secondErr)
	}
	if n := e.inFlightCount(); n != 0 {
		t.Fatalf("inFlightCount() = %d after both requests finished, want 0", n)
	}
}

// TestEngine_QueryRows_EmptyRequestIDStillRuns: an empty RequestID means "do
// not track this request at all" (engine.go's begin doc comment) -- it must
// still run to completion and return rows, but never appear in the registry
// and never be cancellable via Cancel.
func TestEngine_QueryRows_EmptyRequestIDStillRuns(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	rs, err := e.QueryRows(context.Background(), QueryRequest{RequestID: "", Handle: res.Handle, Filter: evenFilter(), Offset: 0, Limit: 10, WantTotal: true})
	if err != nil {
		t.Fatalf("QueryRows(RequestID=\"\") error = %v, want nil", err)
	}
	if len(rs.Rows) == 0 {
		t.Fatalf("QueryRows(RequestID=\"\") returned 0 rows, want > 0")
	}
	if n := e.inFlightCount(); n != 0 {
		t.Fatalf("inFlightCount() = %d after an empty-RequestID query, want 0 (never registered)", n)
	}
	if err := e.Cancel(""); err == nil {
		t.Fatalf("Cancel(\"\") error = nil, want non-nil (an empty RequestID is never registered/cancellable)")
	}
}

// TestEngine_Cancel_ReleasesRegistryOnCompletion: after a normal (uncancelled)
// QueryRows completes, its RequestID must no longer be in the registry (no
// leak), so a subsequent Cancel on that same id errors.
func TestEngine_Cancel_ReleasesRegistryOnCompletion(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	if _, err := e.QueryRows(context.Background(), QueryRequest{RequestID: "r1", Handle: res.Handle, Filter: evenFilter(), Offset: 0, Limit: 10, WantTotal: true}); err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if n := e.inFlightCount(); n != 0 {
		t.Fatalf("inFlightCount() = %d after a completed query, want 0 (no leak)", n)
	}
	if err := e.Cancel("r1"); err == nil {
		t.Fatalf("Cancel(r1) after completion error = nil, want non-nil (r1 must no longer be registered)")
	}
}

// TestEngine_OpenSource_HonorsCancelledContext covers all four OpenSource
// tiers (ndjson/csv -> ingest [memory tier], sqlite, parquet): an
// already-cancelled ctx must make OpenSource return context.Canceled and an
// EMPTY OpenResult, never a populated one, proving ctx reaches openBackend's
// per-tier constructor (openIngestBackend's n=0 stride check; newSQLBackend/
// newParquetBackend's profiling-pass scan, whose own idx=0 stride check
// fires before any row is read).
func TestEngine_OpenSource_HonorsCancelledContext(t *testing.T) {
	maps := fixtureRecords()
	ndjsonPath := writeNDJSONFile(t, maps)
	csvPath := writeCSVFile(t, []string{"name", "age", "even"}, maps)
	sqlitePath := makeSQLiteFixture(t,
		"CREATE TABLE t (name TEXT, idx INTEGER, even INTEGER)",
		sqlInsertStatements("t", []string{"name", "idx", "even"}, sqlNameParityRows())...,
	)
	parquetPath := writeParquetFixture(t, parquetNameParityRows(), 0)

	cases := []struct {
		name string
		path string
	}{
		{"ndjson", ndjsonPath},
		{"csv", csvPath},
		{"sqlite", sqlitePath},
		{"parquet", parquetPath},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEngine()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			res, err := e.OpenSource(ctx, OpenRequest{Path: c.path})
			if err == nil {
				t.Fatalf("OpenSource(%s, cancelled ctx) error = nil, want non-nil; res = %#v", c.name, res)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("OpenSource(%s, cancelled ctx) error = %v, want errors.Is(err, context.Canceled)", c.name, err)
			}
			if res.Handle != "" {
				t.Fatalf("OpenSource(%s, cancelled ctx) Handle = %q, want empty (no populated OpenResult on a cancelled open)", c.name, res.Handle)
			}
		})
	}
}

// fakeRecordStream is a readers.RecordStream test double for
// TestOpenIngestBackend_CancelsMidIngest: its Next() never returns io.EOF (so
// the ingest loop can only ever end via the ctx stride check under test), and
// it invokes onCall exactly once per call with the 1-based call number, used
// to fire the test's cancel() deterministically on a specific call -- the
// same "no sleep, no goroutine timing" discipline cancelAfterNCalls
// (memstore_test.go) and countCancelConn (sqlbackend_test.go:437) use for
// their respective backends, applied here at the readers.RecordStream seam.
type fakeRecordStream struct {
	calls  int
	onCall func(call int)
}

func (f *fakeRecordStream) Next() (any, error) {
	f.calls++
	if f.onCall != nil {
		f.onCall(f.calls)
	}
	return map[string]any{"n": f.calls}, nil
}

func (f *fakeRecordStream) Skipped() int { return 0 }

// TestOpenIngestBackend_CancelsMidIngest calls openIngestBackend directly
// (bypassing OpenSource/openBackend) with a fakeRecordStream that never
// reaches EOF, firing cancel() on exactly the 4096th Next() call -- the call
// immediately preceding the loop's n=4096 "n%cancelCheckStride==0" check
// (n=0's Next() is call 1, ..., n=4095's Next() is call 4096; the very next
// iteration, n=4096, is the first opportunity the loop has to observe the
// cancellation). This is deliberately NOT an NDJSON fixture + time.Sleep
// goroutine cancel: openIngestBackend exposes no progress signal and the
// only check points are n=0 and n=4096, so a sleep-aimed cancel lands either
// before n=0 (never reaching the stride check at all) or after this fake
// stream's unbounded EOF-less loop would just spin forever -- neither
// proves the mid-ingest stride check works. DefaultMemBudgetBytes is passed
// so the over-budget break (a tiny map per record, nowhere near 512 MiB by
// call 4096) cannot end the loop first.
func TestOpenIngestBackend_CancelsMidIngest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &fakeRecordStream{}
	stream.onCall = func(call int) {
		if call == cancelCheckStride {
			cancel()
		}
	}

	_, _, err := openIngestBackend(ctx, "fake-path.ndjson", readers.FormatJSON, "", false, stream, DefaultMemBudgetBytes)
	if err == nil {
		t.Fatalf("openIngestBackend(cancelled mid-ingest) error = nil, want non-nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("openIngestBackend(cancelled mid-ingest) error = %v, want errors.Is(err, context.Canceled)", err)
	}
	if stream.calls != cancelCheckStride {
		t.Fatalf("stream.calls = %d, want %d (the loop must stop at the very next stride check after cancel, not keep reading)", stream.calls, cancelCheckStride)
	}
}

// --- IMPORTANT-1: OpenSource must re-check ctx after RowCount -------------

// openSourceEOFStream is a readers.RecordStream test double for
// TestEngine_OpenSource_CancelledDuringRowCount_NotRegistered: unlike
// fakeRecordStream above (which never reaches io.EOF, used to prove ingest
// ABORTS mid-scan), this one returns exactly n real records then io.EOF
// forever after, so openIngestBackend completes NORMALLY (a valid memBackend,
// nil error) -- but onLast fires synchronously on the nth (final) call, in
// the SAME goroutine, letting a test cancel a real context deterministically
// before the loop's very next iteration ever observes io.EOF. n is chosen
// far below cancelCheckStride, so the ingest loop's only stride check (at
// n=0) passes while ctx is still live, and no further check happens before
// EOF -- reproducing the exact window the finding describes: the backend
// finishes building successfully while ctx is already Done.
type openSourceEOFStream struct {
	n      int
	calls  int
	onLast func()
}

func (f *openSourceEOFStream) Next() (any, error) {
	f.calls++
	if f.calls > f.n {
		return nil, io.EOF
	}
	if f.calls == f.n && f.onLast != nil {
		f.onLast()
	}
	return map[string]any{"n": f.calls}, nil
}

func (f *openSourceEOFStream) Skipped() int { return 0 }

// TestEngine_OpenSource_CancelledDuringRowCount_NotRegistered is IMPORTANT-1's
// regression test. TestEngine_OpenSource_HonorsCancelledContext (above) only
// covers a ctx cancelled BEFORE OpenSource ever starts, which openBackend's
// own ctx.Err() guard catches long before RowCount is reached -- it proves
// nothing about the window this test targets: ctx dying AFTER openBackend has
// already built a valid Backend, but before/during the backend.RowCount(ctx)
// call OpenSource makes next. Pre-fix, OpenSource never re-checks ctx after
// RowCount, so it returns a "successful" (err == nil) OpenResult -- with
// RowEstimate/RowExact collapsed to memBackend.RowCount's own (0, false)
// dead-ctx contract, indistinguishable from an empty source -- AND registers
// the backend's handle, leaking it (only CloseSource, which a caller holding
// a seemingly-fine-but-actually-cancelled result has no reason to call, would
// ever release it).
//
// openReaderStream (source.go) is substituted for the duration of this test
// with openSourceEOFStream: it returns 3 real records then io.EOF, firing a
// REAL context.CancelFunc synchronously on the 3rd (last) call. Because the
// ctx passed to OpenSource here is a genuine *context.cancelCtx
// (context.WithCancel(context.Background())), Engine.begin's own
// context.WithCancel(ctx) child is registered as that ctx's direct child
// SYNCHRONOUSLY at Engine.begin's call time (context.propagateCancel: a
// *cancelCtx parent propagates to a child derived directly from it with no
// goroutine involved) -- so calling cancel() from inside the ingest loop
// cancels OpenSource's derived ctx immediately, in the same goroutine, with
// no sleep and no timing race at all.
func TestEngine_OpenSource_CancelledDuringRowCount_NotRegistered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.ndjson")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	callerCtx, callerCancel := context.WithCancel(context.Background())
	defer callerCancel()

	stream := &openSourceEOFStream{n: 3}
	stream.onLast = func() { callerCancel() }

	orig := openReaderStream
	openReaderStream = func(f readers.Format, s readers.Source) (readers.RecordStream, func() error, error) {
		return stream, func() error { return nil }, nil
	}
	defer func() { openReaderStream = orig }()

	e := NewEngine()
	res, err := e.OpenSource(callerCtx, OpenRequest{Path: path, Format: "ndjson"})
	if err == nil {
		t.Fatalf("OpenSource(ctx cancelled during ingest tail, caught at RowCount) error = nil, want non-nil; res = %#v", res)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenSource error = %v, want errors.Is(err, context.Canceled)", err)
	}
	if res.Handle != "" {
		t.Fatalf("OpenSource Handle = %q, want empty (must not return a populated OpenResult on this cancellation)", res.Handle)
	}
	if n := len(e.backends); n != 0 {
		t.Fatalf("len(e.backends) = %d, want 0 (a Backend built under a ctx that died before OpenSource's post-RowCount check must not be registered -- it would otherwise leak, reachable by no handle)", n)
	}
}

// --- E2 Task 3: CountMatches, wide-data DTO fields, CellKind enum export ----

// TestEngine_CountMatches_ExactOnMemoryTier covers the base case (spec §8):
// counting a filter matching a known subset on the memory tier returns the
// exact total, Exact == true, and a non-negative ElapsedMs.
func TestEngine_CountMatches_ExactOnMemoryTier(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	cr, err := e.CountMatches(context.Background(), CountRequest{Handle: res.Handle, Filter: evenFilter()})
	if err != nil {
		t.Fatalf("CountMatches error = %v, want nil", err)
	}
	if cr.Total != 5 || !cr.Exact {
		t.Fatalf("Total/Exact = %d/%v, want 5/true", cr.Total, cr.Exact)
	}
	if cr.ElapsedMs < 0 {
		t.Fatalf("ElapsedMs = %d, want >= 0", cr.ElapsedMs)
	}
}

// TestEngine_CountMatches_MatchAllEqualsRowCount: an empty Filter{} (match
// everything) must return the same total as OpenResult.RowEstimate on the
// memory tier, where RowEstimate is itself exact.
func TestEngine_CountMatches_MatchAllEqualsRowCount(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	cr, err := e.CountMatches(context.Background(), CountRequest{Handle: res.Handle, Filter: Filter{}})
	if err != nil {
		t.Fatalf("CountMatches(empty filter) error = %v, want nil", err)
	}
	if cr.Total != res.RowEstimate {
		t.Fatalf("Total = %d, want %d (== OpenResult.RowEstimate on the memory tier)", cr.Total, res.RowEstimate)
	}
	if !cr.Exact {
		t.Fatalf("Exact = false, want true (memory tier)")
	}
}

// TestEngine_CountMatches_UnknownHandle: an unregistered handle must error
// (naming the handle), never panic.
func TestEngine_CountMatches_UnknownHandle(t *testing.T) {
	e := NewEngine()
	_, err := e.CountMatches(context.Background(), CountRequest{Handle: "no-such-handle"})
	if err == nil {
		t.Fatalf("CountMatches(unknown handle) error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "no-such-handle") {
		t.Fatalf("CountMatches(unknown handle) error = %q, want it to mention the handle", err.Error())
	}
}

// gatedCountingCountBackend is gatedCountingQueryBackend's (above)
// Count-side twin: it wraps a real Backend and, on Count, substitutes a
// decorator around the compiled Filter's predicate that (1) counts calls and
// (2) on its FIRST call, closes started and blocks on <-gate -- letting a
// test pin a CountMatches cancellation strictly AFTER
// memBackend.computeMatchBitset's loop has genuinely started (proven by
// started firing) and strictly BEFORE it is allowed to advance any further
// (held at the gate), exactly like gatedCountingQueryBackend does for Query.
type gatedCountingCountBackend struct {
	Backend
	calls   int64
	started chan struct{}
	gate    chan struct{}
}

func (g *gatedCountingCountBackend) Count(ctx context.Context, f *CompiledFilter) (int64, bool, error) {
	real := f
	hooked := &CompiledFilter{
		pred: func(rec any) bool {
			if atomic.AddInt64(&g.calls, 1) == 1 {
				close(g.started)
				<-g.gate
			}
			return real.Match(rec)
		},
		key: real.Key(),
	}
	return g.Backend.Count(ctx, hooked)
}

// TestEngine_CountMatches_Cancellable is CountMatches' mid-flight cancel
// case, mirroring TestEngine_Cancel_CancelsInFlightQuery's discipline
// exactly: it starts CountMatches on a large mem source in a goroutine under
// RequestID "c1", waits (waitForInFlight) until the engine reports "c1"
// registered, THEN waits for gb.started -- proving computeMatchBitset's scan
// has genuinely begun and is blocked at record 0 -- before calling
// Cancel("c1") and only then releasing the gate. Because the gate release
// happens strictly after Cancel, the cancellation is provably already in
// effect before the scan is allowed to advance past record 0: this cannot be
// short-circuited by CountMatches'/memBackend.Count's top-level ctx.Err()
// guard (which would require the ctx to already be dead BEFORE Count is
// ever called -- it is not, since gb.started only closes from inside the
// scan itself). The predicate-call count landing at exactly
// cancelCheckStride (computeMatchBitset's stride, memstore.go) after the
// gate opens proves the abort happened at the very next stride check, not at
// entry and not never.
func TestEngine_CountMatches_Cancellable(t *testing.T) {
	maps := manyRecords(50000)
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	e.mu.Lock()
	real := e.backends[res.Handle]
	gb := &gatedCountingCountBackend{Backend: real, started: make(chan struct{}), gate: make(chan struct{})}
	e.backends[res.Handle] = gb
	e.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, cerr := e.CountMatches(context.Background(), CountRequest{RequestID: "c1", Handle: res.Handle, Filter: evenFilter()})
		done <- cerr
	}()

	waitForInFlight(t, e, 1)
	<-gb.started // the scan has genuinely entered computeMatchBitset's loop, blocked at record 0
	if err := e.Cancel("c1"); err != nil {
		t.Fatalf("Cancel(c1) error = %v, want nil (c1 must be registered)", err)
	}
	close(gb.gate) // release the scan only now that ctx is already cancelled

	select {
	case cerr := <-done:
		if cerr == nil {
			t.Fatalf("CountMatches(cancelled mid-flight) error = nil, want non-nil")
		}
		if !errors.Is(cerr, context.Canceled) {
			t.Fatalf("CountMatches(cancelled mid-flight) error = %v, want errors.Is(err, context.Canceled)", cerr)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("CountMatches never returned after Cancel(c1)")
	}

	if calls := atomic.LoadInt64(&gb.calls); calls != cancelCheckStride {
		t.Fatalf("predicate calls = %d, want exactly %d (the cancellation must have been caught at computeMatchBitset's very next stride check after the gate released it, proving it landed mid-scan)", calls, cancelCheckStride)
	}
}

// TestEngine_CountMatches_SharesQueryBitset: QueryRows then CountMatches with
// the same logical filter on a memory-tier handle must leave exactly ONE
// entry in the backend's matchCache (Task 1's content-hash Key(), reached
// here via the package-internal *memBackend field, exactly like Task 1's own
// tests do) -- proving CountMatches keys on the same CompiledFilter.Key() as
// Query rather than maintaining its own, separate cache.
//
// CQ-4 review fix: len(matchCache)==1 alone proves same-KEY, not FREE -- it
// would pass identically if CountMatches re-scanned from scratch and
// matchBitsetFor's double-checked lock (memstore.go) simply collapsed the
// duplicate result into the same cache entry the first scan already wrote.
// The actual claim under test is "counting a filter already scrolled costs
// no scan", so this wraps the primed backend in gatedCountingCountBackend
// (already used by TestEngine_CountMatches_Cancellable) with its gate
// pre-closed -- it only needs to COUNT predicate calls, never gate them --
// and asserts CountMatches' own Count call makes ZERO of them: a pure cache
// hit never invokes the compiled predicate at all (matchBitsetFor returns
// straight from its lock-held cache check), so any non-zero count proves a
// real re-scan happened.
func TestEngine_CountMatches_SharesQueryBitset(t *testing.T) {
	maps := fixtureRecords()
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	if _, err := e.QueryRows(context.Background(), QueryRequest{Handle: res.Handle, Filter: evenFilter(), Offset: 0, Limit: 10, WantTotal: true}); err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}

	e.mu.Lock()
	real := e.backends[res.Handle]
	gb := &gatedCountingCountBackend{Backend: real, started: make(chan struct{}), gate: make(chan struct{})}
	close(gb.gate) // never actually gating here: this test only counts calls
	e.backends[res.Handle] = gb
	e.mu.Unlock()

	if _, err := e.CountMatches(context.Background(), CountRequest{Handle: res.Handle, Filter: evenFilter()}); err != nil {
		t.Fatalf("CountMatches error = %v, want nil", err)
	}

	if calls := atomic.LoadInt64(&gb.calls); calls != 0 {
		t.Fatalf("CountMatches predicate calls = %d, want 0 (the filter's match bitset was already computed by the preceding QueryRows; sharing the cache must make this a pure hit, not a re-scan)", calls)
	}

	mb, ok := real.(*memBackend)
	if !ok {
		t.Fatalf("backend is not a *memBackend")
	}
	mb.mu.Lock()
	entries := len(mb.matchCache)
	mb.mu.Unlock()
	if entries != 1 {
		t.Fatalf("matchCache has %d entries after QueryRows then CountMatches over the same logical Filter, want 1 (shared cache, keyed on CompiledFilter.Key())", entries)
	}
}

// fakeInexactCountBackend wraps a real Backend, forcing Count to always
// report a fixed total with exact=false regardless of what the wrapped
// backend would have computed. TestEngine_CountMatches_RescanTierExactFlag
// uses this (CQ-5 review fix) to prove CountMatches genuinely THREADS
// Backend.Count's own Exact return through to CountResult.Exact: the test's
// happy-path assertion alone (Exact == true against a real rescanBackend)
// would pass identically if CountMatches hardcoded Exact: true, since
// rescanBackend.Count's own contract happens to always return exact=true on
// an uncancelled scan.
type fakeInexactCountBackend struct {
	Backend
	total int64
}

func (f *fakeInexactCountBackend) Count(ctx context.Context, cf *CompiledFilter) (int64, bool, error) {
	return f.total, false, nil
}

// TestEngine_CountMatches_RescanTierExactFlag: on a source forced to the
// rescan tier (BudgetMB: 1 over a fixture far larger than that), Exact must
// be reported per Backend.Count's own contract (rescanBackend.Count,
// rescan.go: "a full cancellable scan (exact)" -- unlike Query's Total/
// TotalExact, an uncancelled Count is always exact for rescanBackend), not
// hardcoded to either true or false by CountMatches itself.
func TestEngine_CountMatches_RescanTierExactFlag(t *testing.T) {
	maps := manyRecords(20000) // >> a 1 MiB budget
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path, BudgetMB: 1})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}
	if res.Tier != "rescan" {
		t.Fatalf("Tier = %q, want \"rescan\" (20000 records must exceed a 1 MiB budget)", res.Tier)
	}

	cr, err := e.CountMatches(context.Background(), CountRequest{Handle: res.Handle, Filter: evenFilter()})
	if err != nil {
		t.Fatalf("CountMatches error = %v, want nil", err)
	}
	if cr.Total != int64(len(maps))/2 {
		t.Fatalf("Total = %d, want %d (exact \"even\" match count)", cr.Total, len(maps)/2)
	}
	if !cr.Exact {
		t.Fatalf("Exact = false, want true (rescanBackend.Count's own contract: an uncancelled full scan is always exact)")
	}

	// CQ-5 review fix: substitute a fake backend whose Count always reports
	// exact=false, and confirm CountMatches' result reflects THAT rather than
	// a hardcoded true -- discriminating what the assertions above cannot.
	e.mu.Lock()
	real := e.backends[res.Handle]
	e.backends[res.Handle] = &fakeInexactCountBackend{Backend: real, total: 999}
	e.mu.Unlock()

	cr2, err := e.CountMatches(context.Background(), CountRequest{Handle: res.Handle, Filter: evenFilter()})
	if err != nil {
		t.Fatalf("CountMatches(fake inexact backend) error = %v, want nil", err)
	}
	if cr2.Total != 999 {
		t.Fatalf("Total = %d, want 999 (threaded from the fake backend's Count)", cr2.Total)
	}
	if cr2.Exact {
		t.Fatalf("Exact = true, want false (CountMatches must thread Backend.Count's own Exact return rather than hardcode true)")
	}
}

// wideFixtureRecord returns a single record containing n distinct scalar
// fields ("field000".."field(n-1)"), used to drive buildColumnModel's
// MaxColumns cap (columns.go) end-to-end through OpenSource/QueryRows.
func wideFixtureRecord(n int) map[string]any {
	rec := make(map[string]any, n)
	for i := 0; i < n; i++ {
		rec[fmt.Sprintf("field%03d", i)] = json.Number(fmt.Sprintf("%d", i))
	}
	return rec
}

// TestEngine_OpenSource_ReportsColumnTruncation: a fixture whose single
// record has MaxColumns+10 distinct keys must report ColumnsTruncated==true,
// TotalPaths==MaxColumns+10, and len(Columns)==MaxColumns; a narrow fixture
// (well under the cap) must report ColumnsTruncated==false and
// TotalPaths==len(Columns).
func TestEngine_OpenSource_ReportsColumnTruncation(t *testing.T) {
	wide := []map[string]any{wideFixtureRecord(MaxColumns + 10)}
	widePath := writeNDJSONFile(t, wide)

	e := NewEngine()
	wideRes, err := e.OpenSource(context.Background(), OpenRequest{Path: widePath})
	if err != nil {
		t.Fatalf("OpenSource(wide) error = %v, want nil", err)
	}
	if !wideRes.ColumnsTruncated {
		t.Fatalf("ColumnsTruncated = false, want true (%d distinct paths > MaxColumns=%d)", MaxColumns+10, MaxColumns)
	}
	if wideRes.TotalPaths != MaxColumns+10 {
		t.Fatalf("TotalPaths = %d, want %d", wideRes.TotalPaths, MaxColumns+10)
	}
	if len(wideRes.Columns) != MaxColumns {
		t.Fatalf("len(Columns) = %d, want %d (MaxColumns cap)", len(wideRes.Columns), MaxColumns)
	}

	narrow := fixtureRecords()
	narrowPath := writeNDJSONFile(t, narrow)
	narrowRes, err := e.OpenSource(context.Background(), OpenRequest{Path: narrowPath})
	if err != nil {
		t.Fatalf("OpenSource(narrow) error = %v, want nil", err)
	}
	if narrowRes.ColumnsTruncated {
		t.Fatalf("ColumnsTruncated = true, want false (narrow fixture, well under MaxColumns)")
	}
	if narrowRes.TotalPaths != len(narrowRes.Columns) {
		t.Fatalf("TotalPaths = %d, want %d (== len(Columns): no truncation)", narrowRes.TotalPaths, len(narrowRes.Columns))
	}
}

// TestEngine_QueryRows_ReportsColumnTruncation: the same wide fixture must
// set the same two fields on the RowSet returned by QueryRows.
func TestEngine_QueryRows_ReportsColumnTruncation(t *testing.T) {
	wide := []map[string]any{wideFixtureRecord(MaxColumns + 10)}
	path := writeNDJSONFile(t, wide)

	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	rs, err := e.QueryRows(context.Background(), QueryRequest{Handle: res.Handle, Offset: 0, Limit: 1, WantTotal: true})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if !rs.ColumnsTruncated {
		t.Fatalf("RowSet.ColumnsTruncated = false, want true")
	}
	if rs.TotalPaths != MaxColumns+10 {
		t.Fatalf("RowSet.TotalPaths = %d, want %d", rs.TotalPaths, MaxColumns+10)
	}
}

// TestEngine_QueryRows_SelectTransform_UntruncatedProjection: naming exactly
// 2 paths in Transform.Select over a source wide enough to truncate the base
// ColumnModel must NOT surface the base model's truncation on the PROJECTED
// RowSet -- Select is authoritative and unbounded (spec/columns.go's
// MaxColumns doc comment: naming a path explicitly in Select overrides the
// cap), so ColumnsTruncated must be false and TotalPaths must describe the
// projected (2-column) set, not the uncapped base-path count.
func TestEngine_QueryRows_SelectTransform_UntruncatedProjection(t *testing.T) {
	wide := []map[string]any{wideFixtureRecord(MaxColumns + 10)}
	path := writeNDJSONFile(t, wide)

	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	rs, err := e.QueryRows(context.Background(), QueryRequest{
		Handle:    res.Handle,
		Transform: Transform{Select: []ColumnSpec{{Path: "field000"}, {Path: "field001"}}},
		Offset:    0,
		Limit:     1,
		WantTotal: true,
	})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if len(rs.Columns) != 2 {
		t.Fatalf("len(rs.Columns) = %d, want 2 (the selected paths)", len(rs.Columns))
	}
	if rs.ColumnsTruncated {
		t.Fatalf("rs.ColumnsTruncated = true, want false (Select is an explicit, unbounded projection)")
	}
	if rs.TotalPaths != len(rs.Columns) {
		t.Fatalf("rs.TotalPaths = %d, want %d (== len(rs.Columns): projected set is not truncated)", rs.TotalPaths, len(rs.Columns))
	}
}

// TestEngine_QueryRows_IdentityTransform_ReportsBaseTruncation pins that the
// Select/Drop fix above did not disturb the identity-transform case the
// ColumnsTruncated/TotalPaths field pair exists for: an explicit empty
// Transform{} (no Select, no Drop) leaves the base column set unchanged, so
// the base ColumnModel's own truncation must still come through unchanged.
func TestEngine_QueryRows_IdentityTransform_ReportsBaseTruncation(t *testing.T) {
	wide := []map[string]any{wideFixtureRecord(MaxColumns + 10)}
	path := writeNDJSONFile(t, wide)

	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	rs, err := e.QueryRows(context.Background(), QueryRequest{
		Handle:    res.Handle,
		Transform: Transform{}, // explicit identity: no Select, no Drop
		Offset:    0,
		Limit:     1,
		WantTotal: true,
	})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if !rs.ColumnsTruncated {
		t.Fatalf("rs.ColumnsTruncated = false, want true (identity transform: base model's truncation must still show through)")
	}
	if rs.TotalPaths != MaxColumns+10 {
		t.Fatalf("rs.TotalPaths = %d, want %d (uncapped base count, identity transform)", rs.TotalPaths, MaxColumns+10)
	}
}

// TestEngine_QueryRows_DropTransform_NarrowSource_ReportsBaseTotalPaths: a
// Drop transform over a narrow (well under MaxColumns, so never truncated)
// source must still report TotalPaths as the base ColumnModel's own count
// (cm.TotalPaths, the eligible-candidate count before any cap), NOT the
// post-drop len(rs.Columns) -- Drop only narrows the PROJECTION
// (rs.Columns); it says nothing about the base model TotalPaths describes.
// This was fixed alongside the wide-source case (see
// TestEngine_QueryRows_DropTransform_WideSource_ReportsBaseTruncation): the
// two differ only in whether cm.Truncated happens to be true, not in
// whether Drop affects these fields (it never does). This test previously
// asserted the opposite (TotalPaths == len(rs.Columns) post-drop) -- that
// was the "Drop lumped in with Select" regression this fix repairs; on a
// narrow source cm.Truncated is false either way, so only TotalPaths
// exposed the difference.
func TestEngine_QueryRows_DropTransform_NarrowSource_ReportsBaseTotalPaths(t *testing.T) {
	narrow := fixtureRecords() // "name", "age", "even" -- well under MaxColumns
	path := writeNDJSONFile(t, narrow)

	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	rs, err := e.QueryRows(context.Background(), QueryRequest{
		Handle:    res.Handle,
		Transform: Transform{Drop: []string{"age"}},
		Offset:    0,
		Limit:     1,
		WantTotal: true,
	})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if rs.ColumnsTruncated {
		t.Fatalf("rs.ColumnsTruncated = true, want false (narrow source: base model was never truncated)")
	}
	if len(rs.Columns) != 2 {
		t.Fatalf("len(rs.Columns) = %d, want 2 (base 3 minus dropped \"age\")", len(rs.Columns))
	}
	if rs.TotalPaths != 3 {
		t.Fatalf("rs.TotalPaths = %d, want 3 (base ColumnModel's own count, NOT the post-drop len(rs.Columns)=%d)", rs.TotalPaths, len(rs.Columns))
	}
}

// TestEngine_QueryRows_DropTransform_WideSource_ReportsBaseTruncation: a
// Drop-only transform over a WIDE source (more distinct paths than
// MaxColumns) must still report the base ColumnModel's own truncation.
// CompileTransform's Drop-only path starts from baseOutCols(cm), which is
// built from cm.Columns -- the ALREADY-CAPPED slice (transform.go) -- and
// only subtracts named entries (applyDrop); it can never reach a path the
// cap already excluded. So on a truncated base model, Drop hides columns for
// two independent reasons (the cap, plus the drop), and ColumnsTruncated/
// TotalPaths must still describe the base model's cap, not the post-drop
// length -- exactly like the identity-transform case above. "field000" is
// one of the MaxColumns kept-by-presence-tie-break-to-first-seen columns
// (wideFixtureRecord's single record gives every field equal presence, so
// ties resolve to first-seen order), so dropping it changes len(rs.Columns)
// without changing whether the base model was truncated.
func TestEngine_QueryRows_DropTransform_WideSource_ReportsBaseTruncation(t *testing.T) {
	wide := []map[string]any{wideFixtureRecord(MaxColumns + 10)}
	path := writeNDJSONFile(t, wide)

	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	rs, err := e.QueryRows(context.Background(), QueryRequest{
		Handle:    res.Handle,
		Transform: Transform{Drop: []string{"field000"}},
		Offset:    0,
		Limit:     1,
		WantTotal: true,
	})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if len(rs.Columns) != MaxColumns-1 {
		t.Fatalf("len(rs.Columns) = %d, want %d (capped MaxColumns minus the dropped column)", len(rs.Columns), MaxColumns-1)
	}
	if !rs.ColumnsTruncated {
		t.Fatalf("rs.ColumnsTruncated = false, want true (base model IS truncated; Drop-only transform cannot un-cap it)")
	}
	if rs.TotalPaths != MaxColumns+10 {
		t.Fatalf("rs.TotalPaths = %d, want %d (uncapped base count, NOT the post-drop len(rs.Columns)=%d)", rs.TotalPaths, MaxColumns+10, len(rs.Columns))
	}
}

// TestEngine_QueryRows_SelectAndDropTransform_WideSource_UntruncatedProjection:
// when BOTH Select and Drop are non-empty, CompileTransform takes the Select
// branch outright (rule 1: "Drop is ignored whenever Select is non-empty") --
// so the projection must follow the Select rule (unbounded, never truncated),
// exactly as with Select alone, regardless of the (ignored) Drop content.
func TestEngine_QueryRows_SelectAndDropTransform_WideSource_UntruncatedProjection(t *testing.T) {
	wide := []map[string]any{wideFixtureRecord(MaxColumns + 10)}
	path := writeNDJSONFile(t, wide)

	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}

	rs, err := e.QueryRows(context.Background(), QueryRequest{
		Handle: res.Handle,
		Transform: Transform{
			Select: []ColumnSpec{{Path: "field000"}, {Path: "field001"}},
			Drop:   []string{"field001"}, // ignored: Select is non-empty
		},
		Offset:    0,
		Limit:     1,
		WantTotal: true,
	})
	if err != nil {
		t.Fatalf("QueryRows error = %v, want nil", err)
	}
	if len(rs.Columns) != 2 {
		t.Fatalf("len(rs.Columns) = %d, want 2 (Drop is ignored when Select is non-empty)", len(rs.Columns))
	}
	if rs.ColumnsTruncated {
		t.Fatalf("rs.ColumnsTruncated = true, want false (Select present -> unbounded projection, Drop's presence doesn't matter)")
	}
	if rs.TotalPaths != len(rs.Columns) {
		t.Fatalf("rs.TotalPaths = %d, want %d (== len(rs.Columns): projected set is not truncated)", rs.TotalPaths, len(rs.Columns))
	}
}

// TestOpenResult_JSONShape asserts tag conformance: the frontend reads these
// exact field names off the wire (spec §8's DTO boundary), so a marshaled
// OpenResult's raw JSON must contain "columnsTruncated"/"totalPaths", not
// e.g. Go's default field-name casing.
func TestOpenResult_JSONShape(t *testing.T) {
	res := OpenResult{Handle: "h1", ColumnsTruncated: true, TotalPaths: 42}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal(OpenResult) error = %v, want nil", err)
	}
	s := string(b)
	if !strings.Contains(s, `"columnsTruncated"`) {
		t.Fatalf("marshaled JSON = %s, want it to contain \"columnsTruncated\"", s)
	}
	if !strings.Contains(s, `"totalPaths"`) {
		t.Fatalf("marshaled JSON = %s, want it to contain \"totalPaths\"", s)
	}
}

// --- GetCell (E6 Task 2) ----------------------------------------------------

func TestEngine_GetCell_FullValueNestedPath(t *testing.T) {
	maps := []map[string]any{
		{"user": map[string]any{"name": "alice", "age": json.Number("30")}},
		{"user": map[string]any{"name": "bob"}},
	}
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}

	// A dotted path resolves the same way the table's columns do.
	cell, err := e.GetCell(context.Background(), CellRequest{Handle: res.Handle, Index: 0, Path: "user.name"})
	if err != nil {
		t.Fatalf("GetCell: %v", err)
	}
	if !cell.Found {
		t.Fatalf("Found = false, want true")
	}
	if string(cell.Value) != `"alice"` {
		t.Fatalf("Value = %s, want \"alice\"", cell.Value)
	}

	// The whole nested object at "user" comes back in full.
	cell, err = e.GetCell(context.Background(), CellRequest{Handle: res.Handle, Index: 0, Path: "user"})
	if err != nil {
		t.Fatalf("GetCell user: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(cell.Value, &obj); err != nil || obj["name"] != "alice" {
		t.Fatalf("Value = %s (err %v), want the full user object", cell.Value, err)
	}
}

func TestEngine_GetCell_FoundDistinguishesMissingFromNull(t *testing.T) {
	maps := []map[string]any{
		{"a": nil, "b": "x"}, // a present but explicit null
		{"b": "y"},           // a absent
	}
	path := writeNDJSONFile(t, maps)
	e := NewEngine()
	res, err := e.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}

	cell, err := e.GetCell(context.Background(), CellRequest{Handle: res.Handle, Index: 0, Path: "a"})
	if err != nil {
		t.Fatalf("GetCell null: %v", err)
	}
	if !cell.Found || string(cell.Value) != "null" {
		t.Fatalf("explicit null: Found=%v Value=%s, want true/null", cell.Found, cell.Value)
	}
	cell, err = e.GetCell(context.Background(), CellRequest{Handle: res.Handle, Index: 1, Path: "a"})
	if err != nil {
		t.Fatalf("GetCell missing: %v", err)
	}
	if cell.Found || string(cell.Value) != "null" {
		t.Fatalf("missing: Found=%v Value=%s, want false/null", cell.Found, cell.Value)
	}
}

func TestEngine_GetCell_UnknownHandleErrors(t *testing.T) {
	e := NewEngine()
	if _, err := e.GetCell(context.Background(), CellRequest{Handle: "nope", Index: 0, Path: "x"}); err == nil {
		t.Fatalf("GetCell(unknown handle) err = nil, want error")
	}
}
