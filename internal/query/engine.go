package query

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hoijun-kim/shape/internal/profile"
)

// Engine is the handle registry behind the shape data-explorer's query
// surface (spec §2/§8): OpenSource builds/selects a Backend for a source and
// returns an opaque handle string; QueryRows/CloseSource/Cancel (and, in
// later tasks, CountMatches/Codegen/ExportQuery/GetCell) look the backend/
// in-flight request up by that handle/RequestID. A mutex guards only the
// registry maps themselves -- once a Backend is looked up, its own methods
// handle their own concurrency (spec §8: "each scan opens its own file
// handle, so ops on one handle run concurrently; a mutex guards only the
// caches").
type Engine struct {
	mu       sync.Mutex
	backends map[string]Backend
	next     uint64

	// inflight maps a caller-supplied RequestID to the CancelFunc of the ctx
	// that request is running under, so Cancel(requestID) can interrupt a
	// long scan from another goroutine (the GUI's stale-scroll and
	// "stop counting" paths, spec §8). Entries are removed when the request
	// returns; an empty RequestID is never registered. gen distinguishes two
	// requests that reused the same id, so a finishing older request cannot
	// unregister the newer one that superseded it.
	inflight map[string]inflightEntry
	gen      uint64
}

type inflightEntry struct {
	cancel context.CancelFunc
	gen    uint64
}

// NewEngine returns an empty Engine with no open sources.
func NewEngine() *Engine {
	return &Engine{
		backends: make(map[string]Backend),
		inflight: make(map[string]inflightEntry),
	}
}

// begin derives a cancellable ctx for requestID and registers it. The returned
// release function cancels the derived ctx and unregisters it, and is safe to
// call exactly once via defer. An empty requestID is not registered (the
// request is simply uncancellable) but still gets its own derived ctx, so the
// release path is uniform. Registering a requestID that is already in flight
// cancels the older one: a caller reusing an id means "supersede", which is
// exactly what a fast scroll wants.
func (e *Engine) begin(ctx context.Context, requestID string) (context.Context, func()) {
	cctx, cancel := context.WithCancel(ctx)
	if requestID == "" {
		return cctx, cancel
	}
	e.mu.Lock()
	if prev, ok := e.inflight[requestID]; ok {
		prev.cancel()
	}
	e.gen++
	myGen := e.gen
	e.inflight[requestID] = inflightEntry{cancel: cancel, gen: myGen}
	e.mu.Unlock()

	return cctx, func() {
		e.mu.Lock()
		// Only unregister if this request still owns the id: a newer request
		// may have superseded it, and that entry must survive.
		if cur, ok := e.inflight[requestID]; ok && cur.gen == myGen {
			delete(e.inflight, requestID)
		}
		e.mu.Unlock()
		cancel()
	}
}

// Cancel interrupts the in-flight request registered under requestID (spec
// §8). It returns an error if no such request is running -- which is a normal
// race (the request may have just finished), so callers may ignore it.
func (e *Engine) Cancel(requestID string) error {
	e.mu.Lock()
	entry, ok := e.inflight[requestID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("query: Cancel: no in-flight request %q", requestID)
	}
	entry.cancel()
	return nil
}

// inFlightCount reports how many requests are registered. Test-only.
func (e *Engine) inFlightCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.inflight)
}

// OpenRequest is the OpenSource request DTO (spec §8). Format is an explicit
// override ("json"|"ndjson"|"csv"|"parquet"|"sqlite"); "" auto-detects via
// readers.DetectFormat. BudgetMB <= 0 defaults to 512 (spec's
// DefaultMemBudgetBytes).
type OpenRequest struct {
	// RequestID, if non-empty, registers this open with the Engine's cancel
	// registry (Engine.Cancel, spec §8) under the same requestId ->
	// requestID path QueryRequest uses. This is a documented deviation from
	// spec §8 (which has no RequestID on OpenRequest): opening a large
	// sqlite/parquet file runs a full-file profiling pass before it ever
	// returns a handle, and that pass should be cancellable exactly like a
	// query is.
	RequestID string `json:"requestId,omitempty"`
	Path      string `json:"path"`
	Format    string `json:"format,omitempty"`
	Table     string `json:"table,omitempty"`
	CSVRaw    bool   `json:"csvRaw,omitempty"`
	BudgetMB  int    `json:"budgetMB,omitempty"`
}

