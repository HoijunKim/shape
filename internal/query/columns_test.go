package query

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hoijunkim/shape/internal/profile"
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

// --- toCell: non-finite raw float64 (I1 fix) --------------------------------
//
// Parquet DOUBLE / SQLite REAL columns can hold NaN/+Inf/-Inf, which arrive
// at toCell as a raw float64 (readers.ToProfileValue), not json.Number (JSON
// itself has no NaN/Inf literal). Pre-fix, Cell.Num would carry the
// non-finite value straight through, and encoding/json.Marshal ERRORS on a
// non-finite float64 -- the E2 Wails-response crash this guards against.
func TestToCell_FloatNonFinite(t *testing.T) {
	cases := []struct {
		name         string
		v            float64
		wantSentinel string
	}{
		{"nan", math.NaN(), "NaN"},
		{"pos_inf", math.Inf(1), "Inf"},
		{"neg_inf", math.Inf(-1), "-Inf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toCell(tc.v)
			if got.Kind != CellFloat {
				t.Fatalf("Kind = %v, want CellFloat", got.Kind)
			}
			if got.Num != 0 {
				t.Fatalf("Num = %v, want 0 (non-finite must not carry through)", got.Num)
			}
			if got.Str != tc.wantSentinel {
				t.Fatalf("Str = %q, want sentinel %q", got.Str, tc.wantSentinel)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("json.Marshal(cell) error = %v, want nil (this is the boundary bug the fix closes)", err)
			}
			// Round-trip: the marshaled Cell must decode back to the exact
			// same sentinel/kind/num (deterministic across backends).
			var back Cell
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("json.Unmarshal(marshaled cell) error = %v, want nil", err)
			}
			if back != got {
				t.Fatalf("round-tripped cell = %#v, want %#v", back, got)
			}
		})
	}
}

// TestRowSet_MarshalWithNonFiniteCell_Succeeds is the E2-crash regression: a
// RowSet carrying a cell derived from a non-finite float (e.g. a Parquet
// DOUBLE column with NaN) must still marshal successfully end-to-end -- this
// is exactly the shape QueryRows returns over the Wails binding. Against
// pre-fix code (toCell passing the raw NaN/Inf straight into Cell.Num), this
// test fails with an UnsupportedValueError from encoding/json.
func TestRowSet_MarshalWithNonFiniteCell_Succeeds(t *testing.T) {
	rs := RowSet{
		Columns: []Column{{Path: "amount", Name: "amount", Type: "float"}},
		Rows: []Row{
			{Index: 0, Cells: []Cell{toCell(math.NaN())}},
			{Index: 1, Cells: []Cell{toCell(math.Inf(1))}},
			{Index: 2, Cells: []Cell{toCell(math.Inf(-1))}},
			{Index: 3, Cells: []Cell{toCell(2.5)}}, // finite sibling: unaffected
		},
		Total:      4,
		TotalExact: true,
	}
	if _, err := json.Marshal(rs); err != nil {
		t.Fatalf("json.Marshal(RowSet with non-finite cell) error = %v, want nil (E2 Wails-response crash regression)", err)
	}
}

// --- compactJSON: non-finite floats inside a container (I1 fix) ------------
//
// compactJSON backs toCell's object/array preview (Cell.Str). Pre-fix, a
// container nesting a non-finite float64 made the marshal fail and the
// preview silently became "" -- a cross-backend divergence (a parquet nested
// NaN vs the logically-equal JSON would render differently). Post-fix the
// preview must be non-empty and deterministic.
func TestCompactJSON_NonFiniteContainer(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"map", map[string]any{"a": math.NaN(), "b": json.Number("1")}},
		{"array", []any{math.Inf(1), math.Inf(-1), json.Number("2")}},
	}

	for _, tc := range cases {
		v := tc.v
		t.Run(tc.name, func(t *testing.T) {
			got := compactJSON(v)
			if got == "" {
				t.Fatalf("compactJSON(%#v) = \"\", want non-empty (non-finite float must not silently empty the preview)", v)
			}
			// Deterministic: repeated calls yield the same string.
			if again := compactJSON(v); again != got {
				t.Fatalf("compactJSON(%#v) not deterministic: %q vs %q", v, got, again)
			}
			// Result must itself be valid JSON (a caller-facing preview that
			// doesn't parse would be worse than an honest empty string).
			var decoded any
			if err := json.Unmarshal([]byte(got), &decoded); err != nil {
				t.Fatalf("compactJSON(%#v) = %q is not valid JSON: %v", v, got, err)
			}
		})
	}
}

