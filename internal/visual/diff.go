package visual

import (
	"strings"

	"github.com/hoijun-kim/shape/internal/diff"
)

// diffGroupOrder fixes the DiffGroup partition/display order, per design §6:
// added -> removed -> changed. A kind with no changes is omitted from Groups
// entirely; its count still surfaces via diffKPIs.
var diffGroupOrder = []struct {
	Kind  diff.ChangeKind
	Label string
}{
	{diff.Added, "Added"},
	{diff.Removed, "Removed"},
	{diff.Changed, "Changed"},
}

// FromDiff builds the two-file comparison model, per design §6. Old/New/
// Breaking/Caveats are copied straight from d; KPIs, verdict, path-
// partitioned groups, and diff-derived critical badges are all computed from
// d.Changes, which arrives already path-sorted from the differ.
func FromDiff(d diff.DiffResult) DiffVisualModel {
	verdict, verdictSev := diffVerdict(d)

	return DiffVisualModel{
		Old:             d.Old,
		New:             d.New,
		Breaking:        d.HasBreaking(),
		Verdict:         verdict,
		VerdictSeverity: verdictSev,
		KPIs:            diffKPIs(d),
		Groups:          diffGroups(d),
		Badges:          diffBadges(d),
		Caveats:         d.Caveats,
	}
}

// diffVerdict picks the headline verdict, per design §6: any breaking change
// wins outright regardless of how many other changes exist; otherwise any
// change at all is merely "compatible"; no changes is the clean case.
func diffVerdict(d diff.DiffResult) (string, Severity) {
	switch {
	case d.Breaking > 0:
		return "Breaking changes", SevCritical
	case len(d.Changes) > 0:
		return "Compatible changes", SevWarning
	default:
		return "No changes", SevGood
	}
}

// diffKPIs assembles the fixed 5-tile KPI row, per design §6.
func diffKPIs(d diff.DiffResult) []KPITile {
	compared := KPITile{Key: "compared", Label: "Compared", Value: fmtInt(d.Compared), Raw: float64(d.Compared)}

	added := KPITile{Key: "added", Label: "Added", Value: fmtInt(d.Added), Raw: float64(d.Added)}
	if d.Added > 0 {
		added.Severity = SevGood
	}

	removed := KPITile{Key: "removed", Label: "Removed", Value: fmtInt(d.Removed), Raw: float64(d.Removed)}
	if d.Removed > 0 {
		removed.Severity = SevWarning
	}

	changed := KPITile{Key: "changed", Label: "Changed", Value: fmtInt(d.Changed), Raw: float64(d.Changed)}
	if d.Changed > 0 {
		changed.Severity = SevWarning
	}

	breaking := KPITile{Key: "breaking", Label: "Breaking", Value: fmtInt(d.Breaking), Raw: float64(d.Breaking), Hero: true}
	if d.Breaking > 0 {
		breaking.Severity = SevCritical
	} else {
		breaking.Severity = SevGood
	}

	return []KPITile{compared, added, removed, changed, breaking}
}

// diffGroups partitions d.Changes (already path-sorted) by Change.Kind into
// the fixed added->removed->changed display order, per design §6. Bucketing
// preserves each Change's relative position from d.Changes, so rows within a
// group stay path-sorted. A kind with zero changes is omitted rather than
// emitted as an empty group.
func diffGroups(d diff.DiffResult) []DiffGroup {
	buckets := map[diff.ChangeKind][]diff.Change{}
	for _, ch := range d.Changes {
		buckets[ch.Kind] = append(buckets[ch.Kind], ch)
	}

	var groups []DiffGroup
	for _, o := range diffGroupOrder {
		changes := buckets[o.Kind]
		if len(changes) == 0 {
			continue
		}
		rows := make([]DiffRow, len(changes))
		for i, ch := range changes {
			rows[i] = diffRow(ch)
		}
		groups = append(groups, DiffGroup{
			Kind:  string(o.Kind),
			Label: o.Label,
			Count: len(rows),
			Rows:  rows,
		})
	}
	return groups
}

