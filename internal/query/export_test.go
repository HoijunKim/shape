package query

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// exportCols builds the minimal []Column an encoder needs: encoders key on
// Column.Path (the unique dotted path), never Column.Name -- base columns are
// LEAF-named, so `user.id` and `order.id` are both named "id" and only their
// paths tell them apart.
func exportCols(paths ...string) []Column {
	cols := make([]Column, len(paths))
	for i, p := range paths {
		cols[i] = Column{Path: p, Name: p, Index: i}
	}
	return cols
}

// --- jsonEncoder: object key order ------------------------------------------

// TestJSONEncoder_PreservesColumnOrder pins the reason the encoder hand-rolls
// its object framing instead of marshalling a map: Go marshals map keys in
// SORTED order, which would silently reorder the columns the user chose.
//
// Mutation that must break it: build a map[string]any of the row and
// json.Marshal it -- the keys come back "alpha","zeta" and this fails.
func TestJSONEncoder_PreservesColumnOrder(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("zeta", "alpha"), false)
	if err := enc.Encode(0, []any{json.Number("1"), json.Number("2")}); err != nil {
		t.Fatalf("Encode error = %v, want nil", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	got := strings.TrimSpace(buf.String())
	want := `{"zeta":1,"alpha":2}`
	if got != want {
		t.Fatalf("output = %s, want %s (declared column order, not sorted keys)", got, want)
	}
}

// --- jsonEncoder: missing vs null -------------------------------------------

func TestJSONEncoder_MissingOmitsKeyNullWritesNull(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("a", "b", "c"), false)
	if err := enc.Encode(0, []any{json.Number("1"), nil, Missing}); err != nil {
		t.Fatalf("Encode error = %v, want nil", err)
	}
	got := strings.TrimSpace(buf.String())
	want := `{"a":1,"b":null}`
	if got != want {
		t.Fatalf("output = %s, want %s (an absent path omits its key; an explicit null writes null)", got, want)
	}
}

// --- jsonEncoder: exact numeric literals ------------------------------------

func TestJSONEncoder_WritesExactNumericLiterals(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("big", "precise"), false)
	if err := enc.Encode(0, []any{json.Number("123456789012345678901"), json.Number("0.1000000000000000055511151231257827")}); err != nil {
		t.Fatalf("Encode error = %v, want nil", err)
	}
	got := strings.TrimSpace(buf.String())
	want := `{"big":123456789012345678901,"precise":0.1000000000000000055511151231257827}`
	if got != want {
		t.Fatalf("output = %s, want %s (a float64 round-trip would print 1.2345678901234568e+20)", got, want)
	}
}

// --- jsonEncoder: non-finite floats -----------------------------------------

// TestJSONEncoder_NonFiniteFloatsBecomeNull covers the failure mode that would
// otherwise abort a multi-GB export mid-file: encoding/json REFUSES NaN/±Inf
// ("json: unsupported value: NaN"), so one bad float 900k rows in would kill
// the whole run.
//
// Mutation that must break it: drop the jsonSafe call in Encode -- Encode then
// returns an error and both assertions fail.
func TestJSONEncoder_NonFiniteFloatsBecomeNull(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("top", "nested"), false)
	nested := map[string]any{"xs": []any{json.Number("1"), math.Inf(1), math.NaN()}}
	if err := enc.Encode(0, []any{math.NaN(), nested}); err != nil {
		t.Fatalf("Encode error = %v, want nil (a non-finite float must not fail the export)", err)
	}
	got := strings.TrimSpace(buf.String())
	want := `{"top":null,"nested":{"xs":[1,null,null]}}`
	if got != want {
		t.Fatalf("output = %s, want %s", got, want)
	}
	// The source value must not have been mutated in place.
	xs := nested["xs"].([]any)
	if f, ok := xs[1].(float64); !ok || !math.IsInf(f, 1) {
		t.Fatalf("jsonSafe mutated the source record: xs[1] = %#v, want +Inf", xs[1])
	}
}

func TestJSONSafe_ReturnsInputUnchangedWhenNothingToRewrite(t *testing.T) {
	v := map[string]any{"a": json.Number("1"), "b": []any{"x", nil, true}}
	got := jsonSafe(v)
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("jsonSafe returned %T, want map[string]any", got)
	}
	if len(gotMap) != len(v) {
		t.Fatalf("jsonSafe rewrote a clean value: %#v", gotMap)
	}
	// Same backing map: a clean value must not be copied on the hot path.
	v["sentinel"] = true
	if _, ok := gotMap["sentinel"]; !ok {
		t.Fatalf("jsonSafe copied a value that needed no rewriting")
	}
}

// --- jsonEncoder: no HTML escaping ------------------------------------------

func TestJSONEncoder_DoesNotHTMLEscape(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("html"), false)
	if err := enc.Encode(0, []any{`<a href="x">&</a>`}); err != nil {
		t.Fatalf("Encode error = %v, want nil", err)
	}
	got := strings.TrimSpace(buf.String())
	want := `{"html":"<a href=\"x\">&</a>"}`
	if got != want {
		t.Fatalf("output = %s, want %s (a data export must not turn < into \\u003c)", got, want)
	}
}

// --- jsonEncoder: array framing ---------------------------------------------

func TestJSONEncoder_ArrayModeZeroRowsWritesEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("a"), true)
	if err := enc.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if got := buf.String(); got != "[]\n" {
		t.Fatalf("zero-row array export = %q, want %q (an empty file is not valid JSON)", got, "[]\n")
	}
}

func TestJSONEncoder_ArrayModeIsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("a", "b"), true)
	for i := 0; i < 2; i++ {
		if err := enc.Encode(int64(i), []any{json.Number("1"), "x"}); err != nil {
			t.Fatalf("Encode error = %v, want nil", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("array export is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(out) != 2 {
		t.Fatalf("decoded %d elements, want 2", len(out))
	}
}

func TestJSONEncoder_LineModeWritesOneObjectPerLine(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("a"), false)
	for i := 0; i < 2; i++ {
		if err := enc.Encode(int64(i), []any{json.Number("1")}); err != nil {
			t.Fatalf("Encode error = %v, want nil", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if got := buf.String(); got != "{\"a\":1}\n{\"a\":1}\n" {
		t.Fatalf("ndjson export = %q, want two newline-terminated objects and no wrapper", got)
	}
}

func TestJSONEncoder_LineModeZeroRowsWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("a"), false)
	if err := enc.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("zero-row ndjson export = %q, want an empty file", buf.String())
	}
}

// --- jsonEncoder: container fidelity ----------------------------------------

// TestJSONEncoder_WritesContainersInFull is Task 1's promise re-asserted at the
// encoder boundary: nothing between the record and the file may apply a
// previewCap-style truncation.
func TestJSONEncoder_WritesContainersInFull(t *testing.T) {
	_, meta := bigNestedRecord()
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf, exportCols("meta"), false)
	if err := enc.Encode(0, []any{meta}); err != nil {
		t.Fatalf("Encode error = %v, want nil", err)
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(out["meta"]) != len(meta) {
		t.Fatalf("exported object has %d keys, want %d", len(out["meta"]), len(meta))
	}
	if buf.Len() <= previewCap {
		t.Fatalf("fixture too small to discriminate: %d bytes <= previewCap %d", buf.Len(), previewCap)
	}
}
