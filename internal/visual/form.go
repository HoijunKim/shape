package visual

import "github.com/hoijunkim/shape/internal/profile"

// dominantKind returns the single non-null folded kind ("number", "string",
// "bool", "array", "object") observed in fp.TypeDist. Int and float fold to
// "number". Returns "" if zero or more than one non-null kind has a positive
// share (all-null/no-observations, or ambiguous/drifted).
func dominantKind(fp profile.FieldProfile) string {
	kinds := map[string]bool{}
	for k, frac := range fp.TypeDist {
		if frac <= 0 || k == profile.KindNull {
			continue
		}
		switch k {
		case profile.KindInt, profile.KindFloat:
			kinds["number"] = true
		default:
			kinds[string(k)] = true
		}
	}
	if len(kinds) != 1 {
		return ""
	}
	for k := range kinds {
		return k
	}
	return ""
}

// selectForm chooses the chart form for a field, per design §3. First match
// wins. Returns the form and the resolved Kind string.
func selectForm(fp profile.FieldProfile) (ChartForm, string) {
	if fp.Observations == 0 || fp.NullRate >= 1.0 {
		return FormEmpty, "empty"
	}
	if profile.IsTypeDrift(fp) {
		return FormTypeMix, "mixed"
	}
	dom := dominantKind(fp)
	switch dom {
	case "number":
		if fp.DistinctExact && fp.DistinctCount >= 1 && fp.DistinctCount <= DiscreteNumericMax {
			return FormCategorical, "number"
		}
		if len(fp.Histogram) > 0 {
			return FormHistogram, "number"
		}
		return FormMeter, "number"
	case "string":
		if fp.DistinctExact && fp.DistinctCount >= 1 && fp.DistinctCount <= CategoricalMaxDistinct {
			return FormCategorical, "string"
		}
		return FormHighCard, "string"
	case "array":
		return FormArray, "array"
	case "bool", "object":
		return FormMeter, dom
	}
	return FormEmpty, "empty"
}

// enumLike reports whether a string field looks like a small enum: exact
// distinct count within [EnumMinDistinct, EnumMaxDistinct], every distinct
// value captured in TopValues, and the dominant kind is "string".
func enumLike(fp profile.FieldProfile) bool {
	return fp.DistinctExact &&
		fp.DistinctCount >= EnumMinDistinct && fp.DistinctCount <= EnumMaxDistinct &&
		fp.DistinctCount == len(fp.TopValues) &&
		dominantKind(fp) == "string"
}
