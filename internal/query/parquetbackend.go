// Package query: parquetBackend (spec §4, docs/superpowers/specs/2026-07-17-
// shape-engine-design.md) implements Backend for a FormatParquet source by
// reading the footer's row-group NumRows() for an exact, free Total, using
// parquet-go's GenericReader[any].SeekToRow (verified present -- see the
// task report) for O(window) random access on the empty-filter fast path,
// and falling back to a full sequential scan (the shared Go
// CompiledFilter/CompiledTransform, identical to mem/rescan/sqlBackend) for
// any non-empty filter. Row-level filtering/projection ALWAYS runs in Go, on
// fully-decoded records -- exactly like every other backend -- so results
// are byte-identical across all four backends by construction (spec §9).
package query

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers"
	"github.com/parquet-go/parquet-go"
)

// var _ Backend ensures parquetBackend satisfies the Backend interface at
// compile time (mirrors memstore.go/rescan.go/sqlbackend.go's compile-time
// checks).
var _ Backend = (*parquetBackend)(nil)

// parquetBatchSize bounds how many decoded rows parquetBackend reads per
// GenericReader.Read call (mirrors internal/readers/parquetreader's own
// batchSize=256 -- same reasonable amortization constant, kept local since
// parquetreader does not export it).
const parquetBatchSize = 256

// parquetBackend is the Parquet-native Backend (spec §4). Unlike sqlBackend
// (one long-lived connection) it holds only the already-opened *os.File and
// *parquet.File (footer + row-group metadata, parsed once at open) and
// constructs a FRESH *parquet.GenericReader[any] per scan/window-read,
// closing it when that call returns: parquet.GenericReader.Close only
// releases the reader's own internal state (column decompression buffers,
// etc.) -- it never closes the underlying io.ReaderAt (verified by reading
// parquet-go's reader.go: Reader.Close calls the lowercase reader.Close,
// which is distinct from *os.File.Close; parquetreader.go's own cleanup
// closes gr AND f separately for the same reason) -- so many independent
// GenericReader instances can safely share one *parquet.File/*os.File across
// concurrent calls, exactly like *os.File.ReadAt is safe for concurrent use.
type parquetBackend struct {
	path string
	f    *os.File
	pf   *parquet.File

	total int64 // sum of every row-group's NumRows() from the footer -- exact, free (spec §4)

	cm   *ColumnModel
	prof profile.ProfileResult
}

// newParquetBackend opens path via parquet.OpenFile (mirroring
// internal/readers/parquetreader.go's open: os.Open, Stat, OpenFile --
// reused here directly rather than through readers.Open/RecordStream because
// this backend needs the untyped *parquet.File/GenericReader machinery
// itself -- SeekToRow, per-row-group NumRows(), and the raw Schema -- which
// readers.RecordStream's Next()/Skipped() interface does not expose; a
// duplicated ~15-line open+convert step is the same judgment call
// sqlbackend.go documents for its own connection helpers: a smaller, safer
// blast radius than changing parquetreader's unexported surface).
//
// Total is computed by summing every RowGroup's NumRows() (spec §4's exact
// wording), free (footer-only, no data page is read). The base ColumnModel
// is built from the Parquet schema's REAL column order (parquetSchemaOrder)
// passed as buildColumnModel's sourceOrder hint -- unlike JSON/CSV ingest,
// Parquet (like SQLite/CSV) has a true source order to offer (spec §3/§4).
// The ProfileResult is computed by running profile.Profiler over every
// decoded record via one full scan (consistent with mem/rescan/sqlBackend:
// "you may run the existing profiler over the rows ... OR a lighter
// per-column pass" -- this runs the real profiler for sidebar consistency).
//
// That full scan is cancellable: it runs through pb.scan(ctx, 0, ...), which
// checks ctx every cancelCheckStride rows exactly like every other scan this
// backend runs (Query/Count/Export), so a ctx that dies during this initial
// profiling pass aborts newParquetBackend with an error rather than running
// to completion uncancellably.
func newParquetBackend(ctx context.Context, path string) (*parquetBackend, error) {
	if path == "" {
		return nil, fmt.Errorf("query: parquet cannot be read from stdin; provide a file path")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("query: open parquet %s: %w", path, err)
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("query: stat parquet %s: %w", path, err)
	}
	pf, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("query: open parquet %s: %w", path, err)
	}

	var total int64
	for _, rg := range pf.RowGroups() {
		total += rg.NumRows()
	}

	pb := &parquetBackend{path: path, f: f, pf: pf, total: total}

	sourceOrder := parquetSchemaOrder(pf.Schema().Fields(), "")
	disc := newColumnDiscoverer()
	prof := profile.NewProfiler()
	if serr := pb.scan(ctx, 0, func(_ int64, rec any) (bool, error) {
		disc.Observe(rec)
		prof.AddRecord(rec)
		return false, nil
	}); serr != nil {
		f.Close()
		return nil, fmt.Errorf("query: profile parquet %s: %w", path, serr)
	}

	profResult := prof.Result()
	pb.prof = profResult
	pb.cm = buildColumnModel(disc, profResult, sourceOrder)
	return pb, nil
}

