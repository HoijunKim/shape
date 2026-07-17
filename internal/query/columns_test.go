package query

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

// --- parsePath -------------------------------------------------------------

func TestParsePath(t *testing.T) {
	cases := []struct {
		name   string
		dotted string
		want   []Seg
	}{
		{"root", "$", nil},
		{"dotted_keys", "a.b", []Seg{{Key: "a"}, {Key: "b"}}},
		{"elem_wildcard", "user.tags[]", []Seg{{Key: "user"}, {Key: "tags"}, {Elem: true}}},
		{"bracket_dotted_key", `["a.b"]`, []Seg{{Key: "a.b"}}},
		{"mixed_bracket", `x.["a.b"].y`, []Seg{{Key: "x"}, {Key: "a.b"}, {Key: "y"}}},
		{"root_array_elem", "[]", []Seg{{Elem: true}}},
		{"root_array_of_objects", "[].name", []Seg{{Elem: true}, {Key: "name"}}},
		{"double_elem", "a[].b[]", []Seg{{Key: "a"}, {Elem: true}, {Key: "b"}, {Elem: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePath(tc.dotted)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parsePath(%q) = %#v, want %#v", tc.dotted, got, tc.want)
			}
		})
	}
}

// --- resolve -----------------------------------------------------------------

func TestResolve_Root(t *testing.T) {
	record := map[string]any{"a": json.Number("1")}
	got := resolve(record, parsePath("$"))
	if len(got) != 1 {
		t.Fatalf("resolve($) len = %d, want 1", len(got))
	}
	m, ok := got[0].(map[string]any)
	if !ok || !reflect.DeepEqual(m, record) {
		t.Fatalf("resolve($) = %#v, want the record itself", got[0])
	}
}

func TestResolve_ScalarPresent(t *testing.T) {
	record := map[string]any{"a": map[string]any{"b": json.Number("5")}}
	got := resolve(record, parsePath("a.b"))
	want := []any{json.Number("5")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolve(a.b) = %#v, want %#v", got, want)
	}
}

func TestResolve_ScalarMissing(t *testing.T) {
	record := map[string]any{"a": map[string]any{}}
	got := resolve(record, parsePath("a.b"))
	if len(got) != 0 {
		t.Fatalf("resolve(a.b) on absent b = %#v, want empty (0 values)", got)
	}
}

func TestResolve_KeyEntirelyAbsent(t *testing.T) {
	record := map[string]any{"x": json.Number("1")}
	got := resolve(record, parsePath("a.b"))
	if len(got) != 0 {
		t.Fatalf("resolve(a.b) on record without a = %#v, want empty", got)
	}
}

func TestResolve_ScalarNullPresent(t *testing.T) {
	// a present-but-null value must still resolve to exactly one value
	// (nil), distinct from a wholly absent key (0 values) - the CellNull
	// vs CellMissing distinction depends on this.
	record := map[string]any{"a": nil}
	got := resolve(record, parsePath("a"))
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("resolve(a) on null a = %#v, want [nil]", got)
	}
}

func TestResolve_ElemArray(t *testing.T) {
	record := map[string]any{"user": map[string]any{"tags": []any{"x", "y", "z"}}}
	got := resolve(record, parsePath("user.tags[]"))
	want := []any{"x", "y", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolve(user.tags[]) = %#v, want %#v", got, want)
	}
}

func TestResolve_ElemEmptyArray(t *testing.T) {
	record := map[string]any{"user": map[string]any{"tags": []any{}}}
	got := resolve(record, parsePath("user.tags[]"))
	if len(got) != 0 {
		t.Fatalf("resolve(user.tags[]) on empty array = %#v, want empty", got)
	}
}

func TestResolve_ElemNotArray(t *testing.T) {
	// the path expects an array (Elem seg) but the actual value is a
	// scalar: 0 values, not a panic.
	record := map[string]any{"user": map[string]any{"tags": "not-an-array"}}
	got := resolve(record, parsePath("user.tags[]"))
	if len(got) != 0 {
		t.Fatalf("resolve(user.tags[]) on scalar tags = %#v, want empty", got)
	}
}

