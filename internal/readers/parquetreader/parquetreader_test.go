package parquetreader

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
	"github.com/parquet-go/parquet-go"
)

type fixtureRow struct {
	ID     int64  `parquet:"id"`
	Name   string `parquet:"name"`
	Active bool   `parquet:"active"`
}

func writeFixture(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[fixtureRow](&buf)
	if _, err := w.Write([]fixtureRow{
		{ID: 1, Name: "Alice", Active: true},
		{ID: 2, Name: "Bob", Active: false},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	path := t.TempDir() + "/fixture.parquet"
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func TestParquetRoundTrip(t *testing.T) {
	path := writeFixture(t)
	s, cleanup, err := readers.Open(readers.FormatParquet, readers.Source{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()
	var rows []map[string]any
	for {
		rec, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		rows = append(rows, rec.(map[string]any))
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["id"] != json.Number("1") { // int64 -> json.Number via ToProfileValue
		t.Errorf("id = %v (%T), want json.Number 1", rows[0]["id"], rows[0]["id"])
	}
	if rows[0]["name"] != "Alice" || rows[0]["active"] != true {
		t.Errorf("row0 = %v", rows[0])
	}
}

func TestParquetRejectsStdin(t *testing.T) {
	if _, _, err := readers.Open(readers.FormatParquet, readers.Source{Path: ""}); err == nil {
		t.Error("parquet from stdin (empty path) must error")
	}
}
