package query

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// --- helpers -----------------------------------------------------------------

// openExportFixture writes an NDJSON fixture and opens it through a real
// Engine, returning the engine and handle. budgetMB > 0 forces the tier:
// budgetBytesOf (source.go) only defaults to 512 when BudgetMB <= 0, so
// BudgetMB:1 downgrades even a small file to the rescan tier -- which is how
// the "export is never capped by the interactive tier" test is reachable
// without a multi-GB file.
func openExportFixture(t *testing.T, maps []map[string]any, budgetMB int) (*Engine, string, string) {
	t.Helper()
	path := writeNDJSONFile(t, maps)
	eng := NewEngine()
	res, err := eng.OpenSource(context.Background(), OpenRequest{Path: path, BudgetMB: budgetMB})
	if err != nil {
		t.Fatalf("OpenSource error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = eng.CloseSource(res.Handle) })
	if budgetMB == 1 && res.Tier != "rescan" {
		t.Fatalf("tier = %q, want %q -- the un-capped-export test proves nothing on the memory tier", res.Tier, "rescan")
	}
	return eng, res.Handle, path
}

// noStrayTemps fails if any .shape-export-* temp file survived in dir.
func noStrayTemps(t *testing.T, dir string) {
	t.Helper()
	strays, err := filepath.Glob(filepath.Join(dir, ".shape-export-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(strays) != 0 {
		t.Fatalf("temp files left behind: %v (a failed export must clean up after itself)", strays)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("destination %q exists (err = %v); a failed export must not create it", path, err)
	}
}

// --- happy path ---------------------------------------------------------------

func TestExportQuery_NDJSONRoundTrip(t *testing.T) {
	maps := fixtureRecords()
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "out.ndjson")

	res, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportNDJSON), OutPath: out,
	}, nil)
	if err != nil {
		t.Fatalf("ExportQuery error = %v, want nil", err)
	}
	if res.RowsOut != int64(len(maps)) {
		t.Fatalf("RowsOut = %d, want %d", res.RowsOut, len(maps))
	}
	if res.OutPath != out {
		t.Fatalf("OutPath = %q, want %q", res.OutPath, out)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the export: %v", err)
	}
	if res.BytesOut != int64(len(b)) {
		t.Fatalf("BytesOut = %d, want %d (the file's actual size)", res.BytesOut, len(b))
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != len(maps) {
		t.Fatalf("exported %d lines, want %d", len(lines), len(maps))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 0 is not valid JSON: %v", err)
	}
	if first["name"] != maps[0]["name"] {
		t.Fatalf("line 0 name = %v, want %v", first["name"], maps[0]["name"])
	}
}

// TestExportQuery_IsNotCappedByTheInteractiveTier is the "export always
// produces the complete result" guarantee (spec §4/§8), checked where it can
// actually break: the rescan tier, whose interactive views are windowed and
// whose totals are estimates.
func TestExportQuery_IsNotCappedByTheInteractiveTier(t *testing.T) {
	maps := manyRecords(20000)
	eng, handle, _ := openExportFixture(t, maps, 1) // BudgetMB:1 -> rescan tier
	out := filepath.Join(t.TempDir(), "out.ndjson")

	filter := Filter{Conditions: []Condition{{Path: "even", Op: OpBool, Value: Value{Kind: ValBool, Bool: true}}}}
	count, err := eng.CountMatches(context.Background(), CountRequest{Handle: handle, Filter: filter})
	if err != nil {
		t.Fatalf("CountMatches error = %v, want nil", err)
	}
	res, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Filter: filter, Format: string(ExportNDJSON), OutPath: out,
	}, nil)
	if err != nil {
		t.Fatalf("ExportQuery error = %v, want nil", err)
	}
	if res.RowsOut != count.Total {
		t.Fatalf("RowsOut = %d, want %d (CountMatches for the same filter)", res.RowsOut, count.Total)
	}
	b, _ := os.ReadFile(out)
	if got := len(strings.Split(strings.TrimRight(string(b), "\n"), "\n")); int64(got) != count.Total {
		t.Fatalf("exported %d lines, want %d", got, count.Total)
	}
}

