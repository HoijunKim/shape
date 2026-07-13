package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

// build profiles a set of NDJSON records and reconstructs their schema.
func build(t *testing.T, records ...string) map[string]any {
	t.Helper()
	p := profile.NewProfiler()
	for _, r := range records {
		d := json.NewDecoder(strings.NewReader(r))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			t.Fatalf("decode %q: %v", r, err)
		}
		p.AddRecord(v)
	}
	return Reconstruct(p.Result())
}

func props(t *testing.T, s map[string]any) map[string]any {
	t.Helper()
	p, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties in %v", s)
	}
	return p
}

func TestSchemaRootAndTypes(t *testing.T) {
	s := build(t, `{"name":"x","age":30}`)
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", s["$schema"])
	}
	if s["type"] != "object" {
		t.Errorf("root type = %v, want object", s["type"])
	}
	p := props(t, s)
	if p["name"].(map[string]any)["type"] != "string" {
		t.Errorf("name type = %v, want string", p["name"])
	}
	if p["age"].(map[string]any)["type"] != "integer" {
		t.Errorf("age type = %v, want integer", p["age"])
	}
}

func TestSchemaNullUnion(t *testing.T) {
	s := build(t, `{"e":"a@b.c"}`, `{"e":null}`)
	e := props(t, s)["e"].(map[string]any)
	ty, ok := e["type"].([]any)
	if !ok || len(ty) != 2 || ty[0] != "string" || ty[1] != "null" {
		t.Errorf("e type = %v, want [string null]", e["type"])
	}
}

func TestSchemaDriftTypeArray(t *testing.T) {
	s := build(t, `{"id":1}`, `{"id":"two"}`)
	id := props(t, s)["id"].(map[string]any)
	ty, ok := id["type"].([]any)
	if !ok || len(ty) != 2 || ty[0] != "string" || ty[1] != "integer" {
		t.Errorf("id type = %v, want [string integer] (canonical order)", id["type"])
	}
}

func TestSchemaNestedAndArray(t *testing.T) {
	s := build(t, `{"user":{"name":"x"},"tags":["a","b"]}`)
	p := props(t, s)
	user := p["user"].(map[string]any)
	if user["type"] != "object" || props(t, user)["name"].(map[string]any)["type"] != "string" {
		t.Errorf("user schema wrong: %v", user)
	}
	tags := p["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Fatalf("tags type = %v, want array", tags["type"])
	}
	if tags["items"].(map[string]any)["type"] != "string" {
		t.Errorf("tags items = %v, want string", tags["items"])
	}
}

func TestSchemaEmptyArrayNoItems(t *testing.T) {
	s := build(t, `{"tags":[]}`)
	tags := props(t, s)["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Fatalf("tags type = %v, want array", tags["type"])
	}
	if _, has := tags["items"]; has {
		t.Errorf("empty array must not emit items, got %v", tags["items"])
	}
}

func TestSchemaRequiredParentConditional(t *testing.T) {
	// id present in all 3; email present in 2 of 3 -> only id required.
	s := build(t,
		`{"id":1,"email":"a@b.c"}`,
		`{"id":2,"email":null}`,
		`{"id":3}`,
	)
	req := toStrings(s["required"])
	if !contains(req, "id") {
		t.Errorf("required = %v, want id", req)
	}
	if contains(req, "email") {
		t.Errorf("required = %v, must not contain email (present 2 of 3)", req)
	}
}

func TestSchemaRequiredSuppressedInArray(t *testing.T) {
	// item objects live under items[]; their fields must never be required.
	s := build(t, `{"items":[{"sku":"a"},{"sku":"b"}]}`)
	items := props(t, s)["items"].(map[string]any)
	elem := items["items"].(map[string]any)
	if _, has := elem["required"]; has {
		t.Errorf("array-element object must not carry required, got %v", elem["required"])
	}
}

func toStrings(v any) []string {
	out := []string{}
	if arr, ok := v.([]string); ok {
		return arr
	}
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
