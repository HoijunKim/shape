package visual

import (
	"math"

	"github.com/hoijun-kim/shape/internal/profile"
)

// ---------------------------------------------------------------------------
// 5. Sparkline, type-mix, meter, stats
// ---------------------------------------------------------------------------

// familyOf folds a JSONKind into its kindOrder family: int and float collapse
// to "number"; everything else keeps its own name.
func familyOf(k profile.JSONKind) string {
	switch k {
	case profile.KindInt, profile.KindFloat:
		return "number"
	default:
		return string(k)
	}
}

// typeMix projects fp.TypeDist through the fixed kindOrder slice (never the
// map) over non-null observations, per design §5. Always returns >=1 segment
// for a field with at least one non-null observation; returns nil for an
// all-null/no-observation field (guards the 1-NullRate divide-by-zero).
func typeMix(fp profile.FieldProfile) []TypeSegment {
	nonNull := 1 - fp.NullRate
	if nonNull <= 0 {
		return nil
	}

	// Fold TypeDist shares into fixed kindOrder slots. Iterating fp.TypeDist
	// here only accumulates into fixed-size slots keyed by kindOrder index,
	// so the resulting order below still comes solely from kindOrder.
	folded := make([]float64, len(kindOrder))
	for k, frac := range fp.TypeDist {
		if frac <= 0 || k == profile.KindNull {
			continue
		}
		family := familyOf(k)
		for i, ko := range kindOrder {
			if ko.Kind == family {
				folded[i] += frac
				break
			}
		}
	}

	var segs []TypeSegment
	offset := 0.0
	for i, ko := range kindOrder {
		s := folded[i]
		if s <= 0 {
			continue
		}
		frac := s / nonNull
		segs = append(segs, TypeSegment{
			Kind:    ko.Kind,
			Label:   ko.Label,
			Frac:    frac,
			Offset:  offset,
			Count:   int(math.Round(s * float64(fp.Observations))),
			Percent: int(math.Round(100 * frac)),
			Series:  i,
		})
		offset += frac
	}
	return segs
}

// nullStatus buckets a null rate into a severity band per design §5.
func nullStatus(rate float64) Severity {
	switch {
	case rate >= 1.0:
		return SevCritical
	case rate >= NullSeriousBand:
		return SevSerious
	case rate >= NullWarnBand:
		return SevWarning
	default:
		return SevNone
	}
}

// meter builds the presence/null gauge geometry for every field card.
func meter(fp profile.FieldProfile) Meter {
	return Meter{
		PresenceRate: fp.PresenceRate,
		NullRate:     fp.NullRate,
		PresenceText: fmtPct(fp.PresenceRate),
		NullText:     fmtPct(fp.NullRate),
		NullStatus:   nullStatus(fp.NullRate),
	}
}

// nonFinite reports whether f is NaN or +/-Inf; such stats are never emitted.
func nonFinite(f float64) bool {
	return math.IsNaN(f) || math.IsInf(f, 0)
}

// numericStats builds the fixed-order (min, mean, median, p95, max, distinct)
// stat rows for a numeric field, per design §5. Returns nil if fp has no
// recorded numeric extent (Min/Max unset). NaN/Inf-valued stats are skipped.
func numericStats(fp profile.FieldProfile) []Stat {
	if fp.Min == nil || fp.Max == nil {
		return nil
	}

	var stats []Stat
	if !nonFinite(*fp.Min) {
		stats = append(stats, Stat{Key: "min", Label: "Min", Text: fmtNum(*fp.Min)})
	}

	if len(fp.Histogram) > 0 {
		var sumV, sumC float64
		for _, b := range fp.Histogram {
			sumV += b.Value * float64(b.Count)
			sumC += float64(b.Count)
		}
		if sumC > 0 {
			mean := sumV / sumC
			if !nonFinite(mean) {
				stats = append(stats, Stat{Key: "mean", Label: "Mean", Text: fmtNum(mean), Approx: true})
			}
		}
		if fp.Median != nil && !nonFinite(*fp.Median) {
			stats = append(stats, Stat{Key: "median", Label: "Median", Text: fmtNum(*fp.Median), Approx: true})
		}
		if fp.P95 != nil && !nonFinite(*fp.P95) {
			stats = append(stats, Stat{Key: "p95", Label: "P95", Text: fmtNum(*fp.P95), Approx: true})
		}
	}

	if !nonFinite(*fp.Max) {
		stats = append(stats, Stat{Key: "max", Label: "Max", Text: fmtNum(*fp.Max)})
	}

	stats = append(stats, Stat{
		Key:    "distinct",
		Label:  "Distinct",
		Text:   fmtDistinct(fp.DistinctCount, fp.DistinctExact),
		Approx: !fp.DistinctExact,
	})
	return stats
}

// stringStats builds the stat rows for a string field: distinct then
// observations, per design §5.
func stringStats(fp profile.FieldProfile) []Stat {
	return []Stat{
		{Key: "distinct", Label: "Distinct", Text: fmtDistinct(fp.DistinctCount, fp.DistinctExact), Approx: !fp.DistinctExact},
		{Key: "observations", Label: "Observations", Text: fmtInt(fp.Observations)},
	}
}

// otherStats builds the stat rows for bool/array/object fields: observations
// only, per design §5.
func otherStats(fp profile.FieldProfile) []Stat {
	return []Stat{
		{Key: "observations", Label: "Observations", Text: fmtInt(fp.Observations)},
	}
}

// sparkFromBins builds sparkline points from display histogram bars.
func sparkFromBins(bins []HistBar) []SparkPoint {
	n := len(bins)
	if n == 0 {
		return nil
	}
	pts := make([]SparkPoint, n)
	for i, b := range bins {
		pts[i] = SparkPoint{X: (float64(i) + 0.5) / float64(n), Y: b.Frac}
	}
	return pts
}

// sparkFromBars builds sparkline points from categorical bars (desc count).
func sparkFromBars(bars []CategoryBar) []SparkPoint {
	n := len(bars)
	if n == 0 {
		return nil
	}
	pts := make([]SparkPoint, n)
	for i, b := range bars {
		pts[i] = SparkPoint{X: (float64(i) + 0.5) / float64(n), Y: b.Frac}
	}
	return pts
}