// TestToCell_ContainerWithNonFiniteFloat confirms the fix end-to-end through
// toCell's object/array branch (not just compactJSON directly): the
// resulting Cell.Str preview is non-empty and the Cell itself marshals.
func TestToCell_ContainerWithNonFiniteFloat(t *testing.T) {
	v := map[string]any{"x": math.NaN()}
	got := toCell(v)
	if got.Kind != CellObject {
		t.Fatalf("Kind = %v, want CellObject", got.Kind)
	}
	if got.Str == "" {
		t.Fatalf("Str = \"\", want a non-empty preview containing the sanitized sentinel")
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("json.Marshal(cell) error = %v, want nil", err)
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
	cm := buildColumnModel(disc, prof, nil)

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

// multiSiblingRecord returns a record introducing 8 new sibling top-level
// keys in ONE Observe call -- the COMMON case (a uniform CSV/JSON where
// every column appears in the first record) that
// TestColumnDiscoverer_FirstSeenOrderNotAlphabetical and
// TestBuildColumnModel_OrderIsFirstSeenNotAlphabetical above do NOT cover:
// both of those fixtures introduce at most one brand-new path per record,
// sidestepping the intra-call sibling tie entirely. Keys are listed in a
// deliberately non-alphabetical logical order (a fruit-stand walk order) so
// a test asserting alphabetical Path order cannot be accidentally satisfied
// by coincidence.
func multiSiblingRecord() map[string]any {
	return map[string]any{
		"zebra":  json.Number("1"),
		"mango":  json.Number("2"),
		"apple":  json.Number("3"),
		"kiwi":   json.Number("4"),
		"banana": json.Number("5"),
		"fig":    json.Number("6"),
		"cherry": json.Number("7"),
		"date":   json.Number("8"),
	}
}

// TestBuildColumnModel_MultiNewSiblingOrderIsDeterministic is the
// regression test for the Critical finding: pre-fix, columnDiscoverer.walk
// registered a record's newly-seen sibling paths via a raw
// `for k, cv := range t` over its map[string]any, so when a SINGLE Observe
// call first-saw multiple sibling paths (this fixture: 8 at once), their
// relative registration order depended on Go's randomized map iteration --
// varying across runs of the identical input. The fix sorts the
// newly-discovered paths (bytewise, by dotted path string) before
// registering them, so the order asserted below (alphabetical, since these
// are single-segment top-level paths) must come out identical every time,
// regardless of map iteration order.
//
// Run the whole disc+build pipeline fresh several times (a fresh map
// literal and a fresh columnDiscoverer each time, so each run gets its own
// independent map iteration) and assert every run produces the exact same
// order: this is the "stable across repeated calls" assertion, and it is
// also what makes this test flaky/failing against the pre-fix map-range
// code (see task report for the pre-fix RED run demonstrating this).
func TestBuildColumnModel_MultiNewSiblingOrderIsDeterministic(t *testing.T) {
	want := []string{"apple", "banana", "cherry", "date", "fig", "kiwi", "mango", "zebra"}

	build := func() []string {
		rec := multiSiblingRecord()
		disc, prof := discoverAndProfile([]map[string]any{rec})
		cm := buildColumnModel(disc, prof, nil)
		got := make([]string, len(cm.Columns))
		for i, c := range cm.Columns {
			got[i] = c.Path
		}
		return got
	}

	const runs = 5
	for i := 0; i < runs; i++ {
		got := build()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: Columns paths = %#v, want deterministic sorted order %#v (non-deterministic map-order regression)", i, got, want)
		}
	}
}

// TestBuildColumnModel_SourceOrderHintHonored covers Part B: when a caller
// supplies sourceOrder (the seam a backend with a REAL column order -- CSV
// header, SQLite/Parquet schema -- fills in later tasks), eligible columns
// are ordered by their sourceOrder position; a column absent from
// sourceOrder is placed after, in the deterministic first-seen order from
// Part A (here: alphabetical, since every path is a single-segment
// top-level path discovered in one Observe call).
func TestBuildColumnModel_SourceOrderHintHonored(t *testing.T) {
	rec := multiSiblingRecord()
	disc, prof := discoverAndProfile([]map[string]any{rec})

	// Deliberately non-alphabetical, and omits "date" and "fig" so both the
	// "honor sourceOrder position" and "unmatched columns fall back to
	// deterministic order" behaviors are exercised in one test.
	sourceOrder := []string{"zebra", "kiwi", "apple", "mango", "banana", "cherry"}
	cm := buildColumnModel(disc, prof, sourceOrder)

	var got []string
	for _, c := range cm.Columns {
		got = append(got, c.Path)
	}
	// "date" and "fig" (both absent from sourceOrder) come after the
	// sourceOrder-matched columns, in deterministic (alphabetical) order
	// relative to each other: "date" < "fig".
	want := []string{"zebra", "kiwi", "apple", "mango", "banana", "cherry", "date", "fig"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Columns paths = %#v, want sourceOrder-then-deterministic order %#v", got, want)
	}
}

func TestBuildColumnModel_ElemPathsExcluded(t *testing.T) {
	records := []map[string]any{
		{"tags": []any{"a", "b"}},
		{"tags": []any{"c"}},
	}
	disc, prof := discoverAndProfile(records)
	cm := buildColumnModel(disc, prof, nil)

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
	cm := buildColumnModel(disc, prof, nil)

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
	cm := buildColumnModel(disc, prof, nil)

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
	cm := buildColumnModel(disc, prof, nil)

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
	cm := buildColumnModel(disc, prof, nil)

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
	cm := buildColumnModel(disc, prof, nil)

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
	cm := buildColumnModel(disc, prof, nil)

	if got := cm.resolveCol(-1, map[string]any{}); got != (Cell{Kind: CellMissing}) {
		t.Fatalf("resolveCol(-1, ...) = %#v, want CellMissing", got)
	}
	if got := cm.resolveCol(len(cm.Columns), map[string]any{}); got != (Cell{Kind: CellMissing}) {
		t.Fatalf("resolveCol(len(Columns), ...) = %#v, want CellMissing", got)
	}
}

// --- AllCellKindValues -------------------------------------------------------

// parseCellKindConsts parses filename (a .go source file in this package)
// with go/parser and returns the set of CellKind values declared by any
// `const` spec resolving to type CellKind -- either an explicit
// `CellMissing CellKind = "missing"`, or a later spec in the same const
// block that omits BOTH Type and Values, which Go's implicit-repetition
// shorthand resolves by inheriting the closest preceding spec's Type and
// Values verbatim (https://go.dev/ref/spec#Constant_declarations), e.g.:
//
//	const (
//	    CellMissing CellKind = "missing"
//	    CellNinth                        // inherits type CellKind, value "missing"
//	)
//
// -- by reading the string literal actually assigned to each one (inherited
// or explicit). This makes the const block itself (not a hand-maintained
// copy of it) the source of truth TestAllCellKindValues_CoversEveryKind
// checks against: a ninth CellKind constant added to the block, in EITHER
// style, and forgotten in AllCellKindValues changes what this function
// returns, without anyone having to remember to update a second, unlinked
// list. A spec shape this walk doesn't recognize (anything other than
// *ast.ValueSpec inside a `const` GenDecl, or a repetition spec with no
// preceding spec to inherit from) is a hard test failure, not a silent skip
// -- so an exotic const style this function doesn't yet understand fails
// loudly instead of quietly under-counting.
func parseCellKindConsts(t *testing.T, filename string) map[CellKind]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parser.ParseFile(%q) error = %v", filename, err)
	}

	declared := make(map[CellKind]bool)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}

		// lastType/lastValues track the most recently seen Type/Values
		// within THIS const block, so an implicit-repetition spec (Type ==
		// nil AND Values == nil) can inherit them per Go's const-block
		// semantics, rather than being silently treated as "not a CellKind
		// spec" and dropped.
		var lastType ast.Expr
		var lastValues []ast.Expr
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				t.Fatalf("unexpected const spec shape in %q: %#v (want *ast.ValueSpec)", filename, spec)
			}

			typ, values := vs.Type, vs.Values
			if typ == nil && values == nil {
				if lastType == nil && lastValues == nil {
					t.Fatalf("const spec %v in %q uses implicit repetition with no preceding spec to inherit from", vs.Names, filename)
				}
				typ, values = lastType, lastValues
			}
			lastType, lastValues = typ, values

			typeIdent, ok := typ.(*ast.Ident)
			if !ok || typeIdent.Name != "CellKind" {
				// A Cell-prefixed const that did NOT resolve to an explicit
				// CellKind type is the one shape that could smuggle in a new
				// kind unseen -- e.g. `CellFoo = CellKind("foo")`, whose Type
				// is nil and whose Values are non-nil, so it never reaches the
				// repetition branch above. Hard-fail rather than skip: this
				// guard's whole purpose is that an added-but-unregistered kind
				// cannot pass silently.
				for _, name := range vs.Names {
					if strings.HasPrefix(name.Name, "Cell") {
						t.Fatalf("const %s in %q is Cell-prefixed but has no explicit CellKind type; "+
							"declare it as `%s CellKind = \"...\"` so this exhaustiveness guard can see it",
							name.Name, filename, name.Name)
					}
				}
				continue // not a `<name> CellKind = "..."` spec (explicit or inherited)
			}
			for i, name := range vs.Names {
				if i >= len(values) {
					t.Fatalf("CellKind const %s in %q has no value expression", name.Name, filename)
				}
				lit, ok := values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("CellKind const %s in %q is not a string literal: %#v", name.Name, filename, values[i])
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("CellKind const %s in %q: unquote %s: %v", name.Name, filename, lit.Value, err)
				}
				declared[CellKind(val)] = true
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("parsed 0 CellKind consts from %q -- the AST walk likely no longer matches the const block's shape", filename)
	}
	return declared
}

