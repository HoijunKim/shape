package query

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/hoijun-kim/shape/internal/pipeline"
	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers"
)

// var _ Backend ensures rescanBackend satisfies the Backend interface at
// compile time (mirrors memstore.go's compile-time check).
var _ Backend = (*rescanBackend)(nil)

// rescanBackend is the Tier-2 stateless-scan Backend (spec §4): built when
// OpenSource's ingest pass exceeds its memory budget for a JSON/CSV source.
// It holds no decoded records at all -- every Query/Count/Export call
// re-opens the source path fresh (via pipeline.OpenSource + readers.Open)
// and streams through it once, so memory use is O(Window.Limit * columns)
// regardless of file size, and each call gets its OWN *os.File, making
// concurrent/re-entrant scans over the same handle trivially safe (spec §2:
// "each scan gets its own *os.File").
//
// rawFormat/csvRaw mirror the readers.Source knobs OpenSource's ingest pass
// used when it originally opened the path (the explicit format override --
// e.g. "ndjson" -- and the CSV raw-string-values flag): a re-scan MUST
// reproduce them so a downgraded source is read identically to how it would
// have been read had it stayed under budget (this is a deliberate, narrow
// extension of the task brief's suggested newRescanBackend signature --
// noted in the task report -- required for correctness on CSVRaw=true or an
// explicit json/ndjson override; readers.Format itself is always the same
// since format detection does not change between the ingest pass and a
// downgrade).
type rescanBackend struct {
	path      string
	format    readers.Format
	rawFormat string
	csvRaw    bool

	cm   *ColumnModel
	prof profile.ProfileResult

	avgBytes    float64 // average decoded-record bytes sampled during the ingest pass (0 if unknown)
	fileSize    int64   // on-disk file size in bytes (0 if unknown)
	rowEstimate int64   // fileSize/avgBytes, precomputed once (spec §4's unfiltered-total formula)
}

// newRescanBackend builds a rescanBackend for an already-detected path/format,
// given the ColumnModel and (possibly partial-sample) ProfileResult computed
// by OpenSource's ingest pass before it hit budget, plus that pass's sampled
// avgBytes-per-record and the source's on-disk fileSize (spec §4).
func newRescanBackend(path string, format readers.Format, rawFormat string, csvRaw bool, cm *ColumnModel, prof profile.ProfileResult, avgBytes float64, fileSize int64) *rescanBackend {
	return &rescanBackend{
		path:        path,
		format:      format,
		rawFormat:   rawFormat,
		csvRaw:      csvRaw,
		cm:          cm,
		prof:        prof,
		avgBytes:    avgBytes,
		fileSize:    fileSize,
		rowEstimate: estimateRowCount(fileSize, avgBytes),
	}
}

// estimateRowCount returns the spec §4 unfiltered-total estimate
// (fileSize/avgBytesPerRecord), or 0 when either input is unknown/non-
// positive (no sample was ever taken, or the file size could not be
// determined) -- 0 is paired with RowCount's exact=false and Query's
// TotalExact=false, so a caller never mistakes it for an exact empty result.
func estimateRowCount(fileSize int64, avgBytes float64) int64 {
	if fileSize <= 0 || avgBytes <= 0 {
		return 0
	}
	return int64(math.Round(float64(fileSize) / avgBytes))
}

// Columns returns the ColumnModel computed by OpenSource's ingest sample.
func (r *rescanBackend) Columns() *ColumnModel { return r.cm }

// Profile returns the ProfileResult computed by OpenSource's ingest sample
// (a SAMPLE, not the whole file: the ingest pass stopped as soon as the
// budget was exceeded, per spec §1/§4 -- a rescanBackend's sidebar structure
// map is necessarily best-effort).
func (r *rescanBackend) Profile() profile.ProfileResult { return r.prof }