func TestResolve_ElemThenKey_ArrayMembership(t *testing.T) {
	record := map[string]any{
		"items": []any{
			map[string]any{"id": json.Number("1")},
			map[string]any{"id": json.Number("2")},
			map[string]any{"other": "no id here"},
		},
	}
	got := resolve(record, parsePath("items[].id"))
	want := []any{json.Number("1"), json.Number("2")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolve(items[].id) = %#v, want %#v", got, want)
	}
}

// --- toCell ------------------------------------------------------------------

func TestToCell_Null(t *testing.T) {
	got := toCell(nil)
	want := Cell{Kind: CellNull}
	if got != want {
		t.Fatalf("toCell(nil) = %#v, want %#v", got, want)
	}
}

func TestToCell_Bool(t *testing.T) {
	got := toCell(true)
	want := Cell{Kind: CellBool, Bool: true}
	if got != want {
		t.Fatalf("toCell(true) = %#v, want %#v", got, want)
	}
	got = toCell(false)
	want = Cell{Kind: CellBool, Bool: false}
	if got != want {
		t.Fatalf("toCell(false) = %#v, want %#v", got, want)
	}
}

func TestToCell_Int(t *testing.T) {
	got := toCell(json.Number("42"))
	want := Cell{Kind: CellInt, Num: 42, Str: "42"}
	if got != want {
		t.Fatalf("toCell(42) = %#v, want %#v", got, want)
	}
}

func TestToCell_IntExactLiteralRoundTrip(t *testing.T) {
	// Beyond float64's exact-integer range (2^53): Num necessarily loses
	// precision, but Str must carry the exact literal so callers can
	// round-trip big ints without float corruption.
	const lit = "9223372036854775807" // math.MaxInt64
	got := toCell(json.Number(lit))
	if got.Kind != CellInt {
		t.Fatalf("Kind = %v, want CellInt", got.Kind)
	}
	if got.Str != lit {
		t.Fatalf("Str = %q, want exact literal %q", got.Str, lit)
	}
	if got.Num == 0 {
		t.Fatalf("Num = 0, want a parsed (if imprecise) approximation")
	}
}

func TestToCell_Float(t *testing.T) {
	got := toCell(json.Number("3.14"))
	want := Cell{Kind: CellFloat, Num: 3.14, Str: "3.14"}
	if got != want {
		t.Fatalf("toCell(3.14) = %#v, want %#v", got, want)
	}
}

func TestToCell_FloatPreciseDecimalRoundTrip(t *testing.T) {
	const lit = "0.100000000000000005" // more precision than float64 holds exactly
	got := toCell(json.Number(lit))
	if got.Kind != CellFloat {
		t.Fatalf("Kind = %v, want CellFloat", got.Kind)
	}
	if got.Str != lit {
		t.Fatalf("Str = %q, want exact literal %q", got.Str, lit)
	}
}

func TestToCell_FloatRawFloat64(t *testing.T) {
	// float64 fallback path (a record decoded without json.Decoder.UseNumber).
	got := toCell(2.5)
	want := Cell{Kind: CellFloat, Num: 2.5, Str: "2.5"}
	if got != want {
		t.Fatalf("toCell(2.5) = %#v, want %#v", got, want)
	}
}

func TestToCell_String(t *testing.T) {
	got := toCell("hello")
	want := Cell{Kind: CellString, Str: "hello"}
	if got != want {
		t.Fatalf("toCell(\"hello\") = %#v, want %#v", got, want)
	}
}

func TestToCell_Object(t *testing.T) {
	v := map[string]any{"x": json.Number("1"), "y": json.Number("2")}
	got := toCell(v)
	if got.Kind != CellObject {
		t.Fatalf("Kind = %v, want CellObject", got.Kind)
	}
	if got.Count != 2 {
		t.Fatalf("Count = %d, want 2", got.Count)
	}
	if got.HasMore {
		t.Fatalf("HasMore = true, want false (small object, no truncation)")
	}
	const want = `{"x":1,"y":2}` // encoding/json marshals map[string]any keys sorted
	if got.Str != want {
		t.Fatalf("Str = %q, want %q", got.Str, want)
	}
}

