package query

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// --- CompileTransform: empty Select+Drop -> base column set ------------------

func TestCompileTransform_EmptySelectDrop_BaseColumns(t *testing.T) {
	records := []map[string]any{
		{"a": json.Number("1"), "b": "x"},
		{"a": json.Number("2"), "b": "y"},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{}, cm)
	if err != nil {
		t.Fatalf("CompileTransform(empty) error = %v, want nil", err)
	}
	got := ct.Columns()
	if !reflect.DeepEqual(got, cm.Columns) {
		t.Fatalf("Columns() = %#v, want cm.Columns %#v", got, cm.Columns)
	}
}

// --- CompileTransform: Select reorders, renames, flattens, un-caps -----------

func TestCompileTransform_Select_Reorder(t *testing.T) {
	records := []map[string]any{
		{"a": json.Number("1"), "b": "x"},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{Select: []ColumnSpec{{Path: "b"}, {Path: "a"}}}, cm)
	if err != nil {
		t.Fatalf("CompileTransform error = %v, want nil", err)
	}
	cols := ct.Columns()
	if len(cols) != 2 {
		t.Fatalf("len(Columns()) = %d, want 2", len(cols))
	}
	if cols[0].Name != "b" || cols[1].Name != "a" {
		t.Fatalf("Columns() order = [%q, %q], want [\"b\", \"a\"] (reversed from base order)", cols[0].Name, cols[1].Name)
	}
	if cols[0].Index != 0 || cols[1].Index != 1 {
		t.Fatalf("Columns() indices = [%d, %d], want [0, 1]", cols[0].Index, cols[1].Index)
	}
}

func TestCompileTransform_Select_Rename(t *testing.T) {
	records := []map[string]any{{"a": json.Number("1")}}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{Select: []ColumnSpec{{Path: "a", As: "alpha"}}}, cm)
	if err != nil {
		t.Fatalf("CompileTransform error = %v, want nil", err)
	}
	cols := ct.Columns()
	if len(cols) != 1 {
		t.Fatalf("len(Columns()) = %d, want 1", len(cols))
	}
	if cols[0].Name != "alpha" {
		t.Fatalf("Columns()[0].Name = %q, want %q (As rename)", cols[0].Name, "alpha")
	}
	// Preserve carried-over metadata (Type) from the underlying base column.
	if cols[0].Type != "int" {
		t.Fatalf("Columns()[0].Type = %q, want %q (metadata carried through rename)", cols[0].Type, "int")
	}

	rec := map[string]any{"a": json.Number("42")}
	row := ct.Project(rec, 0)
	if len(row.Cells) != 1 || row.Cells[0].Kind != CellInt || row.Cells[0].Num != 42 {
		t.Fatalf("Project renamed column = %#v, want CellInt 42", row.Cells)
	}
}

