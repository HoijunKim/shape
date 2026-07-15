package visual

import (
	"reflect"
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

// ---------------------------------------------------------------------------
// fieldBadges: single-trigger cases (design §5.1 trigger table)
// ---------------------------------------------------------------------------

func TestFieldBadgesTriggers(t *testing.T) {
	tests := []struct {
		name string
		fp   profile.FieldProfile
		form ChartForm
		want Badge
	}{
		{
			name: "all_null",
			fp: profile.FieldProfile{
				Path: "a.null_field", Observations: 5, NullRate: 1.0,
				TypeDist: num(profile.KindNull),
			},
			form: FormEmpty,
			want: Badge{Severity: SevCritical, Code: "all_null", Icon: severityIcon[SevCritical],
				Label: "All null", Detail: "Every value is null.", Path: "a.null_field"},
		},
		{
			name: "type_drift",
			fp: profile.FieldProfile{
				Path: "b.mixed", Observations: 10, NullRate: 0,
				TypeDist:      num(profile.KindInt, profile.KindString),
				DistinctExact: true, DistinctCount: 5,
			},
			form: FormTypeMix,
			want: Badge{Severity: SevSerious, Code: "type_drift", Icon: severityIcon[SevSerious],
				Label: "Mixed types", Detail: "Number 50%, String 50%", Path: "b.mixed"},
		},
		{
			name: "null_high_at_0.50",
			fp: profile.FieldProfile{
				Path: "c.highnull", Observations: 10, NullRate: 0.50,
				TypeDist: num(profile.KindString), DistinctExact: true, DistinctCount: 5,
			},
			form: FormCategorical,
			want: Badge{Severity: SevSerious, Code: "null_high", Icon: severityIcon[SevSerious],
				Label: "High null rate", Detail: "50%", Path: "c.highnull"},
		},
		{
			name: "null_elevated_at_0.20",
			fp: profile.FieldProfile{
				Path: "d.elevated", Observations: 10, NullRate: 0.20,
				TypeDist: num(profile.KindString), DistinctExact: true, DistinctCount: 5,
			},
			form: FormCategorical,
			want: Badge{Severity: SevWarning, Code: "null_elevated", Icon: severityIcon[SevWarning],
				Label: "Elevated nulls", Detail: "20%", Path: "d.elevated"},
		},
		{
			name: "high_cardinality_via_form",
			fp: profile.FieldProfile{
				Path: "e.highcard", Observations: 1000, NullRate: 0,
				TypeDist: num(profile.KindString), DistinctExact: false, DistinctCount: 1000,
			},
			form: FormHighCard,
			want: Badge{Severity: SevWarning, Code: "high_cardinality", Icon: severityIcon[SevWarning],
				Label: "High cardinality", Detail: "~1,000", Path: "e.highcard"},
		},
		{
			name: "constant",
			fp: profile.FieldProfile{
				Path: "f.const", Observations: 10, NullRate: 0,
				TypeDist: num(profile.KindString), DistinctExact: true, DistinctCount: 1,
			},
			form: FormCategorical,
			want: Badge{Severity: SevWarning, Code: "constant", Icon: severityIcon[SevWarning],
				Label: "Single value", Detail: "Only one distinct value.", Path: "f.const"},
		},
		{
			name: "clean_fallback",
			fp: profile.FieldProfile{
				Path: "g.clean", Observations: 10, NullRate: 0.05,
				TypeDist: num(profile.KindString), DistinctExact: true, DistinctCount: 5,
			},
			form: FormCategorical,
			want: Badge{Severity: SevGood, Code: "clean", Icon: severityIcon[SevGood],
				Label: "Clean", Detail: "", Path: "g.clean"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fieldBadges(tt.fp, tt.form)
			want := []Badge{tt.want}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("fieldBadges() = %#v, want %#v", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fieldBadges: never empty, and null-band boundaries are mutually exclusive.
// ---------------------------------------------------------------------------

func TestFieldBadgesNullBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		nullRate float64
		wantCode string
	}{
		{"0.19_below_warn_band_is_clean", 0.19, "clean"},
		{"0.20_elevated", 0.20, "null_elevated"},
		{"0.50_high", 0.50, "null_high"},
		{"1.00_all_null", 1.0, "all_null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typeDist := num(profile.KindString)
			if tt.nullRate >= 1.0 {
				typeDist = num(profile.KindNull)
			}
			fp := profile.FieldProfile{
				Path: "x", Observations: 5, NullRate: tt.nullRate,
				TypeDist: typeDist, DistinctExact: true, DistinctCount: 5,
			}
			got := fieldBadges(fp, FormCategorical)
			if len(got) != 1 {
				t.Fatalf("nullRate=%v: got %d badges, want 1: %#v", tt.nullRate, len(got), got)
			}
			if got[0].Code != tt.wantCode {
				t.Errorf("nullRate=%v: code = %q, want %q", tt.nullRate, got[0].Code, tt.wantCode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fieldBadges: sort order (severity desc, code asc) across multiple triggers.
// ---------------------------------------------------------------------------

func TestFieldBadgesSortOrder(t *testing.T) {
	t.Run("serious_then_two_warnings_by_code", func(t *testing.T) {
		fp := profile.FieldProfile{
			Path: "combo.a", Observations: 100, NullRate: 0.25,
			TypeDist:      num(profile.KindInt, profile.KindString),
			DistinctExact: false, DistinctCount: 99999,
		}
		got := fieldBadges(fp, FormHighCard)
		wantCodes := []string{"type_drift", "high_cardinality", "null_elevated"}
		gotCodes := codesOf(got)
		if !reflect.DeepEqual(gotCodes, wantCodes) {
			t.Errorf("codes = %v, want %v", gotCodes, wantCodes)
		}
	})

	t.Run("two_serious_by_code_then_warning", func(t *testing.T) {
		fp := profile.FieldProfile{
			Path:         "combo.b",
			Observations: 100,
			NullRate:     0.50,
			TypeDist: map[profile.JSONKind]float64{
				profile.KindInt: 0.3, profile.KindString: 0.2, profile.KindNull: 0.5,
			},
			DistinctExact: false, DistinctCount: 99999,
		}
		got := fieldBadges(fp, FormHighCard)
		wantCodes := []string{"null_high", "type_drift", "high_cardinality"}
		gotCodes := codesOf(got)
		if !reflect.DeepEqual(gotCodes, wantCodes) {
			t.Errorf("codes = %v, want %v", gotCodes, wantCodes)
		}
	})
}

func codesOf(badges []Badge) []string {
	out := make([]string, len(badges))
	for i, b := range badges {
		out[i] = b.Code
	}
	return out
}

// ---------------------------------------------------------------------------
// fileBadges
// ---------------------------------------------------------------------------

func TestFileBadges(t *testing.T) {
	t.Run("neither", func(t *testing.T) {
		got := fileBadges(profile.ProfileResult{Records: 10, Skipped: 0})
		if len(got) != 0 {
			t.Errorf("got %d badges, want 0: %#v", len(got), got)
		}
	})

	t.Run("only_skipped", func(t *testing.T) {
		got := fileBadges(profile.ProfileResult{Records: 10, Skipped: 3})
		want := []Badge{{Severity: SevWarning, Code: "skipped_records", Icon: severityIcon[SevWarning],
			Label: "Skipped records", Detail: "3 records skipped"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	})

	t.Run("only_no_records", func(t *testing.T) {
		got := fileBadges(profile.ProfileResult{Records: 0, Skipped: 0})
		want := []Badge{{Severity: SevCritical, Code: "no_records", Icon: severityIcon[SevCritical],
			Label: "No records", Detail: "The file contains no records."}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	})

	t.Run("both_sorted_critical_first", func(t *testing.T) {
		got := fileBadges(profile.ProfileResult{Records: 0, Skipped: 5})
		wantCodes := []string{"no_records", "skipped_records"}
		if gotCodes := codesOf(got); !reflect.DeepEqual(gotCodes, wantCodes) {
			t.Errorf("codes = %v, want %v", gotCodes, wantCodes)
		}
		if got[1].Detail != "5 records skipped" {
			t.Errorf("skipped_records detail = %q, want %q", got[1].Detail, "5 records skipped")
		}
	})
}

// ---------------------------------------------------------------------------
// worstSeverity
// ---------------------------------------------------------------------------

func TestWorstSeverity(t *testing.T) {
	tests := []struct {
		name   string
		badges []Badge
		want   Severity
	}{
		{"empty", nil, SevNone},
		{"single_good", []Badge{{Severity: SevGood}}, SevGood},
		{"single_warning", []Badge{{Severity: SevWarning}}, SevWarning},
		{"mixed_picks_critical", []Badge{{Severity: SevWarning}, {Severity: SevCritical}, {Severity: SevGood}}, SevCritical},
		{"mixed_picks_serious", []Badge{{Severity: SevGood}, {Severity: SevSerious}, {Severity: SevWarning}}, SevSerious},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := worstSeverity(tt.badges); got != tt.want {
				t.Errorf("worstSeverity() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// healthScore
// ---------------------------------------------------------------------------

// statusCards builds `total` FieldCards, the first `critical` of which are
// SevCritical and the rest SevGood.
func statusCards(total, critical int) []FieldCard {
	cs := make([]FieldCard, total)
	for i := 0; i < total; i++ {
		if i < critical {
			cs[i] = FieldCard{Status: SevCritical}
		} else {
			cs[i] = FieldCard{Status: SevGood}
		}
	}
	return cs
}

func TestHealthScoreAllClean(t *testing.T) {
	cards := statusCards(5, 0) // all good
	score, grade, sev := healthScore(cards, 1000, 0)
	if score != 100 || grade != "Excellent" || sev != SevGood {
		t.Errorf("got (%d,%q,%v), want (100,\"Excellent\",SevGood)", score, grade, sev)
	}
}

func TestHealthScoreAllCritical(t *testing.T) {
	cards := statusCards(5, 5) // all critical
	score, grade, sev := healthScore(cards, 1000, 0)
	if score != 0 || grade != "Critical" || sev != SevCritical {
		t.Errorf("got (%d,%q,%v), want (0,\"Critical\",SevCritical)", score, grade, sev)
	}
}

func TestHealthScoreNoFields(t *testing.T) {
	// F==0 -> raw stays 100 per design (no per-field average to compute).
	score, grade, sev := healthScore(nil, 10, 0)
	if score != 100 || grade != "Excellent" || sev != SevGood {
		t.Errorf("got (%d,%q,%v), want (100,\"Excellent\",SevGood)", score, grade, sev)
	}
}

// TestHealthScoreMixed hand-computes the exact score:
//
//	cards: Good, Good, Warning, Serious, Critical (F=5)
//	sum   = 0 + 0 + 0.15 + 0.50 + 1.00 = 1.65
//	raw   = 100 * (1 - 1.65/5) = 100 * (1 - 0.33) = 67.0  -> round -> 67
//	records=70, skipped=30 -> skipRatio = 30/(70+30) = 0.30
//	skipPenalty = round(20 * 0.30) = round(6.0) = 6
//	score = 67 - 6 = 61 -> clamp(61,0,100) = 61
//	grade band: 50 <= 61 < 75 -> "Fair" / SevWarning
func TestHealthScoreMixed(t *testing.T) {
	cards := []FieldCard{
		{Status: SevGood}, {Status: SevGood}, {Status: SevWarning},
		{Status: SevSerious}, {Status: SevCritical},
	}
	score, grade, sev := healthScore(cards, 70, 30)
	if score != 61 || grade != "Fair" || sev != SevWarning {
		t.Errorf("got (%d,%q,%v), want (61,\"Fair\",SevWarning)", score, grade, sev)
	}
}

func TestHealthScoreGradeBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		critical  int
		wantScore int
		wantGrade string
		wantSev   Severity
	}{
		// raw = 100 * (1 - critical/total); skipped=0 so skipPenalty=0, score==raw.
		{"raw_90_excellent_boundary", 10, 1, 90, "Excellent", SevGood},
		{"raw_89_good_just_below_90", 100, 11, 89, "Good", SevGood},
		{"raw_75_good_boundary", 4, 1, 75, "Good", SevGood},
		{"raw_74_fair_just_below_75", 100, 26, 74, "Fair", SevWarning},
		{"raw_50_fair_boundary", 2, 1, 50, "Fair", SevWarning},
		{"raw_49_poor_just_below_50", 100, 51, 49, "Poor", SevSerious},
		{"raw_25_poor_boundary", 4, 3, 25, "Poor", SevSerious},
		{"raw_24_critical_just_below_25", 100, 76, 24, "Critical", SevCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cards := statusCards(tt.total, tt.critical)
			score, grade, sev := healthScore(cards, 100, 0)
			if score != tt.wantScore || grade != tt.wantGrade || sev != tt.wantSev {
				t.Errorf("got (%d,%q,%v), want (%d,%q,%v)", score, grade, sev, tt.wantScore, tt.wantGrade, tt.wantSev)
			}
		})
	}
}
