package pipeline

import "testing"

func TestProfileJSON(t *testing.T) {
	res, err := Profile(Options{Path: "../cmd/testdata/sample.ndjson", Format: "auto"})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if res.Records != 3 {
		t.Errorf("records = %d, want 3", res.Records)
	}
	if res.Source != "../cmd/testdata/sample.ndjson" {
		t.Errorf("source = %q", res.Source)
	}
}

func TestProfileCSV(t *testing.T) {
	res, err := Profile(Options{Path: "../cmd/testdata/sample.csv", Format: "auto"})
	if err != nil {
		t.Fatalf("profile csv: %v", err)
	}
	if res.Records != 3 {
		t.Errorf("records = %d, want 3", res.Records)
	}
}

func TestSchema(t *testing.T) {
	s, err := Schema(Options{Path: "../cmd/testdata/sample.ndjson", Format: "auto"})
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", s["$schema"])
	}
}

func TestDiff(t *testing.T) {
	d, err := Diff(
		Options{Path: "../cmd/testdata/diff_old.ndjson", Format: "auto"},
		Options{Path: "../cmd/testdata/diff_new.ndjson", Format: "auto"},
	)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if d.Breaking != 3 {
		t.Errorf("breaking = %d, want 3", d.Breaking)
	}
}
