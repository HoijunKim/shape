package jsonreader

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
)

func TestRegisteredJSONFactory(t *testing.T) {
	src := readers.Source{Reader: strings.NewReader("{\"a\":1}\n{\"a\":2}\n"), RawFormat: "ndjson"}
	stream, cleanup, err := readers.Open(readers.FormatJSON, src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()
	n := 0
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		n++
	}
	if n != 2 {
		t.Errorf("records = %d, want 2", n)
	}
}
