package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
)

func runSchema(t *testing.T, args ...string) map[string]any {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"schema"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (out: %s)", err, out.String())
	}
	var s map[string]any
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	return s
}

func TestSchemaCommand(t *testing.T) {
	s := runSchema(t, "testdata/sample.ndjson")
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", s["$schema"])
	}
	if s["type"] != "object" {
		t.Errorf("root type = %v, want object", s["type"])
	}
	p, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties: %v", s)
	}
	for _, k := range []string{"id", "email", "tags"} {
		if _, has := p[k]; !has {
			t.Errorf("missing property %q", k)
		}
	}
	// id drifts int/string; tags is an array of strings.
	if _, ok := p["id"].(map[string]any)["type"].([]any); !ok {
		t.Errorf("id should have a union type, got %v", p["id"])
	}
	if p["tags"].(map[string]any)["type"] != "array" {
		t.Errorf("tags type = %v, want array", p["tags"])
	}
}
