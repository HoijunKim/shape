package query

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hoijun-kim/shape/internal/profile"
)

// Op names a Condition's comparison operator (spec §5).
type Op string

const (
	OpEq       Op = "eq"
	OpNe       Op = "ne"
	OpLt       Op = "lt"
	OpLte      Op = "lte"
	OpGt       Op = "gt"
	OpGte      Op = "gte"
	OpContains Op = "contains"
	OpRegex    Op = "regex"
	OpIn       Op = "in"
	OpIsNull   Op = "isnull"
	OpNotNull  Op = "notnull"
	OpBool     Op = "bool"
)

// ValueKind tags which field of Value carries the operand.
type ValueKind string

const (
	ValString ValueKind = "string"
	ValNumber ValueKind = "number"
	ValBool   ValueKind = "bool"
	ValNull   ValueKind = "null"
)

// Value is a Condition operand: exactly one of Str/Num/Bool is meaningful,
// selected by Kind, except for OpIn where List carries the candidate set (one
// Value per list element, each itself Kind-tagged the same way).
type Value struct {
	Kind ValueKind `json:"kind"`
	Str  string    `json:"str,omitempty"`
	Num  float64   `json:"num,omitempty"`
	Bool bool      `json:"bool,omitempty"`
	List []Value   `json:"list,omitempty"` // OpIn candidate set
}

// Condition is one leaf test in a Filter tree: resolve Path (spec §3's
// existential value set) and test it against Op/Value. CaseInsensitive
// applies to contains/regex/string-eq (see CompileFilter's per-op handling).
type Condition struct {
	Path            string `json:"path"`
	Op              Op     `json:"op"`
	Value           Value  `json:"value,omitempty"`
	CaseInsensitive bool   `json:"ci,omitempty"`
}

// Combinator selects how a Filter combines its Conditions and Groups.
type Combinator string

const (
	And Combinator = "and"
	Or  Combinator = "or"
)

// Filter is a tree of Conditions and nested Groups combined by Combinator,
// with Negate inverting the whole node. The zero value (no Conditions, no
// Groups, Negate false) matches every record: an AND of zero terms is
// vacuously true, which is exactly "match everything".
type Filter struct {
	Combinator Combinator  `json:"combinator"`
	Conditions []Condition `json:"conditions,omitempty"`
	Groups     []Filter    `json:"groups,omitempty"`
	Negate     bool        `json:"negate,omitempty"`
}

// CompiledFilter wraps the pure predicate produced by CompileFilter. A nil
// pred (the zero value, or the result of compiling an empty Filter) means
// match-all: Match never needs a nil check at call sites.
type CompiledFilter struct {
	pred func(rec any) bool
	key  string // canonical Filter hash; see Key
}

// Key returns a canonical, stable cache key for the Filter this predicate was
// compiled from: two CompiledFilters compiled from the same logical Filter
// always share a key, and any difference in the Filter produces a different
// one. A nil *CompiledFilter returns "".
func (cf *CompiledFilter) Key() string {
	if cf == nil {
		return ""
	}
	return cf.key
}

// Match reports whether rec satisfies the compiled filter. It never errors:
// all fallible work (path parsing, regex compilation, in-set construction)
// happened once in CompileFilter. A nil *CompiledFilter or a nil pred (the
// empty-Filter case) always matches.
func (cf *CompiledFilter) Match(rec any) bool {
	if cf == nil || cf.pred == nil {
		return true
	}
	return cf.pred(rec)
}

// CompileFilter compiles f into a CompiledFilter: a pure, allocation-light,
// error-free predicate. cm supplies precompiled Seg slices for paths that are
// already known Columns (avoiding re-parsing); a path not present in cm (e.g.
// an Elem/"[]" array-element path, which ColumnModel never lists as a column)
// falls back to parsePath directly, so any resolvable path works regardless
// of whether it made the column cut. cm may be nil (path resolution then
// always goes through parsePath).
//
// CompileFilter is where every fallible or expensive step happens exactly
// once: path validation, regexp.Compile, and building OpIn's type-bucketed
// membership sets. It returns an error for a malformed Path or an invalid
// regex; the resulting predicate itself can never error or panic on that
// account again.
func CompileFilter(f Filter, cm *ColumnModel) (*CompiledFilter, error) {
	key, err := canonicalFilterKey(f)
	if err != nil {
		return nil, fmt.Errorf("query: compile filter: key: %w", err)
	}
	if isEmptyFilter(f) {
		return &CompiledFilter{key: key}, nil // nil pred: match-all
	}
	pred, err := compileGroup(f, cm)
	if err != nil {
		return nil, err
	}
	return &CompiledFilter{pred: pred, key: key}, nil
}

