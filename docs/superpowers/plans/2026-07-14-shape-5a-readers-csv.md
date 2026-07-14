# Shape Plan 5a: reader interface refactor + CSV

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a common `RecordStream` reader interface with a format registry, make `jsonreader` conform, add a CSV reader (stdlib, zero new dependency), and route `shape profile/schema/diff` through the registry so `.csv` inputs profile/schema/diff for free - while JSON/NDJSON behavior stays byte-identical. This also lands the `Source{Path, Reader, ...}` model that later plans (Parquet, SQLite - file-only formats) need, so they require no second refactor.

**Architecture:** A new leaf package `internal/readers` defines `RecordStream` (`Next() (any,error)`, `Skipped() int`), a `Format` enum, a `Source` handle (path + reader + peek + options), a `Factory` registry, `Open`, `DetectFormat`, and the `ToProfileValue` native-to-profiler value converter. Each reader package registers its factory in `init()` and is blank-imported by `internal/cmd`. `profileSource` detects the format and dispatches through `readers.Open`. CSV values are type-inferred by default (empty->null, true/false->bool, strict int/float literals->json.Number, else string), with `--csv-raw` for all-strings.

**Tech Stack:** Go stdlib only (`encoding/csv`, `encoding/json`, `strconv`, `math`, `time`), `spf13/cobra`, standard `testing`. NO new external dependency in this plan.

## Global Constraints

- Module `github.com/hoijun-kim/shape`. Go floor `go 1.23`.
- Package `internal/readers` is a LEAF: it imports only stdlib. Reader packages (`internal/readers/jsonreader`, `internal/readers/csvreader`) import `internal/readers` but `internal/readers` imports none of them (registry filled via `init()`). No import cycles.
- Records handed to the profiler must have scalar values in {nil, bool, string, json.Number, float64} (what `profile.Flatten`/`profile.KindOf` classify). New readers convert via `readers.ToProfileValue`.
- JSON/NDJSON output MUST stay byte-identical (existing profile/schema/diff tests unchanged).
- Plain ASCII hyphen `-` only. Conventional Commits, NO co-author trailer. TDD.
- `FormatParquet`/`FormatSQLite` constants exist but have NO factory in this plan; `Open` on them returns a clear "unsupported format" error (they arrive in Plans 5b/5c).

---

### Task 1: readers core (interface, registry, detection, value conversion)

**Files:**
- Create: `internal/readers/readers.go`
- Test: `internal/readers/readers_test.go`

**Interfaces produced:** `RecordStream`; `Format` + consts; `Source`; `Factory`; `Register`; `Open`; `DetectFormat`; `ToProfileValue`.

- [ ] **Step 1: Write the failing test**

`internal/readers/readers_test.go`:

```go
package readers

import (
	"encoding/json"
	"testing"
)

func TestDetectFormat(t *testing.T) {
	cases := []struct {
		path, flag string
		peek       []byte
		want       Format
	}{
		{"x.csv", "auto", nil, FormatCSV},
		{"x.tsv", "auto", nil, FormatCSV},
		{"x.parquet", "auto", nil, FormatParquet},
		{"x.sqlite", "auto", nil, FormatSQLite},
		{"x.db", "auto", nil, FormatSQLite},
		{"x.json", "auto", nil, FormatJSON},
		{"x.ndjson", "auto", nil, FormatJSON},
		{"-", "csv", nil, FormatCSV},                  // explicit flag wins
		{"x.csv", "json", nil, FormatJSON},            // explicit flag over ext
		{"-", "auto", []byte("PAR1..."), FormatParquet},
		{"-", "auto", []byte("SQLite format 3\x00"), FormatSQLite},
		{"-", "auto", []byte(`{"a":1}`), FormatJSON},  // default
	}
	for _, c := range cases {
		if got := DetectFormat(c.path, c.flag, c.peek); got != c.want {
			t.Errorf("DetectFormat(%q,%q,%q) = %s, want %s", c.path, c.flag, c.peek, got, c.want)
		}
	}
}

func TestToProfileValue(t *testing.T) {
	if got := ToProfileValue(int64(42)); got != json.Number("42") {
		t.Errorf("int64 -> %v (%T), want json.Number 42", got, got)
	}
	if got := ToProfileValue(float32(1.5)); got != float64(1.5) {
		t.Errorf("float32 -> %v (%T), want float64 1.5", got, got)
	}
	if got := ToProfileValue([]byte("hi")); got != "hi" {
		t.Errorf("[]byte -> %v, want string hi", got)
	}
	if got := ToProfileValue(nil); got != nil {
		t.Errorf("nil -> %v, want nil", got)
	}
	if got := ToProfileValue("s"); got != "s" {
		t.Errorf("string passthrough failed: %v", got)
	}
}

func TestOpenUnsupported(t *testing.T) {
	if _, _, err := Open(FormatParquet, Source{}); err == nil {
		t.Error("Open on an unregistered format must error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/readers/ -v`