// TestCompileTransform_Select_DeepLeafFlattens exercises naming a path that
// resolves in the data but is NOT itself a base ColumnModel column: an Elem
// ("[]") path is always excluded from Columns (spec §3 rule 1 -- array
// elements are previews, not fixed columns), so "tags[]" only becomes
// addressable via an explicit Select entry (spec §6: "unnesting them is a
// later Transform, not a base column" / "Select ... may name ... a deep leaf
// (flatten...)").
func TestCompileTransform_Select_DeepLeafFlattens(t *testing.T) {
	records := []map[string]any{
		{"tags": []any{"x", "y"}},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	if _, ok := cm.byPath["tags[]"]; ok {
		t.Fatalf("fixture invalid: cm.byPath contains \"tags[]\", want it excluded (Elem path)")
	}

	ct, err := CompileTransform(Transform{Select: []ColumnSpec{{Path: "tags[]"}}}, cm)
	if err != nil {
		t.Fatalf("CompileTransform(deep leaf) error = %v, want nil", err)
	}
	cols := ct.Columns()
	if len(cols) != 1 {
		t.Fatalf("len(Columns()) = %d, want 1", len(cols))
	}

	row := ct.Project(records[0], 0)
	if len(row.Cells) != 1 {
		t.Fatalf("len(Project cells) = %d, want 1", len(row.Cells))
	}
	// resolveCol-consistent behavior: an Elem path resolving to multiple
	// values (["x","y"]) takes the FIRST value, not a container preview.
	if row.Cells[0].Kind != CellString || row.Cells[0].Str != "x" {
		t.Fatalf("Project(tags[]) cell = %#v, want CellString \"x\" (first element)", row.Cells[0])
	}
}

// TestCompileTransform_Select_UnCapsBeyondMaxColumns builds a ColumnModel
// with more eligible columns than MaxColumns (520 > 512), confirms the
// target column was actually dropped by the cap, then Selects it explicitly
// and confirms it resolves correctly anyway (spec §6: "a path beyond
// MaxColumns" / "un-cap -- an explicit projection is unbounded").
func TestCompileTransform_Select_UnCapsBeyondMaxColumns(t *testing.T) {
	const n = 520
	colName := func(k int) string { return fmt.Sprintf("col%03d", k) }

	// Record i contains {col_k : k >= i}: col_k's presence is (n-k)/n... wait,
	// mirror columns_test.go's TestBuildColumnModel_MaxColumnsCap fixture
	// exactly: record i contains col_k for k >= i, so col_k's presence is
	// strictly increasing with k (col0 has the LOWEST presence and is
	// dropped by the cap).
	records := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		rec := make(map[string]any, n-i)
		for k := i; k < n; k++ {
			rec[colName(k)] = true
		}
		records[i] = rec
	}

	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	if !cm.Truncated {
		t.Fatalf("fixture invalid: cm.Truncated = false, want true")
	}
	target := colName(0) // lowest presence: guaranteed dropped by the cap
	if _, ok := cm.byPath[target]; ok {
		t.Fatalf("fixture invalid: byPath contains %q, want it capped out", target)
	}

	ct, err := CompileTransform(Transform{Select: []ColumnSpec{{Path: target}}}, cm)
	if err != nil {
		t.Fatalf("CompileTransform(uncap) error = %v, want nil", err)
	}
	cols := ct.Columns()
	if len(cols) != 1 || cols[0].Name != target {
		t.Fatalf("Columns() = %#v, want a single column named %q", cols, target)
	}

	// records[0] has every col_k (k from 0..n-1) set to true.
	row := ct.Project(records[0], 0)
	if len(row.Cells) != 1 || row.Cells[0].Kind != CellBool || !row.Cells[0].Bool {
		t.Fatalf("Project(%s) on records[0] = %#v, want CellBool true", target, row.Cells)
	}
}

// --- CompileTransform: Drop is expanded against the ColumnModel --------------

func TestCompileTransform_Drop_ExpandedSubtree(t *testing.T) {
	// "user" is a pure interior object (no drift) with a deeper column
	// "user.name": per Task 2's rule, "user" itself is NOT a base column,
	// only "user.name" is. Naming "user" in Drop must still remove
	// "user.name" (Drop is "expanded against the ColumnModel": naming a
	// path removes it AND everything nested under it, spec §6: "Drop ...
	// SQL cannot say all but X").
	records := []map[string]any{
		{"user": map[string]any{"name": "Alice"}, "other": json.Number("1")},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	if _, ok := cm.byPath["user"]; ok {
		t.Fatalf("fixture invalid: byPath contains \"user\" (should be dropped, pure interior object)")
	}
	if _, ok := cm.byPath["user.name"]; !ok {
		t.Fatalf("fixture invalid: byPath missing \"user.name\"")
	}

	ct, err := CompileTransform(Transform{Drop: []string{"user"}}, cm)
	if err != nil {
		t.Fatalf("CompileTransform(drop) error = %v, want nil", err)
	}
	cols := ct.Columns()
	var names []string
	for _, c := range cols {
		names = append(names, c.Name)
	}
	if len(cols) != 1 || cols[0].Path != "other" {
		t.Fatalf("Columns() after Drop([\"user\"]) = %#v (names %v), want only \"other\" (user.name expanded/removed)", cols, names)
	}
}

func TestCompileTransform_Drop_ExactColumn(t *testing.T) {
	records := []map[string]any{
		{"a": json.Number("1"), "b": "x", "c": true},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{Drop: []string{"b"}}, cm)
	if err != nil {
		t.Fatalf("CompileTransform(drop) error = %v, want nil", err)
	}
	cols := ct.Columns()
	if len(cols) != 2 {
		t.Fatalf("len(Columns()) = %d, want 2 (base minus \"b\")", len(cols))
	}
	for _, c := range cols {
		if c.Path == "b" {
			t.Fatalf("Columns() still contains dropped column %q", c.Path)
		}
	}
	// Base order (a, b, c) minus b preserves relative order: a, c.
	if cols[0].Path != "a" || cols[1].Path != "c" {
		t.Fatalf("Columns() = [%q, %q], want [\"a\", \"c\"] (order preserved minus drop)", cols[0].Path, cols[1].Path)
	}
}

func TestCompileTransform_Drop_IgnoredWhenSelectNonEmpty(t *testing.T) {
	// Spec §6: Drop "used only when Select empty". A non-empty Select must
	// produce EXACT output columns regardless of Drop's contents.
	records := []map[string]any{{"a": json.Number("1"), "b": "x"}}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{
		Select: []ColumnSpec{{Path: "a"}, {Path: "b"}},
		Drop:   []string{"a"}, // must be ignored: Select is non-empty
	}, cm)
	if err != nil {
		t.Fatalf("CompileTransform error = %v, want nil", err)
	}
	cols := ct.Columns()
	if len(cols) != 2 || cols[0].Path != "a" || cols[1].Path != "b" {
		t.Fatalf("Columns() = %#v, want [\"a\",\"b\"] (Drop ignored, Select is authoritative)", cols)
	}
}

