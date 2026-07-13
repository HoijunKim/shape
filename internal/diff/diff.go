package diff

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hoijun-kim/shape/internal/profile"
)

// ChangeKind categorizes a path-level change.
type ChangeKind string

const (
	Added   ChangeKind = "added"
	Removed ChangeKind = "removed"
	Changed ChangeKind = "changed"
)

// Reason categorizes one dimension of a change.
type Reason string

const (
	ReasonPresence Reason = "presence"
	ReasonType     Reason = "type"
	ReasonEnum     Reason = "enum"
)

// Detail is one differing dimension of a path.
type Detail struct {
	Reason   Reason `json:"reason"`
	Breaking bool   `json:"breaking"`
	Message  string `json:"message"`
	Old      string `json:"old,omitempty"`
	New      string `json:"new,omitempty"`
}

// Change is the aggregate change for one path.
type Change struct {
	Path     string     `json:"path"`
	Kind     ChangeKind `json:"kind"`
	Breaking bool       `json:"breaking"`
	Details  []Detail   `json:"details"`
}

// DiffResult is the full comparison of two profiles.
type DiffResult struct {
	Old      string   `json:"old"`
	New      string   `json:"new"`
	Compared int      `json:"compared"`
	Added    int      `json:"added"`
	Removed  int      `json:"removed"`
	Changed  int      `json:"changed"`
	Breaking int      `json:"breaking"`
	Caveats  []string `json:"caveats,omitempty"`
	Changes  []Change `json:"changes"`
}

// HasBreaking reports whether any breaking change was found.
func (d DiffResult) HasBreaking() bool { return d.Breaking > 0 }

const presenceEps = 1e-9

func guaranteed(fp profile.FieldProfile) bool { return fp.PresenceRate >= 1.0-presenceEps }

func pct(f float64) string { return strconv.Itoa(int(f*100+0.5)) + "%" }

// typeSet returns the JSON Schema type tokens a field shows, folding int/float
// to "number" and keeping "null" (null introduced in new data is a real break).
func typeSet(fp profile.FieldProfile) map[string]bool {
	set := map[string]bool{}
	for k, frac := range fp.TypeDist {
		if frac <= 0 {
			continue
		}
		switch k {
		case profile.KindInt, profile.KindFloat:
			set["number"] = true
		case profile.KindNull:
			set["null"] = true
		case profile.KindBool:
			set["boolean"] = true
		case profile.KindString:
			set["string"] = true
		case profile.KindArray:
			set["array"] = true
		case profile.KindObject:
			set["object"] = true
		}
	}
	return set
}

