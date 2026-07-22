package query

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ExportRequest is the ExportQuery request DTO (spec §8). Filter and Transform
// are the SAME model the interactive view uses, so an export is exactly "what
// I am looking at, complete": the engine re-runs them over the whole source
// rather than dumping whatever the interactive tier happened to have cached.
type ExportRequest struct {
	RequestID string    `json:"requestId,omitempty"`
	Handle    string    `json:"handle"`
	Filter    Filter    `json:"filter"`
	Transform Transform `json:"transform"`
	Format    string    `json:"format"` // json|ndjson|csv|tsv|parquet
	OutPath   string    `json:"outPath"`
}

// ExportResult is the ExportQuery response DTO (spec §8). Warnings carries
// per-format fidelity notes -- today only Parquet produces one, when a value
// did not fit its column's type and was written as null. It is an addition to
// spec §8's field list: silently dropping that information would make an
// export look lossless when it was not.
type ExportResult struct {
	OutPath   string   `json:"outPath"`
	RowsOut   int64    `json:"rowsOut"`
	BytesOut  int64    `json:"bytesOut"`
	ElapsedMs int64    `json:"elapsedMs"`
	Warnings  []string `json:"warnings,omitempty"`
}

// exportTempPattern is the temp-file pattern written next to the destination.
// The leading dot keeps it out of the way in a file picker, and the fixed
// prefix lets tests (and a user) recognize an abandoned one.
const exportTempPattern = ".shape-export-*"

// exportProgressStride is how many written rows pass between progress
// callbacks. The callback crosses into the Wails layer, which throttles again
// by time before emitting an event; this stride just keeps the call itself off
// the per-row path.
const exportProgressStride = 4096

// ExportQuery streams every row matching req.Filter, projected through
// req.Transform, into req.OutPath in req.Format (spec §8).
//
// Three properties matter more than the mechanics:
//
//   - COMPLETE: it runs a fresh full pass over the source through
//     Backend.Export, so an over-budget (rescan-tier) file exports every
//     matching row even though its interactive view is windowed and its totals
//     are estimates.
//   - BOUNDED: every encoder streams; memory is O(one row) for the text
//     formats and O(one batch) for Parquet, whatever the file size.
//   - ALL-OR-NOTHING: bytes go to a temp file in the destination's own
//     directory and are renamed into place only on success. A failed or
//     cancelled export leaves the destination exactly as it was -- untouched
//     if it did not exist, unmodified if it did -- and removes its temp.
//
// progress, when non-nil, is called every exportProgressStride rows with the
// number written so far. It is a plain Go callback, never part of the DTO, so
// the engine stays free of any Wails dependency; the App layer turns it into a
// throttled event.
func (e *Engine) ExportQuery(ctx context.Context, req ExportRequest, progress func(rows int64)) (ExportResult, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return ExportResult{}, err
	}
	format, err := parseExportFormat(req.Format)
	if err != nil {
		return ExportResult{}, err
	}
	plan, err := CompilePlan(req.Filter, req.Transform, backend.Columns())
	if err != nil {
		return ExportResult{}, fmt.Errorf("query: ExportQuery: %w", err)
	}
	cols := plan.Transform.Columns()
	if err := validateExportTarget(req.OutPath, e.sourcePath(req.Handle)); err != nil {
		return ExportResult{}, err
	}
	if err := validateExportColumns(cols); err != nil {
		return ExportResult{}, err
	}

	ctx, release := e.begin(ctx, req.RequestID)
	defer release()

	start := time.Now()
	rows, bytesOut, warnings, err := writeExport(ctx, backend, plan, cols, format, req.OutPath, progress)
	if err != nil {
		return ExportResult{}, err
	}
	return ExportResult{
		OutPath:   req.OutPath,
		RowsOut:   rows,
		BytesOut:  bytesOut,
		ElapsedMs: time.Since(start).Milliseconds(),
		Warnings:  warnings,
	}, nil
}

