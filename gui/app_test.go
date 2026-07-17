package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppProfileFile(t *testing.T) {
	a := NewApp()
	vm, err := a.ProfileFile("../internal/cmd/testdata/sample.ndjson")
	if err != nil {
		t.Fatalf("ProfileFile: %v", err)
	}
	if vm.Summary.Records != 3 {
		t.Errorf("records = %d, want 3", vm.Summary.Records)
	}
	if len(vm.KPIs) != 5 {
		t.Errorf("len(KPIs) = %d, want 5", len(vm.KPIs))
	}
	if len(vm.Fields) == 0 {
		t.Fatal("Fields is empty")
	}
	if vm.Summary.Format == "" {
		t.Error("Summary.Format is empty")
	}
}

func TestAppDiffFiles(t *testing.T) {
	a := NewApp()
	dvm, err := a.DiffFiles("../internal/cmd/testdata/diff_old.ndjson", "../internal/cmd/testdata/diff_new.ndjson")
	if err != nil {
		t.Fatalf("DiffFiles: %v", err)
	}
	if !dvm.Breaking {
		t.Error("Breaking = false, want true")
	}
	if dvm.Verdict != "Breaking changes" {
		t.Errorf("Verdict = %q, want %q", dvm.Verdict, "Breaking changes")
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