// --- CompiledTransform.Project -----------------------------------------------

func TestCompiledTransform_Project_AlignedRow(t *testing.T) {
	records := []map[string]any{
		{"a": json.Number("1"), "b": "x"},
		{"a": json.Number("2")}, // "b" absent this record
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{}, cm)
	if err != nil {
		t.Fatalf("CompileTransform error = %v, want nil", err)
	}
	cols := ct.Columns()

	row0 := ct.Project(records[0], 7)
	if row0.Index != 7 {
		t.Fatalf("Project row0.Index = %d, want 7", row0.Index)
	}
	if len(row0.Cells) != len(cols) {
		t.Fatalf("len(row0.Cells) = %d, want %d (aligned to Columns())", len(row0.Cells), len(cols))
	}
	// Verify each cell matches what cm.resolveCol would produce for the same
	// underlying base column, since Transform{} projects the base set 1:1.
	for i, c := range cols {
		idx := cm.byPath[c.Path]
		want := cm.resolveCol(idx, records[0])
		if row0.Cells[i] != want {
			t.Fatalf("row0.Cells[%d] (%s) = %#v, want %#v", i, c.Path, row0.Cells[i], want)
		}
	}

	row1 := ct.Project(records[1], 8)
	if row1.Index != 8 {
		t.Fatalf("Project row1.Index = %d, want 8", row1.Index)
	}
	bIdx := -1
	for i, c := range cols {
		if c.Path == "b" {
			bIdx = i
		}
	}
	if bIdx < 0 {
		t.Fatalf("fixture invalid: no \"b\" column found")
	}
	if row1.Cells[bIdx].Kind != CellMissing {
		t.Fatalf("row1.Cells[%d] (b, absent) = %#v, want CellMissing", bIdx, row1.Cells[bIdx])
	}
}

// --- CompiledFilter.Key -------------------------------------------------------

func TestCompiledFilter_Key_SameLogicalFilterSameKey(t *testing.T) {
	f := Filter{Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}}}}
	a, err := CompileFilter(f, nil)
	if err != nil {
		t.Fatalf("CompileFilter(a) error = %v, want nil", err)
	}
	b, err := CompileFilter(f, nil)
	if err != nil {
		t.Fatalf("CompileFilter(b) error = %v, want nil", err)
	}
	if a == b {
		t.Fatalf("fixture invalid: a and b are the same *CompiledFilter pointer")
	}
	if a.Key() != b.Key() {
		t.Fatalf("Key() differs for two compiles of the identical logical Filter: %q != %q", a.Key(), b.Key())
	}
}

func TestCompiledFilter_Key_DifferentFilterDifferentKey(t *testing.T) {
	f1 := Filter{Conditions: []Condition{{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 10}}}}
	f2 := Filter{Conditions: []Condition{{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 20}}}}
	a, err := CompileFilter(f1, nil)
	if err != nil {
		t.Fatalf("CompileFilter(f1) error = %v, want nil", err)
	}
	b, err := CompileFilter(f2, nil)
	if err != nil {
		t.Fatalf("CompileFilter(f2) error = %v, want nil", err)
	}
	if a.Key() == b.Key() {
		t.Fatalf("Key() identical for filters differing only in Condition.Value.Num: %q", a.Key())
	}
}

