package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers"
	_ "github.com/hoijun-kim/shape/internal/readers/csvreader"     // register csv
	_ "github.com/hoijun-kim/shape/internal/readers/jsonreader"    // register json
	_ "github.com/hoijun-kim/shape/internal/readers/parquetreader" // register parquet
	_ "github.com/hoijun-kim/shape/internal/readers/sqlitereader"  // register sqlite
)

// profileSource opens src (file path or "-"), detects the format, streams the
// records through the matching reader, and returns the assembled profile.
func profileSource(src, format string, csvRaw bool, table string) (profile.ProfileResult, error) {
	source, closeSrc, err := openSource(src)
	if err != nil {
		return profile.ProfileResult{}, err
	}
	defer closeSrc()
	source.RawFormat = format
	source.CSVRaw = csvRaw
	source.Table = table

	f := readers.DetectFormat(src, format, source.Peek)
	stream, closeStream, err := readers.Open(f, source)
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
			return profile.ProfileResult{}, fmt.Errorf("read %s: %w", src, err)
		}
		p.AddRecord(rec)
	}
	p.AddSkipped(stream.Skipped())
	res := p.Result()
	res.Source = src
	return res, nil
}

// openSource opens a file path or stdin ("-") into a readers.Source with a peek.
func openSource(src string) (readers.Source, func() error, error) {
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