func TestToCell_Array(t *testing.T) {
	v := []any{json.Number("1"), json.Number("2"), json.Number("3")}
	got := toCell(v)
	if got.Kind != CellArray {
		t.Fatalf("Kind = %v, want CellArray", got.Kind)
	}
	if got.Count != 3 {
		t.Fatalf("Count = %d, want 3", got.Count)
	}
	if got.HasMore {
		t.Fatalf("HasMore = true, want false (small array, no truncation)")
	}
	const want = "[1,2,3]"
	if got.Str != want {
		t.Fatalf("Str = %q, want %q", got.Str, want)
	}
}

func TestToCell_EmptyArray(t *testing.T) {
	got := toCell([]any{})
	if got.Kind != CellArray || got.Count != 0 || got.HasMore || got.Str != "[]" {
		t.Fatalf("toCell([]any{}) = %#v, want empty CellArray with Str=\"[]\"", got)
	}
}

func TestToCell_ContainerPreviewTruncation(t *testing.T) {
	elems := make([]any, 100)
	for i := range elems {
		elems[i] = json.Number("123456789") // "123456789," x100 >> previewCap
	}
	full := compactJSON(elems)
	if len(full) <= previewCap {
		t.Fatalf("fixture too small to exercise truncation: %d bytes", len(full))
	}

	got := toCell(elems)
	if got.Kind != CellArray {
		t.Fatalf("Kind = %v, want CellArray", got.Kind)
	}
	if !got.HasMore {
		t.Fatalf("HasMore = false, want true (preview truncated)")
	}
	if got.Count != len(elems) {
		t.Fatalf("Count = %d, want %d (full element count, unaffected by preview truncation)", got.Count, len(elems))
	}
	wantPreview := string([]rune(full)[:previewCap])
	if got.Str != wantPreview {
		t.Fatalf("Str = %q, want %q", got.Str, wantPreview)
	}
	if n := len([]rune(got.Str)); n != previewCap {
		t.Fatalf("preview rune length = %d, want previewCap %d", n, previewCap)
	}
}

func TestTruncate(t *testing.T) {
	if s, more := truncate("short", 200); s != "short" || more {
		t.Fatalf("truncate(short) = (%q, %v), want (\"short\", false)", s, more)
	}
	s, more := truncate("abcdefghij", 5)
	if s != "abcde" || !more {
		t.Fatalf("truncate(len10, 5) = (%q, %v), want (\"abcde\", true)", s, more)
	}
	// exactly at the cap: no truncation.
	if s, more := truncate("abcde", 5); s != "abcde" || more {
		t.Fatalf("truncate(len5, 5) = (%q, %v), want (\"abcde\", false)", s, more)
	}
}

func TestToCell_EmptyResolveIsCallerMissing(t *testing.T) {
	// resolve() returning zero values (path absent) is distinct from a
	// present-but-null value (see TestResolve_ScalarNullPresent). toCell
	// only classifies an actual value; the caller maps an empty resolve()
	// set to CellMissing itself. This test documents and verifies that
	// composition, matching spec §3's "empty resolve set -> CellMissing".
	record := map[string]any{"a": map[string]any{}}
	values := resolve(record, parsePath("a.b"))
	if len(values) != 0 {
		t.Fatalf("resolve(a.b) = %#v, want empty (missing path)", values)
	}

	cell := Cell{Kind: CellMissing}
	if len(values) > 0 {
		cell = toCell(values[0])
	}
	if cell != (Cell{Kind: CellMissing}) {
		t.Fatalf("caller-composed cell = %#v, want CellMissing", cell)
	}
}

// --- columnDiscoverer --------------------------------------------------------

func TestColumnDiscoverer_FirstSeenOrderNotAlphabetical(t *testing.T) {
	disc := newColumnDiscoverer()
	// Each record below introduces exactly one brand-new top-level key, so
	// relative discovery order is deterministic: Go's randomized map
	// iteration only ever matters between two paths BOTH first seen in the
	// same Observe call, which never happens here.
	disc.Observe(map[string]any{"zebra": json.Number("1")})
	disc.Observe(map[string]any{"zebra": json.Number("2"), "apple": json.Number("3")})
	disc.Observe(map[string]any{"zebra": json.Number("4"), "apple": json.Number("5"), "mango": json.Number("6")})

	want := []string{"zebra", "apple", "mango"}
	if !reflect.DeepEqual(disc.order, want) {
		t.Fatalf("disc.order = %#v, want %#v (first-seen order)", disc.order, want)
	}
	alpha := append([]string(nil), want...)
	sort.Strings(alpha)
	if reflect.DeepEqual(disc.order, alpha) {
		t.Fatalf("disc.order coincides with alphabetical order %#v; fixture does not discriminate", alpha)
	}
}

