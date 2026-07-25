package query

import (
	"context"
	"testing"
)

// numericStatsFixture: a field "n" with more than DiscreteNumericMax (12)
// distinct values so the profiler+visual pick the histogram form (<=12 distinct
// numbers are rendered categorical instead), plus a string field "s".
func numericStatsFixture() []map[string]any {
	recs := make([]map[string]any, 0, 20)
	for i := 0; i < 20; i++ {
		recs = append(recs, map[string]any{"n": i, "s": "row"})
	}
	return recs
}

func TestColumnStats_NumericFieldHasHistogram(t *testing.T) {
	eng, handle, _ := openExportFixture(t, numericStatsFixture(), 0)

	res, err := eng.ColumnStats(context.Background(), ColumnStatsRequest{Handle: handle, Path: "n"})
	if err != nil {
		t.Fatalf("ColumnStats error = %v, want nil", err)
	}
	if !res.Found {
		t.Fatalf("Found = false, want true for an existing field")
	}
	// The mutation (return Fields[0] ignoring Path) can still pass Found if "n"
	// sorts first, so ALSO assert the returned card is actually "n".
	if res.Card.Path != "n" {
		t.Fatalf("Card.Path = %q, want %q", res.Card.Path, "n")
	}
	if res.Card.Histogram == nil {
		t.Fatalf("Card.Histogram = nil, want a histogram for a numeric field with >12 distinct values")
	}
}

func TestColumnStats_UnknownPathIsNotFound(t *testing.T) {
	eng, handle, _ := openExportFixture(t, numericStatsFixture(), 0)

	res, err := eng.ColumnStats(context.Background(), ColumnStatsRequest{Handle: handle, Path: "does_not_exist"})
	if err != nil {
		t.Fatalf("ColumnStats error = %v, want nil", err)
	}
	if res.Found {
		t.Fatalf("Found = true, want false for an unknown path")
	}
}

func TestColumnStats_UnknownHandleErrors(t *testing.T) {
	eng := NewEngine()
	_, err := eng.ColumnStats(context.Background(), ColumnStatsRequest{Handle: "nope", Path: "n"})
	if err == nil {
		t.Fatalf("ColumnStats error = nil, want an unknown-handle error")
	}
}
