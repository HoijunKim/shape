package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runProfile(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"profile"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (out: %s)", err, out.String())
	}
	return out.String()
}

func TestProfileTableFromFile(t *testing.T) {
	out := runProfile(t, "testdata/sample.ndjson")
	if !strings.Contains(out, "records: 3") {
		t.Errorf("expected 3 records:\n%s", out)
	}
	if !strings.Contains(out, "id") || !strings.Contains(out, "!") {
		t.Errorf("expected id field flagged as drift:\n%s", out)
	}
}

func TestProfileJSONFromFile(t *testing.T) {
	out := runProfile(t, "--json", "testdata/sample.ndjson")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if parsed["records"].(float64) != 3 {
		t.Errorf("records = %v, want 3", parsed["records"])
	}
}