// TestAllCellKindValues_CoversEveryKind is a compile-time-adjacent guard: a
// future ninth CellKind added to the const block (columns.go) but forgotten
// in AllCellKindValues would otherwise silently ship a TS union missing a
// member (Wails' EnumBind generates its TypeScript enum FROM this slice,
// Task 4) -- this test catches that omission at test time instead.
//
// The set it checks against is parsed directly out of columns.go's CellKind
// const block (parseCellKindConsts, via go/parser/go/ast), not a second
// hand-maintained list: a hardcoded `want` map here would only ever go out of
// sync WITH the very omission this test exists to catch, since both the
// const block and a hardcoded want map are edited by hand with no compiler-
// enforced link between them (see the CQ-1 finding this replaces). Parsing
// the real source -- rather than checking a hand-copied list -- is what
// makes an added-but-unregistered CellKind break this test, for every
// const-spec style the columns.go block actually uses today: an explicit
// `Name CellKind = "value"`, the shared-line/multi-name form (`CellA, CellB
// CellKind = "a", "b"`), and Go's implicit-repetition shorthand (a spec
// naming only an identifier, inheriting the preceding spec's type and value
// from the one before it -- see parseCellKindConsts). This walk identifies a
// CellKind spec syntactically, from its (possibly inherited) `Type` field --
// it does not type-check the package, so a spec whose CellKind-ness comes
// only from a value-conversion expression with no `Type` field of its own
// (e.g. `CellFoo = CellKind("foo")`, rather than `CellFoo CellKind =
// "foo"`) is outside what it resolves; columns.go's block doesn't use that
// style. Any const spec shape parseCellKindConsts doesn't recognize at all
// (not a *ast.ValueSpec, or implicit repetition with nothing to inherit
// from) is a hard parse-time failure, not a silent skip.
func TestAllCellKindValues_CoversEveryKind(t *testing.T) {
	declared := parseCellKindConsts(t, "columns.go")

	got := make(map[CellKind]bool, len(AllCellKindValues))
	for _, v := range AllCellKindValues {
		if v.TSName == "" {
			t.Fatalf("AllCellKindValues entry for %q has an empty TSName", v.Value)
		}
		if got[v.Value] {
			t.Fatalf("AllCellKindValues has a duplicate Value entry: %q", v.Value)
		}
		got[v.Value] = true
	}

	// Redundant tripwire derived from the parsed set (not a hardcoded
	// number): a mismatched count alone pinpoints "something's missing or
	// extra" before the per-key loops below identify which.
	if len(got) != len(declared) {
		t.Fatalf("AllCellKindValues has %d distinct entries, want %d (one per CellKind const declared in columns.go)", len(got), len(declared))
	}
	for k := range declared {
		if !got[k] {
			t.Fatalf("AllCellKindValues missing %q (declared as a CellKind const in columns.go but not listed here)", k)
		}
	}
	for k := range got {
		if !declared[k] {
			t.Fatalf("AllCellKindValues has %q, which is not declared as a CellKind const in columns.go", k)
		}
	}
}
