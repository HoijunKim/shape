package query

import (
	"context"

	"github.com/hoijun-kim/shape/internal/profile"
)

// Window selects a bounded [Offset, Offset+Limit) slice of a Backend's
// MATCHING rows -- offset/limit apply to the filtered result, not to raw
// record position (spec §4).
type Window struct {
	Offset int64 `json:"offset"`
	Limit  int   `json:"limit"`
}

// RowSet is the result of one Backend.Query call (spec §4). Total/TotalExact
// describe the match count (not the window size): Total == -1 means unknown
// (a backend that skipped counting because wantTotal was false), TotalExact
// == false means Total is an estimate or a lower bound (e.g. rescanBackend
// past its memory budget) rather than an exact count.
type RowSet struct {
	Columns    []Column `json:"columns"`
	Rows       []Row    `json:"rows"`
	Offset     int64    `json:"offset"`
	Total      int64    `json:"total"`      // -1 = unknown
	TotalExact bool     `json:"totalExact"` // false = estimate or lower bound
	Scanned    int64    `json:"scanned"`
	Truncated  bool     `json:"truncated"` // fewer than Limit rows: EOF reached
	ElapsedMs  int64    `json:"elapsedMs"`

	// ColumnsTruncated and TotalPaths surface spec §3's wide-data bound: the
	// column set is capped at MaxColumns (keeping highest-presence first, then
	// first-seen), so a source with more distinct paths than that reports
	// ColumnsTruncated=true and TotalPaths = the uncapped count. The UI shows
	// "showing 512 of N columns". Note this is NOT RowSet.Truncated, which
	// means "fewer rows than Limit: EOF reached".
	ColumnsTruncated bool `json:"columnsTruncated"`
	TotalPaths       int  `json:"totalPaths"`
}

// RowEncoder is the minimal streaming sink Backend.Export writes projected
// rows into, one at a time, in match order. It is intentionally the smallest
// possible interface: format-specific encoders (JSON/NDJSON/CSV/Parquet
// writers, Task 8/E4) implement Encode however they need to (buffering,
// flushing, converting Row's Cells to their wire representation); Export
// itself only needs to hand rows over one by one and react to a write
// failure by aborting the scan.
type RowEncoder interface {
	Encode(row Row) error
}

// Backend evaluates a CompiledPlan over one opened source (spec §4). The
// four concrete backends -- memBackend (this task), rescanBackend,
// sqlBackend, parquetBackend (later tasks) -- all evaluate the SAME
// CompiledFilter/CompiledTransform pair, so a caller sees identical
// Query/Count/Export results regardless of which backend an OpenSource call
// picked; only performance/exactness characteristics (RowSet.TotalExact,
// RowCount's exact flag) differ by tier.
type Backend interface {
	// Columns returns the source's base ColumnModel (before any
	// Transform is applied).
	Columns() *ColumnModel

	// Profile returns the sidebar structure map computed when the source
	// was opened.
	Profile() profile.ProfileResult

	// RowCount returns the source's total record count and whether that
	// count is exact (true for mem/sqlite/parquet; false -- an estimate --
	// for rescanBackend past its memory budget). A cancelled ctx returns
	// (0, false).
	RowCount(ctx context.Context) (n int64, exact bool)

	// Query runs p (a compiled Filter+Transform) over the source and
	// returns the window [w.Offset, w.Offset+w.Limit) of MATCHING,
	// PROJECTED rows, in source record order. wantTotal requests an
	// exact/estimated match count via RowSet.Total/TotalExact; a backend
	// may skip computing it when wantTotal is false (RowSet.Total may then
	// be a lower bound or -1), trading an up-front count pass for latency
	// on a tier where counting is expensive (rescanBackend). ctx governs
	// cancellation of long-running scans.
	Query(ctx context.Context, p *CompiledPlan, w Window, wantTotal bool) (RowSet, error)

	// Count returns the total number of records matching f (exact for
	// mem/sqlite/parquet; an estimate/cancellable full scan for
	// rescanBackend -- see CountMatches at the Wails-binding layer).
	Count(ctx context.Context, f *CompiledFilter) (total int64, exact bool, err error)

	// Export streams every matching, projected row through enc, in source
	// order, regardless of any interactive-tier cap: export always
	// produces the complete result, never a capped/sampled view (spec
	// §4/§8, the "export is never capped" escape hatch).
	Export(ctx context.Context, p *CompiledPlan, enc RowEncoder) (rows int64, err error)

	// Close releases any resources (open file handles, caches) held by the
	// backend. It is safe to call once a backend is no longer needed;
	// further calls to the other methods after Close are not guaranteed to
	// work.
	Close() error
}
