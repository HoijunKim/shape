package query

import (
	"encoding/json"
	"reflect"
	"testing"
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