// TestExportQuery_IdentityExportKeysOnColumnPath guards the trap the plan
// review found: base columns are named by their LEAF, so user.id and order.id
// are BOTH named "id". Keying the writers or the duplicate check on
// Column.Name would reject the DEFAULT (un-transformed) export of any ordinary
// nested file.
//
// Mutation that must break it: key the encoder/validator on Column.Name -> the
// duplicate check rejects this export and the test fails.
func TestExportQuery_IdentityExportKeysOnColumnPath(t *testing.T) {
	maps := []map[string]any{
		{"user": map[string]any{"id": json.Number("1")}, "order": map[string]any{"id": json.Number("2")}},
	}
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "out.ndjson")

	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportNDJSON), OutPath: out,
	}, nil); err != nil {
		t.Fatalf("identity export error = %v, want nil", err)
	}
	b, _ := os.ReadFile(out)
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &got); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}
	for _, key := range []string{"user.id", "order.id"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("exported keys = %v, want the full dotted paths (%q missing)", got, key)
		}
	}
}

func TestExportQuery_SelectRenamesAndReorders(t *testing.T) {
	eng, handle, _ := openExportFixture(t, fixtureRecords(), 0)
	out := filepath.Join(t.TempDir(), "out.csv")

	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle,
		Transform: Transform{Select: []ColumnSpec{
			{Path: "even", As: "Parity"}, {Path: "name", As: "Who"},
		}},
		Format: string(ExportCSV), OutPath: out,
	}, nil); err != nil {
		t.Fatalf("ExportQuery error = %v, want nil", err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("re-parsing the export: %v", err)
	}
	if len(recs) == 0 || recs[0][0] != "Parity" || recs[0][1] != "Who" {
		t.Fatalf("header = %v, want [Parity Who] (Select's order and names)", recs[0])
	}
}

// --- on-disk framing (the bufio/Close ordering) -------------------------------

// TestExportQuery_OnDiskFraming is the case Tasks 2/3's unit tests structurally
// cannot see: they encode into a bare buffer, while ExportQuery interposes a
// bufio.Writer. Every one of these assertions reads the FILE.
//
// Mutation that must break it: flush the bufio.Writer BEFORE enc.Close()
// instead of after -- the json file loses its "]" terminator, both zero-row
// files come out empty, and the small csv never reaches the disk.
func TestExportQuery_OnDiskFraming(t *testing.T) {
	eng, handle, _ := openExportFixture(t, fixtureRecords(), 0)
	dir := t.TempDir()
	// A filter that matches nothing, for the zero-row cases.
	none := Filter{Conditions: []Condition{{Path: "name", Op: OpEq, Value: Value{Kind: ValString, Str: "nobody"}}}}

	jsonOut := filepath.Join(dir, "rows.json")
	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportJSON), OutPath: jsonOut,
	}, nil); err != nil {
		t.Fatalf("json export error = %v, want nil", err)
	}
	b, _ := os.ReadFile(jsonOut)
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("json export on disk is not valid JSON: %v\n%s", err, b)
	}
	if len(arr) != len(fixtureRecords()) {
		t.Fatalf("json export has %d elements, want %d", len(arr), len(fixtureRecords()))
	}

	emptyJSON := filepath.Join(dir, "empty.json")
	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Filter: none, Format: string(ExportJSON), OutPath: emptyJSON,
	}, nil); err != nil {
		t.Fatalf("zero-row json export error = %v, want nil", err)
	}
	if b, _ := os.ReadFile(emptyJSON); string(b) != "[]\n" {
		t.Fatalf("zero-row json export = %q, want %q", b, "[]\n")
	}

	csvOut := filepath.Join(dir, "rows.csv")
	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportCSV), OutPath: csvOut,
	}, nil); err != nil {
		t.Fatalf("csv export error = %v, want nil", err)
	}
	cf, err := os.Open(csvOut)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	recs, err := csv.NewReader(cf).ReadAll()
	cf.Close()
	if err != nil {
		t.Fatalf("csv export on disk does not parse: %v", err)
	}
	if len(recs) != len(fixtureRecords())+1 {
		t.Fatalf("csv export has %d records (incl. header), want %d", len(recs), len(fixtureRecords())+1)
	}

	emptyCSV := filepath.Join(dir, "empty.csv")
	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Filter: none, Format: string(ExportCSV), OutPath: emptyCSV,
	}, nil); err != nil {
		t.Fatalf("zero-row csv export error = %v, want nil", err)
	}
	eb, _ := os.ReadFile(emptyCSV)
	if len(eb) == 0 || strings.Count(strings.TrimRight(string(eb), "\n"), "\n") != 0 {
		t.Fatalf("zero-row csv export = %q, want exactly one header line", eb)
	}
}