Expected: FAIL (build error: undefined symbols).

- [ ] **Step 3: Write minimal implementation**

`internal/readers/readers.go`:

```go
package readers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// RecordStream yields one decoded record at a time until io.EOF. A record is
// what the profiler consumes via profile.Flatten: a map[string]any, []any, or a
// scalar whose value is already profiler-compatible (nil, bool, string,
// json.Number, or float64).
type RecordStream interface {
	Next() (any, error)
	Skipped() int
}

// Format identifies an input format.
type Format string

const (
	FormatJSON    Format = "json"
	FormatCSV     Format = "csv"
	FormatParquet Format = "parquet"
	FormatSQLite  Format = "sqlite"
)

// Source is a resolved input handle. Path is "" for stdin. Reader is set for
// streamable formats; file-only formats (parquet/sqlite) use Path.
type Source struct {
	Path      string
	Reader    io.Reader
	Peek      []byte
	RawFormat string
	CSVRaw    bool
}

// Factory builds a RecordStream and its cleanup for a Source.
type Factory func(Source) (RecordStream, func() error, error)

var registry = map[Format]Factory{}

// Register wires a format's factory; called from each reader package's init().
func Register(f Format, mk Factory) { registry[f] = mk }

// Open builds the stream for a format, or errors if the format is unregistered.
func Open(f Format, s Source) (RecordStream, func() error, error) {
	mk, ok := registry[f]
	if !ok {
		return nil, nil, fmt.Errorf("unsupported format %q (not built in)", f)
	}
	return mk(s)
}

// DetectFormat chooses a reader Format from an explicit flag, then the path
// extension, then a content peek.
func DetectFormat(path, formatFlag string, peek []byte) Format {
	switch formatFlag {
	case "json", "ndjson":
		return FormatJSON
	case "csv":
		return FormatCSV
	case "parquet":
		return FormatParquet
	case "sqlite":
		return FormatSQLite
	}
	l := strings.ToLower(path)
	switch {
	case strings.HasSuffix(l, ".csv"), strings.HasSuffix(l, ".tsv"):
		return FormatCSV
	case strings.HasSuffix(l, ".parquet"), strings.HasSuffix(l, ".pqt"):
		return FormatParquet
	case strings.HasSuffix(l, ".sqlite"), strings.HasSuffix(l, ".sqlite3"), strings.HasSuffix(l, ".db"):
		return FormatSQLite
	case strings.HasSuffix(l, ".json"), strings.HasSuffix(l, ".ndjson"), strings.HasSuffix(l, ".jsonl"):
		return FormatJSON
	}
	if bytes.HasPrefix(peek, []byte("PAR1")) {
		return FormatParquet
	}
	if bytes.HasPrefix(peek, []byte("SQLite format 3\x00")) {
		return FormatSQLite
	}
	return FormatJSON
}

// ToProfileValue maps a native typed cell value to the profiler-compatible set
// {nil, bool, string, json.Number, float64}. Integers become json.Number (the
// only route to KindInt); float32 becomes float64 (KindFloat); bytes/time
// become strings.
func ToProfileValue(v any) any {
	switch t := v.(type) {
	case nil, bool, string, float64, json.Number:
		return t
	case int:
		return json.Number(strconv.FormatInt(int64(t), 10))
	case int8:
		return json.Number(strconv.FormatInt(int64(t), 10))
	case int16:
		return json.Number(strconv.FormatInt(int64(t), 10))
	case int32:
		return json.Number(strconv.FormatInt(int64(t), 10))
	case int64:
		return json.Number(strconv.FormatInt(t, 10))
	case uint:
		return json.Number(strconv.FormatUint(uint64(t), 10))
	case uint8:
		return json.Number(strconv.FormatUint(uint64(t), 10))
	case uint16:
		return json.Number(strconv.FormatUint(uint64(t), 10))
	case uint32:
		return json.Number(strconv.FormatUint(uint64(t), 10))
	case uint64:
		return json.Number(strconv.FormatUint(t, 10))
	case float32:
		return float64(t)
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", t)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/readers/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/readers/readers.go internal/readers/readers_test.go
git commit -m "feat: reader interface, format registry, and value conversion"
```

