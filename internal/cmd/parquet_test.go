package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/parquet-go/parquet-go"
)

type pqRow struct {
	ID  int64  `parquet:"id"`
	Tag string `parquet:"tag"`
}

func writeParquet(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[pqRow](&buf)
	if _, err := w.Write([]pqRow{{ID: 1, Tag: "a"}, {ID: 2, Tag: "b"}, {ID: 3, Tag: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/t.parquet"
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProfileParquet(t *testing.T) {
	path := writeParquet(t)
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "--json", path})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (%s)", err, out.String())
	}
	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if res["records"].(float64) != 3 {
		t.Errorf("records = %v, want 3", res["records"])
	}
}
