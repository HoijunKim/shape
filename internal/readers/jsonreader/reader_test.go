package jsonreader

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func drain(t *testing.T, s *Stream) []any {
	t.Helper()
	var got []any
	for {
		v, err := s.Next()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		got = append(got, v)
	}
}

func TestWholeModeArrayStreamsElements(t *testing.T) {
	s := New(strings.NewReader(`[{"a":1},{"a":2}]`), WholeMode)
	if got := drain(t, s); len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
}

func TestWholeModeSingleObject(t *testing.T) {
	s := New(strings.NewReader(`{"a":1}`), WholeMode)
	if got := drain(t, s); len(got) != 1 {
		t.Fatalf("records = %d, want 1", len(got))
	}
}

func TestLineModeSkipsMalformed(t *testing.T) {
	s := New(strings.NewReader("{\"a\":1}\nnot json\n{\"a\":2}\n"), LineMode)
	got := drain(t, s)
	if len(got) != 2 {
		t.Fatalf("records = %d, want 2", len(got))
	}
	if s.Skipped() != 1 {
		t.Fatalf("skipped = %d, want 1", s.Skipped())
	}
}

func TestDetectMode(t *testing.T) {
	if DetectMode("x.ndjson", "auto", nil) != LineMode {
		t.Error("ndjson ext should be LineMode")
	}
	if DetectMode("x.json", "auto", nil) != WholeMode {
		t.Error("json ext should be WholeMode")
	}
	if DetectMode("-", "auto", []byte("  [")) != WholeMode {
		t.Error("leading [ should be WholeMode")
	}
	if DetectMode("-", "auto", []byte(`{"a":1}`)) != LineMode {
		t.Error("leading { with no ext should be LineMode")
	}
	if DetectMode("x.json", "ndjson", nil) != LineMode {
		t.Error("explicit ndjson flag overrides ext")
	}
}