---

### Task 2: make jsonreader register itself

**Files:**
- Create: `internal/readers/jsonreader/register.go`
- Test: `internal/readers/jsonreader/register_test.go`

**Interfaces produced:** `jsonreader` registers a `FormatJSON` factory in `init()` and asserts `*Stream` satisfies `readers.RecordStream`. No change to the existing reader logic.

- [ ] **Step 1: Write the failing test**

`internal/readers/jsonreader/register_test.go`:

```go
package jsonreader

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
)

func TestRegisteredJSONFactory(t *testing.T) {
	src := readers.Source{Reader: strings.NewReader("{\"a\":1}\n{\"a\":2}\n"), RawFormat: "ndjson"}
	stream, cleanup, err := readers.Open(readers.FormatJSON, src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()
	n := 0
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		n++
	}
	if n != 2 {
		t.Errorf("records = %d, want 2", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/readers/jsonreader/ -run TestRegisteredJSONFactory -v`
Expected: FAIL (the FormatJSON factory is not registered yet).

- [ ] **Step 3: Write minimal implementation**

`internal/readers/jsonreader/register.go`:

```go
package jsonreader

import "github.com/hoijun-kim/shape/internal/readers"

// compile-time proof that Stream satisfies the shared reader contract.
var _ readers.RecordStream = (*Stream)(nil)

func init() {
	readers.Register(readers.FormatJSON, open)
}

// open builds a JSON/NDJSON stream, picking Whole vs Line mode from the source.
func open(s readers.Source) (readers.RecordStream, func() error, error) {
	mode := DetectMode(s.Path, s.RawFormat, s.Peek)
	return New(s.Reader, mode), func() error { return nil }, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/readers/jsonreader/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/readers/jsonreader/register.go internal/readers/jsonreader/register_test.go
git commit -m "feat: register jsonreader as the json/ndjson factory"
```

---

### Task 3: CSV reader with type inference

**Files:**
- Create: `internal/readers/csvreader/csvreader.go`
- Test: `internal/readers/csvreader/csvreader_test.go`

**Interfaces produced:** `csvreader` registers a `FormatCSV` factory; internal `newStream`, `inferValue`. Reads a header row then one `map[string]any` per row; default type inference, `Source.CSVRaw` for all-strings.

- [ ] **Step 1: Write the failing test**

`internal/readers/csvreader/csvreader_test.go`:

