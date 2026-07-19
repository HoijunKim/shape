package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoijun-kim/shape/internal/query"
)

const sampleNDJSON = "../internal/cmd/testdata/sample.ndjson"

func TestAppProfileFile(t *testing.T) {
	a := NewApp()
	vm, err := a.ProfileFile("../internal/cmd/testdata/sample.ndjson")
	if err != nil {
		t.Fatalf("ProfileFile: %v", err)
	}
	if vm.Summary.Records != 3 {
		t.Errorf("records = %d, want 3", vm.Summary.Records)
	}
	if len(vm.KPIs) != 5 {
		t.Errorf("len(KPIs) = %d, want 5", len(vm.KPIs))
	}
	if len(vm.Fields) == 0 {
		t.Fatal("Fields is empty")
	}
	if vm.Summary.Format == "" {
		t.Error("Summary.Format is empty")
	}
}

func TestAppDiffFiles(t *testing.T) {
	a := NewApp()
	dvm, err := a.DiffFiles("../internal/cmd/testdata/diff_old.ndjson", "../internal/cmd/testdata/diff_new.ndjson")
	if err != nil {
		t.Fatalf("DiffFiles: %v", err)
	}
	if !dvm.Breaking {
		t.Error("Breaking = false, want true")
	}
	if dvm.Verdict != "Breaking changes" {
		t.Errorf("Verdict = %q, want %q", dvm.Verdict, "Breaking changes")
	}
}

func TestAppSchemaJSON(t *testing.T) {
	a := NewApp()
	s, err := a.SchemaJSON("../internal/cmd/testdata/sample.ndjson")
	if err != nil {
		t.Fatalf("SchemaJSON: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		t.Fatalf("schema not JSON: %v\n%s", err, s)
	}
	if !strings.Contains(s, "draft/2020-12") {
		t.Errorf("expected a Draft 2020-12 schema:\n%s", s)
	}
}

func TestAppOpenSourceAndQueryRows(t *testing.T) {
	a := NewApp() // nil ctx: startup is never called
	res, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	if res.Handle == "" {
		t.Error("Handle is empty")
	}
	if res.Tier != "memory" {
		t.Errorf("Tier = %q, want %q", res.Tier, "memory")
	}
	if len(res.Columns) == 0 {
		t.Error("Columns is empty")
	}
	if len(res.Profile.Fields) == 0 {
		t.Error("Profile.Fields is empty")
	}

	rs, err := a.QueryRows(query.QueryRequest{Handle: res.Handle, Limit: 10, WantTotal: true})
	if err != nil {
		t.Fatalf("QueryRows: %v", err)
	}
	if len(rs.Rows) == 0 {
		t.Fatal("Rows is empty")
	}
	if rs.Rows[0].Index != 0 {
		t.Errorf("Rows[0].Index = %d, want 0", rs.Rows[0].Index)
	}
	if len(rs.Rows[0].Cells) != len(rs.Columns) {
		t.Errorf("len(Rows[0].Cells) = %d, want %d (len(Columns))", len(rs.Rows[0].Cells), len(rs.Columns))
	}
}

func TestAppOpenSourceClosesPrevious(t *testing.T) {
	a := NewApp()
	first, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON})
	if err != nil {
		t.Fatalf("OpenSource (1st): %v", err)
	}
	second, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON})
	if err != nil {
		t.Fatalf("OpenSource (2nd): %v", err)
	}
	if first.Handle == second.Handle {
		t.Fatalf("expected distinct handles, got %q both times", first.Handle)
	}
	if _, err := a.QueryRows(query.QueryRequest{Handle: first.Handle, Limit: 10}); err == nil {
		t.Error("QueryRows on the closed first handle: want error, got nil")
	}
}