// setDiff returns the sorted members present in from but not in to.
func setDiff(from, to map[string]bool) []string {
	var out []string
	for k := range from {
		if !to[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func joinSet(s map[string]bool) string {
	ks := make([]string, 0, len(s))
	for k := range s {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func typeChange(a, b profile.FieldProfile) (Detail, bool) {
	sa, sb := typeSet(a), typeSet(b)
	added := setDiff(sb, sa)
	dropped := setDiff(sa, sb)
	if len(added) == 0 && len(dropped) == 0 {
		return Detail{}, false
	}
	parts := make([]string, 0, len(added)+len(dropped))
	for _, t := range added {
		parts = append(parts, "+"+t)
	}
	for _, t := range dropped {
		parts = append(parts, "-"+t)
	}
	return Detail{
		Reason: ReasonType, Breaking: len(added) > 0,
		Message: "type " + strings.Join(parts, " "),
		Old:     joinSet(sa), New: joinSet(sb),
	}, true
}

func presenceChange(a, b profile.FieldProfile) (Detail, bool) {
	ga, gb := guaranteed(a), guaranteed(b)
	switch {
	case ga && !gb:
		return Detail{Reason: ReasonPresence, Breaking: true, Message: "always-present -> optional", Old: pct(a.PresenceRate), New: pct(b.PresenceRate)}, true
	case !ga && gb:
		return Detail{Reason: ReasonPresence, Breaking: false, Message: "optional -> always-present", Old: pct(a.PresenceRate), New: pct(b.PresenceRate)}, true
	default:
		return Detail{}, false
	}
}

// completeEnum reports whether the profiler proved a complete small value set.
// The profiler caps TopValues at 10 (accumulator.topValues), so
// DistinctCount == len(TopValues) can only hold for 2..10 distinct values;
// enum-member changes on fields with more than 10 distinct values are
// intentionally not asserted (fail-safe: no false breaks from large/free-text
// fields). If that cap changes, this window changes with it.
func completeEnum(fp profile.FieldProfile) bool {
	return fp.DistinctExact && fp.DistinctCount >= 2 && fp.DistinctCount == len(fp.TopValues)
}

// pureString reports whether the field's only non-null value type is string, so
// its value set is a real categorical/enum (not a numeric or mixed-type field
// that merely happens to have few distinct values).
func pureString(fp profile.FieldProfile) bool {
	hasString, only := false, true
	for k, frac := range fp.TypeDist {
		if frac <= 0 || k == profile.KindNull {
			continue
		}
		if k == profile.KindString {
			hasString = true
		} else {
			only = false
		}
	}
	return hasString && only
}

func valueSet(fp profile.FieldProfile) map[string]bool {
	s := map[string]bool{}
	for _, v := range fp.TopValues {
		s[v.Value] = true
	}
	return s
}

func enumChange(a, b profile.FieldProfile) (Detail, bool) {
	if !(completeEnum(a) && completeEnum(b) && pureString(a) && pureString(b)) {
		return Detail{}, false
	}
	sa, sb := valueSet(a), valueSet(b)
	added := setDiff(sb, sa)
	lost := setDiff(sa, sb)
	if len(added) == 0 && len(lost) == 0 {
		return Detail{}, false
	}
	parts := make([]string, 0, len(added)+len(lost))
	for _, v := range added {
		parts = append(parts, "+"+strconv.Quote(v))
	}
	for _, v := range lost {
		parts = append(parts, "-"+strconv.Quote(v))
	}
	return Detail{
		Reason: ReasonEnum, Breaking: len(added) > 0,
		Message: "enum " + strings.Join(parts, " "),
		Old:     joinSet(sa), New: joinSet(sb),
	}, true
}

func index(p profile.ProfileResult) map[string]profile.FieldProfile {
	m := make(map[string]profile.FieldProfile, len(p.Fields))
	for _, f := range p.Fields {
		m[f.Path] = f
	}
	return m
}

// classify produces zero or one Change for a path. a/b are nil when the path is
// absent from that side.
func classify(path string, a, b *profile.FieldProfile) (Change, bool) {
	switch {
	case a != nil && b == nil:
		br := guaranteed(*a)
		msg := "removed (was optional)"
		if br {
			msg = "removed (was always-present)"
		}
		return Change{Path: path, Kind: Removed, Breaking: br,
			Details: []Detail{{Reason: ReasonPresence, Breaking: br, Message: msg, Old: pct(a.PresenceRate), New: "-"}}}, true
	case a == nil && b != nil:
		return Change{Path: path, Kind: Added, Breaking: false,
			Details: []Detail{{Reason: ReasonPresence, Breaking: false, Message: "new field", Old: "-", New: pct(b.PresenceRate)}}}, true
	default:
		var details []Detail
		if d, ok := typeChange(*a, *b); ok {
			details = append(details, d)
		}
		if d, ok := presenceChange(*a, *b); ok {
			details = append(details, d)
		}
		if d, ok := enumChange(*a, *b); ok {
			details = append(details, d)
		}
		if len(details) == 0 {
			return Change{}, false
		}
		br := false
		for _, d := range details {
			if d.Breaking {
				br = true
			}
		}
		return Change{Path: path, Kind: Changed, Breaking: br, Details: details}, true
	}
}

// Diff compares two profiles into a DiffResult. Breaking is judged from the
// perspective of a consumer built for old data meeting new data.
func Diff(old, new profile.ProfileResult) DiffResult {
	ia, ib := index(old), index(new)
	seen := map[string]bool{}
	var paths []string
	for p := range ia {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for p := range ib {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	res := DiffResult{Old: old.Source, New: new.Source, Compared: len(paths)}
	for _, p := range paths {
		var a, b *profile.FieldProfile
		if f, ok := ia[p]; ok {
			a = &f
		}
		if f, ok := ib[p]; ok {
			b = &f
		}
		ch, ok := classify(p, a, b)
		if !ok {
			continue
		}
		res.Changes = append(res.Changes, ch)
		switch ch.Kind {
		case Added:
			res.Added++
		case Removed:
			res.Removed++
		case Changed:
			res.Changed++
		}
		if ch.Breaking {
			res.Breaking++
		}
	}
	res.Caveats = caveats(old, new)
	return res
}

// caveats warns when the two profiles may not be soundly comparable.
func caveats(old, new profile.ProfileResult) []string {
	var cs []string
	if old.Skipped > 0 || new.Skipped > 0 {
		cs = append(cs, fmt.Sprintf("skipped lines (old=%d, new=%d): removed/dropped-type signals may be parse artifacts", old.Skipped, new.Skipped))
	}
	lo, hi := old.Records, new.Records
	if lo > hi {
		lo, hi = hi, lo
	}
	switch {
	case lo == 0 && hi > 0:
		cs = append(cs, fmt.Sprintf("one side has 0 records (old=%d, new=%d): the comparison may be meaningless", old.Records, new.Records))
	case lo > 0 && hi >= lo*100:
		cs = append(cs, fmt.Sprintf("record counts differ widely (old=%d, new=%d): set differences may reflect sample size, not a real change", old.Records, new.Records))
	}
	return cs
}