// isEmptyFilter reports whether f has no Conditions, no Groups, and no
// Negate -- the canonical "matches everything" shape.
func isEmptyFilter(f Filter) bool {
	return len(f.Conditions) == 0 && len(f.Groups) == 0 && !f.Negate
}

// compileGroup compiles one Filter node (its own Conditions plus nested
// Groups, recursively) into a single predicate, applying Combinator and
// Negate. An empty node (reached only via a nested Groups entry, since the
// top-level empty case is short-circuited by CompileFilter) combines
// vacuously: AND of zero terms is true, OR of zero terms is false, matching
// standard logic and giving Negate something well-defined to invert.
func compileGroup(f Filter, cm *ColumnModel) (func(rec any) bool, error) {
	terms := make([]func(rec any) bool, 0, len(f.Conditions)+len(f.Groups))
	for _, c := range f.Conditions {
		cc, err := compileCondition(c, cm)
		if err != nil {
			return nil, err
		}
		terms = append(terms, cc.matchesRecord)
	}
	for _, g := range f.Groups {
		fn, err := compileGroup(g, cm)
		if err != nil {
			return nil, err
		}
		terms = append(terms, fn)
	}

	or := f.Combinator == Or
	negate := f.Negate
	return func(rec any) bool {
		var result bool
		if or {
			result = false
			for _, fn := range terms {
				if fn(rec) {
					result = true
					break
				}
			}
		} else {
			result = true
			for _, fn := range terms {
				if !fn(rec) {
					result = false
					break
				}
			}
		}
		if negate {
			return !result
		}
		return result
	}, nil
}

// compiledCondition holds everything one Condition needs to test a record
// with no further fallible work: the resolved Seg path, a pre-compiled
// regexp (OpRegex), pre-lowered CI operands, and OpIn's type-bucketed
// membership sets.
type compiledCondition struct {
	segs []Seg
	op   Op
	val  Value
	ci   bool

	re              *regexp.Regexp // OpRegex
	containsOperand string         // OpContains, pre-lowered if ci
	eqOperandLower  string         // OpEq/OpNe, pre-lowered if ci and Value.Kind==ValString

	inStrings   map[string]bool // OpIn
	inNumbers   map[float64]bool
	inBoolTrue  bool
	inBoolFalse bool
}

// resolveSegs resolves path to compiled Seg, validating it first (see
// validatePath). A path already known to cm as a column reuses cm's
// precomputed segments; anything else (including a valid Elem/"[]" path,
// which ColumnModel never lists as a column since array elements are
// previews, not columns) falls back to parsePath directly. cm may be nil.
func resolveSegs(path string, cm *ColumnModel) ([]Seg, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	if cm != nil {
		if idx, ok := cm.byPath[path]; ok {
			return cm.segs[idx], nil
		}
	}
	return parsePath(path), nil
}

