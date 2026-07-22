package query

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// typedExportCols builds output columns with explicit engine types, which is
// what drives the Parquet schema (int -> INT64, float -> DOUBLE, bool ->
// BOOLEAN, everything else -> UTF8).
func typedExportCols(pairs ...[2]string) []Column {
	cols := make([]Column, len(pairs))
	for i, p := range pairs {
		cols[i] = Column{Path: p[0], Name: p[0], Type: p[1], Index: i}
	}
	return cols
}

// writeParquetExport runs rows through a parquetEncoder into a temp file and
// returns the path plus the encoder's warnings.
func writeParquetExport(t *testing.T, cols []Column, rows [][]any) (string, []string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.parquet")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	enc, err := newParquetEncoder(f, cols)
	if err != nil {
		f.Close()
		t.Fatalf("newParquetEncoder error = %v, want nil", err)
	}
	// One reused scratch buffer for every row, exactly as Backend.Export
	// does -- so an encoder that retained it would corrupt its own batch.
	buf := make([]any, len(cols))
	for i, r := range rows {
		copy(buf, r)
		if err := enc.Encode(int64(i), buf); err != nil {
			f.Close()
			t.Fatalf("Encode(%d) error = %v, want nil", i, err)
		}
	}
	if err := enc.Close(); err != nil {
		f.Close()
		t.Fatalf("encoder Close error = %v, want nil", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("file Close error = %v, want nil", err)
	}
	return path, enc.Warnings()
}

// readBackParquet re-opens an exported file through shape's own engine -- the
// strongest available round-trip check -- and returns its columns and rows.
func readBackParquet(t *testing.T, path string, limit int) ([]Column, []Row) {
	t.Helper()
	eng := NewEngine()
	res, err := eng.OpenSource(context.Background(), OpenRequest{Path: path})
	if err != nil {
		t.Fatalf("re-opening the exported parquet failed: %v", err)
	}
	// Windows cannot remove an open file: t.TempDir()'s cleanup would fail
	// if the backend still held the handle.
	t.Cleanup(func() { _ = eng.CloseSource(res.Handle) })
	rs, err := eng.QueryRows(context.Background(), QueryRequest{Handle: res.Handle, Limit: limit})
	if err != nil {
		t.Fatalf("QueryRows on the exported parquet failed: %v", err)
	}
	return res.Columns, rs.Rows
}

// --- column order -------------------------------------------------------------

// TestParquetEncoder_PreservesColumnOrder is why the encoder wraps
// parquet.Group at all: Group is a map[string]Node and its Fields() sorts by
// name (parquet-go node.go), so a plain Group would silently alphabetize the
// user's chosen column order.
//
// Mutation that must break it: build the schema from a bare parquet.Group
// instead of orderedGroup -- the columns come back alphabetized and this fails.
func TestParquetEncoder_PreservesColumnOrder(t *testing.T) {
	cols := typedExportCols([2]string{"zeta", "string"}, [2]string{"alpha", "int"}, [2]string{"mid", "bool"})
	path, _ := writeParquetExport(t, cols, [][]any{{"a", json.Number("1"), true}})

	got, _ := readBackParquet(t, path, 10)
	want := []string{"zeta", "alpha", "mid"}
	if len(got) != len(want) {
		t.Fatalf("re-opened %d columns, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Path != want[i] {
			t.Fatalf("column %d = %q, want %q (declared order, not alphabetical)", i, got[i].Path, want[i])
		}
	}
}

// --- the zero-value trap -------------------------------------------------------

// TestParquetEncoder_ZeroValuesAreNotNull covers parquet-go's most dangerous
// behavior for a dynamic writer: for an OPTIONAL leaf written through a
// map[string]any row, isNullValue ends in `value.IsZero()`, so a bare int64(0),
// float64(0), false or "" is written as NULL -- indistinguishable from a real
// null, and green under any test that only checks nulls.
//
// Mutation that must break it: store the coerced value directly in the row map
// instead of behind a freshly allocated pointer -- all four columns read back
// as CellNull and this fails.
func TestParquetEncoder_ZeroValuesAreNotNull(t *testing.T) {
	cols := typedExportCols(
		[2]string{"i", "int"}, [2]string{"f", "float"},
		[2]string{"b", "bool"}, [2]string{"s", "string"},
	)
	path, _ := writeParquetExport(t, cols, [][]any{{json.Number("0"), 0.0, false, ""}})

	_, rows := readBackParquet(t, path, 10)
	if len(rows) != 1 {
		t.Fatalf("re-opened %d rows, want 1", len(rows))
	}
	cells := rows[0].Cells
	for i, c := range cells {
		if c.Kind == CellNull || c.Kind == CellMissing {
			t.Fatalf("column %q read back as %s; a Go zero value must survive as itself, not as null", cols[i].Path, c.Kind)
		}
	}
	if cells[0].Kind != CellInt || cells[0].Str != "0" {
		t.Fatalf("int 0 = %#v, want CellInt \"0\"", cells[0])
	}
	if cells[1].Kind != CellFloat || cells[1].Num != 0 {
		t.Fatalf("float 0 = %#v, want CellFloat 0", cells[1])
	}
	if cells[2].Kind != CellBool || cells[2].Bool {
		t.Fatalf("bool false = %#v, want CellBool false", cells[2])
	}
	if cells[3].Kind != CellString || cells[3].Str != "" {
		t.Fatalf("empty string = %#v, want CellString \"\"", cells[3])
	}
}

// --- nulls and missing ---------------------------------------------------------

func TestParquetEncoder_NullAndMissingReadBackAsNull(t *testing.T) {
	cols := typedExportCols([2]string{"i", "int"}, [2]string{"s", "string"})
	path, _ := writeParquetExport(t, cols, [][]any{{nil, Missing}})

	_, rows := readBackParquet(t, path, 10)
	for i, c := range rows[0].Cells {
		if c.Kind != CellNull {
			t.Fatalf("column %d = %#v, want CellNull for an explicit null / absent path", i, c)
		}
	}
}

// --- coercion failures are reported, not hidden --------------------------------

// Mutation that must break it: drop the per-column counter (write NULL and move
// on) -- Warnings() comes back empty and this fails.
func TestParquetEncoder_UncoercibleValueIsNulledAndWarned(t *testing.T) {
	cols := typedExportCols([2]string{"n", "int"})
	path, warnings := writeParquetExport(t, cols, [][]any{{json.Number("1")}, {"not a number"}})

	if len(warnings) == 0 {
		t.Fatalf("Warnings() is empty; a value silently written as null must be reported")
	}
	if !strings.Contains(warnings[0], "n") {
		t.Fatalf("warning = %q, want it to name the affected column", warnings[0])
	}
	_, rows := readBackParquet(t, path, 10)
	if len(rows) != 2 {
		t.Fatalf("re-opened %d rows, want 2", len(rows))
	}
	if rows[0].Cells[0].Kind != CellInt {
		t.Fatalf("row 0 = %#v, want the coercible value intact", rows[0].Cells[0])
	}
	if rows[1].Cells[0].Kind != CellNull {
		t.Fatalf("row 1 = %#v, want CellNull for the uncoercible value", rows[1].Cells[0])
	}
}

// --- containers and non-finite floats ------------------------------------------

func TestParquetEncoder_ContainerBecomesCompactJSONText(t *testing.T) {
	cols := typedExportCols([2]string{"meta", "object"})
	nested := map[string]any{"k": []any{json.Number("1"), "<x>"}}
	path, _ := writeParquetExport(t, cols, [][]any{{nested}})

	_, rows := readBackParquet(t, path, 10)
	got := rows[0].Cells[0]
	if got.Kind != CellString || got.Str != `{"k":[1,"<x>"]}` {
		t.Fatalf("container cell = %#v, want compact unescaped JSON text", got)
	}
}

func TestParquetEncoder_KeepsNaNInAFloatColumn(t *testing.T) {
	cols := typedExportCols([2]string{"f", "float"})
	path, _ := writeParquetExport(t, cols, [][]any{{math.NaN()}})

	_, rows := readBackParquet(t, path, 10)
	got := rows[0].Cells[0]
	// The value survives as a genuine non-finite DOUBLE -- Parquet can hold
	// one, unlike JSON. What comes back is the READER's existing display form
	// for it: toCell (columns.go) renders a non-finite float as the sentinel
	// string "NaN" with Num forced to 0, because a non-finite Num would make
	// encoding/json error on the RowSet crossing the Wails bridge. So assert
	// on Str, not Num: a NULL here (the failure this test guards) would be
	// CellNull, and a coerced-away value would be CellString.
	if got.Kind != CellFloat || got.Str != "NaN" {
		t.Fatalf("float cell = %#v, want CellFloat with Str \"NaN\"", got)
	}
}

// --- batching ------------------------------------------------------------------

// TestParquetEncoder_BatchesCopyTheReusedBuffer writes rows through ONE scratch
// buffer (writeParquetExport reuses it, like Backend.Export) across more than
// one batch, so a buffered encoder that retained the slice would emit the last
// row's values for every row in the batch.
func TestParquetEncoder_BatchesCopyTheReusedBuffer(t *testing.T) {
	cols := typedExportCols([2]string{"i", "int"})
	const n = parquetBatchRows + 7
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{json.Number(itoa(i))}
	}
	path, _ := writeParquetExport(t, cols, rows)

	_, got := readBackParquet(t, path, n)
	if len(got) != n {
		t.Fatalf("re-opened %d rows, want %d (a dropped tail batch or a lost row)", len(got), n)
	}
	for i, r := range got {
		if r.Cells[0].Str != itoa(i) {
			t.Fatalf("row %d = %q, want %q (every row must keep its OWN values)", i, r.Cells[0].Str, itoa(i))
		}
	}
}

// --- duplicate paths -----------------------------------------------------------

func TestParquetEncoder_RejectsDuplicateColumnPaths(t *testing.T) {
	cols := typedExportCols([2]string{"a", "int"}, [2]string{"a", "string"})
	if _, err := newParquetEncoder(discardWriter{}, cols); err == nil {
		t.Fatalf("newParquetEncoder(duplicate paths) error = nil, want an error (a map-keyed schema would silently drop a column)")
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func itoa(i int) string { return json.Number(strconv.Itoa(i)).String() }