func TestColumnDiscoverer_NestedPathsRegisterInWalkOrder(t *testing.T) {
	disc := newColumnDiscoverer()
	disc.Observe(map[string]any{"user": map[string]any{"name": "Alice"}})

	want := []string{"user", "user.name"}
	if !reflect.DeepEqual(disc.order, want) {
		t.Fatalf("disc.order = %#v, want %#v", disc.order, want)
	}
}

func TestColumnDiscoverer_DedupesRepeatedPaths(t *testing.T) {
	disc := newColumnDiscoverer()
	rec := map[string]any{"a": json.Number("1")}
	disc.Observe(rec)
	disc.Observe(rec)
	disc.Observe(rec)

	want := []string{"a"}
	if !reflect.DeepEqual(disc.order, want) {
		t.Fatalf("disc.order = %#v, want %#v (repeat observation must not duplicate)", disc.order, want)
	}
}

func TestColumnDiscoverer_ArrayPaths(t *testing.T) {
	disc := newColumnDiscoverer()
	disc.Observe(map[string]any{"tags": []any{"a", "b"}})

	want := []string{"tags", "tags[]"}
	if !reflect.DeepEqual(disc.order, want) {
		t.Fatalf("disc.order = %#v, want %#v", disc.order, want)
	}
}

// --- buildColumnModel --------------------------------------------------------

// discoverAndProfile feeds records through both a columnDiscoverer and a
// profile.Profiler (as OpenSource's single ingest pass would) and returns
// the pair buildColumnModel needs.
func discoverAndProfile(records []map[string]any) (*columnDiscoverer, profile.ProfileResult) {
	disc := newColumnDiscoverer()
	prof := profile.NewProfiler()
	for _, r := range records {
		disc.Observe(r)
		prof.AddRecord(r)
	}
	return disc, prof.Result()
}

func TestBuildColumnModel_OrderIsFirstSeenNotAlphabetical(t *testing.T) {
	records := []map[string]any{
		{"zebra": json.Number("1")},
		{"zebra": json.Number("2"), "apple": json.Number("3")},
		{"zebra": json.Number("4"), "apple": json.Number("5"), "mango": json.Number("6")},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof)

	var gotPaths []string
	for _, c := range cm.Columns {
		gotPaths = append(gotPaths, c.Path)
	}
	want := []string{"zebra", "apple", "mango"}
	if !reflect.DeepEqual(gotPaths, want) {
		t.Fatalf("Columns paths = %#v, want first-seen order %#v", gotPaths, want)
	}
	for i, c := range cm.Columns {
		if c.Index != i {
			t.Fatalf("Columns[%d].Index = %d, want %d", i, c.Index, i)
		}
	}
}

func TestBuildColumnModel_ElemPathsExcluded(t *testing.T) {
	records := []map[string]any{
		{"tags": []any{"a", "b"}},
		{"tags": []any{"c"}},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof)

	if _, ok := cm.byPath["tags[]"]; ok {
		t.Fatalf("byPath contains \"tags[]\", want Elem paths excluded from columns")
	}
	i, ok := cm.byPath["tags"]
	if !ok {
		t.Fatalf("byPath missing \"tags\" (array container column should be kept)")
	}
	col := cm.Columns[i]
	if col.Type != "array" {
		t.Fatalf("tags column Type = %q, want %q", col.Type, "array")
	}
	if !col.Container {
		t.Fatalf("tags column Container = false, want true")
	}
}