// RowCount returns the fileSize/avgBytes ESTIMATE, never exact (spec §4:
// "RowCount: (estimate, false)").
func (r *rescanBackend) RowCount() (n int64, exact bool) {
	return r.rowEstimate, false
}

// openStream re-opens r.path fresh (pipeline.OpenSource + readers.Open),
// reproducing the exact readers.Source knobs (RawFormat/CSVRaw) the ingest
// pass used, and returns a single close func that releases both the reader
// stream and the underlying file handle, in that order.
func (r *rescanBackend) openStream() (readers.RecordStream, func() error, error) {
	src, closeSrc, err := pipeline.OpenSource(r.path)
	if err != nil {
		return nil, nil, fmt.Errorf("query: rescanBackend: reopen %s: %w", r.path, err)
	}
	src.RawFormat = r.rawFormat
	src.CSVRaw = r.csvRaw

	stream, closeStream, err := readers.Open(r.format, src)
	if err != nil {
		_ = closeSrc()
		return nil, nil, fmt.Errorf("query: rescanBackend: open reader for %s: %w", r.path, err)
	}
	return stream, func() error {
		err1 := closeStream()
		err2 := closeSrc()
		if err1 != nil {
			return err1
		}
		return err2
	}, nil
}

// cancelCheckStride is how often (in raw records read) rescanBackend's scans
// check ctx.Err(), per spec §4 ("~every 4096 records" -- matches
// memBackend's computeMatchBitset/Export discipline in memstore.go).
const cancelCheckStride = 4096

// scanFunc is called once per raw record read from the stream, in file
// order, with the record's 0-based raw ordinal (idx). Returning stop==true
// ends the scan early without reading further; a non-nil err both ends the
// scan and is returned by scan (used by Export to propagate an encoder
// failure without treating it as an early stop).
type scanFunc func(idx int64, rec any) (stop bool, err error)

// scan re-opens the source (openStream) and streams every record through fn,
// in file order, checking ctx for cancellation every cancelCheckStride
// records read. It is the one shared loop Query/Count/Export build on, so
// the re-open, decode-error-wrapping, and cancellation discipline live in
// exactly one place.
func (r *rescanBackend) scan(ctx context.Context, fn scanFunc) error {
	stream, closeStream, err := r.openStream()
	if err != nil {
		return err
	}
	defer closeStream()

	for idx := int64(0); ; idx++ {
		if idx%cancelCheckStride == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		rec, nerr := stream.Next()
		if errors.Is(nerr, io.EOF) {
			return nil
		}
		if nerr != nil {
			return fmt.Errorf("query: rescanBackend: read %s: %w", r.path, nerr)
		}
		stop, ferr := fn(idx, rec)
		if ferr != nil {
			return ferr
		}
		if stop {
			return nil
		}
	}
}

// isMatchAllFilter reports whether cf is the "match everything" compiled
// filter (a nil *CompiledFilter, or the pred-less result of compiling an
// empty Filter -- see filter.go's CompileFilter). rescanBackend.Query uses
// this to pick between the free fileSize/avgBytes total ESTIMATE (unfiltered:
// every record matches, so no scan-time counting is needed to know the
// total) and the matched-so-far count a real filter requires (spec §4).
func isMatchAllFilter(cf *CompiledFilter) bool {
	return cf == nil || cf.pred == nil
}

