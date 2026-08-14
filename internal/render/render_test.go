package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoijunkim/shape/internal/profile"
)

func sample() profile.ProfileResult {
	p := profile.NewProfiler()
	dec := func(s string) any {
		d := json.NewDecoder(strings.NewReader(s))
		d.UseNumber()
		var v any
		_ = d.Decode(&v)
		return v
	}
	p.AddRecord(dec(`{"id":1,"email":"a@b.c"}`))
	p.AddRecord(dec(`{"id":"two"}`))
	return p.Result()
}

func TestTableMarksDrift(t *testing.T) {
	var b bytes.Buffer
	Table(&b, sample())
	out := b.String()
	if !strings.Contains(out, "id") || !strings.Contains(out, "email") {
		t.Fatalf("table missing fields:\n%s", out)
	}
	if !strings.Contains(out, "!") {
		t.Errorf("expected a drift marker for id:\n%s", out)
	}
}

func TestTableApproximateDistinctMarker(t *testing.T) {
	res := profile.ProfileResult{
		Records: 100000,
		Fields: []profile.FieldProfile{
			{Path: "id", PresenceRate: 1, TypeDist: map[profile.JSONKind]float64{profile.KindString: 1}, DistinctCount: 98765, DistinctExact: false},
		},
	}
	var b bytes.Buffer
	Table(&b, res)
	out := b.String()
	if !strings.Contains(out, "~98765") {
		t.Errorf("approximate distinct should render as ~98765, got:\n%s", out)
	}
	if strings.Contains(out, "98765+") {
		t.Errorf("must not use the misleading '+' suffix for an estimate:\n%s", out)
	}
}

func TestJSONStableShape(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, sample()); err != nil {
		t.Fatalf("json: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(b.Bytes(), &back); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if _, ok := back["fields"]; !ok {
		t.Errorf("expected top-level fields key, got %v", back)
	}
	if _, ok := back["records"]; !ok {
		t.Errorf("expected top-level records key, got %v", back)
	}
}
