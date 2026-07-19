package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/hoijun-kim/shape/internal/diff"
	"github.com/hoijun-kim/shape/internal/pipeline"
	"github.com/hoijun-kim/shape/internal/query"
	"github.com/hoijun-kim/shape/internal/visual"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// sourceEngine is the subset of *query.Engine that App drives. Extracted so a
// test can substitute a fake that gates OpenSource's completion deterministically
// (house pattern: channels, not sleeps -- mirrors internal/query/engine_test.go's
// gatedCountingQueryBackend), which query.Engine's own unexported registry
// cannot be hooked into from this package. *query.Engine satisfies this
// structurally; see the compile-time assertion below.
type sourceEngine interface {
	OpenSource(ctx context.Context, req query.OpenRequest) (query.OpenResult, error)
	QueryRows(ctx context.Context, req query.QueryRequest) (query.RowSet, error)
	CountMatches(ctx context.Context, req query.CountRequest) (query.CountResult, error)
	Cancel(requestID string) error
	CloseSource(handle string) error
}

var _ sourceEngine = (*query.Engine)(nil)

// App is the Wails-bound application. Every exported method becomes a callable
// TypeScript binding. App owns exactly one query.Engine and, at most one open
// source at a time: opening a new one closes the previous handle so a memory-
// tier store (up to 512 MiB) or a sqlite connection is never leaked.
type App struct {
	ctx context.Context
	eng sourceEngine

	mu      sync.Mutex
	handle  string // current open source handle; "" when none
	openSeq uint64 // ordering counter; see resolveOpenSeq

	// openGate, if non-nil, runs at the very top of OpenSource -- before
	// resolveOpenSeq's bookkeeping. Production code (NewApp) never sets it;
	// it exists so a test can pin, deterministically, which of two
	// concurrent OpenSource calls reaches that bookkeeping first (I-1's
	// regression test, TestAppOpenSourceHonorsInvertedGenArrival). Reading it
	// without a.mu is safe: a test always finishes constructing the *App
	// (including this field) before the `go` statements that later read it,
	// and the Go memory model guarantees a goroutine's start happens-after
	// everything that precedes its `go` statement.
	openGate func()
}

func NewApp() *App { return &App{eng: query.NewEngine()} }

// startup captures the runtime context (wired via OnStartup, not a binding).
func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// shutdown releases the last open source's backend at teardown (wired via
// OnShutdown, not a binding). Without this the last open handle's Close --
// e.g. a sqlite connection's db.Close() -- never runs; process exit reclaims
// the OS-level resource anyway, so this is not a live leak, but a graceful
// close is still the right thing to do rather than relying on that.
//
// M-1 review fix: bumping a.openSeq here, in the SAME critical section that
// clears a.handle, forces any OpenSource call still inside a.eng.OpenSource
// at this moment to find itself stale once it returns (its captured mySeq
// can no longer equal the post-bump a.openSeq) -- see resolveOpenSeq. Without
// this, such a call would complete after shutdown, see prev == "" (this
// method already cleared a.handle), and set a.handle = its own handle: a
// live backend re-adopted after teardown, and the very backend this method
// exists to close is then never closed by anyone.
func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	h := a.handle
	a.handle = ""
	a.openSeq++
	a.mu.Unlock()
	if h != "" {
		_ = a.eng.CloseSource(h)
	}
}

// reqCtx returns the context requests run under. a.ctx is nil until Wails
// calls startup, and the Go tests never call it, so fall back to Background
// rather than passing a nil ctx into the engine.
func (a *App) reqCtx() context.Context {
	if a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}

// openGenRequestIDPrefix is the RequestID prefix store.ts's open() sends:
// "open" followed by its own monotonically-increasing `gen` counter (e.g.
// "open2"). Any other RequestID -- "", an unparseable value, or one from a
// caller that isn't store.ts (store.ts is OpenSource's sole caller in the
// running app; the Go tests in this package are the other caller and always
// leave RequestID "") -- falls back to resolveOpenSeq's legacy a.openSeq++
// scheme.
const openGenRequestIDPrefix = "open"