// Query streams the source once, applying p's compiled filter to every
// record, in file order (spec §4): records matching before the window
// (the first w.Offset MATCHES) are decoded and tested but never projected;
// the next w.Limit MATCHES are projected via p.Transform.Project into
// RowSet.Rows. Memory is O(w.Limit * columns), independent of file size.
//
// When wantTotal is false, the scan stops as soon as the window is full
// (spec's early-stop); when wantTotal is true, the scan continues to EOF so
// RowSet.Total reflects the fuller picture -- but, per spec §4, Query NEVER
// performs a dedicated exact-count pass for a real filter (that is
// Backend.Count's/CountMatches' job, spec §4/§8): RowSet.TotalExact is
// always false for rescanBackend.Query, whether Total ends up being the free
// fileSize/avgBytes ESTIMATE (an empty/match-all filter) or a matched-so-far
// count (a real filter, which happens to equal the exact total when the scan
// reaches true EOF, but is reported as an inexact lower bound regardless --
// the caller wanting a guaranteed-exact filtered count uses Count).
func (r *rescanBackend) Query(ctx context.Context, p *CompiledPlan, w Window, wantTotal bool) (RowSet, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return RowSet{}, err
	}
	if p == nil {
		return RowSet{}, fmt.Errorf("query: rescanBackend.Query: nil CompiledPlan")
	}

	limit := w.Limit
	if limit < 0 {
		limit = 0
	}
	offset := w.Offset
	if offset < 0 {
		offset = 0
	}
	unfiltered := isMatchAllFilter(p.Filter)

	rows := make([]Row, 0, limit)
	var scanned int64
	var matched int64

	err := r.scan(ctx, func(idx int64, rec any) (bool, error) {
		scanned = idx + 1
		if p.Filter.Match(rec) {
			matched++
			if matched > offset && int64(len(rows)) < int64(limit) {
				rows = append(rows, p.Transform.Project(rec, idx))
			}
		}
		if !wantTotal && int64(len(rows)) >= int64(limit) {
			return true, nil // early-stop: window full, caller does not need a total
		}
		return false, nil
	})
	if err != nil {
		return RowSet{}, err
	}

	rs := RowSet{
		Columns:   p.Transform.Columns(),
		Rows:      rows,
		Offset:    w.Offset,
		Scanned:   scanned,
		Truncated: int64(len(rows)) < int64(limit),
		ElapsedMs: time.Since(start).Milliseconds(),
	}

	switch {
	case !wantTotal:
		rs.Total = -1
		rs.TotalExact = false
	case unfiltered:
		rs.Total = r.rowEstimate
		rs.TotalExact = false
	default:
		rs.Total = matched
		rs.TotalExact = false
	}
	return rs, nil
}

// Count runs a full, cancellable scan counting every record f matches (spec
// §4: "a full cancellable scan (exact)"). Unlike Query it never early-stops
// on a window -- there is no window -- so a successful (uncancelled) result
// is always the exact total.
func (r *rescanBackend) Count(ctx context.Context, f *CompiledFilter) (total int64, exact bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	var matched int64
	scanErr := r.scan(ctx, func(idx int64, rec any) (bool, error) {
		if f.Match(rec) {
			matched++
		}
		return false, nil
	})
	if scanErr != nil {
		return 0, false, scanErr
	}
	return matched, true, nil
}

// Export streams the WHOLE source once, projecting and encoding every
// matching record in file order, regardless of any interactive-tier window
// (spec §4/§8's "export is never capped" escape hatch: identical shape to
// memBackend.Export, just re-reading from disk instead of RAM).
func (r *rescanBackend) Export(ctx context.Context, p *CompiledPlan, enc RowEncoder) (rows int64, err error) {
	if p == nil {
		return 0, fmt.Errorf("query: rescanBackend.Export: nil CompiledPlan")
	}
	if enc == nil {
		return 0, fmt.Errorf("query: rescanBackend.Export: nil RowEncoder")
	}
	var n int64
	scanErr := r.scan(ctx, func(idx int64, rec any) (bool, error) {
		if !p.Filter.Match(rec) {
			return false, nil
		}
		if err := enc.Encode(p.Transform.Project(rec, idx)); err != nil {
			return true, err
		}
		n++
		return false, nil
	})
	if scanErr != nil {
		return n, scanErr
	}
	return n, nil
}

// Close is a no-op: rescanBackend holds no persistent resource between
// calls (every scan opens and closes its own file handle in openStream/scan).
func (r *rescanBackend) Close() error { return nil }