// parseExportFormat validates req.Format against the five writable formats.
func parseExportFormat(s string) (ExportFormat, error) {
	switch ExportFormat(s) {
	case ExportJSON, ExportNDJSON, ExportCSV, ExportTSV, ExportParquet:
		return ExportFormat(s), nil
	default:
		return "", fmt.Errorf("query: ExportQuery: unknown format %q (want json, ndjson, csv, tsv or parquet)", s)
	}
}

// validateExportTarget rejects an unusable destination BEFORE anything is
// created: an empty path, or the source file this handle is reading from.
//
// The self-export guard is not pedantry: the export streams FROM that file
// while writing, and the rename would replace the data mid-read. Note that
// the OS does NOT save us here -- the memory tier holds no file handle at all
// (the records are already decoded in RAM) and the rescan tier only opens one
// per scan, so the rename usually succeeds and the source is simply gone.
func validateExportTarget(outPath, sourcePath string) error {
	if strings.TrimSpace(outPath) == "" {
		return fmt.Errorf("query: ExportQuery: an output path is required")
	}
	if sourcePath == "" {
		return nil
	}
	if sameFilePath(outPath, sourcePath) {
		return fmt.Errorf("query: ExportQuery: cannot export onto the file being read (%s) -- choose another path", outPath)
	}
	return nil
}

// sameFilePath reports whether two paths denote the same file.
//
// It asks the FILE SYSTEM first (os.SameFile: device+inode on POSIX, volume
// serial + file index on Windows), because path comparison alone is
// bypassable in ways users hit by accident: a symlinked or junctioned parent
// directory, a Windows 8.3 short name, a subst'd drive, a hard link. Getting
// this wrong destroys the source file, so string equality is only the
// fallback for when either path cannot be stat'ed (the destination usually
// does not exist yet, which is exactly the normal case).
func sameFilePath(a, b string) bool {
	if fa, errA := os.Stat(a); errA == nil {
		if fb, errB := os.Stat(b); errB == nil {
			return os.SameFile(fa, fb)
		}
	}
	absA, err := filepath.Abs(a)
	if err != nil {
		absA = filepath.Clean(a)
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		absB = filepath.Clean(b)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(absA, absB)
	}
	return absA == absB
}

// validateExportColumns rejects a projection nothing can be written from: no
// columns at all, or two columns sharing an output path.
//
// Duplicates are checked on Column.PATH, not Column.Name, because that is the
// key space every encoder writes (JSON object keys, the CSV header, the
// Parquet schema) -- and because base columns are named by their LEAF, so an
// ordinary nested file has "user.id" and "order.id" both NAMED "id". Keying
// this check on Name would reject the default, un-transformed export of any
// such file.
func validateExportColumns(cols []Column) error {
	if len(cols) == 0 {
		return fmt.Errorf("query: ExportQuery: nothing to export (no columns selected)")
	}
	seen := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		if _, dup := seen[c.Path]; dup {
			return fmt.Errorf("query: ExportQuery: duplicate output column %q (rename one of them)", c.Path)
		}
		seen[c.Path] = struct{}{}
	}
	return nil
}

