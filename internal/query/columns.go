// Package query implements the internal/query engine core for the shape
// data explorer: compiled path segments, value resolution, and cell/row
// rendering (spec §3, docs/superpowers/specs/2026-07-17-shape-engine-design.md).
package query

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hoijun-kim/shape/internal/profile"
)

// Seg is one compiled path segment. A plain field segment carries Key with
// Elem == false; Elem == true marks the "[]" array-element wildcard, in
// which case Key is unused (empty). Navigation uses these compiled segments
// (not a re-parsed dotted string) so that keys containing "." and array
// wildcards are handled correctly; see parsePath.
type Seg struct {
	Key  string
	Elem bool
}

// parsePath parses the profiler's dotted path grammar (see
// internal/profile/flatten.go: Flatten/walk) into a slice of compiled Seg.
//
// Grammar:
//
//	"$"        -> root reference: no segments (resolve returns the record itself)
//	"a.b"      -> two key segments
//	"a[]"      -> a key segment immediately followed by an Elem segment
//	              (no "." between the key and its own "[]", matching how
//	              Flatten builds an element path: path+"[]")
//	`["a.b"]`  -> a single key segment whose literal key contains "."
//	              (bracket-quoted form, used when a real key has a dot)
//	"[]"       -> a bare Elem segment (root-level array; Flatten emits this
//	              when the record itself is an array)
//
// Bracket and plain forms may be freely chained (e.g. `a.["b.c"].d`). Segment
// Key values keep the exact literal characters seen (including any "." in a
// bracket-quoted key), so the dotted display form can be reconstructed by a
// caller without re-parsing.
func parsePath(dotted string) []Seg {
	if dotted == "" || dotted == "$" {
		return nil
	}

	var segs []Seg
	i, n := 0, len(dotted)
	for i < n {
		switch {
		case dotted[i] == '.':
			i++
			continue
		case dotted[i] == '[' && i+1 < n && dotted[i+1] == ']':
			segs = append(segs, Seg{Elem: true})
			i += 2
		case dotted[i] == '[' && i+1 < n && dotted[i+1] == '"':
			key, next := scanBracketKey(dotted, i)
			segs = append(segs, Seg{Key: key})
			i = next
		default:
			j := i
			for j < n && dotted[j] != '.' && dotted[j] != '[' {
				j++
			}
			segs = append(segs, Seg{Key: dotted[i:j]})
			i = j
		}

		// A key (or bracket-quoted key) segment may be directly followed by
		// one or more "[]" wildcards with no separating "." (e.g. "tags[]",
		// matching Flatten's path+"[]" concatenation).
		for i+1 < n && dotted[i] == '[' && dotted[i+1] == ']' {
			segs = append(segs, Seg{Elem: true})
			i += 2
		}
	}
	return segs
}

// scanBracketKey reads a `["..."]` bracket-quoted key starting at position i
// (dotted[i] must be '['), unescaping `\"` to `"`. It returns the decoded key
// and the index just past the closing ']'.
func scanBracketKey(dotted string, i int) (string, int) {
	n := len(dotted)
	j := i + 2 // skip `["`
	var sb strings.Builder
	for j < n && dotted[j] != '"' {
		if dotted[j] == '\\' && j+1 < n {
			sb.WriteByte(dotted[j+1])
			j += 2
			continue
		}
		sb.WriteByte(dotted[j])
		j++
	}
	j++ // skip closing quote
	if j < n && dotted[j] == ']' {
		j++
	}
	return sb.String(), j
}

// resolve walks record along segs, returning every value reached: the
// existential value set that powers cell rendering, filter evaluation, and
// array-membership checks with one primitive. A scalar leaf yields 0 values
// (the path is absent) or 1 value (present, possibly null); a path
// containing an Elem segment yields 0..n values, one per matching array
// element encountered at that point.
func resolve(record any, segs []Seg) []any {
	current := []any{record}
	for _, seg := range segs {
		var next []any
		for _, v := range current {
			if seg.Elem {
				if arr, ok := v.([]any); ok {
					next = append(next, arr...)
				}
				continue
			}
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if cv, exists := m[seg.Key]; exists {
				next = append(next, cv)
			}
		}
		current = next
	}
	return current
}