// gatedOpenEngine wraps a real *query.Engine and, on its FIRST OpenSource
// call only, blocks (after signalling gateEntered) until the test releases
// releaseFirst -- mirroring internal/query/engine_test.go's
// gatedCountingQueryBackend: no sleep, no scheduling-luck timing margin. This
// lets a test deterministically start call A, wait until it has genuinely
// entered the engine (proven by gateEntered firing), start call B and let it
// run to completion FIRST, and only then release A -- reproducing the
// "last-to-COMPLETE, not last-to-START" race from the CRITICAL review finding
// with channels pinning the order instead of gambling on goroutine scheduling.
type gatedOpenEngine struct {
	*query.Engine
	calls        int64
	gateEntered  chan struct{}
	releaseFirst chan struct{}
}

func (g *gatedOpenEngine) OpenSource(ctx context.Context, req query.OpenRequest) (query.OpenResult, error) {
	if atomic.AddInt64(&g.calls, 1) == 1 {
		close(g.gateEntered)
		<-g.releaseFirst
	}
	return g.Engine.OpenSource(ctx, req)
}

// TestAppOpenSourceNeverDisplacesANewerOpen is the CRITICAL review fix's
// required regression test: it drives two OpenSource calls (A, started
// first; B, started second) whose COMPLETION order is deliberately inverted
// -- A is gated so B finishes first -- and asserts that B (the second-
// started, first-completed handle) remains the live one: QueryRows on it
// must still succeed once both calls have returned. Without the openSeq
// guard, A's late completion would see a.handle == B (set when B finished)
// and, believing itself newer just because it finished last, overwrite
// a.handle with its own handle AND close B's backend out from under the
// still-current view -- exactly the "open slow file A, open fast file B, B
// renders ready, A then closes B" trace in the review. This is verified by
// reverting the openSeq fix in app.go: the test fails (QueryRows on B's
// handle then errors "unknown handle") -- see the task report for the exact
// failure output.
func TestAppOpenSourceNeverDisplacesANewerOpen(t *testing.T) {
	g := &gatedOpenEngine{
		Engine:       query.NewEngine(),
		gateEntered:  make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	a := &App{eng: g}

	type openOutcome struct {
		res query.OpenResult
		err error
	}
	firstDone := make(chan openOutcome, 1)
	go func() {
		res, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON}) // A: started first
		firstDone <- openOutcome{res, err}
	}()

	select {
	case <-g.gateEntered: // A has genuinely entered the engine and is blocked there
	case <-time.After(5 * time.Second):
		t.Fatal("first OpenSource call (A) never reached the engine")
	}

	second, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON}) // B: started second, runs to completion FIRST
	if err != nil {
		t.Fatalf("OpenSource (B, second-started): %v", err)
	}

	close(g.releaseFirst) // only now let A (first-started) proceed to completion

	var first openOutcome
	select {
	case first = <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first OpenSource call (A) never returned after being released")
	}
	if first.err != nil {
		t.Fatalf("OpenSource (A, first-started): %v", first.err)
	}
	if first.res.Handle == second.Handle {
		t.Fatalf("expected distinct handles for A and B, got %q both times", first.res.Handle)
	}

	// The crux of the fix: B (second-started, first-completed) must still be
	// queryable -- A's late completion must not have closed it out from
	// under the caller.
	if _, err := a.QueryRows(query.QueryRequest{Handle: second.Handle, Limit: 10}); err != nil {
		t.Errorf("QueryRows(B's handle) after A's inverted-order completion: %v, want nil", err)
	}
	// A's own late-arriving open must self-close rather than becoming
	// current: it must not still be the live handle, and must itself be
	// unqueryable now (it closed its own backend on the stale path).
	if _, err := a.QueryRows(query.QueryRequest{Handle: first.res.Handle, Limit: 10}); err == nil {
		t.Error("QueryRows(A's handle) after A went stale: want error (self-closed), got nil")
	}
	// M-2: pin a.handle itself, not just queryability. Both assertions above
	// would also pass if the fix erroneously left a.handle == "" (a real
	// leak: B's backend would stay registered in the engine forever, since
	// the next open() only closes whatever a.handle currently names, and
	// CloseSource(B) is never called by anyone in that scenario) -- B would
	// still answer QueryRows (it's still registered in the engine, just not
	// tracked as "current"), and A would still be unqueryable (self-closed on
	// the stale path either way).
	a.mu.Lock()
	gotHandle := a.handle
	a.mu.Unlock()
	if gotHandle != second.Handle {
		t.Errorf("a.handle = %q, want %q (B's handle)", gotHandle, second.Handle)
	}
}

