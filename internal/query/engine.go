package query

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/hoijun-kim/shape/internal/profile"
)

// Engine is the handle registry behind the shape data-explorer's query
// surface (spec §2/§8): OpenSource builds/selects a Backend for a source and
// returns an opaque handle string; QueryRows/CloseSource (and, in later
// tasks, CountMatches/Codegen/ExportQuery/GetCell/Cancel) look the backend up
// by that handle. A mutex guards only the registry map itself -- once a
// Backend is looked up, its own methods handle their own concurrency (spec
// §8: "each scan opens its own file handle, so ops on one handle run
// concurrently; a mutex guards only the caches").
type Engine struct {
	mu       sync.Mutex
	backends map[string]Backend
	next     uint64
}

// NewEngine returns an empty Engine with no open sources.
func NewEngine() *Engine {
	return &Engine{backends: make(map[string]Backend)}
}

// OpenRequest is the OpenSource request DTO (spec §8). Format is an explicit
// override ("json"|"ndjson"|"csv"|"parquet"|"sqlite"); "" auto-detects via
// readers.DetectFormat. BudgetMB <= 0 defaults to 512 (spec's
// DefaultMemBudgetBytes).
type OpenRequest struct {
	Path     string `json:"path"`
	Format   string `json:"format,omitempty"`
	Table    string `json:"table,omitempty"`
	CSVRaw   bool   `json:"csvRaw,omitempty"`
	BudgetMB int    `json:"budgetMB,omitempty"`
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
func (e *Engine) OpenSource(req OpenRequest) (OpenResult, error) {
	if req.Path == "" || req.Path == "-" {
		return OpenResult{}, fmt.Errorf("query: OpenSource: a real file path is required (stdin/empty rejected, spec §2)")
	}

	backend, format, tier, err := openBackend(req)
	if err != nil {
		return OpenResult{}, err
	}

	n, exact := backend.RowCount()
	var warnings []string
	if tier == "rescan" {
		warnings = append(warnings, "large file — streaming mode (totals are estimates)")
	}

	handle := e.register(backend)
	return OpenResult{
		Handle:      handle,
		Format:      string(format),
		Tier:        tier,
		Columns:     backend.Columns().Columns,
		Profile:     adaptProfile(backend.Profile()),
		Sampled:     tier == "rescan",
		RowEstimate: n,
		RowExact:    exact,
		Warnings:    warnings,
	}, nil
}

// QueryRows compiles req's Filter/Transform against the handle's Backend and
// runs Query over the requested window (spec §2/§8).
func (e *Engine) QueryRows(req QueryRequest) (RowSet, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return RowSet{}, err
	}
	plan, err := CompilePlan(req.Filter, req.Transform, backend.Columns())
	if err != nil {
		return RowSet{}, fmt.Errorf("query: QueryRows: %w", err)
	}
	return backend.Query(context.Background(), plan, Window{Offset: req.Offset, Limit: req.Limit}, req.WantTotal)
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