func TestCompiledFilter_Key_EmptyFilterStable(t *testing.T) {
	a, err := CompileFilter(Filter{}, nil)
	if err != nil {
		t.Fatalf("CompileFilter(empty) error = %v, want nil", err)
	}
	b, err := CompileFilter(Filter{}, nil)
	if err != nil {
		t.Fatalf("CompileFilter(empty) second call error = %v, want nil", err)
	}
	if a.Key() == "" {
		t.Fatalf("Key() = empty string for the empty (match-all) Filter, want a non-empty stable key")
	}
	if a.Key() != b.Key() {
		t.Fatalf("Key() not stable across two compiles of Filter{}: %q != %q", a.Key(), b.Key())
	}
}

// --- CompiledPlan / FilterKey -------------------------------------------------

func testColumnModel(t *testing.T) *ColumnModel {
	t.Helper()
	records := []map[string]any{
		{"name": "alice", "age": json.Number("30")},
		{"name": "bob", "age": json.Number("25")},
	}
	disc, prof := discoverAndProfile(records)
	return buildColumnModel(disc, prof, nil)
}

func TestCompiledPlan_FilterKey_StableForIdenticalInput(t *testing.T) {
	cm := testColumnModel(t)
	f := Filter{Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}}}}
	tr := Transform{Select: []ColumnSpec{{Path: "name"}, {Path: "age"}}}

	p1, err := CompilePlan(f, tr, cm)
	if err != nil {
		t.Fatalf("CompilePlan error = %v, want nil", err)
	}
	p2, err := CompilePlan(f, tr, cm)
	if err != nil {
		t.Fatalf("CompilePlan (second call) error = %v, want nil", err)
	}
	if p1.FilterKey() == "" {
		t.Fatalf("FilterKey() = empty string, want a non-empty canonical key")
	}
	if p1.FilterKey() != p2.FilterKey() {
		t.Fatalf("FilterKey() not stable: %q != %q for two CompilePlan calls over the identical Filter", p1.FilterKey(), p2.FilterKey())
	}
}

func TestCompiledPlan_FilterKey_DistinctForDifferentFilter(t *testing.T) {
	cm := testColumnModel(t)
	tr := Transform{}
	f1 := Filter{Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}}}}
	f2 := Filter{Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "bob"}}}}

	p1, err := CompilePlan(f1, tr, cm)
	if err != nil {
		t.Fatalf("CompilePlan(f1) error = %v, want nil", err)
	}
	p2, err := CompilePlan(f2, tr, cm)
	if err != nil {
		t.Fatalf("CompilePlan(f2) error = %v, want nil", err)
	}
	if p1.FilterKey() == p2.FilterKey() {
		t.Fatalf("FilterKey() identical for different filters: %q", p1.FilterKey())
	}
}

// TestCompiledPlan_FilterKey_IgnoresTransform pins the E2 behavior change:
// FilterKey is now filter-only, so two CompiledPlans over the identical
// Filter but DIFFERENT Transforms must share one key -- Query and Count share
// one match bitset regardless of what a Transform later projects.
func TestCompiledPlan_FilterKey_IgnoresTransform(t *testing.T) {
	cm := testColumnModel(t)
	f := Filter{Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}}}}

	p1, err := CompilePlan(f, Transform{}, cm)
	if err != nil {
		t.Fatalf("CompilePlan(t1) error = %v, want nil", err)
	}
	p2, err := CompilePlan(f, Transform{Select: []ColumnSpec{{Path: "name"}}}, cm)
	if err != nil {
		t.Fatalf("CompilePlan(t2) error = %v, want nil", err)
	}
	if p1.FilterKey() != p2.FilterKey() {
		t.Fatalf("FilterKey() differs across Transforms over the identical Filter: %q != %q, want equal (filter-only key)", p1.FilterKey(), p2.FilterKey())
	}
}