// OpenResult is the OpenSource response DTO (spec §8). Tier reports which
// Backend strategy was chosen ("memory"|"rescan"|"sqlite"|"parquet");
// Sampled/RowEstimate/RowExact describe how trustworthy RowEstimate is
// (RowExact is true only for memory/sqlite/parquet tiers -- see
// Backend.RowCount).
type OpenResult struct {
	Handle      string     `json:"handle"`
	Format      string     `json:"format"`
	Tier        string     `json:"tier"`
	Columns     []Column   `json:"columns"`
	Profile     ProfileDTO `json:"profile"`
	Sampled     bool       `json:"sampled"`
	RowEstimate int64      `json:"rowEstimate"`
	RowExact    bool       `json:"rowExact"`
	Warnings    []string   `json:"warnings,omitempty"`

	// ColumnsTruncated and TotalPaths surface spec §3's wide-data bound:
	// OpenResult.Columns is always the source's base ColumnModel (OpenSource
	// has no Transform -- unlike RowSet.Columns, see backend.go, which is a
	// Transform's PROJECTED output and so needs its own, conditional version
	// of this comment). The base column set is capped at MaxColumns (keeping
	// highest-presence first, then first-seen), so a source with more
	// distinct paths than that reports ColumnsTruncated=true and TotalPaths =
	// the uncapped count; otherwise ColumnsTruncated=false and TotalPaths ==
	// len(Columns). The UI shows "showing 512 of N columns". Note this is NOT
	// RowSet.Truncated, which means "fewer rows than Limit: EOF reached".
	ColumnsTruncated bool `json:"columnsTruncated"`
	TotalPaths       int  `json:"totalPaths"`
}

// CountRequest is the CountMatches request DTO (spec §8).
type CountRequest struct {
	RequestID string `json:"requestId,omitempty"`
	Handle    string `json:"handle"`
	Filter    Filter `json:"filter"`
}

// CountResult is the CountMatches response DTO (spec §8). Exact is false when
// the backend can only supply a lower bound or an estimate.
type CountResult struct {
	Total     int64 `json:"total"`
	Exact     bool  `json:"exact"`
	ElapsedMs int64 `json:"elapsedMs"`
}

// CountMatches returns how many records match req.Filter (spec §8): the
// cancellable, cached exact count behind the UI's "counting..." affordance,
// used when a tier can only report a lower bound or estimate from Query.
// On the memory tier it shares Query's match bitset (both key on
// CompiledFilter.Key()), so counting a filter already scrolled is free.
func (e *Engine) CountMatches(ctx context.Context, req CountRequest) (CountResult, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return CountResult{}, err
	}
	cf, err := CompileFilter(req.Filter, backend.Columns())
	if err != nil {
		return CountResult{}, fmt.Errorf("query: CountMatches: %w", err)
	}
	ctx, release := e.begin(ctx, req.RequestID)
	defer release()

	start := time.Now()
	total, exact, err := backend.Count(ctx, cf)
	if err != nil {
		return CountResult{}, err
	}
	return CountResult{Total: total, Exact: exact, ElapsedMs: time.Since(start).Milliseconds()}, nil
}

// QueryRequest is the QueryRows request DTO (spec §8).
type QueryRequest struct {
	RequestID string    `json:"requestId,omitempty"`
	Handle    string    `json:"handle"`
	Filter    Filter    `json:"filter"`
	Transform Transform `json:"transform"`
	Offset    int64     `json:"offset"`
	Limit     int       `json:"limit"`
	WantTotal bool      `json:"wantTotal"`
}

// ProfileDTO adapts profile.ProfileResult to a TS-friendly shape (spec §8):
// map[profile.JSONKind]float64 becomes an ordered []TypeShare (sorted by
// Kind, so encoding never depends on Go's randomized map iteration -- the
// engine's determinism constraint, spec §9).
type ProfileDTO struct {
	Records int        `json:"records"`
	Skipped int        `json:"skipped"`
	Fields  []FieldDTO `json:"fields"`
}

// FieldDTO adapts one profile.FieldProfile (spec §8).
type FieldDTO struct {
	Path          string       `json:"path"`
	Types         []TypeShare  `json:"types"`
	Presence      float64      `json:"presence"`
	NullRate      float64      `json:"nullRate"`
	Distinct      int          `json:"distinct"`
	DistinctExact bool         `json:"distinctExact"`
	Min           *float64     `json:"min,omitempty"`
	Max           *float64     `json:"max,omitempty"`
	TopValues     []ValueCount `json:"topValues,omitempty"`
	Drift         bool         `json:"drift"`
}