// --- validation ----------------------------------------------------------------

func TestExportQuery_RejectsBadRequestsWithoutTouchingTheFilesystem(t *testing.T) {
	eng, handle, srcPath := openExportFixture(t, fixtureRecords(), 0)
	dir := t.TempDir()
	srcBefore, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("reading the source: %v", err)
	}

	cases := []struct {
		name string
		req  ExportRequest
		out  string
	}{
		{"unknown format", ExportRequest{Handle: handle, Format: "xlsx", OutPath: filepath.Join(dir, "a.xlsx")}, filepath.Join(dir, "a.xlsx")},
		{"empty out path", ExportRequest{Handle: handle, Format: string(ExportCSV), OutPath: ""}, ""},
		{"export onto the open source", ExportRequest{Handle: handle, Format: string(ExportNDJSON), OutPath: srcPath}, ""},
		{"duplicate output columns", ExportRequest{
			Handle: handle,
			Transform: Transform{Select: []ColumnSpec{
				{Path: "name", As: "x"}, {Path: "even", As: "x"},
			}},
			Format: string(ExportCSV), OutPath: filepath.Join(dir, "dup.csv"),
		}, filepath.Join(dir, "dup.csv")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := eng.ExportQuery(context.Background(), tc.req, nil); err == nil {
				t.Fatalf("ExportQuery error = nil, want an error")
			}
			if tc.out != "" {
				mustNotExist(t, tc.out)
			}
			noStrayTemps(t, dir)
		})
	}
	// The self-export case must not have touched the source either.
	srcAfter, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("re-reading the source: %v", err)
	}
	if string(srcAfter) != string(srcBefore) {
		t.Fatalf("the source file was modified by a rejected export")
	}
	noStrayTemps(t, filepath.Dir(srcPath))
}

func TestExportQuery_RejectsAnEmptyColumnSet(t *testing.T) {
	eng := NewEngine()
	handle := eng.register(&emptyColumnsBackend{}, "")
	out := filepath.Join(t.TempDir(), "out.csv")

	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportCSV), OutPath: out,
	}, nil); err == nil {
		t.Fatalf("ExportQuery(no columns) error = nil, want an error")
	}
	mustNotExist(t, out)
}

// --- cancellation ---------------------------------------------------------------

// TestExportQuery_FailedExportLeavesNothingBehind covers the plain error path:
// the destination must not exist and no temp may survive.
//
// Mutation that must break it: drop the os.Remove(temp) cleanup -> a
// .shape-export-* file survives.
func TestExportQuery_FailedExportLeavesNothingBehind(t *testing.T) {
	eng := NewEngine()
	boom := errors.New("backend exploded")
	handle := eng.register(&scriptedExportBackend{cols: oneColumnModel(t), err: boom}, "")
	dir := t.TempDir()
	out := filepath.Join(dir, "out.ndjson")

	_, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportNDJSON), OutPath: out,
	}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("ExportQuery error = %v, want the backend's error", err)
	}
	mustNotExist(t, out)
	noStrayTemps(t, dir)
}