func TestBuildColumnModel_PureInteriorObjectDroppedDriftKept(t *testing.T) {
	records := []map[string]any{
		{
			"user": map[string]any{"name": "Alice"},
			"meta": map[string]any{"note": "hello"},
		},
		{
			"user": map[string]any{"name": "Bob"},
			"meta": "just-a-string",
		},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof)

	if _, ok := cm.byPath["user"]; ok {
		t.Fatalf("byPath contains \"user\": pure interior object (always object, has deeper column user.name) should be dropped")
	}
	nameIdx, ok := cm.byPath["user.name"]
	if !ok {
		t.Fatalf("byPath missing \"user.name\": leaf column under a dropped interior object must still be kept")
	}
	if got := cm.Columns[nameIdx].Name; got != "name" {
		t.Fatalf("user.name column Name = %q, want %q", got, "name")
	}

	metaIdx, ok := cm.byPath["meta"]
	if !ok {
		t.Fatalf("byPath missing \"meta\": a drifting (sometimes object, sometimes string) path must be kept")
	}
	metaCol := cm.Columns[metaIdx]
	if metaCol.Type != "mixed" {
		t.Fatalf("meta column Type = %q, want %q (drift)", metaCol.Type, "mixed")
	}
	if metaCol.Name != "meta" {
		t.Fatalf("meta column Name = %q, want %q", metaCol.Name, "meta")
	}

	if _, ok := cm.byPath["meta.note"]; !ok {
		t.Fatalf("byPath missing \"meta.note\": leaf under the drifting meta path must still be its own column")
	}

	// Render the drift column's cells for both records: object occurrence
	// previews as CellObject, scalar occurrence as CellString -- drift is
	// shown, not hidden.
	c0 := cm.resolveCol(metaIdx, records[0])
	if c0.Kind != CellObject {
		t.Fatalf("resolveCol(meta, records[0]).Kind = %v, want CellObject", c0.Kind)
	}
	c1 := cm.resolveCol(metaIdx, records[1])
	if c1.Kind != CellString || c1.Str != "just-a-string" {
		t.Fatalf("resolveCol(meta, records[1]) = %#v, want CellString %q", c1, "just-a-string")
	}
}

func TestBuildColumnModel_TypeNullablePresenceDistinctFromFieldProfile(t *testing.T) {
	records := []map[string]any{
		{"name": "Alice", "age": json.Number("30")},
		{"name": "Bob", "age": nil},
		{"name": "Alice"}, // age entirely absent this record
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof)

	nameIdx, ok := cm.byPath["name"]
	if !ok {
		t.Fatalf("byPath missing \"name\"")
	}
	name := cm.Columns[nameIdx]
	if name.Type != "string" {
		t.Fatalf("name.Type = %q, want %q", name.Type, "string")
	}
	if name.Nullable {
		t.Fatalf("name.Nullable = true, want false (never null)")
	}
	if name.Presence != 1.0 {
		t.Fatalf("name.Presence = %v, want 1.0 (present in every record)", name.Presence)
	}
	if name.Distinct != 2 {
		t.Fatalf("name.Distinct = %d, want 2 (\"Alice\", \"Bob\")", name.Distinct)
	}

	ageIdx, ok := cm.byPath["age"]
	if !ok {
		t.Fatalf("byPath missing \"age\"")
	}
	age := cm.Columns[ageIdx]
	if age.Type != "int" {
		t.Fatalf("age.Type = %q, want %q", age.Type, "int")
	}
	if !age.Nullable {
		t.Fatalf("age.Nullable = false, want true (null in record[1])")
	}
	const wantPresence = 2.0 / 3.0
	if age.Presence != wantPresence {
		t.Fatalf("age.Presence = %v, want %v (present in 2 of 3 records)", age.Presence, wantPresence)
	}
	if age.Distinct != 1 {
		t.Fatalf("age.Distinct = %d, want 1 (only one non-null value: 30)", age.Distinct)
	}
}