// TestAppOpenSourceHonorsInvertedGenArrival is I-1's required regression
// test. It uses App's openGate hook, which runs at the very start of
// OpenSource -- before resolveOpenSeq's bookkeeping -- to pin
// deterministically which of two concurrent OpenSource calls reaches that
// bookkeeping first. This is a DIFFERENT seam than gatedOpenEngine above:
// gatedOpenEngine can only pin COMPLETION order (its gate sits inside
// a.eng.OpenSource, which resolveOpenSeq has already run by the time either
// call reaches it); openGate pins bookkeeping-ARRIVAL order, which is what
// this test needs to reproduce I-1 (a race at `a.mu.Lock(); a.openSeq++`
// itself, not at the engine call after it).
//
// It is deliberately a DIFFERENT race than
// TestAppOpenSourceNeverDisplacesANewerOpen's: that test inverts COMPLETION
// order (A starts first, B finishes first) and is already satisfied by the
// openSeq guard alone, independent of I-1. This test inverts
// BOOKKEEPING-ARRIVAL order instead -- the exact race I-1 describes: A (JS
// gen "open1", the file opened FIRST) is gated so its resolveOpenSeq call
// runs LAST, while B (JS gen "open2", opened SECOND) runs its resolveOpenSeq
// call, and its entire OpenSource call, to completion FIRST -- entirely
// before A's bookkeeping runs at all. Note COMPLETION order here is
// perfectly normal (B finishes before A even starts) -- the only inversion
// is in which goroutine's bookkeeping the Go scheduler happened to run
// first, independent of which gen it carries.
//
// Under the OLD a.openSeq++ scheme (arrival-order-based, blind to
// RequestID), this alone reproduces the CRITICAL finding's bug: whichever
// call's bookkeeping runs first gets assigned the LOWER internal number, so
// B (running first here) would get mySeq=1 and A (running second) mySeq=2 --
// old code would then treat A, not B, as "current" once A finishes: it
// adopts A's handle and closes B's out from under the caller, exactly
// backwards from what store.ts's own gen counter (and the user) expects.
// This is the same failure shape as the CRITICAL finding, reached via a
// goroutine-scheduling route instead of a completion-order one -- see
// OpenSource's and resolveOpenSeq's doc comments.
//
// The fix (parsing N out of RequestID and folding it into a.openSeq via
// max(), see resolveOpenSeq) makes the result depend only on the gen VALUES
// carried in RequestID, not on which call's bookkeeping happens to run
// first, so B (gen 2) must still be the adopted handle here despite running
// through bookkeeping and the engine call entirely before A's bookkeeping
// even starts.
func TestAppOpenSourceHonorsInvertedGenArrival(t *testing.T) {
	gateEntered := make(chan struct{})
	releaseA := make(chan struct{})
	var gateCalls int64

	a := &App{eng: query.NewEngine()}
	a.openGate = func() {
		// Only the FIRST call to reach the gate blocks. Because the test
		// below starts A's goroutine and waits for gateEntered (proving A is
		// genuinely blocked here) before calling B at all, A is guaranteed to
		// be the first call to reach this gate and B the second -- so A
		// blocks and B passes straight through, regardless of how the Go
		// scheduler would otherwise have interleaved them.
		if atomic.AddInt64(&gateCalls, 1) == 1 {
			close(gateEntered)
			<-releaseA
		}
	}

	type openOutcome struct {
		res query.OpenResult
		err error
	}
	aDone := make(chan openOutcome, 1)
	go func() {
		res, err := a.OpenSource(query.OpenRequest{RequestID: "open1", Path: sampleNDJSON}) // A: older gen, opened first
		aDone <- openOutcome{res, err}
	}()

	select {
	case <-gateEntered: // A has genuinely reached openGate and is blocked there, before its own resolveOpenSeq call
	case <-time.After(5 * time.Second):
		t.Fatal("A (open1) never reached openGate")
	}

	// B: newer gen, opened second -- runs its ENTIRE OpenSource call
	// (bookkeeping through completion) while A remains blocked before its
	// own bookkeeping.
	b, err := a.OpenSource(query.OpenRequest{RequestID: "open2", Path: sampleNDJSON})
	if err != nil {
		t.Fatalf("OpenSource (B, open2): %v", err)
	}

	close(releaseA) // only now let A's bookkeeping, and the rest of its call, run

	var aOut openOutcome
	select {
	case aOut = <-aDone:
	case <-time.After(5 * time.Second):
		t.Fatal("A (open1) never returned after being released")
	}
	if aOut.err != nil {
		t.Fatalf("OpenSource (A, open1): %v", aOut.err)
	}
	if aOut.res.Handle == b.Handle {
		t.Fatalf("expected distinct handles for A and B, got %q both times", b.Handle)
	}

	// The crux: B (the newer gen) must be the adopted handle, even though its
	// bookkeeping+completion ran entirely before A's bookkeeping even started.
	a.mu.Lock()
	gotHandle := a.handle
	a.mu.Unlock()
	if gotHandle != b.Handle {
		t.Errorf("a.handle = %q, want %q (B's handle, the newer gen)", gotHandle, b.Handle)
	}
	if _, err := a.QueryRows(query.QueryRequest{Handle: b.Handle, Limit: 10}); err != nil {
		t.Errorf("QueryRows(B's handle): %v, want nil", err)
	}
	if _, err := a.QueryRows(query.QueryRequest{Handle: aOut.res.Handle, Limit: 10}); err == nil {
		t.Error("QueryRows(A's handle) after A went stale: want error (self-closed), got nil")
	}
}

