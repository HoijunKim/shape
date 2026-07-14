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

type addrRow struct {
	City string `parquet:"city"`
	Zip  int32  `parquet:"zip"`
}

type nestedRow struct {
	ID   int64    `parquet:"id"`
	Addr addrRow  `parquet:"addr"`
	Tags []string `parquet:"tags"`
}

func TestParquetNestedConversion(t *testing.T) {
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[nestedRow](&buf)
	if _, err := w.Write([]nestedRow{
		{ID: 1, Addr: addrRow{City: "Seoul", Zip: 100}, Tags: []string{"a", "b"}},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	path := t.TempDir() + "/nested.parquet"
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s, cleanup, err := readers.Open(readers.FormatParquet, readers.Source{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()
	rec, err := s.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	m := rec.(map[string]any)

	addr, ok := m["addr"].(map[string]any)
	if !ok {
		t.Fatalf("addr should be a nested map, got %T", m["addr"])
	}
	if addr["city"] != "Seoul" || addr["zip"] != json.Number("100") { // int32 -> json.Number via convertDeep
		t.Errorf("nested addr conversion wrong: %v", addr)
	}
	tags, ok := m["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags list conversion wrong: %v (%T)", m["tags"], m["tags"])
	}
}

func TestParquetMultiBatch(t *testing.T) {
	const n = 1000 // > batchSize (256) -> multiple Read calls + a final partial batch
	rows := make([]fixtureRow, n)
	for i := range rows {
		rows[i] = fixtureRow{ID: int64(i), Name: "x", Active: true}
	}
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[fixtureRow](&buf)
	if _, err := w.Write(rows); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	path := t.TempDir() + "/multi.parquet"
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s, cleanup, err := readers.Open(readers.FormatParquet, readers.Source{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()
	count := 0
	for {
		_, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		count++
	}
	if count != n {
		t.Errorf("read %d rows, want %d (multi-batch must not drop the last batch)", count, n)
	}
}