```go
package csvreader

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
)

func drain(t *testing.T, s readers.RecordStream) []map[string]any {
	t.Helper()
	var out []map[string]any
	for {
		rec, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		out = append(out, rec.(map[string]any))
	}
}

func TestCSVInference(t *testing.T) {
	data := "id,age,active,zip,name\n1,42,true,007,alice\n2,,false,012,\n"
	s, _, err := readers.Open(readers.FormatCSV, readers.Source{Reader: strings.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, s)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	r0 := rows[0]
	if r0["id"] != json.Number("1") || r0["age"] != json.Number("42") {
		t.Errorf("numeric inference failed: %v", r0)
	}
	if r0["active"] != true {
		t.Errorf("bool inference failed: %v", r0["active"])
	}
	if r0["zip"] != "007" { // leading-zero stays a string (identifier)
		t.Errorf("zip should stay string 007, got %v", r0["zip"])
	}
	r1 := rows[1]
	if r1["age"] != nil { // empty cell -> null
		t.Errorf("empty cell should be nil, got %v", r1["age"])
	}
	if r1["active"] != false {
		t.Errorf("false inference failed: %v", r1["active"])
	}
}

func TestCSVRaw(t *testing.T) {
	data := "n\n42\n"
	s, _, err := readers.Open(readers.FormatCSV, readers.Source{Reader: strings.NewReader(data), CSVRaw: true})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, s)
	if rows[0]["n"] != "42" { // raw: no inference, stays string
		t.Errorf("raw mode should keep 42 as string, got %v (%T)", rows[0]["n"], rows[0]["n"])
	}
}

func TestCSVEmptyFile(t *testing.T) {
	s, _, err := readers.Open(readers.FormatCSV, readers.Source{Reader: strings.NewReader("")})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(t, s); len(got) != 0 {
		t.Errorf("empty file should yield no rows, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/readers/csvreader/ -v`
Expected: FAIL (FormatCSV not registered / package missing).

- [ ] **Step 3: Write minimal implementation**

`internal/readers/csvreader/csvreader.go`:

```go
package csvreader

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/hoijun-kim/shape/internal/readers"
)

var _ readers.RecordStream = (*stream)(nil)

func init() {
	readers.Register(readers.FormatCSV, open)
}

func open(s readers.Source) (readers.RecordStream, func() error, error) {
	return newStream(s.Reader, s.CSVRaw), func() error { return nil }, nil
}

type stream struct {
	r       *csv.Reader
	header  []string
	raw     bool
	started bool
	skipped int
}

func newStream(rd io.Reader, raw bool) *stream {
	c := csv.NewReader(rd)
	c.FieldsPerRecord = -1 // tolerate ragged rows
	return &stream{r: c, raw: raw}
}

func (s *stream) Next() (any, error) {
	if !s.started {
		h, err := s.r.Read()
		if err != nil {
			return nil, err // io.EOF on an empty file
		}
		s.header = h
		s.started = true
	}
	rec, err := s.r.Read()
	if err != nil {
		return nil, err
	}
	row := make(map[string]any, len(s.header))
	for i, col := range s.header {
		cell := ""
		if i < len(rec) {
			cell = rec[i]
		}
		if s.raw {
			if cell == "" {
				row[col] = nil
			} else {
				row[col] = cell
			}
		} else {
			row[col] = inferValue(cell)
		}
	}
	return row, nil
}

func (s *stream) Skipped() int { return s.skipped }

// inferValue applies the default CSV type-inference policy.
func inferValue(cell string) any {
	switch {
	case cell == "":
		return nil
	case cell == "true":
		return true
	case cell == "false":
		return false
	case isIntLiteral(cell):
		return json.Number(cell)
	case isFloatLiteral(cell):
		return json.Number(cell)
	default:
		return cell
	}
}

// isIntLiteral accepts a strict decimal integer, rejecting leading-zero codes
// ("007") and very long digit strings (identifiers) so they stay strings.
func isIntLiteral(s string) bool {
	body := s
	if strings.HasPrefix(body, "-") {
		body = body[1:]
	}
	if body == "" || len(body) > 15 {
		return false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	if len(body) > 1 && body[0] == '0' {
		return false
	}
	return true
}

// isFloatLiteral accepts a real decimal/scientific float (must contain '.' or
// an exponent, and not be NaN/Inf).
func isFloatLiteral(s string) bool {
	if !strings.ContainsAny(s, ".eE") {
		return false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/readers/csvreader/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/readers/csvreader/csvreader.go internal/readers/csvreader/csvreader_test.go
git commit -m "feat: CSV reader with type inference and a raw mode"
```

