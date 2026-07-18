package query

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ColumnSpec names one output column of a Transform.Select: Path identifies
// which value to project (any resolvable path -- a base ColumnModel column,
// a deeper/array leaf, or a path beyond MaxColumns), As optionally renames
// it in the output (spec §6).
type ColumnSpec struct {
	Path string `json:"path"`
	As   string `json:"as,omitempty"`
}

// Transform describes how to reshape a ColumnModel's base column set into
// the columns a Query/Export actually returns (spec §6).
//
// Select, when non-empty, is authoritative: it is the EXACT output column
// set, in the given order (reorder), with each ColumnSpec.Path resolved
// (flatten -- any resolvable path works, not just a base column -- and
// un-cap -- a path beyond MaxColumns is still addressable by naming it
// explicitly) and optionally renamed via As. When Select is empty, Drop (if
// non-empty) is applied against the base column set instead: SQL has no way
// to say "all but X", so Drop is expanded at compile time into "every base
// column not named (or nested under a name) in Drop". When BOTH Select and
// Drop are empty, the output is exactly the base column set
// (ColumnModel.Columns), unchanged.
//
// FlattenObjects defaults to true (nested objects explode into dotted base
// columns) -- see CompileTransform's doc comment for how this field is
// currently handled: the base ColumnModel (Task 2) already applies this
// explosion when it groups columns, so today the field is accepted and
// carried through the API surface but does not yet gate a distinct
// collapsed/un-flattened rendering of the base set; see the judgment-call
// note on CompileTransform.
type Transform struct {
	Select         []ColumnSpec `json:"select,omitempty"`
	Drop           []string     `json:"drop,omitempty"`
	FlattenObjects bool         `json:"flattenObjects"`
}

// outCol is one compiled output column: segs are the compiled path segments
// Project resolves against each record, and col is the full Column metadata
// (including its display name, After Select's As-or-leaf-name rule; equal to
// the base Column.Name otherwise) returned by CompiledTransform.Columns.
type outCol struct {
	segs []Seg
	col  Column
}

// CompiledTransform is the compiled form of a Transform: a fixed, ordered
// list of output columns, each already carrying its own resolved []Seg so
// Project never re-parses a path per record.
type CompiledTransform struct {
	cols []outCol
}

// CompileTransform compiles t against cm (the source's base ColumnModel),
// applying spec §6's rules:
//
//  1. Select non-empty: the EXACT output columns, in Select's order. Each
//     ColumnSpec.Path is resolved by preferring cm's precompiled segments
//     when the path is already a known base column (reusing its full
//     metadata -- Type/Nullable/Presence/Distinct/Container); any other
//     resolvable path (a deep leaf excluded from Columns because it
//     contains an Elem "[]" segment -- see spec §3 rule 1 -- or a base
//     candidate dropped only because it was beyond MaxColumns) falls back
//     to parsePath directly. Path/Name in the output column become As when
//     set, else the path's leaf name (columnName) -- see the judgment-call
//     note below. Drop is ignored whenever Select is non-empty (spec §6:
//     "Drop ... used only when Select empty").
//  2. Select empty: start from the base column set (cm.Columns, in cm's
//     order) and, if Drop is non-empty, remove every base column whose Path
//     equals a Drop entry OR is nested under one (a "." or "[]" prefix
//     match) -- this is the "expanded against the ColumnModel" step spec §6
//     calls out, since a Drop entry may name an interior path (e.g. "user")
//     that was never itself a materialized base column (Task 2 drops pure
//     interior objects), yet must still remove every column nested under it
//     (e.g. "user.name").
//
// cm must be non-nil: the base column set and Drop's expansion both need
// it, and a Select-only caller can still benefit from cm's precompiled
// segments for paths that are known columns.
//
// Judgment call: spec §6 says an output column renamed via As uses that
// name for "Column.Name/Path in output" -- both fields, not just Name --
// so a Select-projected column's Path in the returned Column is the display
// name (As, or the path's leaf name), not the original dotted source path.
// The original resolvable path is preserved internally in outCol.segs (used
// by Project) regardless of what the output Column.Path says.
//
// Judgment call: a Select entry naming a path outside cm.byPath (a deep
// leaf or a MaxColumns-capped path) has no profiled metadata available (cm
// does not retain discarded/capped candidates' FieldProfile data), so its
// output Column carries zero-valued Type/Nullable/Presence/Distinct/
// Container -- only Path/Name/Index are meaningful. This is the simplest
// behavior consistent with "an explicit projection is unbounded": the
// column becomes addressable even though nothing describes its shape.
func CompileTransform(t Transform, cm *ColumnModel) (*CompiledTransform, error) {
	if cm == nil {
		return nil, fmt.Errorf("query: CompileTransform requires a non-nil ColumnModel")
	}

	if len(t.Select) > 0 {
		cols, err := compileSelect(t.Select, cm)
		if err != nil {
			return nil, err
		}
		return &CompiledTransform{cols: cols}, nil
	}

	cols := baseOutCols(cm)
	if len(t.Drop) > 0 {
		cols = applyDrop(cols, t.Drop)
	}
	return &CompiledTransform{cols: cols}, nil
}

// baseOutCols returns cm.Columns, unchanged, as the base output column set
// (in cm's own order): the starting point when Transform.Select is empty.
func baseOutCols(cm *ColumnModel) []outCol {
	cols := make([]outCol, len(cm.Columns))
	for i, c := range cm.Columns {
		cols[i] = outCol{segs: cm.segs[i], col: c}
	}
	return cols
}

