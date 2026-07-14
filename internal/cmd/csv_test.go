package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProfileCSV(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "--json", "testdata/sample.csv"})
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
	fields, _ := res["fields"].([]any)
	got := map[string]any{}
	for _, f := range fields {
		m := f.(map[string]any)
		got[m["path"].(string)] = m
	}
	for _, k := range []string{"id", "email", "age"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing field %q", k)
		}
	}
	// id is int in rows 1-2 and string in row 3 -> drift.
	if got["id"].(map[string]any)["drift"] != true {
		t.Errorf("id should drift (int + string), got %v", got["id"])
	}
}

func TestSchemaCSV(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"schema", "testdata/sample.csv"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (%s)", err, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"type": "object"`)) {
		t.Errorf("expected an object schema from CSV:\n%s", out.String())
	}
}
