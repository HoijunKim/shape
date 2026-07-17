package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/pipeline"
	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers"
)

// DefaultMemBudgetBytes is OpenSource's default ingest budget (spec §4):
// 512 MiB of ESTIMATED decoded record size before an over-budget JSON/CSV
// source downgrades from memBackend to rescanBackend.
const DefaultMemBudgetBytes int64 = 512 << 20

// ErrParquetBackendNotImplemented marks the one backend not yet built (Task
// 8, spec §1's per-format Backend table). openBackend's switch is the ONLY
// place that task needs to touch to slot parquetBackend in -- the
// FormatParquet branch below becomes "return newParquetBackend(...), ..."
// and nothing else in this package changes (Engine.OpenSource just forwards
// whatever openBackend returns). FormatSQLite's equivalent stub was
// ErrSQLiteBackendNotImplemented until Task 7 wired in newSQLBackend
// (sqlbackend.go) below.
var ErrParquetBackendNotImplemented = errors.New("query: parquet backend not yet implemented (Task 8)")

// budgetBytesOf resolves req.BudgetMB (spec §8: "0 ⇒ 512") to a byte budget.
func budgetBytesOf(req OpenRequest) int64 {
	mb := req.BudgetMB
	if mb <= 0 {
		mb = 512
	}
	return int64(mb) << 20
}

// fileSizeOf returns path's on-disk size, or 0 if it cannot be statted (e.g.
// an already-vanished file racing with a later rescan; rescanBackend treats
// 0 as "unknown" -- see estimateRowCount in rescan.go).
func fileSizeOf(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// openBackend selects and builds a Backend for req.Path, per
// readers.DetectFormat (spec §1/§2's per-format Backend table):
// FormatJSON/FormatCSV run OpenSource's shared ingest pass
// (openIngestBackend, this task's mem-or-rescan routing); FormatSQLite is
// routed to newSQLBackend (sqlbackend.go, Task 7 -- native projection/
// window/count pushdown with a Go residual for filter correctness);
// FormatParquet remains routed to its native-pushdown backend, stubbed here
// (see ErrParquetBackendNotImplemented) until Task 8 lands parquetbackend.go.
//
// The source is opened exactly once here (pipeline.OpenSource, closed via
// defer): readers.DetectFormat only needs the small peek pipeline.OpenSource
// already takes, and for the JSON/CSV path the very same open stream is
// handed straight to the ingest pass rather than reopening the file a second
// time just to detect its format.
func openBackend(req OpenRequest) (backend Backend, format readers.Format, tier string, err error) {
	src, closeSrc, err := pipeline.OpenSource(req.Path)
	if err != nil {
		return nil, "", "", fmt.Errorf("query: open %s: %w", req.Path, err)
	}
	defer closeSrc()
	src.RawFormat = req.Format
	src.CSVRaw = req.CSVRaw
	src.Table = req.Table

	format = readers.DetectFormat(req.Path, req.Format, src.Peek)

	switch format {
	case readers.FormatJSON, readers.FormatCSV:
		stream, closeStream, oerr := readers.Open(format, src)
		if oerr != nil {
			return nil, format, "", fmt.Errorf("query: open reader for %s: %w", req.Path, oerr)
		}
		defer closeStream()
		backend, tier, err = openIngestBackend(req.Path, format, req.Format, req.CSVRaw, stream, budgetBytesOf(req))
		return backend, format, tier, err
	case readers.FormatSQLite:
		sb, serr := newSQLBackend(req.Path, req.Table)
		if serr != nil {
			return nil, format, "", fmt.Errorf("query: open sqlite backend for %s: %w", req.Path, serr)
		}
		return sb, format, "sqlite", nil
	case readers.FormatParquet:
		return nil, format, "", ErrParquetBackendNotImplemented
	default:
		return nil, format, "", fmt.Errorf("query: unsupported format %q", format)
	}
}

// openIngestBackend runs OpenSource's single bounded ingest pass (spec
// §1/§2/§4) over an already-open stream for a streamable format
// (FormatJSON/FormatCSV): it simultaneously feeds a profile.Profiler (the
// sidebar structure map), a columnDiscoverer (first-seen column order/set),
// and an in-memory []any record slice, until EITHER EOF (-> memBackend,
// exact totals) OR a running sizeOf(rec) estimate exceeds budgetBytes (->
// the slice is dropped and a rescanBackend is built instead, estimated
// totals). Exactly one of those two things happens; there is no third
// outcome.
//
// rawFormat/csvRaw are threaded through to a downgraded rescanBackend so a
// later re-scan reopens the path with the identical readers.Source knobs
// this ingest pass used (see rescan.go's rescanBackend doc comment).
func openIngestBackend(path string, format readers.Format, rawFormat string, csvRaw bool, stream readers.RecordStream, budgetBytes int64) (Backend, string, error) {
	if budgetBytes <= 0 {
		budgetBytes = DefaultMemBudgetBytes
	}
	fileSize := fileSizeOf(path)

	disc := newColumnDiscoverer()
	prof := profile.NewProfiler()

	var records []any
	var sizeEstimate int64
	overBudget := false

	for {
		rec, nerr := stream.Next()
		if errors.Is(nerr, io.EOF) {
			break
		}
		if nerr != nil {
			return nil, "", fmt.Errorf("query: read %s: %w", path, nerr)
		}

		disc.Observe(rec)
		prof.AddRecord(rec)
		records = append(records, rec)
		sizeEstimate += sizeOf(rec)

		if sizeEstimate > budgetBytes {
			overBudget = true
			break // stop the ingest pass entirely: only a SAMPLE feeds prof/disc past this point
		}
	}
	prof.AddSkipped(stream.Skipped())
	profResult := prof.Result()
	cm := buildColumnModel(disc, profResult, nil)

	if overBudget {
		avgBytes := 0.0
		if len(records) > 0 {
			avgBytes = float64(sizeEstimate) / float64(len(records))
		}
		records = nil // drop the buffered sample: rescanBackend re-reads from disk, never from this slice
		rb := newRescanBackend(path, format, rawFormat, csvRaw, cm, profResult, avgBytes, fileSize)
		return rb, "rescan", nil
	}

	mb := newMemBackend(records, cm, profResult)
	return mb, "memory", nil
}

// sizeOf estimates the in-memory footprint of one decoded record (the
// JSON/CSV reader value set: nil, bool, string, json.Number, float64,
// map[string]any, []any -- see readers.RecordStream's doc comment) for
// OpenSource's ingest-budget accounting (spec §4). It is a coarse,
// allocation-free heuristic (no json.Marshal, no reflection): staying
// roughly proportional to real heap usage across many records -- and never
// zero for a non-trivial value -- is the goal, not byte-exactness.
func sizeOf(v any) int64 {
	switch t := v.(type) {
	case nil:
		return 8
	case bool:
		return 1
	case string:
		return int64(len(t)) + 16 // Go string header + backing bytes
	case json.Number:
		return int64(len(string(t))) + 16
	case float64:
		return 8
	case map[string]any:
		var n int64 = 24 // map header overhead
		for k, cv := range t {
			n += int64(len(k)) + 16 + sizeOf(cv) + 8 // key bytes + header + value + bucket overhead
		}
		return n
	case []any:
		var n int64 = 24 // slice header overhead
		for _, cv := range t {
			n += sizeOf(cv) + 8
		}
		return n
	default:
		return 16
	}
}