// CellKind classifies a resolved value for display.
type CellKind string

const (
	CellMissing CellKind = "missing"
	CellNull    CellKind = "null"
	CellBool    CellKind = "bool"
	CellInt     CellKind = "int"
	CellFloat   CellKind = "float"
	CellString  CellKind = "string"
	CellObject  CellKind = "object"
	CellArray   CellKind = "array"
)

// previewCap bounds the compact-JSON preview stored in Cell.Str for
// container values (object/array).
const previewCap = 200

// Cell is one rendered table cell.
type Cell struct {
	Kind    CellKind `json:"kind"`
	Str     string   `json:"str,omitempty"` // string value OR truncated compact-JSON preview for containers
	Num     float64  `json:"num,omitempty"`
	Bool    bool     `json:"bool,omitempty"`
	Count   int      `json:"count,omitempty"`   // element/key count for containers
	HasMore bool     `json:"hasMore,omitempty"` // container preview truncated
}

// Row is one rendered table row; Cells is positionally aligned to the
// caller's column set.
type Row struct {
	Index int64  `json:"index"` // absolute record ordinal (file order / _rowid_ / parquet row order)
	Cells []Cell `json:"cells"`
}

// toCell classifies a single resolved value into a display Cell, dispatching
// via profile.KindOf (the same classifier the profiler uses, so cell kind
// and field-profile kind never disagree).
//
// json.Number classifies as CellInt/CellFloat with Num holding the parsed
// (possibly imprecise for huge ints or high-precision decimals) float64 AND
// Str holding the exact source literal, so callers needing exact round-trip
// (64-bit ints, precise decimals) use Str rather than Num. map[string]any
// classifies as CellObject and []any as CellArray, both with Str set to a
// truncated compact-JSON preview (see compactJSON/truncate, previewCap=200),
// Count set to the untruncated element/key count, and HasMore set when the
// preview was truncated.
//
// toCell only classifies an actual value: an empty resolve() set (path
// absent) is NOT represented here. Callers that resolve a path and get zero
// values decide CellMissing themselves — that is how "missing" (0 values)
// is kept distinct from "present but null" (1 value, nil).
func toCell(v any) Cell {
	switch profile.KindOf(v) {
	case profile.KindNull:
		return Cell{Kind: CellNull}
	case profile.KindBool:
		b, _ := v.(bool)
		return Cell{Kind: CellBool, Bool: b}
	case profile.KindInt:
		num, _ := v.(json.Number)
		f, _ := num.Float64()
		return Cell{Kind: CellInt, Num: f, Str: num.String()}
	case profile.KindFloat:
		if num, ok := v.(json.Number); ok {
			f, _ := num.Float64()
			return Cell{Kind: CellFloat, Num: f, Str: num.String()}
		}
		f, _ := v.(float64)
		return Cell{Kind: CellFloat, Num: f, Str: strconv.FormatFloat(f, 'g', -1, 64)}
	case profile.KindString:
		s, ok := v.(string)
		if !ok {
			s = fmt.Sprintf("%v", v)
		}
		return Cell{Kind: CellString, Str: s}
	case profile.KindObject:
		m, _ := v.(map[string]any)
		preview, more := truncate(compactJSON(v), previewCap)
		return Cell{Kind: CellObject, Str: preview, Count: len(m), HasMore: more}
	case profile.KindArray:
		a, _ := v.([]any)
		preview, more := truncate(compactJSON(v), previewCap)
		return Cell{Kind: CellArray, Str: preview, Count: len(a), HasMore: more}
	default:
		return Cell{Kind: CellMissing}
	}
}

