package query

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nestedRecords returns records with a nested object and a scalar, so a save
// can prove the nesting survives (the review's C3: the export path would have
// flattened "user.name" to a flat key).
func nestedRecords(n int) []map[string]any {
	recs := make([]map[string]any, n)
	for i := range recs {
		recs[i] = map[string]any{
			"user": map[string]any{"name": "u" + string(rune('a'+i%26)), "age": json.Number("30")},
			"tag":  "x",
		}
	}
	return recs
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func TestSaveEdits_PreservesNestingAndEditsOneLeaf(t *testing.T) {
	maps := nestedRecords(3)
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "out.ndjson")

	res, err := eng.SaveEdits(context.Background(), SaveRequest{
		Handle: handle, Format: "ndjson", OutPath: out,
		Edits: []CellEdit{{Index: 0, Path: "user.name", Kind: "string", Literal: "zzz"}},
	}, nil)
	if err != nil {
		t.Fatalf("SaveEdits: %v", err)
	}
	if res.RowsOut != 3 || res.EditsApplied != 1 || res.EditsUnapplied != 0 {
		t.Fatalf("result = %+v, want 3 rows / 1 applied / 0 unapplied", res)
	}

	lines := readLines(t, out)
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3", len(lines))
	}
	var rec0 map[string]any
	dec := json.NewDecoder(strings.NewReader(lines[0]))
	dec.UseNumber()
	if err := dec.Decode(&rec0); err != nil {
		t.Fatalf("decode line 0 %q: %v", lines[0], err)
	}
	// Mutation: write the flat projected columns instead of the nested record ->
	// rec0["user"] is not an object and this fails.
	usr, ok := rec0["user"].(map[string]any)
	if !ok {
		t.Fatalf("line 0 lost the nested structure: %q", lines[0])
	}
	if usr["name"] != "zzz" {
		t.Fatalf("user.name = %v, want zzz (the edit)", usr["name"])
	}
	if usr["age"] != json.Number("30") || rec0["tag"] != "x" {
		t.Fatalf("line 0 changed a non-edited leaf: %q", lines[0])
	}
	// A non-edited row is untouched.
	var rec1 map[string]any
	dec1 := json.NewDecoder(strings.NewReader(lines[1]))
	dec1.UseNumber()
	_ = dec1.Decode(&rec1)
	if rec1["user"].(map[string]any)["name"] != "ub" {
		t.Fatalf("row 1 was altered: %q", lines[1])
	}
}

func TestSaveEdits_NumberLiteralSurvivesBeyondFloat64(t *testing.T) {
	maps := nestedRecords(1)
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "out.ndjson")

	// 9007199254740993 = 2^53 + 1, which float64 cannot represent (rounds to
	// ...992). A >17-digit decimal likewise. Both must survive byte-exact.
	for _, lit := range []string{"9007199254740993", "12345678901234567890.12345678901234567890"} {
		_, err := eng.SaveEdits(context.Background(), SaveRequest{
			Handle: handle, Format: "ndjson", OutPath: out,
			Edits: []CellEdit{{Index: 0, Path: "user.age", Kind: "number", Literal: lit}},
		}, nil)
		if err != nil {
			t.Fatalf("SaveEdits(%s): %v", lit, err)
		}
		// Mutation: decode CellEdit.Value without UseNumber -> a float64 loses
		// the literal (...993 -> ...992) and this substring check fails.
		body := readLines(t, out)[0]
		if !strings.Contains(body, `"age":`+lit) {
			t.Fatalf("output %q does not contain the exact literal age:%s", body, lit)
		}
	}
}

func TestSaveEdits_WritesAllRowsNotAView(t *testing.T) {
	maps := nestedRecords(50)
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "out.ndjson")
	res, err := eng.SaveEdits(context.Background(), SaveRequest{
		Handle: handle, Format: "ndjson", OutPath: out,
		Edits: []CellEdit{{Index: 10, Path: "tag", Kind: "string", Literal: "y"}},
	}, nil)
	if err != nil {
		t.Fatalf("SaveEdits: %v", err)
	}
	// The C1 fix: save writes the WHOLE file, never a filtered subset.
	if res.RowsOut != 50 || len(readLines(t, out)) != 50 {
		t.Fatalf("RowsOut = %d (%d lines), want 50 (all rows)", res.RowsOut, len(readLines(t, out)))
	}
}