// compileSelect resolves each ColumnSpec in specs (in order) into an outCol,
// per CompileTransform's rule 1.
func compileSelect(specs []ColumnSpec, cm *ColumnModel) ([]outCol, error) {
	cols := make([]outCol, 0, len(specs))
	for _, spec := range specs {
		if err := validatePath(spec.Path); err != nil {
			return nil, fmt.Errorf("query: select path %q: %w", spec.Path, err)
		}

		var segs []Seg
		base := Column{}
		if idx, ok := cm.byPath[spec.Path]; ok {
			segs = cm.segs[idx]
			base = cm.Columns[idx]
		} else {
			segs = parsePath(spec.Path)
		}

		name := spec.As
		if name == "" {
			name = columnName(spec.Path, segs)
		}

		out := base
		out.Path = name
		out.Name = name
		out.Index = len(cols)
		cols = append(cols, outCol{segs: segs, col: out})
	}
	return cols, nil
}

// applyDrop returns cols with every entry dropped whose Path equals, or is
// nested under (a "." or "[]" prefix), some name in drop -- the "expanded
// against the ColumnModel" step from spec §6. Remaining columns are
// reindexed so Column.Index stays a contiguous 0-based output position.
func applyDrop(cols []outCol, drop []string) []outCol {
	kept := make([]outCol, 0, len(cols))
	for _, oc := range cols {
		if isDropped(oc.col.Path, drop) {
			continue
		}
		kept = append(kept, oc)
	}
	for i := range kept {
		kept[i].col.Index = i
	}
	return kept
}

// isDropped reports whether path equals, or is nested under, some entry in
// drop (nested = path has that entry as a "." or "[]" prefix segment
// boundary, e.g. dropping "user" also drops "user.name" and "user[].id").
func isDropped(path string, drop []string) bool {
	for _, d := range drop {
		if path == d || strings.HasPrefix(path, d+".") || strings.HasPrefix(path, d+"[]") {
			return true
		}
	}
	return false
}

// Columns returns ct's output columns, in order: the exact column set a
// Row from Project is positionally aligned to.
func (ct *CompiledTransform) Columns() []Column {
	if ct == nil {
		return nil
	}
	cols := make([]Column, len(ct.cols))
	for i, oc := range ct.cols {
		cols[i] = oc.col
	}
	return cols
}

// Project resolves rec against every output column's compiled segments and
// classifies the result into a Row aligned to Columns(), with Index set to
// idx. Consistent with Task 1's resolveCol: an empty resolve() set (path
// absent for rec) becomes CellMissing; otherwise the FIRST resolved value is
// classified via toCell -- for a plain scalar path this is its only value,
// and for a path containing an Elem ("[]") segment (0..n values) this takes
// the first array element rather than a container preview, matching how the
// base ColumnModel already renders multi-value paths through resolveCol.
func (ct *CompiledTransform) Project(rec any, idx int64) Row {
	if ct == nil {
		return Row{Index: idx}
	}
	cells := make([]Cell, len(ct.cols))
	for i, oc := range ct.cols {
		values := resolve(rec, oc.segs)
		if len(values) == 0 {
			cells[i] = Cell{Kind: CellMissing}
			continue
		}
		cells[i] = toCell(values[0])
	}
	return Row{Index: idx, Cells: cells}
}

// CompiledPlan bundles a compiled Filter, a compiled Transform, and the
// source ColumnModel they were compiled against into the single unit a
// Backend needs to run a query: predicate, projection, and column metadata
// together (spec §4/§6).
type CompiledPlan struct {
	Filter    *CompiledFilter
	Transform *CompiledTransform
	Columns   *ColumnModel

	filterKey string // canonical Filter-only hash (== Filter.Key()); see FilterKey
}

// FilterKey returns a canonical, stable string key for p's Filter alone
// (Transform plays no part), suitable for the bitset/cursor/count caches
// described in spec §4 (memBackend's per-FilterKey match bitset,
// rescanBackend's (FilterKey,endOffset) cursor cache, and CountMatches'
// per-FilterKey memoization): identical logical Filter inputs always produce
// the same key, any difference in the Filter produces a different key, and
// the key is exactly p.Filter.Key() -- see canonicalFilterKey for why the key
// is filter-only.
func (p *CompiledPlan) FilterKey() string {
	if p == nil {
		return ""
	}
	return p.filterKey
}

// CompilePlan compiles f and t against cm and bundles the results into a
// CompiledPlan, taking its canonical FilterKey off the compiled filter.
// Compilation errors from either CompileFilter or CompileTransform are
// returned as-is (wrapped with context); nothing about key computation
// itself can fail here (CompileFilter already computed and validated it).
func CompilePlan(f Filter, t Transform, cm *ColumnModel) (*CompiledPlan, error) {
	cf, err := CompileFilter(f, cm)
	if err != nil {
		return nil, fmt.Errorf("query: compile plan: filter: %w", err)
	}
	ct, err := CompileTransform(t, cm)
	if err != nil {
		return nil, fmt.Errorf("query: compile plan: transform: %w", err)
	}
	return &CompiledPlan{Filter: cf, Transform: ct, Columns: cm, filterKey: cf.Key()}, nil
}

// canonicalFilterKey renders f as canonical JSON and returns the hex-encoded
// SHA-256 digest. Filter contains only structs/slices/scalars (no maps), so
// encoding/json's field-order-following marshal is already deterministic --
// no map-iteration dependence enters the key, and identical logical input
// (down to slice element order) always yields the same bytes.
//
// The key is FILTER-ONLY on purpose: it keys match bitsets, and only the
// Filter determines which records match. Keying it on (Filter, Transform) --
// as the pre-E2 canonicalPlanKey did -- split one logical bitset across as
// many cache entries as there were transforms over it, and gave Count (which
// never has a Transform) no way to share Query's entry at all.
func canonicalFilterKey(f Filter) (string, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