---

### Task 4: route commands through the registry + `--csv-raw`

**Files:**
- Modify: `internal/cmd/source.go` (openSource returns a `readers.Source`; profileSource dispatches; blank-import the reader packages)
- Modify: `internal/cmd/profile.go`, `internal/cmd/schema.go`, `internal/cmd/diff.go` (widen `--format`; add `--csv-raw`; pass it through)
- Test: `internal/cmd/csv_test.go`
- Create (fixture): `internal/cmd/testdata/sample.csv`

**Interfaces:** `profileSource(src, format string, csvRaw bool) (profile.ProfileResult, error)`.

- [ ] **Step 1: Create the CSV fixture**

`internal/cmd/testdata/sample.csv` (exactly these lines):

```
id,email,age
1,a@b.c,30
2,,x
three,d@e.f,40
```

- [ ] **Step 2: Write the failing test**

`internal/cmd/csv_test.go`:

```go
package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProfileCSV(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "--json", "testdata/sample.csv"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (%s)", err, out.String())
	}
	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if res["records"].(float64) != 3 {
		t.Errorf("records = %v, want 3", res["records"])
	}
	fields, _ := res["fields"].([]any)
	got := map[string]any{}
	for _, f := range fields {
		m := f.(map[string]any)
		got[m["path"].(string)] = m
	}
	for _, k := range []string{"id", "email", "age"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing field %q", k)
		}
	}
	// id is int in rows 1-2 and string in row 3 -> drift.
	if got["id"].(map[string]any)["drift"] != true {
		t.Errorf("id should drift (int + string), got %v", got["id"])
	}
}

func TestSchemaCSV(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"schema", "testdata/sample.csv"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (%s)", err, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"type": "object"`)) {
		t.Errorf("expected an object schema from CSV:\n%s", out.String())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run 'TestProfileCSV|TestSchemaCSV' -v`
Expected: FAIL (CSV not routed; `.csv` currently detected as JSON and fails to decode).

- [ ] **Step 4: Refactor source.go**

Replace the entire contents of `internal/cmd/source.go` with:

```go
package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers"
	_ "github.com/hoijun-kim/shape/internal/readers/csvreader"  // register csv
	_ "github.com/hoijun-kim/shape/internal/readers/jsonreader" // register json
)

// profileSource opens src (file path or "-"), detects the format, streams the
// records through the matching reader, and returns the assembled profile.
func profileSource(src, format string, csvRaw bool) (profile.ProfileResult, error) {
	source, closeSrc, err := openSource(src)
	if err != nil {
		return profile.ProfileResult{}, err
	}
	defer closeSrc()
	source.RawFormat = format
	source.CSVRaw = csvRaw

	f := readers.DetectFormat(src, format, source.Peek)
	stream, closeStream, err := readers.Open(f, source)
	if err != nil {
		return profile.ProfileResult{}, err
	}
	defer closeStream()

	p := profile.NewProfiler()
	for {
		rec, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return profile.ProfileResult{}, fmt.Errorf("read %s: %w", src, err)
		}
		p.AddRecord(rec)
	}
	p.AddSkipped(stream.Skipped())
	res := p.Result()
	res.Source = src
	return res, nil
}

