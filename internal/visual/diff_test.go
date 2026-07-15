package visual

import (
	"encoding/json"
	"testing"

	"github.com/hoijun-kim/shape/internal/diff"
)

// ---------------------------------------------------------------------------
// diffBadges (design §6.1)
// ---------------------------------------------------------------------------

func TestDiffBadgesTypeNarrowing(t *testing.T) {
	d := diff.DiffResult{Changes: []diff.Change{{
		Path: "id", Kind: diff.Changed, Breaking: true,
		Details: []diff.Detail{{Reason: diff.ReasonType, Breaking: true, Old: "number,string", New: "number"}},
	}}}
	bs := diffBadges(d)
	found := false
	for _, b := range bs {
		if b.Code == "type_narrowing" && b.Severity == SevCritical {
			found = true
		}
	}
	if !found {
		t.Errorf("expected type_narrowing critical badge, got %+v", bs)
	}
}

func TestDiffBadgesWideningNoBadge(t *testing.T) {
	d := diff.DiffResult{Changes: []diff.Change{{
		Path: "id", Kind: diff.Changed, Breaking: true,
		Details: []diff.Detail{{Reason: diff.ReasonType, Breaking: true, Old: "number", New: "number,string"}},
	}}}
	for _, b := range diffBadges(d) {
		if b.Code == "type_narrowing" {
			t.Errorf("widening must not yield type_narrowing: %+v", b)
		}
	}
}

func TestDiffBadgesFieldRemoved(t *testing.T) {
	d := diff.DiffResult{Changes: []diff.Change{
		{Path: "legacy_field", Kind: diff.Removed, Breaking: true},
		{Path: "optional_field", Kind: diff.Removed, Breaking: false},
	}}
	bs := diffBadges(d)
	found := false
	for _, b := range bs {
		if b.Code == "field_removed" {
			found = true
			if b.Severity != SevCritical || b.Path != "legacy_field" {
				t.Errorf("field_removed badge wrong: %+v", b)
			}
		}
		if b.Path == "optional_field" {
			t.Errorf("non-breaking removed must not badge: %+v", b)
		}
	}
	if !found {
		t.Errorf("expected field_removed critical badge, got %+v", bs)
	}
}

func TestDiffBadgesSortOrder(t *testing.T) {
	// Both badges are critical; path asc must break the tie ("id" < "legacy_field").
	d := diff.DiffResult{Changes: []diff.Change{
		{Path: "legacy_field", Kind: diff.Removed, Breaking: true},
		{Path: "id", Kind: diff.Changed, Breaking: true, Details: []diff.Detail{
			{Reason: diff.ReasonType, Breaking: true, Old: "number,string", New: "number"},
		}},
	}}
	bs := diffBadges(d)
	if len(bs) != 2 {
		t.Fatalf("expected 2 badges, got %d: %+v", len(bs), bs)
	}
	if bs[0].Path != "id" || bs[1].Path != "legacy_field" {
		t.Errorf("expected sort by path asc (id, legacy_field), got (%s, %s)", bs[0].Path, bs[1].Path)
	}
}

// ---------------------------------------------------------------------------
// FromDiff: verdict selection
// ---------------------------------------------------------------------------

