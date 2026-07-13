package diff

import (
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

// completeEnum reports whether the profiler proved a complete small (>=2) value set.
func completeEnum(fp profile.FieldProfile) bool {
	return fp.DistinctExact && fp.DistinctCount >= 2 && fp.DistinctCount == len(fp.TopValues)
}

func valueSet(fp profile.FieldProfile) map[string]bool {
	s := map[string]bool{}
	for _, v := range fp.TopValues {
		s[v.Value] = true
	}
	return s
}

func enumChange(a, b profile.FieldProfile) (Detail, bool) {
	if !(completeEnum(a) && completeEnum(b)) {
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