func TestAppQueryRowsUnknownHandle(t *testing.T) {
	a := NewApp()
	if _, err := a.QueryRows(query.QueryRequest{Handle: "no-such-handle", Limit: 10}); err == nil {
		t.Error("QueryRows with an unknown handle: want error, got nil")
	}
}

func TestAppCountMatches(t *testing.T) {
	a := NewApp()
	res, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	cr, err := a.CountMatches(query.CountRequest{Handle: res.Handle})
	if err != nil {
		t.Fatalf("CountMatches: %v", err)
	}
	if cr.Total != 3 {
		t.Errorf("Total = %d, want 3", cr.Total)
	}
	if !cr.Exact {
		t.Error("Exact = false, want true")
	}
}

func TestAppCancelUnknownRequest(t *testing.T) {
	a := NewApp() // nil ctx path
	if err := a.Cancel("no-such-request"); err == nil {
		t.Error("Cancel of an unknown request: want error, got nil")
	}
}

func TestAppCloseSourceThenQuery(t *testing.T) {
	a := NewApp()
	res, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	if err := a.CloseSource(res.Handle); err != nil {
		t.Fatalf("CloseSource: %v", err)
	}
	if _, err := a.QueryRows(query.QueryRequest{Handle: res.Handle, Limit: 10}); err == nil {
		t.Error("QueryRows after CloseSource: want error, got nil")
	}
	if err := a.CloseSource(res.Handle); err == nil {
		t.Error("second CloseSource: want error, got nil")
	}
}

