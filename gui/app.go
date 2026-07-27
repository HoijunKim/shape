package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
	ExportQuery(ctx context.Context, req query.ExportRequest, progress func(rows int64)) (query.ExportResult, error)
	Codegen(req query.CodegenRequest) (query.Generated, error)
	GetCell(ctx context.Context, req query.CellRequest) (query.CellResult, error)
	ColumnStats(ctx context.Context, req query.ColumnStatsRequest) (query.ColumnStatsResult, error)
	SaveEdits(ctx context.Context, req query.SaveRequest, progress func(rows int64)) (query.SaveResult, error)
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

	// emit, if non-nil, replaces the Wails event emitter. Production code
	// (NewApp) never sets it; it exists because a.ctx is written only by
	// startup (see below) and the Go tests never call it, so a progress
	// emitter that went straight to wr.EventsEmit would emit NOTHING in a
	// test -- making the throttle it is supposed to prove untestable. A fake
	// ctx is not an option either: wails' getEvents demands a
	// frontend.Events value from a package this one cannot import. Same
	// nil-by-default seam as openGate above.
	emit func(event string, data map[string]any)
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

// exportProgressInterval is the minimum wall-clock gap between two
// shape:progress events for one export. The engine already coarsens its
// callback to every 4096 rows; this bounds the event rate on a fast source,
// where 4096 rows can go by in microseconds and a per-callback emit would
// flood the webview bridge with more messages than it can paint.
const exportProgressInterval = 200 * time.Millisecond

// ExportQuery writes the current filter+transform result to a file (spec §8).
//
// The engine reports progress through a plain Go callback; this is where that
// becomes a UI event. The first callback always emits (so the dialog updates
// immediately), and subsequent ones are dropped until exportProgressInterval
// has passed. "total" is -1 on purpose: the number of MATCHING rows is not
// known without a second full pass, and inventing a denominator would turn an
// honest row counter into a fake percentage.
func (a *App) ExportQuery(req query.ExportRequest) (query.ExportResult, error) {
	var mu sync.Mutex
	var last time.Time
	progress := func(rows int64) {
		mu.Lock()
		now := time.Now()
		if !last.IsZero() && now.Sub(last) < exportProgressInterval {
			mu.Unlock()
			return
		}
		last = now
		mu.Unlock()
		a.emitEvent("shape:progress", map[string]any{
			"requestId": req.RequestID,
			"scanned":   rows,
			"total":     int64(-1),
		})
	}
	return a.eng.ExportQuery(a.reqCtx(), req, progress)
}

// emitEvent sends a Wails event, or hands it to the test seam when one is
// installed. Before startup runs, a.ctx is nil and wails' own getEvents would
// log.Fatalf on it, so a nil ctx is a silent no-op rather than a crash.
func (a *App) emitEvent(event string, data map[string]any) {
	if a.emit != nil {
		a.emit(event, data)
		return
	}
	if a.ctx == nil {
		return
	}
	wr.EventsEmit(a.ctx, event, data)
}

// exportFileFilters maps an export format to the native save dialog's filter
// list. An unknown format falls back to "All files", so a new format can never
// make the picker unusable.
func exportFileFilters(format string) []wr.FileFilter {
	switch format {
	case "json":
		return []wr.FileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}}
	case "ndjson":
		return []wr.FileFilter{{DisplayName: "NDJSON (*.ndjson;*.jsonl)", Pattern: "*.ndjson;*.jsonl"}}
	case "csv":
		return []wr.FileFilter{{DisplayName: "CSV (*.csv)", Pattern: "*.csv"}}
	case "tsv":
		return []wr.FileFilter{{DisplayName: "TSV (*.tsv)", Pattern: "*.tsv"}}
	case "parquet":
		return []wr.FileFilter{{DisplayName: "Parquet (*.parquet)", Pattern: "*.parquet"}}
	default:
		return []wr.FileFilter{{DisplayName: "All files (*.*)", Pattern: "*.*"}}
	}
}

// SaveFileDialog opens the native save picker for an export and returns the
// chosen path ("" when cancelled). It writes nothing: ExportQuery does the
// writing, so a cancelled picker leaves no trace and a chosen path is only a
// destination until the export actually succeeds.
func (a *App) SaveFileDialog(defaultName, format string) (string, error) {
	return wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{
		Title:           "Export data",
		DefaultFilename: defaultName,
		Filters:         exportFileFilters(format),
	})
}

// Codegen returns the jq expression and SQL query equivalent to a request's
// filter+transform (spec §8). No ctx: codegen is pure and instant -- it reads
// the handle's format/table/columns and renders strings, never data -- and a
// ctx-less signature is also what keeps *query.Engine satisfying sourceEngine.
func (a *App) Codegen(req query.CodegenRequest) (query.Generated, error) {
	return a.eng.Codegen(req)
}

// GetCell returns the full, untruncated value of one cell (spec §8): the
// tree-view escape hatch behind a click on a truncated object/array cell. It
// is a pass-through like CountMatches -- it reads exactly one record, so it is
// effectively instant, but it still gets a.reqCtx() so it is cancelled at
// teardown like every other data-touching binding.
func (a *App) GetCell(req query.CellRequest) (query.CellResult, error) {
	return a.eng.GetCell(a.reqCtx(), req)
}

// ColumnStats returns the rich profile (visual FieldCard) of one source field
// for the sidebar's expandable stats view (E8). A reqCtx pass-through, like
// GetCell.
func (a *App) ColumnStats(req query.ColumnStatsRequest) (query.ColumnStatsResult, error) {
	return a.eng.ColumnStats(a.reqCtx(), req)
}

// SaveEdits writes a copy of the source with the cell-edit overlay applied
// (spec E7). Like ExportQuery it turns the engine's Go progress callback into a
// throttled shape:progress event so a fast save does not flood the bridge.
func (a *App) SaveEdits(req query.SaveRequest) (query.SaveResult, error) {
	var mu sync.Mutex
	var last time.Time
	progress := func(rows int64) {
		mu.Lock()
		now := time.Now()
		if !last.IsZero() && now.Sub(last) < exportProgressInterval {
			mu.Unlock()
			return
		}
		last = now
		mu.Unlock()
		a.emitEvent("shape:progress", map[string]any{
			"requestId": req.RequestID,
			"scanned":   rows,
			"total":     int64(-1),
		})
	}
	return a.eng.SaveEdits(a.reqCtx(), req, progress)
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

// viewsPath is the saved-views config file: <UserConfigDir>/shape/views.json.
func viewsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "shape", "views.json"), nil
}

// LoadViews returns the saved-views JSON blob, or "" if none has been saved yet.
// The payload is opaque here -- the frontend owns and validates the view schema
// (E11). An absent file is not an error (a fresh install).
func (a *App) LoadViews() (string, error) {
	path, err := viewsPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// saveViewsMu serializes SaveViews so two overlapping calls never race their
// os.Rename onto the same views.json -- on Windows MoveFileEx returns
// ERROR_ACCESS_DENIED when two renames target one destination concurrently, and
// the frontend fires SaveViews without awaiting (persistViews), so a save then a
// quick delete could otherwise drop one write. NOT a.mu (that guards handle).
var saveViewsMu sync.Mutex

// SaveViews atomically writes the saved-views JSON blob: a temp file in the same
// dir + os.Rename, so a crash mid-write never corrupts an existing views.json.
func (a *App) SaveViews(payload string) error {
	saveViewsMu.Lock()
	defer saveViewsMu.Unlock()
	path, err := viewsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "views-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(payload); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