func TestDiffVerdict(t *testing.T) {
	cases := []struct {
		name    string
		d       diff.DiffResult
		verdict string
		sev     Severity
	}{
		{
			name:    "breaking wins outright",
			d:       diff.DiffResult{Breaking: 1, Changes: []diff.Change{{Path: "a", Kind: diff.Changed, Breaking: true}}},
			verdict: "Breaking changes",
			sev:     SevCritical,
		},
		{
			name:    "non-breaking changes are merely compatible",
			d:       diff.DiffResult{Changes: []diff.Change{{Path: "a", Kind: diff.Added}}},
			verdict: "Compatible changes",
			sev:     SevWarning,
		},
		{
			name:    "no changes at all",
			d:       diff.DiffResult{},
			verdict: "No changes",
			sev:     SevGood,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vm := FromDiff(c.d)
			if vm.Verdict != c.verdict || vm.VerdictSeverity != c.sev {
				t.Errorf("got (%q,%q), want (%q,%q)", vm.Verdict, vm.VerdictSeverity, c.verdict, c.sev)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FromDiff: group partition, fixed order, omission
// ---------------------------------------------------------------------------

func TestDiffGroupsOrderAndOmission(t *testing.T) {
	// Changes deliberately NOT in kind order (changed, then added) and with no
	// Removed changes at all, to prove group order is fixed (added->removed->
	// changed) and independent of input order, and that an empty kind bucket
	// is omitted entirely rather than emitted as a zero-row group.
	d := diff.DiffResult{
		Changes: []diff.Change{
			{Path: "z_changed", Kind: diff.Changed, Breaking: false, Details: []diff.Detail{{Reason: diff.ReasonPresence, Message: "m"}}},
			{Path: "a_added", Kind: diff.Added, Breaking: false, Details: []diff.Detail{{Reason: diff.ReasonPresence, Message: "m"}}},
		},
	}
	vm := FromDiff(d)
	if len(vm.Groups) != 2 {
		t.Fatalf("expected 2 groups (added, changed; removed omitted), got %d: %+v", len(vm.Groups), vm.Groups)
	}
	if vm.Groups[0].Kind != string(diff.Added) || vm.Groups[0].Label != "Added" {
		t.Errorf("expected first group Added, got %+v", vm.Groups[0])
	}
	if vm.Groups[1].Kind != string(diff.Changed) || vm.Groups[1].Label != "Changed" {
		t.Errorf("expected second group Changed, got %+v", vm.Groups[1])
	}
	if vm.Groups[0].Count != 1 || len(vm.Groups[0].Rows) != 1 || vm.Groups[0].Rows[0].Path != "a_added" {
		t.Errorf("added group content wrong: %+v", vm.Groups[0])
	}
	if vm.Groups[1].Count != 1 || len(vm.Groups[1].Rows) != 1 || vm.Groups[1].Rows[0].Path != "z_changed" {
		t.Errorf("changed group content wrong: %+v", vm.Groups[1])
	}
}

func TestDiffRowBreakingSeverity(t *testing.T) {
	d := diff.DiffResult{Changes: []diff.Change{
		{Path: "a", Kind: diff.Added, Breaking: false},
		{Path: "b", Kind: diff.Removed, Breaking: true},
		{Path: "c", Kind: diff.Removed, Breaking: false},
		{Path: "e", Kind: diff.Changed, Breaking: false},
	}}
	vm := FromDiff(d)
	rowByPath := map[string]DiffRow{}
	for _, g := range vm.Groups {
		for _, r := range g.Rows {
			rowByPath[r.Path] = r
		}
	}
	cases := []struct {
		path  string
		sev   Severity
		label string
	}{
		{"a", SevGood, "Added"},
		{"b", SevCritical, "Breaking"},
		{"c", SevWarning, "Removed"},
		{"e", SevWarning, "Changed"},
	}
	for _, c := range cases {
		r, ok := rowByPath[c.path]
		if !ok {
			t.Fatalf("missing row for path %q", c.path)
		}
		if r.Severity != c.sev || r.Label != c.label || r.Icon != severityIcon[c.sev] {
			t.Errorf("row %q: got (severity=%q, label=%q, icon=%q), want (severity=%q, label=%q, icon=%q)",
				c.path, r.Severity, r.Label, r.Icon, c.sev, c.label, severityIcon[c.sev])
		}
	}
}

// ---------------------------------------------------------------------------
// FromDiff: detail mapping (em dash on empty old/new)
// ---------------------------------------------------------------------------

func TestDiffDetailEmptyDash(t *testing.T) {
	d := diff.DiffResult{Changes: []diff.Change{{
		Path: "x", Kind: diff.Changed, Breaking: false,
		Details: []diff.Detail{{Reason: diff.ReasonPresence, Breaking: false, Message: "m", Old: "", New: ""}},
	}}}
	vm := FromDiff(d)
	det := vm.Groups[0].Rows[0].Details[0]
	if det.Old != "–" || det.New != "–" {
		t.Errorf("expected em dash for empty old/new, got Old=%q New=%q", det.Old, det.New)
	}
	if det.Severity != SevWarning {
		t.Errorf("expected non-breaking detail severity SevWarning, got %q", det.Severity)
	}
}

// ---------------------------------------------------------------------------
// FromDiff golden
// ---------------------------------------------------------------------------

// sampleDiff builds a diff.DiffResult fixture covering: an added field
// ("address"), a breaking type-narrowing change ("id", triggers the
// type_narrowing badge), a breaking removal of an always-present field
// ("legacy_field", triggers the field_removed badge), a non-breaking removal
// of an optional field ("optional_field"), and a non-breaking enum-widening
// change carrying a synthetic empty-string Detail ("status", exercises the
// em-dash mapping). Changes are given in path-sorted order, matching what the
// differ actually produces.
func sampleDiff() diff.DiffResult {
	return diff.DiffResult{
		Old:      "v1.ndjson",
		New:      "v2.ndjson",
		Compared: 9,
		Added:    1,
		Removed:  2,
		Changed:  2,
		Breaking: 2,
		Caveats: []string{
			"skipped lines (old=0, new=3): removed/dropped-type signals may be parse artifacts",
		},
		Changes: []diff.Change{
			{
				Path:     "address",
				Kind:     diff.Added,
				Breaking: false,
				Details: []diff.Detail{
					{Reason: diff.ReasonPresence, Breaking: false, Message: "new field", Old: "-", New: "100%"},
				},
			},
			{
				Path:     "id",
				Kind:     diff.Changed,
				Breaking: true,
				Details: []diff.Detail{
					{Reason: diff.ReasonType, Breaking: true, Message: "type -string", Old: "number,string", New: "number"},
				},
			},
			{
				Path:     "legacy_field",
				Kind:     diff.Removed,
				Breaking: true,
				Details: []diff.Detail{
					{Reason: diff.ReasonPresence, Breaking: true, Message: "removed (was always-present)", Old: "100%", New: "-"},
				},
			},
			{
				Path:     "optional_field",
				Kind:     diff.Removed,
				Breaking: false,
				Details: []diff.Detail{
					{Reason: diff.ReasonPresence, Breaking: false, Message: "removed (was optional)", Old: "80%", New: "-"},
				},
			},
			{
				Path:     "status",
				Kind:     diff.Changed,
				Breaking: false,
				Details: []diff.Detail{
					{Reason: diff.ReasonEnum, Breaking: false, Message: "enum +\"archived\"", Old: "active,pending", New: "active,archived,pending"},
					{Reason: diff.ReasonPresence, Breaking: false, Message: "presence edge case", Old: "", New: ""},
				},
			},
		},
	}
}

func TestFromDiffGolden(t *testing.T) {
	vm := FromDiff(sampleDiff())
	b, err := json.MarshalIndent(vm, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	goldenCheck(t, "fromdiff.golden", b)
}

// TestFromDiffCaveatsPassthrough checks Caveats is copied verbatim, independent
// of the golden byte comparison above.
func TestFromDiffCaveatsPassthrough(t *testing.T) {
	d := sampleDiff()
	vm := FromDiff(d)
	if len(vm.Caveats) != len(d.Caveats) {
		t.Fatalf("caveats length mismatch: got %d, want %d", len(vm.Caveats), len(d.Caveats))
	}
	for i := range d.Caveats {
		if vm.Caveats[i] != d.Caveats[i] {
			t.Errorf("caveat[%d] = %q, want %q", i, vm.Caveats[i], d.Caveats[i])
		}
	}
}
