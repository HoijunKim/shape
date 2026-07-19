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