// openSource opens a file path or stdin ("-") into a readers.Source with a peek.
func openSource(src string) (readers.Source, func() error, error) {
	if src == "-" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(os.Stdin, buf)
		peek := buf[:n]
		combined := io.MultiReader(bytes.NewReader(peek), os.Stdin)
		return readers.Source{Path: "", Reader: combined, Peek: peek}, func() error { return nil }, nil
	}
	fh, err := os.Open(src)
	if err != nil {
		return readers.Source{}, nil, err
	}
	peek := make([]byte, 512)
	n, _ := fh.Read(peek)
	peek = peek[:n]
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		fh.Close()
		return readers.Source{}, nil, err
	}
	return readers.Source{Path: src, Reader: fh, Peek: peek}, func() error { return fh.Close() }, nil
}
```

- [ ] **Step 5: Update the three commands (widen --format, add --csv-raw, pass through)**

In each of `internal/cmd/profile.go`, `internal/cmd/schema.go`, `internal/cmd/diff.go`:

1. Add a `var csvRaw bool` alongside the other flag vars.
2. Register the flag: `cmd.Flags().BoolVar(&csvRaw, "csv-raw", false, "read CSV cells as raw strings (no type inference)")`.
3. Change the `--format` flag help string to `"input format: auto|json|ndjson|csv"`.
4. Update the `profileSource(...)` call(s) to pass `csvRaw`:
   - profile.go and schema.go: `profileSource(args[0], format, csvRaw)`.
   - diff.go: both calls become `profileSource(args[0], format, csvRaw)` and `profileSource(args[1], format, csvRaw)`.

- [ ] **Step 6: Run tests + build**

Run:
```bash
go test ./... -count=1
go build -o shape.exe .
```
Expected: all PASS (existing JSON tests byte-identical; new CSV tests green); binary builds.

- [ ] **Step 7: Manual smoke check**

Run: `./shape.exe profile internal/cmd/testdata/sample.csv`
Expected: a table with `records: 3` and fields `id` (drift `!`), `email`, `age`.
Run: `printf 'a,b\n1,x\n' | ./shape.exe profile --format csv -`
Expected: profiles the piped CSV (2 columns, 1 row).

- [ ] **Step 8: Commit**

```bash
git add internal/cmd/source.go internal/cmd/profile.go internal/cmd/schema.go internal/cmd/diff.go internal/cmd/csv_test.go internal/cmd/testdata/sample.csv
git commit -m "feat: route profile/schema/diff through the reader registry with CSV support"
```

---

## Plan 5a self-review

Coverage: `RecordStream` interface + registry + `DetectFormat` + `ToProfileValue` (Task 1), jsonreader conformance + registration (Task 2), CSV reader with inference + raw mode (Task 3), command routing + `--csv-raw` + CSV e2e (Task 4). JSON/NDJSON stays byte-identical because `openJSON` uses the unchanged `jsonreader.New`/`DetectMode` and existing tests are unmodified (Task 4 Step 6). The `Source{Path, Reader, Peek}` model carries a path so Plans 5b/5c (file-only Parquet/SQLite) need no second refactor.

Placeholder scan: none; every code step is complete.

Type consistency: `readers.RecordStream`/`Source`/`Format`/`Register`/`Open`/`DetectFormat`/`ToProfileValue` (Task 1) are consumed by jsonreader (Task 2), csvreader (Task 3), and source.go (Task 4); `profileSource(src, format, csvRaw)` matches its three call sites.

Out of scope (later plans): Parquet (5b), SQLite (5c). Their `Format` constants exist and `Open` returns a clear "unsupported format" error until then. Also deferred: per-column CSV type policies, custom delimiters beyond tsv/csv, duplicate-header disambiguation (last-wins in v1).

## Next plans
- Plan 5b: Parquet reader (github.com/parquet-go/parquet-go, pure Go).
- Plan 5c: SQLite reader (modernc.org/sqlite, pure Go; `ORDER BY rowid` for deterministic promoted top-K; `--table` selection).
- Plans 6-7: Wails GUI; distribution.
