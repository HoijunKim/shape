package visual

import (
	"math"
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// ---------------------------------------------------------------------------
// typeMix
// ---------------------------------------------------------------------------

func TestTypeMixClean(t *testing.T) {
	fp := profile.FieldProfile{Observations: 10, NullRate: 0, TypeDist: num(profile.KindString)}
	segs := typeMix(fp)
	if len(segs) != 1 || segs[0].Kind != "string" || segs[0].Offset != 0 {
		t.Fatalf("clean typeMix = %+v", segs)
	}
	if segs[0].Frac < 0.999 {
		t.Errorf("frac = %v, want ~1", segs[0].Frac)
	}
	if segs[0].Count != 10 {
		t.Errorf("count = %d, want 10", segs[0].Count)
	}
	if segs[0].Percent != 100 {
		t.Errorf("percent = %d, want 100", segs[0].Percent)
	}
	if segs[0].Series != 1 { // "string" is index 1 in kindOrder
		t.Errorf("series = %d, want 1", segs[0].Series)
	}
	if segs[0].Label != "String" {
		t.Errorf("label = %q, want %q", segs[0].Label, "String")
	}
}

func TestTypeMixDrift(t *testing.T) {
	// 100 obs, 10% null. Non-null 90 split: int 54 (number), string 27, bool 9.
	fp := profile.FieldProfile{
		Observations: 100,
		NullRate:     0.10,
		TypeDist: map[profile.JSONKind]float64{
			profile.KindInt:    0.54,
			profile.KindString: 0.27,
			profile.KindBool:   0.09,
			profile.KindNull:   0.10,
		},
	}
	segs := typeMix(fp)
	if len(segs) != 3 {
		t.Fatalf("len(segs) = %d, want 3: %+v", len(segs), segs)
	}

	wantKinds := []string{"number", "string", "bool"}
	wantSeries := []int{0, 1, 2}
	wantFrac := []float64{0.6, 0.3, 0.1}
	wantOffset := []float64{0, 0.6, 0.9}
	wantCount := []int{54, 27, 9}
	wantPercent := []int{60, 30, 10}

	fracSum := 0.0
	countSum := 0
	percentSum := 0
	for i, seg := range segs {
		if seg.Kind != wantKinds[i] {
			t.Errorf("seg[%d].Kind = %q, want %q (order must follow kindOrder)", i, seg.Kind, wantKinds[i])
		}
		if seg.Series != wantSeries[i] {
			t.Errorf("seg[%d].Series = %d, want %d", i, seg.Series, wantSeries[i])
		}
		if !almostEqual(seg.Frac, wantFrac[i]) {
			t.Errorf("seg[%d].Frac = %v, want %v", i, seg.Frac, wantFrac[i])
		}
		if !almostEqual(seg.Offset, wantOffset[i]) {
			t.Errorf("seg[%d].Offset = %v, want %v (must be cumulative running sum)", i, seg.Offset, wantOffset[i])
		}
		if seg.Count != wantCount[i] {
			t.Errorf("seg[%d].Count = %d, want %d", i, seg.Count, wantCount[i])
		}
		if seg.Percent != wantPercent[i] {
			t.Errorf("seg[%d].Percent = %d, want %d", i, seg.Percent, wantPercent[i])
		}
		fracSum += seg.Frac
		countSum += seg.Count
		percentSum += seg.Percent
	}
	if !almostEqual(fracSum, 1.0) {
		t.Errorf("Σ Frac = %v, want ~1", fracSum)
	}
	if countSum != 90 { // Observations * (1 - NullRate)
		t.Errorf("Σ Count = %d, want 90", countSum)
	}
	if percentSum != 100 {
		t.Errorf("Σ Percent = %d, want 100", percentSum)
	}
}

func TestTypeMixAllNullGuard(t *testing.T) {
	fp := profile.FieldProfile{Observations: 5, NullRate: 1.0, TypeDist: num(profile.KindNull)}
	segs := typeMix(fp)
	if len(segs) != 0 {
		t.Errorf("all-null typeMix = %+v, want empty (divide-by-zero guard)", segs)
	}
}

// ---------------------------------------------------------------------------
// meter / nullStatus
// ---------------------------------------------------------------------------

func TestMeterTexts(t *testing.T) {
	fp := profile.FieldProfile{PresenceRate: 0.8, NullRate: 0.25}
	m := meter(fp)
	if m.PresenceRate != 0.8 || m.NullRate != 0.25 {
		t.Fatalf("meter rates = %+v, want PresenceRate 0.8, NullRate 0.25", m)
	}
	if m.PresenceText != "80%" {
		t.Errorf("PresenceText = %q, want 80%%", m.PresenceText)
	}
	if m.NullText != "25%" {
		t.Errorf("NullText = %q, want 25%%", m.NullText)
	}
	if m.NullStatus != SevWarning {
		t.Errorf("NullStatus = %q, want %q", m.NullStatus, SevWarning)
	}
}

func TestMeterNullStatusPropagation(t *testing.T) {
	cases := []struct {
		rate float64
		want Severity
	}{
		{0, SevNone}, {0.5, SevSerious}, {1.0, SevCritical},
	}
	for _, c := range cases {
		m := meter(profile.FieldProfile{NullRate: c.rate})
		if m.NullStatus != c.want {
			t.Errorf("meter(NullRate=%v).NullStatus = %q, want %q", c.rate, m.NullStatus, c.want)
		}
	}
}

func TestNullStatusBands(t *testing.T) {
	cases := map[float64]Severity{0.19: SevNone, 0.20: SevWarning, 0.49: SevWarning, 0.50: SevSerious, 1.0: SevCritical}
	for r, want := range cases {
		if got := nullStatus(r); got != want {
			t.Errorf("nullStatus(%v) = %q, want %q", r, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// numericStats
// ---------------------------------------------------------------------------

func TestNumericStatsKeyOrderAndMean(t *testing.T) {
	fp := profile.FieldProfile{
		Min:           fptr(1),
		Max:           fptr(9),
		Median:        fptr(5),
		P95:           fptr(8),
		Histogram:     hbins(2, 3, 6, 1), // mean = (2*3+6*1)/(3+1) = 12/4 = 3
		DistinctCount: 4,
		DistinctExact: true,
	}
	stats := numericStats(fp)
	wantKeys := []string{"min", "mean", "median", "p95", "max", "distinct"}
	if len(stats) != len(wantKeys) {
		t.Fatalf("len(stats) = %d, want %d: %+v", len(stats), len(wantKeys), stats)
	}
	for i, k := range wantKeys {
		if stats[i].Key != k {
			t.Errorf("stats[%d].Key = %q, want %q", i, stats[i].Key, k)
		}
	}

	byKey := map[string]Stat{}
	for _, s := range stats {
		byKey[s.Key] = s
	}

	if got := byKey["min"]; got.Text != "1" || got.Approx {
		t.Errorf("min = %+v, want Text=1 Approx=false", got)
	}
	if got := byKey["max"]; got.Text != "9" || got.Approx {
		t.Errorf("max = %+v, want Text=9 Approx=false", got)
	}
	if got := byKey["mean"]; got.Text != "3" || !got.Approx {
		t.Errorf("mean = %+v, want Text=3 Approx=true", got)
	}
	if got := byKey["median"]; got.Text != "5" || !got.Approx {
		t.Errorf("median = %+v, want Text=5 Approx=true", got)
	}
	if got := byKey["p95"]; got.Text != "8" || !got.Approx {
		t.Errorf("p95 = %+v, want Text=8 Approx=true", got)
	}
	if got := byKey["distinct"]; got.Text != "4" || got.Approx {
		t.Errorf("distinct = %+v, want Text=4 Approx=false", got)
	}
}

func TestNumericStatsPromotedDistinctIsApprox(t *testing.T) {
	fp := profile.FieldProfile{
		Min:           fptr(0),
		Max:           fptr(100),
		DistinctCount: 5000,
		DistinctExact: false,
	}
	stats := numericStats(fp)
	if len(stats) != 3 { // min, max, distinct (no histogram -> no mean/median/p95)
		t.Fatalf("len(stats) = %d, want 3: %+v", len(stats), stats)
	}
	wantKeys := []string{"min", "max", "distinct"}
	for i, k := range wantKeys {
		if stats[i].Key != k {
			t.Errorf("stats[%d].Key = %q, want %q", i, stats[i].Key, k)
		}
	}
	d := stats[2]
	if d.Text != "~5,000" || !d.Approx {
		t.Errorf("distinct = %+v, want Text=~5,000 Approx=true", d)
	}
}

func TestNumericStatsSkipsNaN(t *testing.T) {
	fp := profile.FieldProfile{
		Min:           fptr(nan()),
		Max:           fptr(10),
		DistinctCount: 1,
		DistinctExact: true,
	}
	stats := numericStats(fp)
	for _, s := range stats {
		if s.Key == "min" {
			t.Errorf("NaN min must be skipped, got %+v", s)
		}
	}
	if len(stats) != 2 { // max, distinct only
		t.Errorf("len(stats) = %d, want 2 (min skipped): %+v", len(stats), stats)
	}
}

func TestNumericStatsSkipsInf(t *testing.T) {
	fp := profile.FieldProfile{
		Min:           fptr(0),
		Max:           fptr(inf(1)),
		DistinctCount: 1,
		DistinctExact: true,
	}
	stats := numericStats(fp)
	for _, s := range stats {
		if s.Key == "max" {
			t.Errorf("Inf max must be skipped, got %+v", s)
		}
	}
	if len(stats) != 2 { // min, distinct only
		t.Errorf("len(stats) = %d, want 2 (max skipped): %+v", len(stats), stats)
	}
}

func TestNumericStatsNoMinNoPanic(t *testing.T) {
	fp := profile.FieldProfile{DistinctCount: 1, DistinctExact: true}
	stats := numericStats(fp)
	if stats != nil {
		t.Errorf("numericStats with nil Min = %+v, want nil", stats)
	}
}

// ---------------------------------------------------------------------------
// stringStats / otherStats
// ---------------------------------------------------------------------------

func TestStringStats(t *testing.T) {
	fp := profile.FieldProfile{DistinctCount: 120, DistinctExact: false, Observations: 500}
	stats := stringStats(fp)
	if len(stats) != 2 {
		t.Fatalf("len(stats) = %d, want 2: %+v", len(stats), stats)
	}
	if stats[0].Key != "distinct" || stats[0].Text != "~120" || !stats[0].Approx {
		t.Errorf("stats[0] = %+v, want Key=distinct Text=~120 Approx=true", stats[0])
	}
	if stats[1].Key != "observations" || stats[1].Text != "500" || stats[1].Approx {
		t.Errorf("stats[1] = %+v, want Key=observations Text=500 Approx=false", stats[1])
	}
}

func TestStringStatsExactDistinct(t *testing.T) {
	fp := profile.FieldProfile{DistinctCount: 3, DistinctExact: true, Observations: 10}
	stats := stringStats(fp)
	if stats[0].Text != "3" || stats[0].Approx {
		t.Errorf("stats[0] = %+v, want Text=3 Approx=false", stats[0])
	}
}

func TestOtherStats(t *testing.T) {
	fp := profile.FieldProfile{Observations: 42}
	stats := otherStats(fp)
	if len(stats) != 1 || stats[0].Key != "observations" || stats[0].Text != "42" {
		t.Fatalf("otherStats = %+v, want single observations=42", stats)
	}
}

// ---------------------------------------------------------------------------
// sparkFromBins / sparkFromBars
// ---------------------------------------------------------------------------

func TestSparkFromBins(t *testing.T) {
	bins := []HistBar{{Frac: 0.2}, {Frac: 0.5}, {Frac: 1.0}, {Frac: 0.1}}
	pts := sparkFromBins(bins)
	if len(pts) != 4 {
		t.Fatalf("len(pts) = %d, want 4", len(pts))
	}
	wantX := []float64{0.125, 0.375, 0.625, 0.875} // (i+0.5)/4
	for i, p := range pts {
		if !almostEqual(p.X, wantX[i]) {
			t.Errorf("pts[%d].X = %v, want %v", i, p.X, wantX[i])
		}
		if p.Y != bins[i].Frac {
			t.Errorf("pts[%d].Y = %v, want %v", i, p.Y, bins[i].Frac)
		}
	}
}

func TestSparkFromBars(t *testing.T) {
	bars := []CategoryBar{{Frac: 1.0}, {Frac: 0.4}, {Frac: 0.2}}
	pts := sparkFromBars(bars)
	if len(pts) != 3 {
		t.Fatalf("len(pts) = %d, want 3", len(pts))
	}
	wantX := []float64{0.5 / 3, 1.5 / 3, 2.5 / 3}
	for i, p := range pts {
		if !almostEqual(p.X, wantX[i]) {
			t.Errorf("pts[%d].X = %v, want %v", i, p.X, wantX[i])
		}
		if p.Y != bars[i].Frac {
			t.Errorf("pts[%d].Y = %v, want %v", i, p.Y, bars[i].Frac)
		}
	}
}

func TestSparkEmptyReturnsNil(t *testing.T) {
	if pts := sparkFromBins(nil); pts != nil {
		t.Errorf("sparkFromBins(nil) = %+v, want nil", pts)
	}
	if pts := sparkFromBars(nil); pts != nil {
		t.Errorf("sparkFromBars(nil) = %+v, want nil", pts)
	}
	if pts := sparkFromBins([]HistBar{}); pts != nil {
		t.Errorf("sparkFromBins(empty) = %+v, want nil", pts)
	}
	if pts := sparkFromBars([]CategoryBar{}); pts != nil {
		t.Errorf("sparkFromBars(empty) = %+v, want nil", pts)
	}
}