// TypeShare is one FieldDTO.Types entry: the share (0..1) of a field's
// observed values that classified as Kind.
type TypeShare struct {
	Kind  string  `json:"kind"`
	Share float64 `json:"share"`
}

// ValueCount adapts profile.ValueCount for the DTO layer (json tags).
type ValueCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// adaptProfile converts a profile.ProfileResult into its DTO shape (spec §8).
func adaptProfile(pr profile.ProfileResult) ProfileDTO {
	fields := make([]FieldDTO, len(pr.Fields))
	for i, fp := range pr.Fields {
		fields[i] = FieldDTO{
			Path:          fp.Path,
			Types:         typeShares(fp.TypeDist),
			Presence:      fp.PresenceRate,
			NullRate:      fp.NullRate,
			Distinct:      fp.DistinctCount,
			DistinctExact: fp.DistinctExact,
			Min:           sanitizeFloatPtr(fp.Min),
			Max:           sanitizeFloatPtr(fp.Max),
			TopValues:     adaptTopValues(fp.TopValues),
			Drift:         profile.IsTypeDrift(fp),
		}
	}
	return ProfileDTO{Records: pr.Records, Skipped: pr.Skipped, Fields: fields}
}

// typeShares renders a FieldProfile.TypeDist map as a []TypeShare sorted by
// Kind (bytewise), so the DTO never depends on Go's randomized map iteration
// order (spec §9: "avoid Go map-iteration dependence").
func typeShares(dist map[profile.JSONKind]float64) []TypeShare {
	if len(dist) == 0 {
		return nil
	}
	kinds := make([]string, 0, len(dist))
	for k := range dist {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	shares := make([]TypeShare, len(kinds))
	for i, k := range kinds {
		shares[i] = TypeShare{Kind: k, Share: dist[profile.JSONKind(k)]}
	}
	return shares
}

// adaptTopValues converts []profile.ValueCount (already presence-ranked by
// the accumulator, see accumulator.go's topValues) to the DTO's []ValueCount,
// preserving order.
func adaptTopValues(vs []profile.ValueCount) []ValueCount {
	if len(vs) == 0 {
		return nil
	}
	out := make([]ValueCount, len(vs))
	for i, v := range vs {
		out[i] = ValueCount{Value: v.Value, Count: v.Count}
	}
	return out
}

// OpenSource opens req.Path, selects/builds a Backend per readers.DetectFormat
// (openBackend, source.go), and registers it under a new handle (spec
// §1/§2). A stdin path ("" or "-") is rejected: a stateless/re-scannable
// engine needs a real, re-openable path (spec §2).
//
// ctx is threaded through the whole open (openBackend's per-format
// constructor and the RowCount call below), and -- via req.RequestID -- is
// registered in the Engine's Cancel registry exactly like QueryRows'
// (e.begin/Engine.Cancel, spec §8): opening a large sqlite/parquet file or
// ingesting a large JSON/CSV source runs a full pass before a handle is ever
// returned, and that pass must be cancellable exactly like a query is. A ctx
// that dies at ANY point up to and including the RowCount call below (not
// just inside openBackend) makes OpenSource return an error and register
// nothing -- see the ctx.Err() re-check after RowCount.
func (e *Engine) OpenSource(ctx context.Context, req OpenRequest) (OpenResult, error) {
	if req.Path == "" || req.Path == "-" {
		return OpenResult{}, fmt.Errorf("query: OpenSource: a real file path is required (stdin/empty rejected, spec §2)")
	}
	ctx, release := e.begin(ctx, req.RequestID)
	defer release()

	backend, format, tier, err := openBackend(ctx, req)
	if err != nil {
		return OpenResult{}, err
	}

	n, exact := backend.RowCount(ctx)
	// IMPORTANT-1 review fix: RowCount collapses a dead ctx to (0, false) per
	// its documented contract (see e.g. memBackend.RowCount) rather than
	// erroring, so without this re-check a ctx that dies anywhere between
	// openBackend returning and RowCount running would otherwise fall
	// through to a "successful" (err == nil) OpenResult -- indistinguishable
	// from a genuinely empty source -- AND register backend's handle, which
	// nothing would ever CloseSource, leaking it. Close it and report the
	// cancellation instead of registering.
	if err := ctx.Err(); err != nil {
		backend.Close()
		return OpenResult{}, err
	}
	var warnings []string
	if tier == "rescan" {
		warnings = append(warnings, "large file — streaming mode (totals are estimates)")
	}

	handle := e.register(backend)
	res := OpenResult{
		Handle:      handle,
		Format:      string(format),
		Tier:        tier,
		Profile:     adaptProfile(backend.Profile()),
		Sampled:     tier == "rescan",
		RowEstimate: n,
		RowExact:    exact,
		Warnings:    warnings,
	}
	// CQ-7 review fix: guard backend.Columns() the same way QueryRows does
	// (engine.go) rather than dereferencing it unconditionally -- every real
	// Backend always returns a non-nil ColumnModel today, so this is
	// defense-in-depth, but it should be the SAME posture in both places
	// rather than QueryRows alone being defensive.
	if cm := backend.Columns(); cm != nil {
		res.Columns = cm.Columns
		res.ColumnsTruncated = cm.Truncated
		res.TotalPaths = cm.TotalPaths
	}
	return res, nil
}

// QueryRows compiles req's Filter/Transform against the handle's Backend and
// runs Query over the requested window (spec §2/§8). ctx is threaded into
// the Backend.Query call, and -- via req.RequestID -- is registered in the
// Engine's Cancel registry for the duration of the call (e.begin/
// Engine.Cancel, spec §8), so a caller can interrupt a scan already in
// progress (e.g. the GUI's stale-scroll/"stop counting" paths) rather than
// only a request that has not started yet.
func (e *Engine) QueryRows(ctx context.Context, req QueryRequest) (RowSet, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return RowSet{}, err
	}
	plan, err := CompilePlan(req.Filter, req.Transform, backend.Columns())
	if err != nil {
		return RowSet{}, fmt.Errorf("query: QueryRows: %w", err)
	}
	ctx, release := e.begin(ctx, req.RequestID)
	defer release()
	rs, err := backend.Query(ctx, plan, Window{Offset: req.Offset, Limit: req.Limit}, req.WantTotal)
	if err != nil {
		return RowSet{}, err
	}
	// every Backend.Query sets RowSet.Columns from p.Transform.Columns() --
	// the PROJECTED column set -- not from backend.Columns() (the base
	// ColumnModel). Whether the base model's truncation numbers still apply
	// to that projection depends on Select alone, NOT on isIdentityTransform
	// (Select empty AND Drop empty): Select, when non-empty, is an explicit,
	// unbounded projection (naming a path overrides MaxColumns entirely --
	// see its doc comment, columns.go), so ColumnsTruncated is always false
	// and TotalPaths always == len(rs.Columns) whenever Select is used.
	// Select-EMPTY, by contrast, always inherits the base model's own
	// truncation verbatim, whether or not Drop is also present:
	// CompileTransform's Select-empty path builds from baseOutCols(cm),
	// which starts from cm.Columns -- the ALREADY-CAPPED slice
	// (transform.go) -- and Drop (applyDrop) only ever subtracts named
	// entries from that capped starting point, so it can never reach a path
	// the cap already excluded. A Drop-only transform over a truncated base
	// model therefore hides columns for two independent reasons (the cap,
	// plus the drop), and cm.Truncated/cm.TotalPaths -- not
	// len(rs.Columns) -- are what describe the cap's contribution.
	if len(req.Transform.Select) == 0 {
		if cm := backend.Columns(); cm != nil {
			rs.ColumnsTruncated = cm.Truncated
			rs.TotalPaths = cm.TotalPaths
		}
	} else {
		rs.ColumnsTruncated = false
		rs.TotalPaths = len(rs.Columns)
	}
	return rs, nil
}

// CloseSource closes and unregisters handle's Backend. Calling QueryRows (or
// any other Engine method) with handle afterward returns an error rather
// than panicking (the handle is simply no longer found).
func (e *Engine) CloseSource(handle string) error {
	e.mu.Lock()
	backend, ok := e.backends[handle]
	if ok {
		delete(e.backends, handle)
	}
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("query: CloseSource: unknown handle %q", handle)
	}
	return backend.Close()
}

// register assigns backend a new, unique handle string and stores it.
func (e *Engine) register(backend Backend) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.next++
	handle := fmt.Sprintf("h%d", e.next)
	e.backends[handle] = backend
	return handle
}

// lookup returns the Backend registered under handle, or an error if no such
// handle exists (never open, or already closed via CloseSource).
func (e *Engine) lookup(handle string) (Backend, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	backend, ok := e.backends[handle]
	if !ok {
		return nil, fmt.Errorf("query: unknown handle %q", handle)
	}
	return backend, nil
}