// parquetSchemaOrder walks fields (a parquet.Schema's Fields(), or a nested
// group's) and returns the dotted path order buildColumnModel's sourceOrder
// hint expects, matching columnDiscoverer/profile.Flatten's plain "."-join
// grammar:
//   - a repeated field (Parquet's array/list representation, whether a
//     repeated leaf column or a repeated group / list-of-struct) becomes ONE
//     path entry at this level, never descended into: array elements are
//     previews, not columns (spec §3), matching how columnDiscoverer stages
//     only the container path ("tags") for a decoded []any value and drops
//     any "tags[]" element path from the column candidate set.
//   - a non-repeated, non-leaf field (a nested struct/group, Parquet's other
//     way to NEST) recurses, building "parent.child" paths -- the same
//     dotted convention profile.Flatten/columnDiscoverer.walk use for a
//     decoded nested map[string]any.
//   - a leaf field (a plain scalar column) is one path entry.
func parquetSchemaOrder(fields []parquet.Field, prefix string) []string {
	var order []string
	for _, field := range fields {
		path := field.Name()
		if prefix != "" {
			path = prefix + "." + path
		}
		switch {
		case field.Repeated():
			order = append(order, path)
		case !field.Leaf():
			order = append(order, parquetSchemaOrder(field.Fields(), path)...)
		default:
			order = append(order, path)
		}
	}
	return order
}

// parquetToRecord recursively converts a decoded parquet.GenericReader[any]
// row value into the profiler/query-compatible value set
// (nil/bool/string/json.Number/float64 + nested map[string]any/[]any),
// mirroring internal/readers/parquetreader.go's convertDeep exactly (kept
// local for the same reason newParquetBackend's doc comment gives: this
// backend already needs its own open/read path, so it carries its own
// converter rather than importing an unexported helper from a sibling
// package).
func parquetToRecord(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, cv := range t {
			m[k] = parquetToRecord(cv)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, cv := range t {
			out[i] = parquetToRecord(cv)
		}
		return out
	default:
		return readers.ToProfileValue(v)
	}
}

// newReader constructs a fresh *parquet.GenericReader[any] over p.pf,
// starting at row 0 (see the parquetBackend doc comment: cheap, safe to
// create many of these, each an independent cursor over the shared
// footer-parsed file).
func (p *parquetBackend) newReader() *parquet.GenericReader[any] {
	return parquet.NewGenericReader[any](p.pf)
}

// scan reads every row from startRow to EOF through fn, in file (row) order,
// decoding each row via parquetToRecord and checking ctx for cancellation
// every cancelCheckStride rows (the same discipline/constant
// rescanBackend.scan and sqlBackend.scan use, rescan.go/sqlbackend.go). It is
// the one shared loop the profiling pass (newParquetBackend), Count's
// filtered path, Query's filtered path, and Export all build on.
//
// startRow > 0 seeks first (via GenericReader.SeekToRow, verified supported
// by parquet-go -- see the task report): fn's idx argument is always the
// ABSOLUTE row ordinal regardless of startRow, matching every other
// backend's Row.Index convention (spec §3: "absolute record ordinal").
func (p *parquetBackend) scan(ctx context.Context, startRow int64, fn scanFunc) error {
	gr := p.newReader()
	defer gr.Close()
	if startRow > 0 {
		if err := gr.SeekToRow(startRow); err != nil {
			return fmt.Errorf("query: parquetBackend: seek to row %d: %w", startRow, err)
		}
	}

	buf := make([]any, parquetBatchSize)
	idx := startRow
	// Hoisted above the outer loop (MINOR-4 review fix): the per-row check
	// below only fires for i in [0,n), so a zero-row file (or a file whose
	// row groups are all empty) would otherwise call gr.Read at least once
	// and return via the n==0 branch without ever consulting ctx at all,
	// silently ignoring an already-cancelled ctx. This check catches that
	// case unconditionally, before the first Read.
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		n, rerr := gr.Read(buf)
		for i := 0; i < n; i++ {
			if idx%cancelCheckStride == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			m, ok := buf[i].(map[string]any)
			if !ok {
				return fmt.Errorf("query: parquetBackend: unexpected row type %T", buf[i])
			}
			rec := parquetToRecord(m)
			stop, ferr := fn(idx, rec)
			idx++
			if ferr != nil {
				return ferr
			}
			if stop {
				return nil
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return rerr
		}
		if n == 0 {
			return nil // defensive: a (0, nil) read would otherwise spin (mirrors parquetreader.go's stream.Next)
		}
	}
}

