package visual

import "github.com/hoijun-kim/shape/internal/profile"

// displayHistogram re-bins fp.Histogram (<=64 variable-width streaming
// centroids) into DisplayBins=20 equal-width bars using point-mass at each
// centroid's Value: the centroid's whole Count drops into the bar containing
// it. This keeps counts integer and makes Σ bins == total exactly. The true
// extent is *fp.Min/*fp.Max, not the drifted centroid extremes.
func displayHistogram(fp profile.FieldProfile) Histogram {
	lo, hi := *fp.Min, *fp.Max
	total := 0
	for _, b := range fp.Histogram {
		total += b.Count
	}

	if hi <= lo { // constant field
		return Histogram{
			Min: lo, Max: hi, BinWidth: 0, Total: total, MaxCount: total,
			Bins: []HistBar{{Lo: lo, Hi: hi, Count: total, Frac: 1, Label: fmtNum(lo)}},
		}
	}

	w := (hi - lo) / DisplayBins
	var counts [DisplayBins]int
	for _, b := range fp.Histogram {
		idx := int((b.Value - lo) / w) // floor
		if idx < 0 {
			idx = 0
		}
		if idx >= DisplayBins {
			idx = DisplayBins - 1 // Value==hi clamps to last bin
		}
		counts[idx] += b.Count
	}

	maxCount := 0
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}

	bins := make([]HistBar, DisplayBins)
	for i, c := range counts {
		binLo := lo + float64(i)*w
		binHi := lo + float64(i+1)*w
		bins[i] = HistBar{
			Lo: binLo, Hi: binHi, Count: c,
			Frac:  safeDiv(float64(c), float64(maxCount)),
			Label: fmtNum(binLo) + "-" + fmtNum(binHi),
		}
	}

	return Histogram{Min: lo, Max: hi, BinWidth: w, Bins: bins, MaxCount: maxCount, Total: total}
}
