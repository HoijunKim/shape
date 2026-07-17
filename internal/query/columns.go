// Package query implements the internal/query engine core for the shape
// data explorer: compiled path segments, value resolution, and cell/row
// rendering (spec §3, docs/superpowers/specs/2026-07-17-shape-engine-design.md).
package query

import (
	"encoding/json"
	"fmt"
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
