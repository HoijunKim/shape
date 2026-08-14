package visual

import (
	"testing"

	"github.com/hoijunkim/shape/internal/profile"
)

func num(kinds ...profile.JSONKind) map[profile.JSONKind]float64 {
	m := map[profile.JSONKind]float64{}
	for _, k := range kinds {
		m[k] = 1.0 / float64(len(kinds))
	}
	return m
}

func TestSelectFormBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		fp       profile.FieldProfile
		wantForm ChartForm
		wantKind string
	}{
		{"empty", profile.FieldProfile{Observations: 0}, FormEmpty, "empty"},
		{"all-null", profile.FieldProfile{Observations: 5, NullRate: 1, TypeDist: num(profile.KindNull)}, FormEmpty, "empty"},
		{"drift", profile.FieldProfile{Observations: 5, TypeDist: num(profile.KindInt, profile.KindString), DistinctExact: true, DistinctCount: 5}, FormTypeMix, "mixed"},
		{"discrete-num-12", profile.FieldProfile{Observations: 100, TypeDist: num(profile.KindInt), DistinctExact: true, DistinctCount: 12, TopValues: make([]profile.ValueCount, 12)}, FormCategorical, "number"},
		{"num-13-hist", profile.FieldProfile{Observations: 100, TypeDist: num(profile.KindInt), DistinctExact: true, DistinctCount: 13, Histogram: []profile.HistBin{{Value: 1, Count: 100}}, Min: fptr(1), Max: fptr(9)}, FormHistogram, "number"},
		{"str-25-cat", profile.FieldProfile{Observations: 100, TypeDist: num(profile.KindString), DistinctExact: true, DistinctCount: 25}, FormCategorical, "string"},
		{"str-26-highcard", profile.FieldProfile{Observations: 100, TypeDist: num(profile.KindString), DistinctExact: true, DistinctCount: 26}, FormHighCard, "string"},
		{"promoted-str", profile.FieldProfile{Observations: 100000, TypeDist: num(profile.KindString), DistinctExact: false, DistinctCount: 90000}, FormHighCard, "string"},
		{"num-no-hist-meter", profile.FieldProfile{Observations: 5, TypeDist: num(profile.KindFloat), DistinctExact: true, DistinctCount: 30}, FormMeter, "number"},
		{"array", profile.FieldProfile{Observations: 5, TypeDist: num(profile.KindArray), DistinctExact: true, DistinctCount: 1}, FormArray, "array"},
		{"bool", profile.FieldProfile{Observations: 5, TypeDist: num(profile.KindBool), DistinctExact: true, DistinctCount: 2}, FormMeter, "bool"},
	}
	for _, tt := range tests {
		gotForm, gotKind := selectForm(tt.fp)
		if gotForm != tt.wantForm || gotKind != tt.wantKind {
			t.Errorf("%s: selectForm = (%s,%s), want (%s,%s)", tt.name, gotForm, gotKind, tt.wantForm, tt.wantKind)
		}
	}
}

func fptr(f float64) *float64 { return &f }

func TestEnumLike(t *testing.T) {
	tests := []struct {
		name string
		fp   profile.FieldProfile
		want bool
	}{
		{
			name: "string-3-distinct-true",
			fp: profile.FieldProfile{
				Observations:  100,
				TypeDist:      num(profile.KindString),
				DistinctExact: true,
				DistinctCount: 3,
				TopValues:     make([]profile.ValueCount, 3),
			},
			want: true,
		},
		{
			name: "string-11-distinct-false",
			fp: profile.FieldProfile{
				Observations:  100,
				TypeDist:      num(profile.KindString),
				DistinctExact: true,
				DistinctCount: 11,
				TopValues:     make([]profile.ValueCount, 11),
			},
			want: false,
		},
		{
			name: "numeric-false",
			fp: profile.FieldProfile{
				Observations:  100,
				TypeDist:      num(profile.KindInt),
				DistinctExact: true,
				DistinctCount: 3,
				TopValues:     make([]profile.ValueCount, 3),
			},
			want: false,
		},
	}
	for _, tt := range tests {
		if got := enumLike(tt.fp); got != tt.want {
			t.Errorf("%s: enumLike = %v, want %v", tt.name, got, tt.want)
		}
	}
}