// validatePath checks path against parsePath's bracket grammar (columns.go:
// a bare "[]" Elem wildcard, or a `["...quoted..."]` bracket-quoted key)
// WITHOUT re-implementing path parsing: it only rules out the malformed
// bracket shapes parsePath cannot itself reject (a lone '[' not followed by
// ']' or '"' makes parsePath's internal cursor stall, since none of its
// switch cases advance past it; an unterminated `["...` never finds its
// closing `"]`). CompileFilter must fail such a Condition.Path at compile
// time -- the one place fallible work is allowed -- rather than let
// parsePath loop or silently fabricate a segment at match time.
func validatePath(path string) error {
	n := len(path)
	for i := 0; i < n; i++ {
		if path[i] != '[' {
			continue
		}
		if i+1 >= n {
			return fmt.Errorf("query: invalid path %q: unterminated '[' at byte %d", path, i)
		}
		switch path[i+1] {
		case ']':
			i++ // consumed the "[]" Elem wildcard
		case '"':
			j := i + 2
			closed := false
			for j < n {
				if path[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if path[j] == '"' {
					closed = true
					break
				}
				j++
			}
			if !closed || j+1 >= n || path[j+1] != ']' {
				return fmt.Errorf("query: invalid path %q: unterminated bracket-quoted key at byte %d", path, i)
			}
			i = j + 1 // consumed through the closing ']'
		default:
			return fmt.Errorf("query: invalid path %q: expected ']' or '\"' after '[' at byte %d", path, i)
		}
	}
	return nil
}

// compileCondition validates c.Path and, per c.Op, does the one-time
// fallible/expensive work: resolving the path to []Seg, compiling a regex,
// or building an in-set. It returns an error for a bad path or bad regex;
// nothing it produces can fail again at match time.
func compileCondition(c Condition, cm *ColumnModel) (*compiledCondition, error) {
	segs, err := resolveSegs(c.Path, cm)
	if err != nil {
		return nil, fmt.Errorf("query: condition path %q: %w", c.Path, err)
	}

	cc := &compiledCondition{segs: segs, op: c.Op, val: c.Value, ci: c.CaseInsensitive}

	switch c.Op {
	case OpRegex:
		pattern := c.Value.Str
		if c.CaseInsensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("query: condition path %q: bad regex %q: %w", c.Path, c.Value.Str, err)
		}
		cc.re = re

	case OpContains:
		operand := c.Value.Str
		if c.CaseInsensitive {
			operand = strings.ToLower(operand)
		}
		cc.containsOperand = operand

	case OpEq, OpNe:
		if c.CaseInsensitive && c.Value.Kind == ValString {
			cc.eqOperandLower = strings.ToLower(c.Value.Str)
		}

	case OpIn:
		cc.inStrings = make(map[string]bool, len(c.Value.List))
		cc.inNumbers = make(map[float64]bool, len(c.Value.List))
		for _, item := range c.Value.List {
			switch item.Kind {
			case ValString:
				cc.inStrings[item.Str] = true
			case ValNumber:
				cc.inNumbers[item.Num] = true
			case ValBool:
				if item.Bool {
					cc.inBoolTrue = true
				} else {
					cc.inBoolFalse = true
				}
			}
			// ValNull list entries never match: a resolved value that is
			// null is always skipped upstream (see matchesRecord), so an
			// in-list null could never be reached anyway.
		}
	}

	return cc, nil
}

// matchesRecord resolves cc's path against rec and applies the SQL-native
// null rule (spec §5): isnull/notnull test the resolved value set directly
// (empty-or-null / present-and-non-null); every other op is existential over
// the resolved value set, skipping any null candidate outright, so an empty
// set (missing path) or an all-null set both fall through to false with no
// special-case needed beyond the loop finding nothing to match.
func (cc *compiledCondition) matchesRecord(rec any) bool {
	values := resolve(rec, cc.segs)

	switch cc.op {
	case OpIsNull:
		return len(values) == 0 || anyNullish(values)
	case OpNotNull:
		return len(values) > 0 && allNonNullish(values)
	}

	for _, v := range values {
		if isNullish(v) {
			continue
		}
		if cc.matchesValue(v) {
			return true
		}
	}
	return false
}

// matchesValue tests one non-null resolved value against cc's operator and
// operand. It is only ever called with a value that isNullish reports false
// for (matchesRecord filters nulls before calling it).
func (cc *compiledCondition) matchesValue(v any) bool {
	switch cc.op {
	case OpEq:
		matched, equal := cc.typedEqual(v)
		return matched && equal
	case OpNe:
		matched, equal := cc.typedEqual(v)
		return matched && !equal
	case OpLt, OpLte, OpGt, OpGte:
		return cc.matchesRange(v)
	case OpContains:
		s, ok := strOf(v)
		if !ok {
			return false
		}
		if cc.ci {
			s = strings.ToLower(s)
		}
		return strings.Contains(s, cc.containsOperand)
	case OpRegex:
		s, ok := strOf(v)
		if !ok {
			return false
		}
		return cc.re.MatchString(s)
	case OpIn:
		return cc.matchesIn(v)
	case OpBool:
		b, ok := boolOf(v)
		if !ok {
			return false
		}
		return b == cc.val.Bool
	default:
		return false
	}
}

