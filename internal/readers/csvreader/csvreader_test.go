package csvreader

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hoijunkim/shape/internal/readers"
)

func drain(t *testing.T, s readers.RecordStream) []map[string]any {
	t.Helper()
	var out []map[string]any
	for {
		rec, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		out = append(out, rec.(map[string]any))
	}
}

func TestCSVInference(t *testing.T) {
	data := "id,age,active,zip,name\n1,42,true,007,alice\n2,,false,012,\n"
	s, _, err := readers.Open(readers.FormatCSV, readers.Source{Reader: strings.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, s)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	r0 := rows[0]
	if r0["id"] != json.Number("1") || r0["age"] != json.Number("42") {
		t.Errorf("numeric inference failed: %v", r0)
	}
	if r0["active"] != true {
		t.Errorf("bool inference failed: %v", r0["active"])
	}
	if r0["zip"] != "007" { // leading-zero stays a string (identifier)
		t.Errorf("zip should stay string 007, got %v", r0["zip"])
	}
	r1 := rows[1]
	if r1["age"] != nil { // empty cell -> null
		t.Errorf("empty cell should be nil, got %v", r1["age"])
	}
	if r1["active"] != false {
		t.Errorf("false inference failed: %v", r1["active"])
	}
}

func TestCSVRaw(t *testing.T) {
	data := "n\n42\n"
	s, _, err := readers.Open(readers.FormatCSV, readers.Source{Reader: strings.NewReader(data), CSVRaw: true})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, s)
	if rows[0]["n"] != "42" { // raw: no inference, stays string
		t.Errorf("raw mode should keep 42 as string, got %v (%T)", rows[0]["n"], rows[0]["n"])
	}
}

func TestCSVEmptyFile(t *testing.T) {
	s, _, err := readers.Open(readers.FormatCSV, readers.Source{Reader: strings.NewReader("")})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(t, s); len(got) != 0 {
		t.Errorf("empty file should yield no rows, got %d", len(got))
	}
}

func TestTSVDelimiter(t *testing.T) {
	data := "id\tname\n1\talice\n"
	s, _, err := readers.Open(readers.FormatCSV, readers.Source{Path: "x.tsv", Reader: strings.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, s)
	if len(rows) != 1 || rows[0]["name"] != "alice" || rows[0]["id"] != json.Number("1") {
		t.Errorf("tsv not parsed with tab delimiter: %v", rows)
	}
}
