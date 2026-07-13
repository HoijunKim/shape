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