func TestCompiledPlan_FieldsPopulated(t *testing.T) {
	cm := testColumnModel(t)
	f := Filter{Conditions: []Condition{{Path: "age", Op: OpGt, Value: Value{Kind: ValNumber, Num: 26}}}}
	tr := Transform{}

	p, err := CompilePlan(f, tr, cm)
	if err != nil {
		t.Fatalf("CompilePlan error = %v, want nil", err)
	}
	if p.Filter == nil || p.Transform == nil || p.Columns == nil {
		t.Fatalf("CompiledPlan fields = %#v, want all non-nil", p)
	}
	if !p.Filter.Match(map[string]any{"age": json.Number("30")}) {
		t.Fatalf("p.Filter.Match(age=30) = false, want true (age > 26)")
	}
	if p.Filter.Match(map[string]any{"age": json.Number("10")}) {
		t.Fatalf("p.Filter.Match(age=10) = true, want false (age > 26)")
	}
}

func TestCompilePlan_ErrorPropagatesFromFilter(t *testing.T) {
	cm := testColumnModel(t)
	bad := Filter{Conditions: []Condition{{Path: "name", Op: OpRegex, Value: Value{Str: "("}}}} // unbalanced paren
	if _, err := CompilePlan(bad, Transform{}, cm); err == nil {
		t.Fatalf("CompilePlan(bad regex) error = nil, want an error")
	}
}

// TestCompiledPlan_FilterKey_LooksCanonical is a light sanity check that
// FilterKey does not depend on map iteration: build the same logical Filter
// twice from independently-constructed (but deeply equal) values and confirm
// the key matches, and that it looks like a canonical hex digest (no
// whitespace, no raw JSON leaking through).
func TestCompiledPlan_FilterKey_LooksCanonical(t *testing.T) {
	cm := testColumnModel(t)
	f := Filter{Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "alice"}}}}
	p, err := CompilePlan(f, Transform{}, cm)
	if err != nil {
		t.Fatalf("CompilePlan error = %v, want nil", err)
	}
	key := p.FilterKey()
	if strings.ContainsAny(key, " \t\n{}\"") {
		t.Fatalf("FilterKey() = %q, want a canonical opaque token (no raw JSON/whitespace)", key)
	}
}

// --- isIdentityTransform ------------------------------------------------------

// TestIsIdentityTransform covers every Transform field's effect on the
// predicate Engine.QueryRows uses to decide whose truncation numbers
// (base ColumnModel vs. projected column set) to stamp onto a RowSet: a
// Transform is "identity" (leaves the base column set unchanged) iff both
// Select and Drop are empty. FlattenObjects is deliberately NOT part of the
// predicate: per Transform's doc comment (transform.go) and CompileTransform's
// implementation, FlattenObjects is accepted and carried through the API
// surface but does not yet gate any distinct rendering of the base set --
// CompileTransform never reads it -- so today it cannot affect whether the
// output column set differs from the base one, regardless of its value.
func TestIsIdentityTransform(t *testing.T) {
	cases := []struct {
		name string
		t    Transform
		want bool
	}{
		{"zero value", Transform{}, true},
		{"select non-empty", Transform{Select: []ColumnSpec{{Path: "a"}}}, false},
		{"drop non-empty", Transform{Drop: []string{"a"}}, false},
		{"select and drop both non-empty", Transform{Select: []ColumnSpec{{Path: "a"}}, Drop: []string{"b"}}, false},
		{"flattenObjects true, otherwise zero", Transform{FlattenObjects: true}, true},
		{"flattenObjects false, otherwise zero", Transform{FlattenObjects: false}, true},
		{"flattenObjects true with select", Transform{Select: []ColumnSpec{{Path: "a"}}, FlattenObjects: true}, false},
		{"flattenObjects true with drop", Transform{Drop: []string{"a"}, FlattenObjects: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isIdentityTransform(c.t); got != c.want {
				t.Fatalf("isIdentityTransform(%#v) = %v, want %v", c.t, got, c.want)
			}
		})
	}
}

// --- ProjectValues (E4 Task 1) -----------------------------------------------

// bigNestedRecord returns a record whose "meta" value is a nested object far
// larger than previewCap (200 bytes of compact JSON), so a projection that
// went through toCell would truncate it.
func bigNestedRecord() (rec map[string]any, meta map[string]any) {
	meta = map[string]any{}
	for i := 0; i < 40; i++ {
		meta[fmt.Sprintf("k%02d", i)] = "vvvvvvvvvv"
	}
	return map[string]any{"id": json.Number("1"), "meta": meta}, meta
}

