package query

import (
	"encoding/json"
	"math"
	"math/big"
)

// SortSpec is the DTO-level sort request: a single column path and a direction.
// Path == "" means "no sort" (source-record order, the pre-E9 behavior).
type SortSpec struct {
	Path string `json:"path"`
	Desc bool   `json:"desc"`
}

// CompiledSort holds the resolved sort path segments + direction. nil == no sort.
type CompiledSort struct {
	segs []Seg
	desc bool
}

// CompileSort resolves the sort path once (like CompiledFilter). Returns nil for
// an empty path so callers can treat "no sort" as a cheap nil check.
func CompileSort(spec SortSpec, cm *ColumnModel) (*CompiledSort, error) {
	if spec.Path == "" {
		return nil, nil
	}
	segs, err := resolveSegs(spec.Path, cm)
	if err != nil {
		return nil, err
	}
	return &CompiledSort{segs: segs, desc: spec.Desc}, nil
}

// resolveValue is the first-value scalar for a []Seg against a record: the
// caller-side rule Project/ProjectValues already apply over the real resolver
// resolve(record, segs) []any (columns.go), which returns an EMPTY slice (NOT
// Missing) for an absent path and applies the first-array-element rule via Elem
// segments. Not a duplicate resolver -- resolve is the primitive; this is the
// first-value+Missing adapter the comparator and the keys-only tiers share.
func resolveValue(rec any, segs []Seg) any {
	vs := resolve(rec, segs)
	if len(vs) == 0 {
		return Missing
	}
	return vs[0]
}

// valueKindRank orders values across scalar kinds: Missing < null < bool <
// number < string. Total + deterministic even for mixed-type columns. The
// number rank covers BOTH json.Number (memory tier) and float64 (Parquet
// DOUBLE / SQLite REAL / readers.ToProfileValue passthrough). NAME NOTE: a
// package-level `var kindRank` already exists (columns.go) for JSONKind, so this
// MUST be a different identifier.
func valueKindRank(v any) int {
	switch v.(type) {
	case nil:
		return 1
	case bool:
		return 2
	case json.Number, float64:
		return 3
	case string:
		return 4
	}
	if IsMissing(v) {
		return 0
	}
	return 5 // any unexpected type sorts last, deterministically
}

// compareValues is the cross-tier total order over profiler scalar values.
// Returns <0, 0, >0. Numbers (json.Number AND float64) compare by EXACT value,
// so json.Number("2.5") == float64(2.5) cross-tier and 9007199254740993 !=
// ...992 (which float64 would collapse).
func compareValues(a, b any) int {
	ra, rb := valueKindRank(a), valueKindRank(b)
	if ra != rb {
		return ra - rb
	}
	switch av := a.(type) {
	case bool:
		bv := b.(bool)
		switch {
		case av == bv:
			return 0
		case !av:
			return -1
		default:
			return 1
		}
	case json.Number, float64:
		return compareNumeric(a, b) // both operands are rank-3 numbers here
	case string:
		bv := b.(string)
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		default:
			return 0
		}
	}
	return 0 // equal within nil/Missing/unexpected kind
}

// numericRat converts a rank-3 numeric (json.Number or float64) to an exact
// big.Rat, or (nil,false) for a non-finite float64 (NaN/±Inf) or an unparseable
// literal.
func numericRat(v any) (*big.Rat, bool) {
	switch x := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(string(x))
		return r, ok
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		return new(big.Rat).SetFloat64(x), true
	}
	return nil, false
}

// compareNumeric compares two rank-3 numbers by exact rational value; a
// non-finite/unparseable operand sorts AFTER a finite one (deterministic), and
// two such compare equal.
func compareNumeric(a, b any) int {
	ra, oka := numericRat(a)
	rb, okb := numericRat(b)
	switch {
	case oka && okb:
		return ra.Cmp(rb)
	case oka:
		return -1
	case okb:
		return 1
	default:
		return 0
	}
}

// LessKeys orders two PRE-RESOLVED keys, ties broken on the absolute ordinal
// ASCENDING. The keys-only tiers (rescan, parquet) hold (key, ordinal) pairs and
// MUST use this -- CompiledSort.Less resolves records first, which those tiers
// cannot do (they discarded the records).
func (cs *CompiledSort) LessKeys(keyA any, ordA int64, keyB any, ordB int64) bool {
	c := compareValues(keyA, keyB)
	if cs.desc {
		c = -c
	}
	if c != 0 {
		return c < 0
	}
	return ordA < ordB
}

// Less orders two RECORDS by resolving each to its sort key, then delegating to
// LessKeys (so record-holding and key-holding tiers share one ordering).
func (cs *CompiledSort) Less(recA any, ordA int64, recB any, ordB int64) bool {
	return cs.LessKeys(resolveValue(recA, cs.segs), ordA, resolveValue(recB, cs.segs), ordB)
}