// parseOpenGen extracts N from a RequestID of the form "open<N>". ok is false
// for "", for anything not matching that exact shape, or for a suffix that
// doesn't parse as a uint64.
func parseOpenGen(requestID string) (n uint64, ok bool) {
	rest := strings.TrimPrefix(requestID, openGenRequestIDPrefix)
	if rest == requestID || rest == "" { // no prefix, or prefix with nothing after it
		return 0, false
	}
	v, err := strconv.ParseUint(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// resolveOpenSeq computes this OpenSource call's position in the open
// ordering and records it into a.openSeq.
//
// I-1 review fix: the previous scheme (a.openSeq++ at the top of OpenSource)
// assigned mySeq by ARRIVAL order at this lock -- but Wails v2.12.0 runs
// every binding call in its own goroutine
// (internal/frontend/desktop/windows/frontend.go:756), so two overlapping
// OpenSource calls race to reach this bookkeeping, and whichever goroutine's
// call the Go scheduler happens to run first got the lower (i.e. "older")
// number, REGARDLESS of which one store.ts actually issued first. JS issues
// open(A) then open(B) and treats B as current; if goroutine B won that
// race, Go would assign B mySeq=1 and A mySeq=2 -- Go then adopts A and
// self-closes B, and every later QueryRows(B's handle) fails "unknown
// handle": the original symptom, reached by a goroutine-scheduling route
// instead of a completion-order one.
//
// The fix is to stop deriving ordering from arrival order at all. store.ts
// is OpenSource's sole caller and already owns a `gen` counter that decides
// what the UI displays; that counter is incremented synchronously in real
// JS call order (JS is single-threaded), so when RequestID parses as
// store.ts's "open<N>", N -- not this method's own arrival order -- is
// authoritative: a.openSeq becomes max(a.openSeq, N) rather than an
// unconditional increment. max is commutative, so whichever of two racing
// goroutines reaches this critical section first, the final a.openSeq (and
// each call's own mySeq, its own N) comes out the same either way --
// ordering no longer depends on Go's goroutine scheduling, only on values
// store.ts already computed correctly. See
// TestAppOpenSourceHonorsInvertedGenArrival, which pins exactly the
// "goroutine B's bookkeeping runs first despite carrying the OLDER gen"
// inversion this closes.
//
// Any RequestID that doesn't parse this way (including "", the Go tests'
// only caller convention) falls back to the original a.openSeq++ scheme, so
// non-store callers are unaffected.
func (a *App) resolveOpenSeq(requestID string) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n, ok := parseOpenGen(requestID); ok {
		if n > a.openSeq {
			a.openSeq = n
		}
		return n
	}
	a.openSeq++
	return a.openSeq
}

// OpenSource opens a data file for exploration and returns its structure map,
// column set, and tier (spec §8). Any previously open source is closed first.
//
// Two overlapping OpenSource calls (a slow rescan-tier open racing a fast one
// started after it) can COMPLETE in either order -- completion order alone
// must never decide which one "wins". mySeq (see resolveOpenSeq) is
// established once per call, at the moment it starts; after the (possibly
// slow) a.eng.OpenSource call returns, mySeq is compared against the CURRENT
// a.openSeq. If a newer call has started in the meantime, this one is stale:
// it must not touch a.handle (a newer open may already have become current
// and must not be displaced by an older one finishing late) and must close
// its OWN just-opened backend itself (rather than relying on the JS layer's
// store.ts:78 generation guard, which cannot see a Go-side completion-order
// race at all -- it only guards its own JS-side gen counter). Closing our
// own handle here is safe even if the JS side also races to close it later:
// CloseSource on an already-closed/unknown handle just errors, which callers
// already ignore (see CloseSource below).
func (a *App) OpenSource(req query.OpenRequest) (query.OpenResult, error) {
	if a.openGate != nil {
		a.openGate()
	}
	mySeq := a.resolveOpenSeq(req.RequestID)

	res, err := a.eng.OpenSource(a.reqCtx(), req)
	if err != nil {
		return query.OpenResult{}, err
	}

	a.mu.Lock()
	stale := mySeq != a.openSeq
	var prev string
	if !stale {
		prev = a.handle
		a.handle = res.Handle
	}
	a.mu.Unlock()

	if stale {
		_ = a.eng.CloseSource(res.Handle) // never displace a newer open; this handle is ours to clean up
		return res, nil
	}
	if prev != "" && prev != res.Handle {
		_ = a.eng.CloseSource(prev) // best effort: a stale handle is not the caller's problem
	}
	return res, nil
}

// QueryRows returns one window of rows for an open handle (spec §8).
func (a *App) QueryRows(req query.QueryRequest) (query.RowSet, error) {
	return a.eng.QueryRows(a.reqCtx(), req)
}

// CountMatches returns the exact match count for a filter (spec §8).
func (a *App) CountMatches(req query.CountRequest) (query.CountResult, error) {
	return a.eng.CountMatches(a.reqCtx(), req)
}

// Cancel interrupts an in-flight request by id (spec §8). An unknown id is a
// normal race (the request may have just finished) and returns an error the
// caller may ignore.
func (a *App) Cancel(requestID string) error { return a.eng.Cancel(requestID) }

// CloseSource releases a handle's backend.
func (a *App) CloseSource(handle string) error {
	a.mu.Lock()
	if a.handle == handle {
		a.handle = ""
	}
	a.mu.Unlock()
	return a.eng.CloseSource(handle)
}

// ProfileFile returns the visual dashboard model for a data file.
func (a *App) ProfileFile(path string) (visual.VisualModel, error) {
	r, err := pipeline.Profile(pipeline.Options{Path: path, Format: "auto"})
	if err != nil {
		return visual.VisualModel{}, err
	}
	return visual.FromProfile(r, visual.Options{Name: r.Source}), nil
}

// DiffFiles returns the visual comparison model between two data files.
func (a *App) DiffFiles(oldPath, newPath string) (visual.DiffVisualModel, error) {
	oldR, err := pipeline.Profile(pipeline.Options{Path: oldPath, Format: "auto"})
	if err != nil {
		return visual.DiffVisualModel{}, err
	}
	newR, err := pipeline.Profile(pipeline.Options{Path: newPath, Format: "auto"})
	if err != nil {
		return visual.DiffVisualModel{}, err
	}
	d := diff.Diff(oldR, newR)
	return visual.FromDiff(d), nil
}

// SchemaJSON returns the inferred JSON Schema as a pretty-printed string.
func (a *App) SchemaJSON(path string) (string, error) {
	s, err := pipeline.Schema(pipeline.Options{Path: path, Format: "auto"})
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// OpenFileDialog opens the native picker (runtime-only; "" when cancelled).
func (a *App) OpenFileDialog() (string, error) {
	return wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Open a data file"})
}

// SaveText prompts for a save path and writes content there (runtime-only).
func (a *App) SaveText(defaultName, content string) (string, error) {
	path, err := wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{DefaultFilename: defaultName})
	if err != nil || path == "" {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