func TestProjectValues_ContainerIsRawAndUntruncated(t *testing.T) {
	rec, meta := bigNestedRecord()
	disc, prof := discoverAndProfile([]map[string]any{rec})
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{Select: []ColumnSpec{{Path: "meta", As: "meta"}}}, cm)
	if err != nil {
		t.Fatalf("CompileTransform error = %v, want nil", err)
	}
	vals := ct.ProjectValues(rec, nil)
	if len(vals) != 1 {
		t.Fatalf("len(ProjectValues) = %d, want 1", len(vals))
	}
	got, ok := vals[0].(map[string]any)
	if !ok {
		t.Fatalf("ProjectValues[0] = %#v (%T), want the raw map[string]any", vals[0], vals[0])
	}
	if !reflect.DeepEqual(got, meta) {
		t.Fatalf("ProjectValues[0] lost data: got %d keys, want %d (a truncated preview string or a re-encoded copy is a bug)", len(got), len(meta))
	}
	// The whole point: the compact JSON of this value is longer than the
	// preview cap a Cell would have applied.
	if b, _ := json.Marshal(meta); len(b) <= previewCap {
		t.Fatalf("fixture is not big enough to discriminate: %d bytes <= previewCap %d", len(b), previewCap)
	}
}

func TestProjectValues_NumbersKeepTheirExactLiteralType(t *testing.T) {
	rec := map[string]any{"n": json.Number("123456789012345678901")}
	disc, prof := discoverAndProfile([]map[string]any{rec})
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{}, cm)
	if err != nil {
		t.Fatalf("CompileTransform error = %v, want nil", err)
	}
	vals := ct.ProjectValues(rec, nil)
	num, ok := vals[0].(json.Number)
	if !ok {
		t.Fatalf("ProjectValues[0] = %#v (%T), want json.Number (a float64 loses precision, a string loses the type)", vals[0], vals[0])
	}
	if string(num) != "123456789012345678901" {
		t.Fatalf("ProjectValues[0] = %q, want the exact source literal", string(num))
	}
}

func TestProjectValues_MissingIsDistinctFromNull(t *testing.T) {
	records := []map[string]any{
		{"a": json.Number("1"), "b": "x"},
		{"a": nil}, // b absent, a explicitly null
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{}, cm)
	if err != nil {
		t.Fatalf("CompileTransform error = %v, want nil", err)
	}
	vals := ct.ProjectValues(records[1], nil)
	byPath := map[string]any{}
	for i, c := range ct.Columns() {
		byPath[c.Path] = vals[i]
	}
	if IsMissing(byPath["a"]) {
		t.Fatalf("a (present, JSON null) reported as Missing; missing and null must stay distinguishable")
	}
	if byPath["a"] != nil {
		t.Fatalf("a = %#v, want untyped nil for an explicit JSON null", byPath["a"])
	}
	if !IsMissing(byPath["b"]) {
		t.Fatalf("b = %#v, want Missing (the path resolves to no value at all)", byPath["b"])
	}
	if IsMissing(nil) || IsMissing("") || IsMissing(false) || IsMissing(json.Number("0")) {
		t.Fatalf("IsMissing must be true only for the Missing sentinel, never for a real value")
	}
}

func TestProjectValues_ReusesTheSuppliedBuffer(t *testing.T) {
	records := []map[string]any{{"a": json.Number("1"), "b": "x"}}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

	ct, err := CompileTransform(Transform{}, cm)
	if err != nil {
		t.Fatalf("CompileTransform error = %v, want nil", err)
	}
	if ct.Len() != len(cm.Columns) {
		t.Fatalf("Len() = %d, want %d", ct.Len(), len(cm.Columns))
	}
	buf := make([]any, ct.Len())
	got := ct.ProjectValues(records[0], buf)
	if len(got) != len(buf) {
		t.Fatalf("len(ProjectValues) = %d, want %d", len(got), len(buf))
	}
	if &got[0] != &buf[0] {
		t.Fatalf("ProjectValues allocated a new slice; it must fill the supplied buffer (Export calls it once per record)")
	}
	// A too-small buffer must still work, by allocating.
	if grown := ct.ProjectValues(records[0], nil); len(grown) != ct.Len() {
		t.Fatalf("ProjectValues(nil buffer) len = %d, want %d", len(grown), ct.Len())
	}
}