// TestAppShutdownClosesLastOpenHandle pins MINOR-7's fix: wails' OnShutdown
// hook must release the last open source's backend at teardown (e.g. a
// sqlite handle's db.Close()), not just leave it to process exit. Regression:
// a no-op shutdown would leave the handle registered and this QueryRows call
// would still succeed instead of erroring.
func TestAppShutdownClosesLastOpenHandle(t *testing.T) {
	a := NewApp()
	res, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	a.shutdown(context.Background())
	if _, err := a.QueryRows(query.QueryRequest{Handle: res.Handle, Limit: 10}); err == nil {
		t.Error("QueryRows after shutdown: want error (handle closed at teardown), got nil")
	}
}

// TestAppShutdownWithNoOpenHandle covers the "nothing was ever opened" path
// (h == "" in shutdown): it must not panic or call CloseSource("").
func TestAppShutdownWithNoOpenHandle(t *testing.T) {
	a := NewApp()
	a.shutdown(context.Background()) // must not panic
}

// TestAppShutdownRacingInFlightOpenSourceDoesNotReadopt pins M-1: shutdown
// clears a.handle and closes it, but an OpenSource call still inside
// a.eng.OpenSource at that moment must not complete afterward and RE-ADOPT
// a.handle (it would find prev == "" -- shutdown already cleared it -- and
// read that as "nothing to close", setting a.handle to its own just-opened
// handle). That backend is then never closed by anyone: exactly the "defeats
// the shutdown commit in the one case it targets" bug M-1 describes. The fix
// bumps a.openSeq inside shutdown's own critical section, so such a call
// finds itself stale once it returns and self-closes instead of adopting.
func TestAppShutdownRacingInFlightOpenSourceDoesNotReadopt(t *testing.T) {
	g := &gatedOpenEngine{
		Engine:       query.NewEngine(),
		gateEntered:  make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	a := &App{eng: g}

	type openOutcome struct {
		res query.OpenResult
		err error
	}
	openDone := make(chan openOutcome, 1)
	go func() {
		res, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON})
		openDone <- openOutcome{res, err}
	}()

	select {
	case <-g.gateEntered: // the open has genuinely entered the engine and is blocked there
	case <-time.After(5 * time.Second):
		t.Fatal("OpenSource never reached the engine")
	}

	a.shutdown(context.Background()) // teardown races the still-in-flight open

	close(g.releaseFirst) // now let the open proceed to completion

	var out openOutcome
	select {
	case out = <-openDone:
	case <-time.After(5 * time.Second):
		t.Fatal("OpenSource never returned after being released")
	}
	if out.err != nil {
		t.Fatalf("OpenSource: %v", out.err)
	}

	// The crux: the late-completing open must have self-closed, not
	// re-adopted a.handle post-teardown.
	a.mu.Lock()
	gotHandle := a.handle
	a.mu.Unlock()
	if gotHandle != "" {
		t.Errorf(`a.handle = %q after shutdown raced an in-flight open, want "" (must not re-adopt post-teardown)`, gotHandle)
	}
	if _, err := a.QueryRows(query.QueryRequest{Handle: out.res.Handle, Limit: 10}); err == nil {
		t.Error("QueryRows on the late-completing open's handle: want error (self-closed), got nil")
	}
}

func TestAppRowSetMarshals(t *testing.T) {
	a := NewApp()
	res, err := a.OpenSource(query.OpenRequest{Path: sampleNDJSON})
	if err != nil {
		t.Fatalf("OpenSource: %v", err)
	}
	rs, err := a.QueryRows(query.QueryRequest{Handle: res.Handle, Limit: 10, WantTotal: true})
	if err != nil {
		t.Fatalf("QueryRows: %v", err)
	}
	b, err := json.Marshal(rs)
	if err != nil {
		t.Fatalf("json.Marshal(RowSet): %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"columnsTruncated"`) {
		t.Errorf("marshaled RowSet missing %q:\n%s", "columnsTruncated", s)
	}
	if !strings.Contains(s, `"cells"`) {
		t.Errorf("marshaled RowSet missing %q:\n%s", "cells", s)
	}
}