// diffRow maps one diff.Change to a DiffRow, per design §6: a breaking change
// is always critical/"Breaking" regardless of its Kind; otherwise an added
// field is good news, and everything else (non-breaking removed/changed) is a
// warning.
func diffRow(ch diff.Change) DiffRow {
	var sev Severity
	switch {
	case ch.Breaking:
		sev = SevCritical
	case ch.Kind == diff.Added:
		sev = SevGood
	default:
		sev = SevWarning
	}

	label := diffKindLabel(ch.Kind)
	if ch.Breaking {
		label = "Breaking"
	}

	details := make([]DiffDetail, len(ch.Details))
	for i, det := range ch.Details {
		details[i] = diffDetail(det)
	}

	return DiffRow{
		Path:     ch.Path,
		Kind:     string(ch.Kind),
		Breaking: ch.Breaking,
		Severity: sev,
		Icon:     severityIcon[sev],
		Label:    label,
		Details:  details,
	}
}

// diffKindLabel titles a ChangeKind for display ("added"->"Added" etc.),
// shared by diffGroups (group Label) and diffRow (non-breaking row Label).
func diffKindLabel(k diff.ChangeKind) string {
	switch k {
	case diff.Added:
		return "Added"
	case diff.Removed:
		return "Removed"
	default:
		return "Changed"
	}
}

// diffDetail maps one diff.Detail 1:1, per design §6. Old/New are the
// differ's own preformatted text; an empty string (never emitted by the
// differ today, but the mapping must hold for any Detail) renders as an em
// dash. Severity follows Breaking alone - there is no serious/good tier at
// this granularity.
func diffDetail(det diff.Detail) DiffDetail {
	sev := SevWarning
	if det.Breaking {
		sev = SevCritical
	}
	return DiffDetail{
		Reason:   string(det.Reason),
		Message:  det.Message,
		Old:      dashIfEmpty(det.Old),
		New:      dashIfEmpty(det.New),
		Breaking: det.Breaking,
		Severity: sev,
	}
}

// dashIfEmpty renders an empty string as an em dash (—, U+2014) - matching
// fmtNum's em dash NaN/Inf placeholder, so the same "missing value" glyph is
// used consistently across the VisualModel.
func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// diffBadges derives the two cross-cutting critical badges from d.Changes
// alone, per design §6.1: an always-present field being removed, and a
// field's type set strictly narrowing (never widening). Sorted by (severity
// desc, path asc, code asc) via the shared sortModelBadges helper (visual.go).
func diffBadges(d diff.DiffResult) []Badge {
	var badges []Badge
	for _, ch := range d.Changes {
		if ch.Kind == diff.Removed && ch.Breaking {
			badges = append(badges, Badge{
				Severity: SevCritical,
				Code:     "field_removed",
				Icon:     severityIcon[SevCritical],
				Label:    "Field removed",
				Detail:   "Always-present field '" + ch.Path + "' was removed.",
				Path:     ch.Path,
			})
		}
		for _, det := range ch.Details {
			if det.Reason == diff.ReasonType && det.Breaking && typeNarrowed(det.Old, det.New) {
				badges = append(badges, Badge{
					Severity: SevCritical,
					Code:     "type_narrowing",
					Icon:     severityIcon[SevCritical],
					Label:    "Type narrowing",
					Detail:   "Field '" + ch.Path + "' narrowed its type set (" + det.Old + " -> " + det.New + ").",
					Path:     ch.Path,
				})
			}
		}
	}
	sortModelBadges(badges)
	return badges
}

// typeNarrowed reports whether newSet is a strict subset of oldSet - every
// token in newSet also appears in oldSet, and newSet is strictly smaller -
// per design §6.1's refinement: detected structurally from the comma-
// separated type-token sets, never by parsing Detail.Message.
func typeNarrowed(oldSet, newSet string) bool {
	oldToks := splitTypeSet(oldSet)
	newToks := splitTypeSet(newSet)
	if len(newToks) == 0 || len(newToks) >= len(oldToks) {
		return false
	}
	for t := range newToks {
		if !oldToks[t] {
			return false
		}
	}
	return true
}

// splitTypeSet splits a comma-separated type-token string (e.g.
// "number,string") into a set, trimming whitespace and dropping empty tokens.
func splitTypeSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok != "" {
			set[tok] = true
		}
	}
	return set
}