func TestBuildColumnModel_MaxColumnsCap(t *testing.T) {
	const n = 520 // > MaxColumns(512), keeps the test fast while proving the cap
	colName := func(k int) string { return fmt.Sprintf("col%03d", k) }

	// Record i contains {col_k : k >= i}, so col_k's presence is
	// (k+1)/n -- STRICTLY increasing with k, no ties -- and col_k is first
	// discovered at record k (record 0 introduces col0..col{n-1} at once,
	// but every later record only reduces the set, so no NEW path is ever
	// introduced after record 0; the presence gradient alone drives which
	// columns the cap keeps).
	records := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		rec := make(map[string]any, n-i)
		for k := i; k < n; k++ {
			rec[colName(k)] = true
		}
		records[i] = rec
	}

	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof)

	if cm.TotalPaths != n {
		t.Fatalf("TotalPaths = %d, want %d", cm.TotalPaths, n)
	}
	if !cm.Truncated {
		t.Fatalf("Truncated = false, want true (%d eligible columns > MaxColumns %d)", n, MaxColumns)
	}
	if len(cm.Columns) != MaxColumns {
		t.Fatalf("len(Columns) = %d, want MaxColumns %d", len(cm.Columns), MaxColumns)
	}

	// The n-MaxColumns lowest-presence columns (col000..col007, the first
	// n-MaxColumns by k) must be dropped; the MaxColumns highest-presence
	// columns (col008..col519) must be kept.
	dropped := n - MaxColumns // 8
	for k := 0; k < dropped; k++ {
		if _, ok := cm.byPath[colName(k)]; ok {
			t.Fatalf("byPath contains %q, want dropped (lowest presence, %d/%d)", colName(k), k+1, n)
		}
	}
	for k := dropped; k < n; k++ {
		if _, ok := cm.byPath[colName(k)]; !ok {
			t.Fatalf("byPath missing %q, want kept (presence %d/%d, among the top %d)", colName(k), k+1, n, MaxColumns)
		}
	}
}

// --- resolveCol --------------------------------------------------------------

func TestResolveCol_AlignedCells(t *testing.T) {
	records := []map[string]any{
		{"a": json.Number("1"), "b": "x"},
		{"a": json.Number("2"), "b": "y"},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof)

	aIdx, ok := cm.byPath["a"]
	if !ok {
		t.Fatalf("byPath missing \"a\"")
	}
	bIdx, ok := cm.byPath["b"]
	if !ok {
		t.Fatalf("byPath missing \"b\"")
	}
	if aIdx == bIdx {
		t.Fatalf("column indices for \"a\" and \"b\" collide: %d", aIdx)
	}

	got := cm.resolveCol(aIdx, records[0])
	want := Cell{Kind: CellInt, Num: 1, Str: "1"}
	if got != want {
		t.Fatalf("resolveCol(a, records[0]) = %#v, want %#v", got, want)
	}
	got = cm.resolveCol(bIdx, records[0])
	want = Cell{Kind: CellString, Str: "x"}
	if got != want {
		t.Fatalf("resolveCol(b, records[0]) = %#v, want %#v", got, want)
	}

	got = cm.resolveCol(aIdx, records[1])
	want = Cell{Kind: CellInt, Num: 2, Str: "2"}
	if got != want {
		t.Fatalf("resolveCol(a, records[1]) = %#v, want %#v", got, want)
	}
	got = cm.resolveCol(bIdx, records[1])
	want = Cell{Kind: CellString, Str: "y"}
	if got != want {
		t.Fatalf("resolveCol(b, records[1]) = %#v, want %#v", got, want)
	}
}

func TestResolveCol_MissingIsCellMissing(t *testing.T) {
	records := []map[string]any{
		{"a": json.Number("1"), "b": "x"},
		{"a": json.Number("2")}, // "b" absent this record
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof)

	bIdx, ok := cm.byPath["b"]
	if !ok {
		t.Fatalf("byPath missing \"b\"")
	}
	got := cm.resolveCol(bIdx, records[1])
	want := Cell{Kind: CellMissing}
	if got != want {
		t.Fatalf("resolveCol(b, records[1]) = %#v, want %#v (absent path)", got, want)
	}
}

func TestResolveCol_OutOfRangeIndexIsCellMissing(t *testing.T) {
	disc, prof := discoverAndProfile([]map[string]any{{"a": json.Number("1")}})
	cm := buildColumnModel(disc, prof)

	if got := cm.resolveCol(-1, map[string]any{}); got != (Cell{Kind: CellMissing}) {
		t.Fatalf("resolveCol(-1, ...) = %#v, want CellMissing", got)
	}
	if got := cm.resolveCol(len(cm.Columns), map[string]any{}); got != (Cell{Kind: CellMissing}) {
		t.Fatalf("resolveCol(len(Columns), ...) = %#v, want CellMissing", got)
	}
}
