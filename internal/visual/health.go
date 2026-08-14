package visual

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/hoijunkim/shape/internal/profile"
)

// fieldBadges evaluates the field-level trigger table (design §5.1) against fp
// and its already-selected chart form, in order. Every firing trigger attaches
// its own badge (no early exit); if nothing fires, a single SevGood "Clean"
// badge is returned so the result is never empty. The returned slice is sorted
// by (severity desc, code asc).
func fieldBadges(fp profile.FieldProfile, form ChartForm) []Badge {
	var badges []Badge
	add := func(sev Severity, code, label, detail string) {
		badges = append(badges, Badge{
			Severity: sev,
			Code:     code,
			Icon:     severityIcon[sev],
			Label:    label,
			Detail:   detail,
			Path:     fp.Path,
		})
	}

	// 1. all_null (critical) - mutually exclusive with the null bands below,
	// since NullRate>=1.0 falls outside [NullWarnBand, 1.0).
	if fp.Observations > 0 && fp.NullRate >= 1.0 {
		add(SevCritical, "all_null", "All null", "Every value is null.")
	}

	// 2. type_drift (serious).
	if profile.IsTypeDrift(fp) {
		add(SevSerious, "type_drift", "Mixed types", typeDriftDetail(fp))
	}

	// 3. null_high / null_elevated - mutually exclusive bands.
	switch {
	case fp.NullRate >= NullSeriousBand && fp.NullRate < 1.0:
		add(SevSerious, "null_high", "High null rate", fmtPct(fp.NullRate))
	case fp.NullRate >= NullWarnBand && fp.NullRate < NullSeriousBand:
		add(SevWarning, "null_elevated", "Elevated nulls", fmtPct(fp.NullRate))
	}

	// 4. high_cardinality (warning).
	if form == FormHighCard {
		add(SevWarning, "high_cardinality", "High cardinality", fmtDistinct(fp.DistinctCount, fp.DistinctExact))
	}

	// 5. constant (warning).
	if fp.DistinctExact && fp.DistinctCount == 1 && fp.Observations > 0 {
		add(SevWarning, "constant", "Single value", "Only one distinct value.")
	}

	if len(badges) == 0 {
		add(SevGood, "clean", "Clean", "")
	}

	sortBadges(badges)
	return badges
}

// typeDriftDetail lists the field's non-null kinds and their shares, e.g.
// "Number 60%, String 40%", in the fixed kindOrder (never a map iteration).
func typeDriftDetail(fp profile.FieldProfile) string {
	segs := typeMix(fp)
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		parts = append(parts, s.Label+" "+strconv.Itoa(s.Percent)+"%")
	}
	return strings.Join(parts, ", ")
}

// fileBadges evaluates the file-level trigger table (design §5.1) against a
// whole ProfileResult. These badges carry no Path (Path=="").
func fileBadges(res profile.ProfileResult) []Badge {
	var badges []Badge
	add := func(sev Severity, code, label, detail string) {
		badges = append(badges, Badge{
			Severity: sev,
			Code:     code,
			Icon:     severityIcon[sev],
			Label:    label,
			Detail:   detail,
		})
	}

	if res.Records == 0 {
		add(SevCritical, "no_records", "No records", "The file contains no records.")
	}
	if res.Skipped > 0 {
		add(SevWarning, "skipped_records", "Skipped records", fmtInt(res.Skipped)+" records skipped")
	}

	sortBadges(badges)
	return badges
}

// sortBadges orders badges by (severity desc via severityRank, then code asc),
// per design §5.1.
func sortBadges(badges []Badge) {
	sort.SliceStable(badges, func(i, j int) bool {
		ri, rj := severityRank[badges[i].Severity], severityRank[badges[j].Severity]
		if ri != rj {
			return ri > rj
		}
		return badges[i].Code < badges[j].Code
	})
}

// worstSeverity returns the highest-ranked severity among badges, or SevNone
// if badges is empty.
func worstSeverity(badges []Badge) Severity {
	worst := SevNone
	for _, b := range badges {
		if severityRank[b.Severity] > severityRank[worst] {
			worst = b.Severity
		}
	}
	return worst
}

// healthScore computes the 0-100 health score, letter grade, and headline
// severity for a whole file, per design §5.1. cards must already have Status
// set to worstSeverity(card.Badges) by the caller. A field is as unhealthy as
// its worst badge (no stacking), averaged across fields, minus a bounded skip
// penalty.
func healthScore(cards []FieldCard, records, skipped int) (int, string, Severity) {
	f := len(cards)
	raw := 100.0
	if f > 0 {
		sum := 0.0
		for _, c := range cards {
			sum += fieldPenalty[c.Status]
		}
		raw = 100 * (1 - sum/float64(f))
	}

	skipRatio := safeDiv(float64(skipped), float64(records+skipped))
	skipPenalty := math.Round(SkipPenaltyMax * skipRatio)

	score := int(clamp(math.Round(raw)-skipPenalty, 0, 100))

	var grade string
	var sev Severity
	switch {
	case score >= 90:
		grade, sev = "Excellent", SevGood
	case score >= 75:
		grade, sev = "Good", SevGood
	case score >= 50:
		grade, sev = "Fair", SevWarning
	case score >= 25:
		grade, sev = "Poor", SevSerious
	default:
		grade, sev = "Critical", SevCritical
	}

	return score, grade, sev
}