// readWindow decodes up to limit rows starting at absolute row offset,
// seeking there directly (GenericReader.SeekToRow) rather than scanning from
// row 0: the empty-filter Query fast path's O(window), file-size-independent
// random access (spec §4's "compute covering row groups from the cumulative
// row prefix, seek" -- parquet-go's SeekToRow already resolves an absolute
// row index to its covering row group internally, so this backend does not
// need to walk row-group boundaries itself; see the task report).
func (p *parquetBackend) readWindow(ctx context.Context, offset int64, limit int) ([]any, error) {
	if limit <= 0 || offset >= p.total {
		return nil, nil
	}
	gr := p.newReader()
	defer gr.Close()
	if offset > 0 {
		if err := gr.SeekToRow(offset); err != nil {
			return nil, fmt.Errorf("query: parquetBackend: seek to row %d: %w", offset, err)
		}
	}

	recs := make([]any, 0, limit)
	buf := make([]any, parquetBatchSize)
	for len(recs) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		want := limit - len(recs)
		if want > len(buf) {
			want = len(buf)
		}
		n, rerr := gr.Read(buf[:want])
		for i := 0; i < n; i++ {
			m, ok := buf[i].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("query: parquetBackend: unexpected row type %T", buf[i])
			}
			recs = append(recs, parquetToRecord(m))
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return nil, rerr
		}
		if n == 0 {
			break
		}
	}
	return recs, nil
}

// --- Backend interface ---------------------------------------------------

// Columns returns the base ColumnModel built from the Parquet schema's real
// column order.
func (p *parquetBackend) Columns() *ColumnModel { return p.cm }

// Profile returns the sidebar structure map computed by newParquetBackend's
// one-time profiling pass.
func (p *parquetBackend) Profile() profile.ProfileResult { return p.prof }

// RowCount returns the footer's exact row count (spec §4: "(footerTotal,
// true)" -- always exact, since Parquet's metadata carries a real row
// count, no sampling involved). A cancelled ctx returns (0, false).
func (p *parquetBackend) RowCount(ctx context.Context) (n int64, exact bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	return p.total, true
}

// Query splits on whether the compiled filter is empty, exactly like
// sqlBackend.Query (sqlbackend.go):
//   - EMPTY filter: readWindow seeks straight to w.Offset and decodes only
//     w.Limit rows -- native random access via parquet-go's SeekToRow, no
//     scan of skipped rows at all (spec §4's headline parquetBackend win;
//     "seek path" in the task report).
//   - NON-EMPTY filter: no pushdown exists for an arbitrary Go predicate (and
//     row-group MIN/MAX pruning is an OPTIONAL, E1-deferred acceleration --
//     see queryFiltered's doc comment), so every row is streamed from row 0
//     via one scan; Offset/Limit apply to the MATCH sequence, not raw row
//     position -- the SAME algorithm rescanBackend.Query/sqlBackend.Query use
//     ("scan path" in the task report), required for the cross-backend
//     row-identity invariant (spec §9).
func (p *parquetBackend) Query(ctx context.Context, plan *CompiledPlan, w Window, wantTotal bool) (RowSet, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return RowSet{}, err
	}
	if plan == nil {
		return RowSet{}, fmt.Errorf("query: parquetBackend.Query: nil CompiledPlan")
	}

	limit := w.Limit
	if limit < 0 {
		limit = 0
	}
	offset := w.Offset
	if offset < 0 {
		offset = 0
	}

	if isMatchAllFilter(plan.Filter) {
		return p.queryUnfiltered(ctx, plan, w, offset, limit, wantTotal, start)
	}
	return p.queryFiltered(ctx, plan, w, offset, limit, wantTotal, start)
}

