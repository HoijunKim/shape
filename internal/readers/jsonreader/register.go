package jsonreader

import "github.com/hoijun-kim/shape/internal/readers"

// compile-time proof that Stream satisfies the shared reader contract.
var _ readers.RecordStream = (*Stream)(nil)

func init() {
	readers.Register(readers.FormatJSON, open)
}

// open builds a JSON/NDJSON stream, picking Whole vs Line mode from the source.
func open(s readers.Source) (readers.RecordStream, func() error, error) {
	mode := DetectMode(s.Path, s.RawFormat, s.Peek)
	return New(s.Reader, mode), func() error { return nil }, nil
}
