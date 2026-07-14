package parquetreader

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/readers"
	"github.com/parquet-go/parquet-go"
)

var _ readers.RecordStream = (*stream)(nil)

func init() {
	readers.Register(readers.FormatParquet, open)
}

// open reads a parquet file by path (parquet needs random access, so stdin is
// rejected).
func open(s readers.Source) (readers.RecordStream, func() error, error) {
	if s.Path == "" {
		return nil, nil, fmt.Errorf("parquet cannot be read from stdin; provide a file path")
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	pf, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("open parquet %s: %w", s.Path, err)
	}
	gr := parquet.NewGenericReader[any](pf)
	st := &stream{gr: gr, buf: make([]any, 256)}
	cleanup := func() error {
		return errors.Join(gr.Close(), f.Close())
	}
	return st, cleanup, nil
}

type stream struct {
	gr   *parquet.GenericReader[any]
	buf  []any
	pos  int
	n    int
	done bool
}

func (s *stream) Next() (any, error) {
	for {
		if s.pos < s.n {
			rec := s.buf[s.pos]
			s.pos++
			m, ok := rec.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unexpected parquet row type %T", rec)
			}
			return convertDeep(m), nil
		}
		if s.done {
			return nil, io.EOF
		}
		n, err := s.gr.Read(s.buf)
		s.n, s.pos = n, 0
		if err != nil {
			if err == io.EOF {
				s.done = true // still serve the n rows read in this batch
				continue
			}
			return nil, err
		}
	}
}

func (s *stream) Skipped() int { return 0 }

// convertDeep recursively maps native parquet values (int32/int64/float32/...)
// to the profiler-compatible set, descending into nested groups and lists.
func convertDeep(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = convertDeep(x)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = convertDeep(x)
		}
		return out
	default:
		return readers.ToProfileValue(v)
	}
}
