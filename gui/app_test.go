package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppProfileFile(t *testing.T) {
	a := NewApp()
	pv, err := a.ProfileFile("../internal/cmd/testdata/sample.ndjson")
	if err != nil {
		t.Fatalf("ProfileFile: %v", err)
	}
	if pv.Records != 3 {
		t.Errorf("records = %d, want 3", pv.Records)
	}
	var id *FieldView
	for i := range pv.Fields {
		if pv.Fields[i].Path == "id" {
			id = &pv.Fields[i]
		}
	}
	if id == nil {
		t.Fatal("id field missing")
	}
	if !id.Drift { // id is int then string across records
		t.Errorf("id should be flagged drift")
	}
}

func TestAppSchemaJSON(t *testing.T) {
	a := NewApp()
	s, err := a.SchemaJSON("../internal/cmd/testdata/sample.ndjson")
	if err != nil {
		t.Fatalf("SchemaJSON: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		t.Fatalf("schema not JSON: %v\n%s", err, s)
	}
	if !strings.Contains(s, "draft/2020-12") {
		t.Errorf("expected a Draft 2020-12 schema:\n%s", s)
	}
}
