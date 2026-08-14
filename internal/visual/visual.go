package visual

import (
	"sort"
	"strconv"
	"strings"

	"github.com/hoijunkim/shape/internal/profile"
)

// FromProfile builds the whole-file dashboard model, per design §2. It
// builds a field index over res.Fields (used only by array containers to
// reach their "path[]" element sibling), one FieldCard per field (in
// ProfileResult.Fields order, already path-sorted), the whole-model Summary
// and fixed 5-tile KPI row, then collects and sorts every field- and
// file-level badge.
func FromProfile(res profile.ProfileResult, opts Options) VisualModel {
	index := make(map[string]profile.FieldProfile, len(res.Fields))
	for _, fp := range res.Fields {
		index[fp.Path] = fp
	}

	cards := make([]FieldCard, len(res.Fields))
	for i, fp := range res.Fields {
		cards[i] = buildCard(fp, index)
	}

	summary := buildSummary(res, cards, opts)
	kpis := buildKPIs(summary, cards)

	var badges []Badge
	for _, c := range cards {
		badges = append(badges, c.Badges...)
	}
	badges = append(badges, fileBadges(res)...)
	sortModelBadges(badges)

	return VisualModel{
		Summary: summary,
		KPIs:    kpis,
		Fields:  cards,
		Badges:  badges,
	}
}

// sortModelBadges orders the whole-model badge list by (severity desc, Path
// asc, Code asc), per design §5.1. This differs from sortBadges (health.go),
// which sorts a single card's own badges (severity desc, Code asc only) -
// Path is redundant there since every badge on a card shares the same Path.
func sortModelBadges(badges []Badge) {
	sort.SliceStable(badges, func(i, j int) bool {
		bi, bj := badges[i], badges[j]
		if ri, rj := severityRank[bi.Severity], severityRank[bj.Severity]; ri != rj {
			return ri > rj
		}
		if bi.Path != bj.Path {
			return bi.Path < bj.Path
		}
		return bi.Code < bj.Code
	})
}

// buildCard assembles one FieldCard for fp, per design §2. index is the
// whole-model path->FieldProfile lookup (used only by array containers to
// reach their "path[]" element sibling via arrayBreakdown).
func buildCard(fp profile.FieldProfile, index map[string]profile.FieldProfile) FieldCard {
	form, kind := selectForm(fp)

	card := FieldCard{
		Path:         fp.Path,
		DisplayName:  displayName(fp.Path),
		Form:         form,
		Kind:         kind,
		EnumLike:     enumLike(fp),
		ArrayElement: strings.HasSuffix(fp.Path, "[]"),
		Observations: fp.Observations,
	}

	// Base payload: always set, independent of chart form.
	card.TypeMix = typeMix(fp)
	card.Meter = meter(fp)
	switch {
	case fp.Min != nil:
		card.Stats = numericStats(fp)
	case kind == "string":
		card.Stats = stringStats(fp)
	default:
		card.Stats = otherStats(fp)
	}
	card.Badges = fieldBadges(fp, form)
	card.Status = worstSeverity(card.Badges)

	// Hero payload: exactly one, selected by form (none for
	// FormTypeMix/FormMeter/FormEmpty).
	switch form {
	case FormHistogram:
		h := displayHistogram(fp)
		card.Histogram = &h
		card.Sparkline = sparkFromBins(h.Bins)
	case FormCategorical:
		card.Categorical = categorical(fp)
		if card.Categorical != nil {
			card.Sparkline = sparkFromBars(card.Categorical.Bars)
		}
	case FormHighCard:
		card.HighCard = highCard(fp)
	case FormArray:
		card.Array = arrayBreakdown(fp, index)
	}

	return card
}

// buildSummary assembles the whole-file Summary, per design §2/§2.1.
// Format resolution: opts.Format if set, else deriveFormat(opts.Name) if
// opts.Name is set, else deriveFormat(res.Source). Name: opts.Name if set,
// else res.Source.
func buildSummary(res profile.ProfileResult, cards []FieldCard, opts Options) Summary {
	name := opts.Name
	if name == "" {
		name = res.Source
	}

	format := opts.Format
	if format == "" {
		if opts.Name != "" {
			format = deriveFormat(opts.Name)
		} else {
			format = deriveFormat(res.Source)
		}
	}

	warningCount := 0
	for _, c := range cards {
		if severityRank[c.Status] >= severityRank[SevWarning] {
			warningCount++
		}
	}

	score, grade, sev := healthScore(cards, res.Records, res.Skipped)

	return Summary{
		Name:           name,
		Format:         format,
		Records:        res.Records,
		Skipped:        res.Skipped,
		FieldCount:     len(cards),
		WarningCount:   warningCount,
		HealthScore:    score,
		HealthGrade:    grade,
		HealthSeverity: sev,
	}
}

// buildKPIs assembles the fixed 5-tile KPI row (records, fields, format,
// warnings, health), per design §2.1. cards supplies the "worst card Status"
// needed for the warnings tile's severity when WarningCount>0 - Summary alone
// does not carry per-card detail.
func buildKPIs(summary Summary, cards []FieldCard) []KPITile {
	records := KPITile{Key: "records", Label: "Records", Value: fmtInt(summary.Records), Raw: float64(summary.Records)}
	if summary.Skipped > 0 {
		records.Sub = fmtInt(summary.Skipped) + " skipped"
		records.Severity = SevWarning
	}

	fields := KPITile{Key: "fields", Label: "Fields", Value: fmtInt(summary.FieldCount), Raw: float64(summary.FieldCount)}

	format := KPITile{Key: "format", Label: "Format", Value: summary.Format}

	warnings := KPITile{Key: "warnings", Label: "Warnings", Value: fmtInt(summary.WarningCount), Raw: float64(summary.WarningCount)}
	if summary.WarningCount == 0 {
		warnings.Severity = SevGood
	} else {
		worst := SevNone
		for _, c := range cards {
			if severityRank[c.Status] > severityRank[worst] {
				worst = c.Status
			}
		}
		warnings.Severity = worst
	}

	health := KPITile{
		Key:      "health",
		Label:    "Health",
		Value:    strconv.Itoa(summary.HealthScore),
		Raw:      float64(summary.HealthScore),
		Sub:      summary.HealthGrade,
		Severity: summary.HealthSeverity,
		Hero:     true,
	}

	return []KPITile{records, fields, format, warnings, health}
}

// displayName derives a field's short display label from its dot-path, per
// design §2.1: the last "."-segment (root "$" stays "$"; a trailing "[]"
// suffix stays attached to that last segment, e.g. "user.tags[]" ->
// "tags[]").
func displayName(path string) string {
	if path == "$" {
		return "$"
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}
