package visual

import (
	"fmt"
	"math"

	"github.com/hoijunkim/shape/internal/profile"
)

// clamp restricts f to the closed interval [lo, hi].
func clamp(f, lo, hi float64) float64 {
	if f < lo {
		return lo
	}
	if f > hi {
		return hi
	}
	return f
}

// categorical builds the bar-chart geometry for a categorical field (discrete
// numeric or low/mid-cardinality string) from fp.TopValues, per design §2.2.
// TopValues is already sorted (count desc, value asc); bars are capped at
// TopK. Total is the observation count backing the percentages (the field's
// dominant, and only, non-null kind). Returns nil only when there are no top
// values to show.
func categorical(fp profile.FieldProfile) *Categorical {
	if len(fp.TopValues) == 0 {
		return nil
	}

	n := len(fp.TopValues)
	if n > TopK {
		n = TopK
	}

	// fp.Observations includes null observations, but fp.TopValues/DistinctCount
	// do not (the accumulator counts nulls into obs but never into addCount).
	// Use the non-null count as the percentage denominator so bar percentages
	// (and the Other bucket) don't fold null mass into the categorical shares.
	total := int(math.Round(float64(fp.Observations) * (1 - fp.NullRate)))
	if total <= 0 {
		return nil
	}
	maxCount := fp.TopValues[0].Count

	bars := make([]CategoryBar, n)
	sum := 0
	for i := 0; i < n; i++ {
		vc := fp.TopValues[i]
		bars[i] = CategoryBar{
			Label:   vc.Value,
			Count:   vc.Count,
			Frac:    safeDiv(float64(vc.Count), float64(maxCount)),
			Percent: int(math.Round(100 * safeDiv(float64(vc.Count), float64(total)))),
		}
		sum += vc.Count
	}

	cat := &Categorical{
		Bars:      bars,
		Total:     total,
		MaxCount:  maxCount,
		Truncated: fp.DistinctCount > len(bars),
	}

	if cat.Truncated {
		otherCount := total - sum
		cat.Other = &CategoryBar{
			Label:   "other",
			Count:   otherCount,
			Frac:    safeDiv(float64(otherCount), float64(maxCount)),
			Percent: int(math.Round(100 * safeDiv(float64(otherCount), float64(total)))),
		}
	}

	return cat
}

// highCard builds the high-cardinality string hero payload, per design §2.2.
// Sample takes the first CardinalitySample values already present in
// fp.TopValues (count desc, value asc); StrLen is attached only when both
// StrLenMin and StrLenMax were recorded.
func highCard(fp profile.FieldProfile) *HighCardString {
	var sample []string
	for i, vc := range fp.TopValues {
		if i >= CardinalitySample {
			break
		}
		sample = append(sample, vc.Value)
	}

	// fp.Observations includes null observations, but fp.DistinctCount does not
	// (the accumulator counts nulls into obs but never into addCount), so the
	// non-null count is the correct denominator here too.
	nonNull := int(math.Round(float64(fp.Observations) * (1 - fp.NullRate)))

	hc := &HighCardString{
		Distinct:     fp.DistinctCount,
		DistinctText: fmtDistinct(fp.DistinctCount, fp.DistinctExact),
		UniqueRatio:  clamp(safeDiv(float64(fp.DistinctCount), float64(nonNull)), 0, 1),
		Sample:       sample,
	}

	if fp.StrLenMin != nil && fp.StrLenMax != nil {
		hc.StrLen = &StrLenBar{
			Min:  *fp.StrLenMin,
			Max:  *fp.StrLenMax,
			Text: fmt.Sprintf("%d-%d chars", *fp.StrLenMin, *fp.StrLenMax),
		}
	}

	return hc
}

// arrayBreakdown builds the array-container hero payload by looking up the
// field's "[]" element sibling in index, per design §2.2/§5. Absent means the
// array never produced an element observation (e.g. always empty), in which
// case Present is false and ElementCount/ElementTypes stay zero/nil.
func arrayBreakdown(fp profile.FieldProfile, index map[string]profile.FieldProfile) *ArrayBreakdown {
	elementPath := fp.Path + "[]"
	ab := &ArrayBreakdown{ElementPath: elementPath}

	elem, ok := index[elementPath]
	if !ok {
		return ab
	}

	ab.Present = true
	ab.ElementCount = elem.Observations
	ab.ElementTypes = typeMix(elem)
	return ab
}
