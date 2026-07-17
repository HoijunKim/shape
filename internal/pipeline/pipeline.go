package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/diff"
	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers"
	_ "github.com/hoijun-kim/shape/internal/readers/csvreader"     // register csv
	_ "github.com/hoijun-kim/shape/internal/readers/jsonreader"    // register json
	_ "github.com/hoijun-kim/shape/internal/readers/parquetreader" // register parquet
	_ "github.com/hoijun-kim/shape/internal/readers/sqlitereader"  // register sqlite
	"github.com/hoijun-kim/shape/internal/schema"
)

// Options carries every knob the CLI flags expose, so the CLI and GUI share one
// contract and never diverge.
type Options struct {
	Path   string // file path, or "-" for stdin (the GUI always passes a real path)
	Format string // auto|json|ndjson|csv|parquet|sqlite; "" == auto
	CSVRaw bool
	Table  string // SQLite table override
}

// Profile opens+detects+streams+profiles one source.
func Profile(o Options) (profile.ProfileResult, error) {
	src, closeSrc, err := OpenSource(o.Path)
	if err != nil {
		return profile.ProfileResult{}, err
	}
	defer closeSrc()
	src.RawFormat = o.Format
	src.CSVRaw = o.CSVRaw
	src.Table = o.Table

	f := readers.DetectFormat(o.Path, o.Format, src.Peek)
	stream, closeStream, err := readers.Open(f, src)
	if err != nil {
		return profile.ProfileResult{}, err
	}
	defer closeStream()

	p := profile.NewProfiler()
	for {
		rec, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return profile.ProfileResult{}, fmt.Errorf("read %s: %w", o.Path, err)
		}
		p.AddRecord(rec)
	}
	p.AddSkipped(stream.Skipped())
	res := p.Result()
	res.Source = o.Path
	return res, nil
}

// Schema profiles then reconstructs a Draft 2020-12 JSON Schema.
func Schema(o Options) (map[string]any, error) {
	res, err := Profile(o)
	if err != nil {
		return nil, err
	}
	return schema.Reconstruct(res), nil
}

// Diff profiles two sources and compares them.
func Diff(oldO, newO Options) (diff.DiffResult, error) {
	a, err := Profile(oldO)
	if err != nil {
		return diff.DiffResult{}, err
	}
	b, err := Profile(newO)
	if err != nil {
		return diff.DiffResult{}, err
	}
	return diff.Diff(a, b), nil
}

// OpenSource opens a file path or stdin ("-") into a readers.Source with a peek.
// (moved verbatim from internal/cmd/source.go; exported so internal/query's
// stateless-re-scan engine can reuse the identical open path -- each Backend
// scan re-opens its own *os.File via this function, spec §2.)
func OpenSource(src string) (readers.Source, func() error, error) {
	if src == "-" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(os.Stdin, buf)
		peek := buf[:n]
		combined := io.MultiReader(bytes.NewReader(peek), os.Stdin)
		return readers.Source{Path: "", Reader: combined, Peek: peek}, func() error { return nil }, nil
	}
	fh, err := os.Open(src)
	if err != nil {
		return readers.Source{}, nil, err
	}
	peek := make([]byte, 512)
	n, _ := fh.Read(peek)
	peek = peek[:n]
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		fh.Close()
		return readers.Source{}, nil, err
	}
	return readers.Source{Path: src, Reader: fh, Peek: peek}, func() error { return fh.Close() }, nil
}