// writeExport performs the temp-file write and the atomic replace. It is split
// out of ExportQuery so every early return above stays free of cleanup duties.
func writeExport(
	ctx context.Context,
	backend Backend,
	plan *CompiledPlan,
	cols []Column,
	format ExportFormat,
	outPath string,
	progress func(rows int64),
) (rows int64, bytesOut int64, warnings []string, err error) {
	dir := filepath.Dir(outPath)
	tmp, err := os.CreateTemp(dir, exportTempPattern)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("query: ExportQuery: creating a temporary file next to %s: %w", outPath, err)
	}
	tmpName := tmp.Name()
	// Until the rename succeeds, every exit removes the temp. Closing an
	// already-closed *os.File is an error we deliberately ignore here: the
	// only thing that matters on a failure path is that nothing survives.
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	counter := &countingWriter{w: tmp}
	var buffered *bufio.Writer
	var sink io.Writer = counter
	if format != ExportParquet {
		// The Parquet writer does its own large-block buffering; the text
		// encoders write small pieces and want one.
		buffered = bufio.NewWriterSize(counter, 64<<10)
		sink = buffered
	}

	enc, err := newExportEncoder(sink, cols, format)
	if err != nil {
		return 0, 0, nil, err
	}

	counted := &progressEncoder{enc: enc, progress: progress}
	rows, err = backend.Export(ctx, plan, counted)
	if err != nil {
		return rows, 0, nil, err
	}
	// A cancelled export can still come back clean: every backend only checks
	// ctx at a 1024/4096-record stride, so a short source finishes before it
	// ever notices. Renaming here would hand the user a file they cancelled.
	// (Engine.OpenSource carries the same re-check for the same reason.)
	if cerr := ctx.Err(); cerr != nil {
		return rows, 0, nil, cerr
	}

	// ORDER MATTERS: Close() is what emits the JSON array terminator, the
	// lazy CSV header, csv.Writer's own buffered bytes and the Parquet
	// footer. Flushing the bufio.Writer before that would strand every one of
	// them -- os.File.Sync/Close does not drain it.
	if err := enc.Close(); err != nil {
		return rows, 0, nil, err
	}
	if buffered != nil {
		if err := buffered.Flush(); err != nil {
			return rows, 0, nil, fmt.Errorf("query: ExportQuery: flushing: %w", err)
		}
	}
	bytesOut = counter.n // only meaningful once everything above has drained
	if err := tmp.Sync(); err != nil {
		return rows, 0, nil, fmt.Errorf("query: ExportQuery: syncing: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return rows, 0, nil, fmt.Errorf("query: ExportQuery: closing the temporary file: %w", err)
	}
	// os.CreateTemp always creates with 0600, and the rename carries that
	// inode's mode onto the destination -- so re-exporting over a
	// world-readable file would silently narrow it to owner-only. Match the
	// destination's existing mode when there is one, else the 0644 the rest
	// of this app writes with. Best-effort: a mode we cannot set is not a
	// reason to fail an otherwise good export (and on Windows Chmod only
	// toggles the read-only bit, so this is a no-op there).
	perm := os.FileMode(0o644)
	if fi, statErr := os.Stat(outPath); statErr == nil && fi.Mode().IsRegular() {
		perm = fi.Mode().Perm()
	}
	_ = os.Chmod(tmpName, perm)
	if err := os.Rename(tmpName, outPath); err != nil {
		// Both POSIX rename and Windows MoveFileEx replace an existing
		// destination -- but Windows refuses when the destination is held
		// open by another process, which is the case a user actually hits
		// (the file is open in Excel, an editor, or shape itself).
		return rows, 0, nil, fmt.Errorf("query: ExportQuery: could not replace %s -- it may be open in another program: %w", outPath, err)
	}
	committed = true

	if pw, ok := enc.(interface{ Warnings() []string }); ok {
		warnings = pw.Warnings()
	}
	return rows, bytesOut, warnings, nil
}

// newExportEncoder builds the encoder for one format over sink.
func newExportEncoder(sink io.Writer, cols []Column, format ExportFormat) (rowEncoder, error) {
	switch format {
	case ExportJSON:
		return newJSONEncoder(sink, cols, true), nil
	case ExportNDJSON:
		return newJSONEncoder(sink, cols, false), nil
	case ExportCSV:
		return newDelimitedEncoder(sink, cols, ','), nil
	case ExportTSV:
		return newDelimitedEncoder(sink, cols, '\t'), nil
	case ExportParquet:
		return newParquetEncoder(sink, cols)
	default:
		return nil, fmt.Errorf("query: ExportQuery: unknown format %q", format)
	}
}

// countingWriter counts the bytes that actually reached the underlying writer,
// which is what ExportResult.BytesOut reports.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// progressEncoder wraps a rowEncoder to report row counts as they are written.
// It sits between Backend.Export and the format encoder so the count is of
// rows actually ENCODED, not merely matched.
type progressEncoder struct {
	enc      rowEncoder
	progress func(rows int64)
	n        int64
}

func (p *progressEncoder) Encode(index int64, values []any) error {
	if err := p.enc.Encode(index, values); err != nil {
		return err
	}
	p.n++
	if p.progress != nil && p.n%exportProgressStride == 0 {
		p.progress(p.n)
	}
	return nil
}