// compactJSON renders v as compact (no-whitespace) JSON for use as a
// container preview. A marshal failure (not expected for values decoded from
// JSON/CSV/SQLite/Parquet readers) yields an empty string rather than a
// panic.
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// truncate caps s at max runes (not bytes, so multi-byte UTF-8 is never
// split mid-character), reporting whether truncation occurred.
func truncate(s string, max int) (string, bool) {
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	return string(r[:max]), true
}

// columnDiscoverer accumulates the set of paths observed across a stream of
// records, in FIRST-SEEN order. It walks each record using the same
// recursive shape as profile.Flatten (internal/profile/flatten.go): interior
// object paths, array-container paths, and scalar leaf paths are all
// registered -- the same path universe that ends up (alphabetized) in
// profile.ProfileResult.Fields -- but, unlike the profiler, first-seen order
// is kept rather than discarded.
//
// This is the ONLY source of column order: profile.Profiler.Result() sorts
// Fields alphabetically (see internal/profile/profiler.go, Result), so it
// cannot supply the order that matches CSV header order or JSON key order.
// buildColumnModel joins disc's ordered path set with prof's per-path type
// info by Path string, so columnDiscoverer's path-building must exactly
// match profile.Flatten's (plain "." concatenation, no escaping) or the
// join would miss entries; see the package doc comment on the resulting
// path-string ambiguity this inherits from flatten.go.
//
// Observe is idempotent per path: an already-registered path is a single
// map lookup, so total work across a stream is bounded by the number of
// DISTINCT paths, not the number of records.
type columnDiscoverer struct {
	order []string // first-seen path order
	seen  map[string]bool
}

// newColumnDiscoverer returns an empty columnDiscoverer.
func newColumnDiscoverer() *columnDiscoverer {
	return &columnDiscoverer{seen: map[string]bool{}}
}

// Observe walks record (record is shaped as produced by the readers/
// profile pipeline: nil/bool/string/json.Number scalars, map[string]any,
// []any) and registers any newly-seen path.
//
// Paths first seen in an EARLIER Observe call always sort before ones first
// seen in a LATER call (true first-seen order, across records). Within a
// SINGLE Observe call, walk necessarily ranges over each map[string]any's
// keys (Go map iteration order is unspecified and randomized per range), so
// two or more sibling paths BOTH introduced by that same call cannot be
// ordered by any real "first-seen" signal -- there isn't one, since generic
// decode into map[string]any already erased true source key order before
// columnDiscoverer ever saw the record (an upstream internal/readers
// limitation, out of this package's scope; see buildColumnModel's
// sourceOrder parameter for the seam a backend with a real column order --
// CSV header, SQLite/Parquet schema -- can use to restore it). What Observe
// DOES guarantee: that intra-call tie is broken the SAME way every time, by
// sorting the paths newly discovered by this call (bytewise, by dotted path
// string) before appending them to the first-seen order slice -- so
// Columns order is stable and reproducible across runs for identical input,
// never dependent on map iteration order.
func (d *columnDiscoverer) Observe(record any) {
	batch := &discoverBatch{seen: map[string]bool{}}
	d.walk("", record, batch)
	sort.Strings(batch.paths)
	for _, p := range batch.paths {
		d.seen[p] = true
		d.order = append(d.order, p)
	}
}

// discoverBatch buffers the paths newly discovered within a single Observe
// call so they can be sorted (breaking the intra-call sibling tie
// deterministically) before being committed to columnDiscoverer.order.
type discoverBatch struct {
	paths []string
	seen  map[string]bool // paths already buffered THIS call (dedupe within the batch)
}