// typedEqual reports (matched, equal): matched is whether v's classified
// kind agrees with cc.val.Kind (the operand's declared kind) at all; equal is
// only meaningful when matched. A cross-type comparison (matched == false)
// makes BOTH eq and ne false at the caller -- spec §5's decisive "cross-type
// ⇒ eq false ⇒ ne false" rule -- because the caller requires "matched &&
// (equal | !equal)" rather than treating "not equal" as sufficient for ne.
func (cc *compiledCondition) typedEqual(v any) (matched, equal bool) {
	switch cc.val.Kind {
	case ValString:
		s, ok := strOf(v)
		if !ok {
			return false, false
		}
		if cc.ci {
			return true, strings.ToLower(s) == cc.eqOperandLower
		}
		return true, s == cc.val.Str
	case ValNumber:
		f, ok := numOf(v)
		if !ok {
			return false, false
		}
		return true, f == cc.val.Num
	case ValBool:
		b, ok := boolOf(v)
		if !ok {
			return false, false
		}
		return true, b == cc.val.Bool
	default: // ValNull or an unrecognized operand kind: never type-matches a non-null v
		return false, false
	}
}

// matchesRange applies lt/lte/gt/gte: a numeric operand forces a numeric
// compare, a string operand forces a lexicographic compare; any other
// operand kind (or a v whose classified kind disagrees) yields false, never
// an error (spec §5: "mismatched type ⇒ false, never error").
func (cc *compiledCondition) matchesRange(v any) bool {
	switch cc.val.Kind {
	case ValNumber:
		f, ok := numOf(v)
		if !ok {
			return false
		}
		return compareOrdered(cc.op, f, cc.val.Num)
	case ValString:
		s, ok := strOf(v)
		if !ok {
			return false
		}
		return compareOrdered(cc.op, s, cc.val.Str)
	default:
		return false
	}
}

// orderedNumOrStr is any type matchesRange's op comparisons run over.
type orderedNumOrStr interface {
	~float64 | ~string
}

// compareOrdered applies op's ordering test to a, b (both float64 or both
// string, per matchesRange's two call sites).
func compareOrdered[T orderedNumOrStr](op Op, a, b T) bool {
	switch op {
	case OpLt:
		return a < b
	case OpLte:
		return a <= b
	case OpGt:
		return a > b
	case OpGte:
		return a >= b
	default:
		return false
	}
}

// matchesIn reports whether v equals some element of cc's pre-built in-set,
// under v's own classified kind (type-matched membership; an empty list
// leaves every set empty, so membership is false with no special case).
func (cc *compiledCondition) matchesIn(v any) bool {
	if s, ok := strOf(v); ok {
		return cc.inStrings[s]
	}
	if f, ok := numOf(v); ok {
		return cc.inNumbers[f]
	}
	if b, ok := boolOf(v); ok {
		if b {
			return cc.inBoolTrue
		}
		return cc.inBoolFalse
	}
	return false // object/array/other: no bucket, never a member
}

// isNullish reports whether v is JSON null (profile.KindOf's KindNull, which
// covers both a literal nil and -- by construction of resolve -- any decoded
// JSON null). Used both for isnull/notnull and to make every comparison op
// skip a null candidate in the existential loop (the SQL-native null rule).
func isNullish(v any) bool {
	return profile.KindOf(v) == profile.KindNull
}

func anyNullish(values []any) bool {
	for _, v := range values {
		if isNullish(v) {
			return true
		}
	}
	return false
}

func allNonNullish(values []any) bool {
	for _, v := range values {
		if isNullish(v) {
			return false
		}
	}
	return true
}

// numOf extracts a float64 from v if and only if v classifies as an int or
// float (profile.KindOf); this matches Task 1/2's cell classification
// (columns.go's toCell) so a filter's notion of "numeric" never disagrees
// with the table's displayed cell kind.
func numOf(v any) (float64, bool) {
	switch profile.KindOf(v) {
	case profile.KindInt, profile.KindFloat:
	default:
		return 0, false
	}
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case float64:
		return t, true
	default:
		return 0, false
	}
}

// strOf extracts a string from v iff v classifies as profile.KindString.
func strOf(v any) (string, bool) {
	if profile.KindOf(v) != profile.KindString {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// boolOf extracts a bool from v iff v classifies as profile.KindBool.
func boolOf(v any) (bool, bool) {
	if profile.KindOf(v) != profile.KindBool {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}
