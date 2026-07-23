package query

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

// --- fixtures ---------------------------------------------------------------

// getCellRecords returns two records exercising every GetCell case that JSON-
// shaped backends (mem/rescan) can represent: record 0 carries a "detail"
// OBJECT whose compact JSON deliberately exceeds previewCap (so a preview-
// returning GetCell would be caught truncating it), a scalar "name", and an
// EXPLICIT null "nick"; record 1 has NO "detail" (the missing-path case).
func getCellRecords() []map[string]any {
	detail := map[string]any{}
	for i := 0; i < 20; i++ {
		detail[fmt.Sprintf("field%02d", i)] = fmt.Sprintf("value-string-%02d", i)
	}
	return []map[string]any{
		{"name": "alice", "nick": nil, "detail": detail},
		{"name": "bob", "nick": "bobby"},
	}
}

// --- container: the full, untruncated value ---------------------------------

func TestGetCell_ContainerReturnsFullValue_MemAndRescan(t *testing.T) {
	maps := getCellRecords()
	segs := parsePath("detail")

	// Precondition: the preview WOULD truncate -- compact JSON exceeds
	// previewCap. Without this the container test proves nothing (a short
	// object is byte-identical whether previewed or not).
	full := compactJSON(maps[0]["detail"])
	if len([]rune(full)) <= previewCap {
		t.Fatalf("fixture detail compact JSON is %d runes, need > previewCap=%d", len([]rune(full)), previewCap)
	}

	mb, _ := newTestMemBackend(t, maps)
	rb, _, _ := newTestRescanBackend(t, maps, 100, 1000)
	for _, tc := range []struct {
		name string
		b    Backend
	}{{"mem", mb}, {"rescan", rb}} {
		raw, found, err := tc.b.GetCell(context.Background(), 0, segs)
		if err != nil {
			t.Fatalf("%s GetCell err = %v, want nil", tc.name, err)
		}
		if !found {
			t.Fatalf("%s found = false, want true", tc.name)
		}
		// A truncated preview is a JSON STRING, not an object: Unmarshal into a
		// map fails, and even if it parsed it would not carry all 20 keys. This
		// is the mutation guard for "GetCell returns toCell(v).Str".
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s GetCell returned non-object JSON %q: %v (a truncated preview would land here)", tc.name, raw, err)
		}
		if len(got) != 20 {
			t.Fatalf("%s GetCell returned %d keys, want 20 (the full, untruncated object)", tc.name, len(got))
		}
	}
}

// --- scalar path across all four backends -----------------------------------