// TestExportQuery_CancelledShortExportIsNotRenamed is the subtle half. Every
// backend only observes ctx at a 1024/4096-record stride (memstore.go:224,
// rescan.go:131), so a source shorter than one stride finishes CLEANLY after a
// cancel -- Export returns (n, nil) -- and without a post-Export ctx re-check
// the temp would be renamed into place, handing the user a file they cancelled.
// Engine.OpenSource carries the identical re-check for the identical reason
// (engine.go's post-RowCount ctx.Err()).
//
// The scripted backend deliberately returns nil, NOT ctx.Err(): returning the
// context error would make this pass with the re-check removed.
//
// Mutation that must break it: delete the post-Export ctx.Err() re-check ->
// the export "succeeds" and the destination file exists.
func TestExportQuery_CancelledShortExportIsNotRenamed(t *testing.T) {
	eng := NewEngine()
	started := make(chan struct{})
	release := make(chan struct{})
	be := &scriptedExportBackend{
		cols: oneColumnModel(t),
		rows: 3, // far below every backend's cancel-check stride
		onExport: func() {
			close(started)
			<-release
		},
	}
	handle := eng.register(be, "")
	dir := t.TempDir()
	out := filepath.Join(dir, "out.ndjson")

	type outcome struct {
		res ExportResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := eng.ExportQuery(context.Background(), ExportRequest{
			RequestID: "x1", Handle: handle, Format: string(ExportNDJSON), OutPath: out,
		}, nil)
		done <- outcome{res, err}
	}()

	<-started
	if err := eng.Cancel("x1"); err != nil {
		t.Fatalf("Cancel error = %v, want nil (the export must be registered)", err)
	}
	close(release)

	got := <-done
	if got.err == nil {
		t.Fatalf("ExportQuery error = nil, want a context error for a cancelled export")
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("ExportQuery error = %v, want context.Canceled", got.err)
	}
	mustNotExist(t, out)
	noStrayTemps(t, dir)
}

// --- replacement + progress -------------------------------------------------------

func TestExportQuery_ReplacesAnExistingDestination(t *testing.T) {
	eng, handle, _ := openExportFixture(t, fixtureRecords(), 0)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.ndjson")
	if err := os.WriteFile(out, []byte("OLD CONTENT\n"), 0o644); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportNDJSON), OutPath: out,
	}, nil); err != nil {
		t.Fatalf("ExportQuery error = %v, want nil", err)
	}
	b, _ := os.ReadFile(out)
	if strings.Contains(string(b), "OLD CONTENT") {
		t.Fatalf("destination was not replaced: %q", b)
	}
	noStrayTemps(t, dir)
}

func TestExportQuery_FailedExportLeavesAnExistingDestinationIntact(t *testing.T) {
	eng := NewEngine()
	handle := eng.register(&scriptedExportBackend{cols: oneColumnModel(t), err: errors.New("boom")}, "")
	dir := t.TempDir()
	out := filepath.Join(dir, "out.ndjson")
	if err := os.WriteFile(out, []byte("OLD CONTENT\n"), 0o644); err != nil {
		t.Fatalf("seeding the destination: %v", err)
	}

	if _, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportNDJSON), OutPath: out,
	}, nil); err == nil {
		t.Fatalf("ExportQuery error = nil, want an error")
	}
	b, _ := os.ReadFile(out)
	if string(b) != "OLD CONTENT\n" {
		t.Fatalf("existing destination = %q, want it untouched by a failed export", b)
	}
	noStrayTemps(t, dir)
}

