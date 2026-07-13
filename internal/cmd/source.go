package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers/jsonreader"
)

// profileSource opens src (a file path or "-"), detects the format, streams the
// records, and returns the assembled profile with Source set.
func profileSource(src, format string) (profile.ProfileResult, error) {
	r, peek, closeFn, err := openSource(src)
	if err != nil {
		return profile.ProfileResult{}, err
	}
	defer closeFn()

	stream := jsonreader.New(r, jsonreader.DetectMode(src, format, peek))
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

// openSource opens a file path or stdin ("-"), returning the reader, a peek of
// the first bytes (for format detection), and a close function.
func openSource(src string) (io.Reader, []byte, func(), error) {
	if src == "-" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(os.Stdin, buf)
		peek := buf[:n]
		combined := io.MultiReader(bytes.NewReader(peek), os.Stdin)
		return combined, peek, func() {}, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, nil, nil, err
	}
	peek := make([]byte, 512)
	n, _ := f.Read(peek)
	peek = peek[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, nil, err
	}
	return f, peek, func() { f.Close() }, nil
}
