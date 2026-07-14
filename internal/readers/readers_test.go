package readers

import (
	"encoding/json"
	"testing"
)

func TestDetectFormat(t *testing.T) {
	cases := []struct {
		path, flag string
		peek       []byte
		want       Format
	}{
		{"x.csv", "auto", nil, FormatCSV},
		{"x.tsv", "auto", nil, FormatCSV},
		{"x.parquet", "auto", nil, FormatParquet},
		{"x.sqlite", "auto", nil, FormatSQLite},
		{"x.db", "auto", nil, FormatSQLite},
		{"x.json", "auto", nil, FormatJSON},
		{"x.ndjson", "auto", nil, FormatJSON},
		{"-", "csv", nil, FormatCSV},       // explicit flag wins
		{"x.csv", "json", nil, FormatJSON}, // explicit flag over ext
		{"-", "auto", []byte("PAR1..."), FormatParquet},
		{"-", "auto", []byte("SQLite format 3\x00"), FormatSQLite},
		{"-", "auto", []byte(`{"a":1}`), FormatJSON}, // default
	}
	for _, c := range cases {
		if got := DetectFormat(c.path, c.flag, c.peek); got != c.want {
			t.Errorf("DetectFormat(%q,%q,%q) = %s, want %s", c.path, c.flag, c.peek, got, c.want)
		}
	}
}

func TestToProfileValue(t *testing.T) {
	if got := ToProfileValue(int64(42)); got != json.Number("42") {
		t.Errorf("int64 -> %v (%T), want json.Number 42", got, got)
	}
	if got := ToProfileValue(float32(1.5)); got != float64(1.5) {
		t.Errorf("float32 -> %v (%T), want float64 1.5", got, got)
	}
	if got := ToProfileValue([]byte("hi")); got != "hi" {
		t.Errorf("[]byte -> %v, want string hi", got)
	}
	if got := ToProfileValue(nil); got != nil {
		t.Errorf("nil -> %v, want nil", got)
	}
	if got := ToProfileValue("s"); got != "s" {
		t.Errorf("string passthrough failed: %v", got)
	}
}

func TestOpenUnsupported(t *testing.T) {
	if _, _, err := Open(FormatParquet, Source{}); err == nil {
		t.Error("Open on an unregistered format must error")
	}
}