func TestGetCell_ScalarPath_AllBackends(t *testing.T) {
	segs := parsePath("name")
	for _, tc := range []struct {
		name  string
		b     Backend
		index int64
		want  string
	}{
		{"mem", func() Backend { b, _ := newTestMemBackend(t, fixtureRecords()); return b }(), 0, `"alice"`},
		{"rescan", func() Backend { b, _, _ := newTestRescanBackend(t, fixtureRecords(), 100, 1000); return b }(), 3, `"dave"`},
		{"sql", newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", sqlNameParityRows()), 0, `"alice"`},
		{"parquet", newTestParquetBackend(t, parquetNameParityRows(), 4), 3, `"dave"`},
	} {
		raw, found, err := tc.b.GetCell(context.Background(), tc.index, segs)
		if err != nil {
			t.Fatalf("%s GetCell err = %v, want nil", tc.name, err)
		}
		if !found {
			t.Fatalf("%s found = false, want true", tc.name)
		}
		if string(raw) != tc.want {
			t.Fatalf("%s GetCell = %s, want %s", tc.name, raw, tc.want)
		}
	}
}

// --- missing vs present-null (the found flag) -------------------------------

func TestGetCell_MissingVsNull_MemAndRescan(t *testing.T) {
	maps := getCellRecords()
	mb, _ := newTestMemBackend(t, maps)
	rb, _, _ := newTestRescanBackend(t, maps, 100, 1000)
	for _, tc := range []struct {
		name string
		b    Backend
	}{{"mem", mb}, {"rescan", rb}} {
		// record 1 has no "detail": found=false, still JSON null.
		raw, found, err := tc.b.GetCell(context.Background(), 1, parsePath("detail"))
		if err != nil {
			t.Fatalf("%s missing GetCell err = %v", tc.name, err)
		}
		if found {
			t.Fatalf("%s missing path: found = true, want false", tc.name)
		}
		if string(raw) != "null" {
			t.Fatalf("%s missing path: raw = %s, want null", tc.name, raw)
		}
		// record 0 has an EXPLICIT null "nick": found=true, JSON null -- the
		// found flag is the ONLY thing distinguishing this from missing.
		raw, found, err = tc.b.GetCell(context.Background(), 0, parsePath("nick"))
		if err != nil {
			t.Fatalf("%s null GetCell err = %v", tc.name, err)
		}
		if !found {
			t.Fatalf("%s present null: found = false, want true", tc.name)
		}
		if string(raw) != "null" {
			t.Fatalf("%s present null: raw = %s, want null", tc.name, raw)
		}
	}
}

// --- out-of-range index errors ----------------------------------------------

func TestGetCell_OutOfRangeIndex_AllBackends(t *testing.T) {
	segs := parsePath("name")
	for _, tc := range []struct {
		name string
		b    Backend
	}{
		{"mem", func() Backend { b, _ := newTestMemBackend(t, fixtureRecords()); return b }()},
		{"rescan", func() Backend { b, _, _ := newTestRescanBackend(t, fixtureRecords(), 100, 1000); return b }()},
		{"sql", newTestSQLBackend(t, "t", []string{"name", "idx", "even"}, "name TEXT, idx INTEGER, even INTEGER", sqlNameParityRows())},
		{"parquet", newTestParquetBackend(t, parquetNameParityRows(), 4)},
	} {
		if _, _, err := tc.b.GetCell(context.Background(), 1000, segs); err == nil {
			t.Fatalf("%s GetCell(1000) err = nil, want out-of-range error", tc.name)
		}
	}
}

// --- a non-finite float deep inside still marshals --------------------------

func TestGetCell_NaNDeepInside_Marshals(t *testing.T) {
	// A NaN reaches a record as a raw float64 (Parquet DOUBLE / SQLite REAL --
	// see readers.ToProfileValue); a naive json.Marshal would ERROR on it. Hand-
	// build a mem record carrying one two levels deep to prove GetCell routes
	// through the sanitizeValue fallback rather than erroring.
	maps := []map[string]any{{"m": map[string]any{"x": math.NaN()}}}
	mb, _ := newTestMemBackend(t, maps)
	raw, found, err := mb.GetCell(context.Background(), 0, parsePath("m"))
	if err != nil {
		t.Fatalf("GetCell err = %v, want nil (sanitizeValue fallback)", err)
	}
	if !found {
		t.Fatalf("found = false, want true")
	}
	if !strings.Contains(string(raw), "NaN") {
		t.Fatalf("raw = %s, want the NaN sentinel present", raw)
	}
}

// --- cross-backend byte-identity (row-identity invariant, spec §9) ----------

func TestGetCell_MemRescanByteIdentical(t *testing.T) {
	maps := getCellRecords()
	mb, _ := newTestMemBackend(t, maps)
	rb, _, _ := newTestRescanBackend(t, maps, 100, 1000)
	for _, path := range []string{"name", "detail", "nick"} {
		segs := parsePath(path)
		for i := int64(0); i < int64(len(maps)); i++ {
			mraw, mfound, merr := mb.GetCell(context.Background(), i, segs)
			rraw, rfound, rerr := rb.GetCell(context.Background(), i, segs)
			if merr != nil || rerr != nil {
				t.Fatalf("path %q idx %d: mem err %v / rescan err %v", path, i, merr, rerr)
			}
			if mfound != rfound {
				t.Fatalf("path %q idx %d: found mem=%v rescan=%v", path, i, mfound, rfound)
			}
			if string(mraw) != string(rraw) {
				t.Fatalf("path %q idx %d: mem=%s rescan=%s (byte-identity broken)", path, i, mraw, rraw)
			}
		}
	}
}