// queryUnfiltered is the empty-filter fast path: SeekToRow(offset) once, then
// decode exactly limit rows -- O(window), independent of file size. Total
// (when wantTotal) is the footer count: free, exact, no scan needed.
func (p *parquetBackend) queryUnfiltered(ctx context.Context, plan *CompiledPlan, w Window, offset int64, limit int, wantTotal bool, start time.Time) (RowSet, error) {
	recs, err := p.readWindow(ctx, offset, limit)
	if err != nil {
		return RowSet{}, err
	}
	rows := make([]Row, len(recs))
	for i, rec := range recs {
		rows[i] = plan.Transform.Project(rec, offset+int64(i))
	}

	rs := RowSet{
		Columns:   plan.Transform.Columns(),
		Rows:      rows,
		Offset:    w.Offset,
		Scanned:   offset + int64(len(rows)),
		Truncated: len(rows) < limit,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	if wantTotal {
		rs.Total = p.total
		rs.TotalExact = true
	} else {
		rs.Total = -1
		rs.TotalExact = false
	}
	return rs, nil
}

// queryFiltered is the non-empty-filter path: a single full scan from row 0,
// applying plan.Filter.Match in Go and collecting Offset/Limit over the
// MATCH sequence -- byte-for-byte the same algorithm as
// rescanBackend.Query/sqlBackend.queryFiltered (rescan.go/sqlbackend.go),
// which the cross-backend row-identity invariant (spec §9) requires.
//
// Row-group MIN/MAX/null-count pruning (spec §4: "skips groups that cannot
// match range/eq/isnull conjuncts") is explicitly OPTIONAL and is NOT
// implemented for E1 (noted in the task report, mirroring how the spec
// itself calls this out as skippable): the Go predicate over every row is
// the source of truth regardless, so skipping pruning costs latency on a
// selective filter over a huge file, never correctness -- consistent with
// wantTotal's exactness contract below. !wantTotal early-stops once the
// window is full; wantTotal forces a full scan to EOF, so (unlike
// rescanBackend, which never asserts Query-time exactness) a completed scan's
// matched count IS the exact filtered total -- no sampling/estimation
// anywhere in this backend, exactly like sqlBackend.
func (p *parquetBackend) queryFiltered(ctx context.Context, plan *CompiledPlan, w Window, offset int64, limit int, wantTotal bool, start time.Time) (RowSet, error) {
	rows := make([]Row, 0, limit)
	var scanned int64
	var matched int64

	err := p.scan(ctx, 0, func(idx int64, rec any) (bool, error) {
		scanned = idx + 1
		if plan.Filter.Match(rec) {
			matched++
			if matched > offset && int64(len(rows)) < int64(limit) {
				rows = append(rows, plan.Transform.Project(rec, idx))
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
		Columns:   plan.Transform.Columns(),
		Rows:      rows,
		Offset:    w.Offset,
		Scanned:   scanned,
		Truncated: int64(len(rows)) < int64(limit),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	if wantTotal {
		rs.Total = matched
		rs.TotalExact = true
	} else {
		rs.Total = -1
		rs.TotalExact = false
	}
	return rs, nil
}

// Count returns the exact number of records matching f: the footer total
// (free) for an empty/match-all filter, else a full cancellable scan
// applying the Go predicate (spec §4: "non-empty -> stream+Go-count (exact,
// cancellable)"), checking ctx every cancelCheckStride (~4096) rows via scan.
func (p *parquetBackend) Count(ctx context.Context, f *CompiledFilter) (total int64, exact bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if isMatchAllFilter(f) {
		return p.total, true, nil
	}
	var matched int64
	scanErr := p.scan(ctx, 0, func(_ int64, rec any) (bool, error) {
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

// Export streams every matching, projected row through enc via a single
// full scan from row 0, in file order, regardless of any interactive-tier
// window (spec §4/§8: export is never capped).
func (p *parquetBackend) Export(ctx context.Context, plan *CompiledPlan, enc RowEncoder) (rows int64, err error) {
	if plan == nil {
		return 0, fmt.Errorf("query: parquetBackend.Export: nil CompiledPlan")
	}
	if enc == nil {
		return 0, fmt.Errorf("query: parquetBackend.Export: nil RowEncoder")
	}
	var n int64
	scanErr := p.scan(ctx, 0, func(idx int64, rec any) (bool, error) {
		if !plan.Filter.Match(rec) {
			return false, nil
		}
		if err := enc.Encode(plan.Transform.Project(rec, idx)); err != nil {
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

// Close closes the underlying *os.File. Every *parquet.GenericReader this
// backend creates is scoped to (and closed within) a single scan/readWindow
// call, so there is nothing else to release here.
func (p *parquetBackend) Close() error {
	return p.f.Close()
}