func TestSaveEdits_UnsettableEditIsCountedUnapplied(t *testing.T) {
	maps := nestedRecords(2)
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "out.ndjson")
	res, err := eng.SaveEdits(context.Background(), SaveRequest{
		Handle: handle, Format: "ndjson", OutPath: out,
		Edits: []CellEdit{
			{Index: 0, Path: "user.name", Kind: "string", Literal: "ok"}, // applies
			{Index: 0, Path: "tag.sub", Kind: "string", Literal: "s"},    // "tag" is a scalar -> not an object -> unapplied
			{Index: 99, Path: "tag", Kind: "string", Literal: "z"},       // index past end -> never streamed -> unapplied
		},
	}, nil)
	if err != nil {
		t.Fatalf("SaveEdits: %v", err)
	}
	if res.EditsApplied != 1 || res.EditsUnapplied != 2 {
		t.Fatalf("applied/unapplied = %d/%d, want 1/2", res.EditsApplied, res.EditsUnapplied)
	}
}

func TestSaveEdits_DoesNotMutateBackendRecords(t *testing.T) {
	maps := nestedRecords(2)
	mb, cm := newTestMemBackend(t, maps)
	_ = cm
	segs := parsePath("user.name")
	before, _, _ := mb.GetCell(context.Background(), 0, segs)
	// Apply setAtPath directly and confirm the backend's own record is untouched.
	if _, err := setAtPath(mb.records[0], segs, "MUTATED"); err != nil {
		t.Fatalf("setAtPath: %v", err)
	}
	after, _, _ := mb.GetCell(context.Background(), 0, segs)
	if string(before) != string(after) {
		t.Fatalf("setAtPath mutated the backend record: before %s after %s", before, after)
	}
}

func TestSaveEdits_RefusesTheOpenSourceFile(t *testing.T) {
	maps := nestedRecords(2)
	eng, handle, srcPath := openExportFixture(t, maps, 0)
	_, err := eng.SaveEdits(context.Background(), SaveRequest{
		Handle: handle, Format: "ndjson", OutPath: srcPath, // the file being read
		Edits: []CellEdit{{Index: 0, Path: "tag", Kind: "string", Literal: "y"}},
	}, nil)
	if err == nil {
		t.Fatalf("SaveEdits onto the open source path succeeded, want a refusal")
	}
}

func TestSaveEdits_EmptyEditsIsAFaithfulCopy(t *testing.T) {
	maps := nestedRecords(4)
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "copy.ndjson")
	res, err := eng.SaveEdits(context.Background(), SaveRequest{
		Handle: handle, Format: "ndjson", OutPath: out, Edits: nil,
	}, nil)
	if err != nil {
		t.Fatalf("SaveEdits: %v", err)
	}
	if res.RowsOut != 4 || res.EditsApplied != 0 {
		t.Fatalf("result = %+v, want 4 rows / 0 applied", res)
	}
	lines := readLines(t, out)
	for i, ln := range lines {
		var rec map[string]any
		dec := json.NewDecoder(strings.NewReader(ln))
		dec.UseNumber()
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("line %d %q: %v", i, ln, err)
		}
		if rec["user"].(map[string]any)["age"] != json.Number("30") {
			t.Fatalf("line %d lost fidelity: %q", i, ln)
		}
	}
}

func TestSaveEdits_JSONArrayFormat(t *testing.T) {
	maps := nestedRecords(2)
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "out.json")
	if _, err := eng.SaveEdits(context.Background(), SaveRequest{
		Handle: handle, Format: "json", OutPath: out,
		Edits: []CellEdit{{Index: 0, Path: "tag", Kind: "string", Literal: "y"}},
	}, nil); err != nil {
		t.Fatalf("SaveEdits: %v", err)
	}
	b, _ := os.ReadFile(out)
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, b)
	}
	if len(arr) != 2 || arr[0]["tag"] != "y" {
		t.Fatalf("json array = %v, want 2 records with the edit", arr)
	}
}