func (d *columnDiscoverer) walk(path string, v any, batch *discoverBatch) {
	switch t := v.(type) {
	case map[string]any:
		if path != "" {
			d.stage(path, batch)
		}
		for k, cv := range t {
			child := k
			if path != "" {
				child = path + "." + k
			}
			d.walk(child, cv, batch)
		}
	case []any:
		d.stage(rootOrPath(path), batch)
		elem := "[]"
		if path != "" {
			elem = path + "[]"
		}
		for _, cv := range t {
			d.walk(elem, cv, batch)
		}
	default:
		d.stage(rootOrPath(path), batch)
	}
}

// stage buffers path into batch if it is new (not already registered from a
// prior Observe call, and not already buffered earlier in THIS call).
func (d *columnDiscoverer) stage(path string, batch *discoverBatch) {
	if d.seen[path] || batch.seen[path] {
		return
	}
	batch.seen[path] = true
	batch.paths = append(batch.paths, path)
}

// rootOrPath mirrors profile.Flatten's internal rootOr: the root scalar/
// array path displays as "$".
func rootOrPath(path string) string {
	if path == "" {
		return "$"
	}
	return path
}

// Column describes one discovered, typed column in a ColumnModel (spec §3).
type Column struct {
	Path      string  `json:"path"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Nullable  bool    `json:"nullable"`
	Presence  float64 `json:"presence"`
	Distinct  int     `json:"distinct"`
	Container bool    `json:"container"`
	Index     int     `json:"index"`
}

// ColumnModel is the resolved, ordered column set for one source: the base
// projection a Query/Export starts from before a Transform narrows or
// reorders it.
type ColumnModel struct {
	Columns []Column `json:"columns"`

	// segs is parallel to Columns (segs[i] holds the compiled path segments
	// for Columns[i]); byPath maps a column's dotted Path to its index in
	// Columns. Both are populated by buildColumnModel and are not
	// serialized.
	segs   [][]Seg
	byPath map[string]int

	Truncated  bool `json:"truncated"`  // MaxColumns cap was hit
	TotalPaths int  `json:"totalPaths"` // count of eligible columns before the cap
}

// MaxColumns bounds the number of columns kept in the base ColumnModel
// (spec §3, wide-data bound). A path beyond the cap remains addressable by
// name -- naming it explicitly in a later Transform.Select overrides the cap
// (an explicit projection is unbounded). columnDiscoverer itself holds a
// bounded path SET, O(distinct paths) not O(rows); this cap instead bounds
// how many of those paths become default, displayed columns.
const MaxColumns = 512

// buildColumnModel combines the first-seen path order recorded by disc with
// the per-path type/nullability/presence/distinct/container information in
// prof (the sidebar profiler's result, run over the same records), applying
// the column-selection rules from spec §3:
//
//  1. A path containing an Elem segment ("[]") is excluded: array elements
//     are previews (see toCell), not fixed columns -- unnesting them is a
//     later Transform, not a base column.
//  2. A path that is a PURE interior object -- no type drift
//     (profile.IsTypeDrift is false) and its only observed kind is
//     profile.KindObject -- AND has at least one deeper discovered path
//     nested under it is dropped: it would be redundant with those deeper
//     columns. A DRIFTING path (sometimes scalar, sometimes object) is KEPT
//     even when it also has deeper paths: its object occurrences render as
//     preview cells via toCell (drift is shown, not hidden). Array
//     containers are never dropped by this rule (only "pure interior
//     OBJECT" is named in the spec); an always-array path such as "tags"
//     stays a column alongside its excluded "tags[]" element path.
//
// The surviving columns are capped at MaxColumns: the top MaxColumns by
// presence-desc (ties keep the resolved order, via a stable sort) are kept,
// but the final Columns slice is re-ordered back to the resolved order among
// the kept set, so "column order == resolved order" remains true for every
// column a caller can see. Truncated/TotalPaths report the cap.
//
// sourceOrder is an optional hint giving the TRUE source column order (e.g.
// a CSV header row, or a SQLite/Parquet schema's column order) as dotted
// path strings matching Column.Path. When non-empty, eligible columns are
// ordered by their position in sourceOrder first; any eligible column not
// present in sourceOrder is placed after, in disc's deterministic first-seen
// order (see columnDiscoverer.Observe). When sourceOrder is nil/empty (as
// from every caller today), the result is exactly disc's deterministic
// first-seen order -- unchanged behavior. JSON/NDJSON readers cannot supply
// a meaningful sourceOrder yet: generic decode into map[string]any already
// erases true source key order before columnDiscoverer ever sees the
// record, an upstream internal/readers limitation deferred to a later task
// (CSV/SQLite/Parquet backends that DO have a real column order wire it
// through this parameter once their readers are built).
func buildColumnModel(disc *columnDiscoverer, prof profile.ProfileResult, sourceOrder []string) *ColumnModel {
	fieldByPath := make(map[string]profile.FieldProfile, len(prof.Fields))
	for _, fp := range prof.Fields {
		fieldByPath[fp.Path] = fp
	}

	// hasChild[p] is true iff some OTHER discovered path nests under p (an
	// object child "p.k" or an array-element path "p[]").
	hasChild := make(map[string]bool, len(disc.order))
	for _, p := range disc.order {
		for _, q := range disc.order {
			if q != p && (strings.HasPrefix(q, p+".") || strings.HasPrefix(q, p+"[]")) {
				hasChild[p] = true
				break
			}
		}
	}

	type candidate struct {
		path string
		col  Column
		segs []Seg
	}
	candidates := make([]candidate, 0, len(disc.order))
	for _, p := range disc.order {
		segs := parsePath(p)
		if hasElemSeg(segs) {
			continue // array elements are previews, not columns
		}
		fp, ok := fieldByPath[p]
		if !ok {
			continue // discovered but never profiled: nothing to type it with
		}
		drift := profile.IsTypeDrift(fp)
		dk := dominantKind(fp)
		if !drift && dk == string(profile.KindObject) && hasChild[p] {
			continue // pure interior object with deeper columns: redundant
		}
		typ := dk
		if drift {
			typ = "mixed"
		}
		candidates = append(candidates, candidate{
			path: p,
			segs: segs,
			col: Column{
				Path:      p,
				Name:      columnName(p, segs),
				Type:      typ,
				Nullable:  fp.NullRate > 0,
				Presence:  fp.PresenceRate,
				Distinct:  fp.DistinctCount,
				Container: fp.TypeDist[profile.KindObject] > 0 || fp.TypeDist[profile.KindArray] > 0,
			},
		})
	}

	total := len(candidates)
	truncated := false
	keepSet := make(map[string]bool, total)
	for _, c := range candidates {
		keepSet[c.path] = true
	}
	if total > MaxColumns {
		truncated = true
		ranked := append([]candidate(nil), candidates...)
		sort.SliceStable(ranked, func(i, j int) bool {
			return ranked[i].col.Presence > ranked[j].col.Presence
		})
		keepSet = make(map[string]bool, MaxColumns)
		for _, c := range ranked[:MaxColumns] {
			keepSet[c.path] = true
		}
	}

	candidateOrder := make([]string, len(candidates))
	byPath := make(map[string]candidate, len(candidates))
	for i, c := range candidates {
		candidateOrder[i] = c.path
		byPath[c.path] = c
	}
	resolved := resolveColumnOrder(candidateOrder, sourceOrder)

	kept := make([]candidate, 0, len(keepSet))
	for _, p := range resolved { // apply the resolved (sourceOrder-hinted or first-seen) order to the kept set
		if keepSet[p] {
			kept = append(kept, byPath[p])
		}
	}

	cm := &ColumnModel{
		Truncated:  truncated,
		TotalPaths: total,
		Columns:    make([]Column, 0, len(kept)),
		segs:       make([][]Seg, 0, len(kept)),
		byPath:     make(map[string]int, len(kept)),
	}
	for i, c := range kept {
		c.col.Index = i
		cm.Columns = append(cm.Columns, c.col)
		cm.segs = append(cm.segs, c.segs)
		cm.byPath[c.path] = i
	}
	return cm
}

// resolveColumnOrder returns candidateOrder (already in deterministic
// first-seen order -- see columnDiscoverer.Observe) re-ordered per
// sourceOrder when sourceOrder is non-empty: a path present in sourceOrder
// sorts by its index there; a path absent from sourceOrder keeps its
// relative first-seen position, placed after every sourceOrder-matched
// path. A stable sort makes this a single pass: candidateOrder's existing
// deterministic order is the natural tiebreak for both "which sourceOrder
// duplicate wins" (first occurrence, via the index map below) and "how do
// unmatched paths order among themselves".
//
// When sourceOrder is nil/empty, candidateOrder is returned unchanged --
// callers that don't have a real source column order (nil, per
// buildColumnModel's doc comment) get exactly the deterministic first-seen
// order, with no behavior change from before this hint existed.
func resolveColumnOrder(candidateOrder []string, sourceOrder []string) []string {
	if len(sourceOrder) == 0 {
		return candidateOrder
	}
	srcIndex := make(map[string]int, len(sourceOrder))
	for i, p := range sourceOrder {
		if _, exists := srcIndex[p]; !exists {
			srcIndex[p] = i
		}
	}
	ordered := append([]string(nil), candidateOrder...)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi, oki := srcIndex[ordered[i]]
		pj, okj := srcIndex[ordered[j]]
		switch {
		case oki && okj:
			return pi < pj
		case oki != okj:
			return oki // the sourceOrder-matched path sorts first
		default:
			return false // neither matched: stable sort keeps first-seen relative order
		}
	})
	return ordered
}

// resolveCol resolves column i's compiled segments against rec (see
// resolve) and classifies the single/first resolved value into a Cell (see
// toCell). An empty resolve() set (path absent for rec) or an out-of-range
// index yields CellMissing.
func (cm *ColumnModel) resolveCol(i int, rec any) Cell {
	if i < 0 || i >= len(cm.segs) {
		return Cell{Kind: CellMissing}
	}
	values := resolve(rec, cm.segs[i])
	if len(values) == 0 {
		return Cell{Kind: CellMissing}
	}
	return toCell(values[0])
}

// hasElemSeg reports whether segs contains an Elem ("[]") segment.
func hasElemSeg(segs []Seg) bool {
	for _, s := range segs {
		if s.Elem {
			return true
		}
	}
	return false
}

// columnName derives a short display name for a column from its parsed
// segments: the last key segment (e.g. "user.name" -> "name"). segs is
// always Elem-free by the time this is called (buildColumnModel excludes
// Elem paths before naming); a root path ("$", zero segments) falls back to
// the dotted path itself.
func columnName(path string, segs []Seg) string {
	if len(segs) == 0 {
		return path
	}
	return segs[len(segs)-1].Key
}

// kindRank lists non-null JSONKinds in a fixed, deterministic order used by
// dominantKind to break ties without depending on Go's randomized map
// iteration order over FieldProfile.TypeDist.
var kindRank = []profile.JSONKind{
	profile.KindBool, profile.KindInt, profile.KindFloat,
	profile.KindString, profile.KindArray, profile.KindObject,
}

// dominantKind returns the non-null JSONKind with the largest share of
// fp.TypeDist (as a string, for direct use as Column.Type), breaking ties by
// kindRank order. A field observed as null only (or never observed with a
// non-null value) falls back to "null".
func dominantKind(fp profile.FieldProfile) string {
	best := profile.JSONKind("")
	bestShare := 0.0
	for _, k := range kindRank {
		if share := fp.TypeDist[k]; share > bestShare {
			bestShare = share
			best = k
		}
	}
	if best == "" {
		return string(profile.KindNull)
	}
	return string(best)
}