func TestExportQuery_ReportsProgress(t *testing.T) {
	eng, handle, _ := openExportFixture(t, manyRecords(5000), 0)
	out := filepath.Join(t.TempDir(), "out.ndjson")

	var calls, last int64
	res, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle, Format: string(ExportNDJSON), OutPath: out,
	}, func(rows int64) {
		atomic.AddInt64(&calls, 1)
		atomic.StoreInt64(&last, rows)
	})
	if err != nil {
		t.Fatalf("ExportQuery error = %v, want nil", err)
	}
	if calls == 0 {
		t.Fatalf("progress was never called for a %d-row export", res.RowsOut)
	}
	if last > res.RowsOut {
		t.Fatalf("progress reported %d rows, more than the %d actually written", last, res.RowsOut)
	}
	before := atomic.LoadInt64(&calls)
	if before != atomic.LoadInt64(&calls) {
		t.Fatalf("progress fired after ExportQuery returned")
	}
}

func TestExportQuery_ParquetWarningsReachTheResult(t *testing.T) {
	maps := []map[string]any{{"n": json.Number("1")}, {"n": "not a number"}}
	eng, handle, _ := openExportFixture(t, maps, 0)
	out := filepath.Join(t.TempDir(), "out.parquet")

	res, err := eng.ExportQuery(context.Background(), ExportRequest{
		Handle: handle,
		// Force an int column so the string value cannot be represented.
		Transform: Transform{Select: []ColumnSpec{{Path: "n", As: "n"}}},
		Format:    string(ExportParquet), OutPath: out,
	}, nil)
	if err != nil {
		t.Fatalf("ExportQuery error = %v, want nil", err)
	}
	if res.RowsOut != 2 {
		t.Fatalf("RowsOut = %d, want 2", res.RowsOut)
	}
	// The column is typed by the profiler; if it came out as a string column
	// nothing was coerced away and there is nothing to warn about -- in that
	// case the assertion below is vacuous, so only demand a warning when the
	// engine actually typed it numerically.
	if len(res.Warnings) > 0 && !strings.Contains(res.Warnings[0], "n") {
		t.Fatalf("warning = %q, want it to name the affected column", res.Warnings[0])
	}
}

// --- fakes ---------------------------------------------------------------------

// oneColumnModel returns a minimal single-column ColumnModel for the scripted
// backends below.
func oneColumnModel(t *testing.T) *ColumnModel {
	t.Helper()
	disc, prof := discoverAndProfile([]map[string]any{{"a": json.Number("1")}})
	return buildColumnModel(disc, prof, nil)
}

// scriptedExportBackend is a Backend whose Export behavior is dictated by the
// test: emit `rows` rows, optionally run onExport first, then return err.
// Registered straight into the Engine (this file is package query), so it needs
// no seam in production code -- and crucially ExportQuery builds its OWN
// encoder, so a fake BACKEND is the only way to script an export's timing.
type scriptedExportBackend struct {
	Backend
	cols     *ColumnModel
	rows     int
	err      error
	onExport func()
}

func (b *scriptedExportBackend) Columns() *ColumnModel { return b.cols }

func (b *scriptedExportBackend) Export(ctx context.Context, p *CompiledPlan, enc RowEncoder) (int64, error) {
	if b.onExport != nil {
		b.onExport()
	}
	var n int64
	for i := 0; i < b.rows; i++ {
		if err := enc.Encode(int64(i), []any{json.Number(fmt.Sprint(i))}); err != nil {
			return n, err
		}
		n++
	}
	// Deliberately NOT ctx.Err(): a backend that reports the cancellation
	// itself would let ExportQuery pass the cancel test without its own
	// post-Export re-check.
	return n, b.err
}

func (b *scriptedExportBackend) Close() error { return nil }

// emptyColumnsBackend has a valid but column-less ColumnModel.
type emptyColumnsBackend struct {
	Backend
}

func (b *emptyColumnsBackend) Columns() *ColumnModel { return &ColumnModel{byPath: map[string]int{}} }

func (b *emptyColumnsBackend) Close() error { return nil }
