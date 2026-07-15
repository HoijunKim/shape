package visual

import (
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

// ---------------------------------------------------------------------------
// categorical
// ---------------------------------------------------------------------------

func TestCategoricalBarsBasic(t *testing.T) {
	fp := profile.FieldProfile{
		Observations:  100,
		DistinctCount: 3,
		DistinctExact: true,
		TopValues: []profile.ValueCount{
			{Value: "a", Count: 50},
			{Value: "b", Count: 30},
			{Value: "c", Count: 20},
		},
	}
	cat := categorical(fp)
	if cat == nil {
		t.Fatal("categorical = nil, want non-nil")
	}
	if len(cat.Bars) != 3 {
		t.Fatalf("len(Bars) = %d, want 3", len(cat.Bars))
	}
	if cat.Total != 100 {
		t.Errorf("Total = %d, want 100", cat.Total)
	}
	if cat.MaxCount != 50 {
		t.Errorf("MaxCount = %d, want 50", cat.MaxCount)
	}
	if cat.Truncated {
		t.Errorf("Truncated = true, want false (DistinctCount == len(bars))")
	}
	if cat.Other != nil {
		t.Errorf("Other = %+v, want nil when not truncated", cat.Other)
	}

	wantLabel := []string{"a", "b", "c"}
	wantCount := []int{50, 30, 20}
	wantFrac := []float64{1.0, 0.6, 0.4}
	wantPercent := []int{50, 30, 20}
	for i, bar := range cat.Bars {
		if bar.Label != wantLabel[i] {
			t.Errorf("Bars[%d].Label = %q, want %q", i, bar.Label, wantLabel[i])
		}
		if bar.Count != wantCount[i] {
			t.Errorf("Bars[%d].Count = %d, want %d", i, bar.Count, wantCount[i])
		}
		if !almostEqual(bar.Frac, wantFrac[i]) {
			t.Errorf("Bars[%d].Frac = %v, want %v", i, bar.Frac, wantFrac[i])
		}
		if bar.Percent != wantPercent[i] {
			t.Errorf("Bars[%d].Percent = %d, want %d", i, bar.Percent, wantPercent[i])
		}
	}
}

func TestCategoricalTruncatedWithOther(t *testing.T) {
	fp := profile.FieldProfile{
		Observations:  100,
		DistinctCount: 5, // more distinct values exist than are shown
		DistinctExact: true,
		TopValues: []profile.ValueCount{
			{Value: "a", Count: 50},
			{Value: "b", Count: 30},
			{Value: "c", Count: 10},
		},
	}
	cat := categorical(fp)
	if cat == nil {
		t.Fatal("categorical = nil, want non-nil")
	}
	if !cat.Truncated {
		t.Fatal("Truncated = false, want true (DistinctCount 5 > len(bars) 3)")
	}
	if cat.Other == nil {
		t.Fatal("Other = nil, want aggregate bar when truncated")
	}
	if cat.Other.Label != "other" {
		t.Errorf("Other.Label = %q, want %q", cat.Other.Label, "other")
	}
	wantOtherCount := 100 - (50 + 30 + 10) // 10
	if cat.Other.Count != wantOtherCount {
		t.Errorf("Other.Count = %d, want %d", cat.Other.Count, wantOtherCount)
	}
	wantFrac := float64(wantOtherCount) / float64(cat.MaxCount) // 10/50 = 0.2
	if !almostEqual(cat.Other.Frac, wantFrac) {
		t.Errorf("Other.Frac = %v, want %v", cat.Other.Frac, wantFrac)
	}
	if cat.Other.Percent != 10 {
		t.Errorf("Other.Percent = %d, want 10", cat.Other.Percent)
	}
}

func TestCategoricalCapsAtTopK(t *testing.T) {
	var top []profile.ValueCount
	labels := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	counts := []int{120, 110, 100, 90, 80, 70, 60, 50, 40, 30, 20, 10}
	for i, l := range labels {
		top = append(top, profile.ValueCount{Value: l, Count: counts[i]})
	}
	fp := profile.FieldProfile{
		Observations:  1000,
		DistinctCount: 12,
		DistinctExact: true,
		TopValues:     top,
	}
	cat := categorical(fp)
	if cat == nil {
		t.Fatal("categorical = nil, want non-nil")
	}
	if len(cat.Bars) != TopK {
		t.Fatalf("len(Bars) = %d, want TopK=%d", len(cat.Bars), TopK)
	}
	if !cat.Truncated {
		t.Errorf("Truncated = false, want true (DistinctCount 12 > TopK 10)")
	}
	if cat.MaxCount != 120 {
		t.Errorf("MaxCount = %d, want 120", cat.MaxCount)
	}
	if cat.Bars[TopK-1].Label != "j" {
		t.Errorf("Bars[%d].Label = %q, want %q (bar 11th+ dropped)", TopK-1, cat.Bars[TopK-1].Label, "j")
	}
}

func TestCategoricalNilWhenNoTopValues(t *testing.T) {
	fp := profile.FieldProfile{Observations: 10}
	if cat := categorical(fp); cat != nil {
		t.Errorf("categorical = %+v, want nil when TopValues is empty", cat)
	}
}

// ---------------------------------------------------------------------------
// highCard
// ---------------------------------------------------------------------------

func TestHighCardDistinctTextExact(t *testing.T) {
	fp := profile.FieldProfile{
		Observations:  200,
		DistinctCount: 42,
		DistinctExact: true,
	}
	hc := highCard(fp)
	if hc.Distinct != 42 {
		t.Errorf("Distinct = %d, want 42", hc.Distinct)
	}
	if hc.DistinctText != "42" {
		t.Errorf("DistinctText = %q, want %q", hc.DistinctText, "42")
	}
}

func TestHighCardDistinctTextApprox(t *testing.T) {
	fp := profile.FieldProfile{
		Observations:  200,
		DistinctCount: 42,
		DistinctExact: false,
	}
	hc := highCard(fp)
	if hc.DistinctText != "~42" {
		t.Errorf("DistinctText = %q, want %q", hc.DistinctText, "~42")
	}
}

func TestHighCardSampleFirstN(t *testing.T) {
	fp := profile.FieldProfile{
		Observations:  200,
		DistinctCount: 8,
		DistinctExact: true,
		TopValues: []profile.ValueCount{
			{Value: "v1", Count: 10}, {Value: "v2", Count: 9}, {Value: "v3", Count: 8},
			{Value: "v4", Count: 7}, {Value: "v5", Count: 6}, {Value: "v6", Count: 5},
			{Value: "v7", Count: 4}, {Value: "v8", Count: 3},
		},
	}
	hc := highCard(fp)
	want := []string{"v1", "v2", "v3", "v4", "v5"}
	if len(hc.Sample) != CardinalitySample {
		t.Fatalf("len(Sample) = %d, want CardinalitySample=%d", len(hc.Sample), CardinalitySample)
	}
	for i, v := range want {
		if hc.Sample[i] != v {
			t.Errorf("Sample[%d] = %q, want %q", i, hc.Sample[i], v)
		}
	}
}

func TestHighCardSampleFewerThanN(t *testing.T) {
	fp := profile.FieldProfile{
		TopValues: []profile.ValueCount{{Value: "only", Count: 1}},
	}
	hc := highCard(fp)
	if len(hc.Sample) != 1 || hc.Sample[0] != "only" {
		t.Errorf("Sample = %+v, want [\"only\"]", hc.Sample)
	}
}

func TestHighCardSampleEmpty(t *testing.T) {
	fp := profile.FieldProfile{}
	hc := highCard(fp)
	if len(hc.Sample) != 0 {
		t.Errorf("Sample = %+v, want empty", hc.Sample)
	}
}

func TestHighCardStrLenPresent(t *testing.T) {
	min, max := 3, 12
	fp := profile.FieldProfile{StrLenMin: &min, StrLenMax: &max}
	hc := highCard(fp)
	if hc.StrLen == nil {
		t.Fatal("StrLen = nil, want non-nil when StrLenMin/Max set")
	}
	if hc.StrLen.Min != 3 || hc.StrLen.Max != 12 {
		t.Errorf("StrLen = %+v, want Min=3 Max=12", hc.StrLen)
	}
	wantText := "3–12 chars" // en dash, U+2013
	if hc.StrLen.Text != wantText {
		t.Errorf("StrLen.Text = %q, want %q", hc.StrLen.Text, wantText)
	}
}

func TestHighCardStrLenAbsent(t *testing.T) {
	cases := []struct {
		name    string
		strLMin *int
		strLMax *int
	}{
		{"both nil", nil, nil},
		{"min only", intp(3), nil},
		{"max only", nil, intp(12)},
	}
	for _, c := range cases {
		fp := profile.FieldProfile{StrLenMin: c.strLMin, StrLenMax: c.strLMax}
		hc := highCard(fp)
		if hc.StrLen != nil {
			t.Errorf("%s: StrLen = %+v, want nil", c.name, hc.StrLen)
		}
	}
}

func TestHighCardUniqueRatio(t *testing.T) {
	cases := []struct {
		name         string
		distinct     int
		observations int
		want         float64
	}{
		{"typical", 80, 100, 0.8},
		{"clamped above 1", 150, 100, 1.0}, // promoted sketches can overshoot exact obs count
		{"zero observations", 5, 0, 0},     // safeDiv guard
	}
	for _, c := range cases {
		fp := profile.FieldProfile{DistinctCount: c.distinct, Observations: c.observations}
		hc := highCard(fp)
		if !almostEqual(hc.UniqueRatio, c.want) {
			t.Errorf("%s: UniqueRatio = %v, want %v", c.name, hc.UniqueRatio, c.want)
		}
	}
}

func intp(i int) *int { return &i }

// ---------------------------------------------------------------------------
// arrayBreakdown
// ---------------------------------------------------------------------------

func TestArrayBreakdownPresent(t *testing.T) {
	fp := profile.FieldProfile{Path: "user.tags"}
	elem := profile.FieldProfile{
		Path:         "user.tags[]",
		Observations: 50,
		NullRate:     0,
		TypeDist:     num(profile.KindString),
	}
	index := map[string]profile.FieldProfile{"user.tags[]": elem}

	ab := arrayBreakdown(fp, index)
	if ab == nil {
		t.Fatal("arrayBreakdown = nil, want non-nil")
	}
	if ab.ElementPath != "user.tags[]" {
		t.Errorf("ElementPath = %q, want %q", ab.ElementPath, "user.tags[]")
	}
	if !ab.Present {
		t.Error("Present = false, want true (element field exists in index)")
	}
	if ab.ElementCount != 50 {
		t.Errorf("ElementCount = %d, want 50", ab.ElementCount)
	}
	wantTypes := typeMix(elem)
	if len(ab.ElementTypes) != len(wantTypes) {
		t.Fatalf("len(ElementTypes) = %d, want %d", len(ab.ElementTypes), len(wantTypes))
	}
	for i := range wantTypes {
		if ab.ElementTypes[i] != wantTypes[i] {
			t.Errorf("ElementTypes[%d] = %+v, want %+v", i, ab.ElementTypes[i], wantTypes[i])
		}
	}
}

func TestArrayBreakdownAbsent(t *testing.T) {
	fp := profile.FieldProfile{Path: "user.emptyTags"}
	index := map[string]profile.FieldProfile{} // no "[]" sibling: array was always empty

	ab := arrayBreakdown(fp, index)
	if ab == nil {
		t.Fatal("arrayBreakdown = nil, want non-nil even when element absent")
	}
	if ab.ElementPath != "user.emptyTags[]" {
		t.Errorf("ElementPath = %q, want %q", ab.ElementPath, "user.emptyTags[]")
	}
	if ab.Present {
		t.Error("Present = true, want false (no element field in index)")
	}
	if ab.ElementCount != 0 {
		t.Errorf("ElementCount = %d, want 0", ab.ElementCount)
	}
	if ab.ElementTypes != nil {
		t.Errorf("ElementTypes = %+v, want nil", ab.ElementTypes)
	}
}

func TestArrayBreakdownMixedElementTypes(t *testing.T) {
	fp := profile.FieldProfile{Path: "items"}
	elem := profile.FieldProfile{
		Path:         "items[]",
		Observations: 100,
		NullRate:     0,
		TypeDist: map[profile.JSONKind]float64{
			profile.KindInt:    0.5,
			profile.KindString: 0.5,
		},
	}
	index := map[string]profile.FieldProfile{"items[]": elem}

	ab := arrayBreakdown(fp, index)
	if !ab.Present {
		t.Fatal("Present = false, want true")
	}
	if len(ab.ElementTypes) != 2 {
		t.Fatalf("len(ElementTypes) = %d, want 2: %+v", len(ab.ElementTypes), ab.ElementTypes)
	}
	if ab.ElementTypes[0].Kind != "number" || ab.ElementTypes[1].Kind != "string" {
		t.Errorf("ElementTypes = %+v, want [number, string] order (kindOrder)", ab.ElementTypes)
	}
}
