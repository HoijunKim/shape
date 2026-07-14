# Shape Plan 5b: Parquet reader

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Parquet reader (pure-Go `github.com/parquet-go/parquet-go`) behind the existing `readers.RecordStream` contract, so `shape profile/schema/diff` work on `.parquet` files. File-only (no stdin). Single static binary preserved (no cgo).

**Architecture:** A new `internal/readers/parquetreader` package opens a file by `Source.Path`, streams rows via `parquet.NewGenericReader[any]` (which yields `map[string]any` rows with native Go types), deep-converts values to the profiler-compatible set via `readers.ToProfileValue`, and registers a `FormatParquet` factory. `internal/cmd/source.go` blank-imports it; the `--format` help widens to include `parquet`. Detection for `.parquet`/`PAR1` already exists from Plan 5a.

**Tech Stack:** Go, `github.com/parquet-go/parquet-go@v0.30.1` (pure Go, cgo-free), standard `testing`.

## Global Constraints

- Module `github.com/hoijun-kim/shape`. Adding parquet-go may raise the `go` directive in `go.mod` (parquet-go requires go 1.24.x) - allow that; the installed toolchain is 1.26.4. Do NOT enable cgo.
- New dependency is `github.com/parquet-go/parquet-go` only (its transitive deps - klauspost/compress, pierrec/lz4, andybalholm/brotli, google/uuid, x/sys, protobuf, etc - are all pure Go). Confirm the build stays cgo-free (`CGO_ENABLED=0 go build` succeeds).
- Package `internal/readers/parquetreader` imports `internal/readers` + parquet-go; it MUST NOT import `internal/cmd`.
- Records handed to the profiler must have scalar leaves in {nil, bool, string, json.Number, float64}; convert via `readers.ToProfileValue` recursively (nested groups arrive as `map[string]any`, lists as `[]any`).
- JSON/CSV behavior unchanged (existing tests byte-identical). Plain ASCII hyphen `-` only. Conventional Commits, NO co-author trailer. TDD.
- Parquet is FILE-ONLY: the factory rejects `Source.Path == ""` (stdin) with a clear error.

### Verified parquet-go API notes (v0.30.1, empirically confirmed)

- `parquet.OpenFile(r io.ReaderAt, size int64) (*parquet.File, error)`; size from `f.Stat().Size()`.
- `parquet.NewGenericReader[any](pf)` - use `any`, NOT `map[string]any` (the latter panics at construction). Returns only the reader (no error; panics on bad config). `(*GenericReader[any]).Read(buf []any) (int, error)` fills rows; each row is a `map[string]any`. `io.EOF` at end (may accompany n>0). `.Close()` and `.NumRows()` exist.
- Value Go types: INT32->int32, INT64->int64, FLOAT->float32, DOUBLE->float64, BYTE_ARRAY/UTF8->string (never []byte), BOOLEAN->bool, null->nil. `readers.ToProfileValue` already handles int32/int64/float32/float64.
- Writer for tests: `parquet.NewGenericWriter[T](w io.Writer)` (no error), `.Write([]T) (int, error)`, `.Close() error` (required, flushes footer).

---

### Task 1: parquetreader package

**Files:**
- Modify: `go.mod`/`go.sum` (add parquet-go)
- Create: `internal/readers/parquetreader/parquetreader.go`
- Test: `internal/readers/parquetreader/parquetreader_test.go`

**Interfaces produced:** registers a `FormatParquet` factory; internal `stream` implementing `readers.RecordStream`; `convertDeep`.

- [ ] **Step 1: Add the dependency**

```bash
cd C:/Users/hoijun/Projects/shape
go get github.com/parquet-go/parquet-go@v0.30.1
```

- [ ] **Step 2: Write the failing test**

`internal/readers/parquetreader/parquetreader_test.go`:

```go
package parquetreader

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
	"github.com/parquet-go/parquet-go"
)

type fixtureRow struct {
	ID     int64  `parquet:"id"`
	Name   string `parquet:"name"`
	Active bool   `parquet:"active"`
}

func writeFixture(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[fixtureRow](&buf)
	if _, err := w.Write([]fixtureRow{
		{ID: 1, Name: "Alice", Active: true},
		{ID: 2, Name: "Bob", Active: false},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	path := t.TempDir() + "/fixture.parquet"
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func TestParquetRoundTrip(t *testing.T) {
	path := writeFixture(t)
	s, cleanup, err := readers.Open(readers.FormatParquet, readers.Source{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()
	var rows []map[string]any
	for {
		rec, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		rows = append(rows, rec.(map[string]any))
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["id"] != json.Number("1") { // int64 -> json.Number via ToProfileValue
		t.Errorf("id = %v (%T), want json.Number 1", rows[0]["id"], rows[0]["id"])
	}
	if rows[0]["name"] != "Alice" || rows[0]["active"] != true {
		t.Errorf("row0 = %v", rows[0])
	}
}

func TestParquetRejectsStdin(t *testing.T) {
	if _, _, err := readers.Open(readers.FormatParquet, readers.Source{Path: ""}); err == nil {
		t.Error("parquet from stdin (empty path) must error")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/readers/parquetreader/ -v`
Expected: FAIL (package/factory missing).

- [ ] **Step 4: Write minimal implementation**

`internal/readers/parquetreader/parquetreader.go`:

```go
package parquetreader

import (
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/readers"
	"github.com/parquet-go/parquet-go"
)

var _ readers.RecordStream = (*stream)(nil)

func init() {
	readers.Register(readers.FormatParquet, open)
}

// open reads a parquet file by path (parquet needs random access, so stdin is
// rejected).
func open(s readers.Source) (readers.RecordStream, func() error, error) {
	if s.Path == "" {
		return nil, nil, fmt.Errorf("parquet cannot be read from stdin; provide a file path")
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	pf, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("open parquet %s: %w", s.Path, err)
	}
	gr := parquet.NewGenericReader[any](pf)
	st := &stream{gr: gr, buf: make([]any, 256)}
	cleanup := func() error {
		gr.Close()
		return f.Close()
	}
	return st, cleanup, nil
}

type stream struct {
	gr   *parquet.GenericReader[any]
	buf  []any
	pos  int
	n    int
	done bool
}

func (s *stream) Next() (any, error) {
	for {
		if s.pos < s.n {
			rec := s.buf[s.pos]
			s.pos++
			m, ok := rec.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unexpected parquet row type %T", rec)
			}
			return convertDeep(m), nil
		}
		if s.done {
			return nil, io.EOF
		}
		n, err := s.gr.Read(s.buf)
		s.n, s.pos = n, 0
		if err != nil {
			if err == io.EOF {
				s.done = true // still serve the n rows read in this batch
				continue
			}
			return nil, err
		}
	}
}

func (s *stream) Skipped() int { return 0 }

// convertDeep recursively maps native parquet values (int32/int64/float32/...)
// to the profiler-compatible set, descending into nested groups and lists.
func convertDeep(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = convertDeep(x)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = convertDeep(x)
		}
		return out
	default:
		return readers.ToProfileValue(v)
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/readers/parquetreader/ -count=1`
Expected: PASS.

- [ ] **Step 6: Confirm cgo-free build**

Run: `CGO_ENABLED=0 go build ./...`
Expected: succeeds (parquet-go is pure Go).

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/readers/parquetreader/parquetreader.go internal/readers/parquetreader/parquetreader_test.go
git commit -m "feat: Parquet reader via pure-Go parquet-go"
```

---

### Task 2: wire Parquet into the commands

**Files:**
- Modify: `internal/cmd/source.go` (blank-import parquetreader)
- Modify: `internal/cmd/profile.go`, `internal/cmd/schema.go`, `internal/cmd/diff.go` (widen `--format` help)
- Test: `internal/cmd/parquet_test.go`

- [ ] **Step 1: Write the failing test**

`internal/cmd/parquet_test.go`:

```go
package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/parquet-go/parquet-go"
)

type pqRow struct {
	ID   int64  `parquet:"id"`
	Tag  string `parquet:"tag"`
}

func writeParquet(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[pqRow](&buf)
	if _, err := w.Write([]pqRow{{ID: 1, Tag: "a"}, {ID: 2, Tag: "b"}, {ID: 3, Tag: "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/t.parquet"
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProfileParquet(t *testing.T) {
	path := writeParquet(t)
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "--json", path})
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
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run TestProfileParquet -v`
Expected: FAIL (parquet factory not registered in the cmd binary - `unsupported format "parquet"`).

- [ ] **Step 3: Implement**

In `internal/cmd/source.go`, add a blank import for the parquet reader alongside the existing csv/json blank imports:

```go
	_ "github.com/hoijun-kim/shape/internal/readers/parquetreader" // register parquet
```

In each of `internal/cmd/profile.go`, `internal/cmd/schema.go`, `internal/cmd/diff.go`, change the `--format` flag help from `"input format: auto|json|ndjson|csv"` to `"input format: auto|json|ndjson|csv|parquet"`.

- [ ] **Step 4: Run test + build**

Run:
```bash
go test ./... -count=1
go build -o shape.exe .
```
Expected: all PASS; binary builds.

- [ ] **Step 5: Manual smoke check**

(Reuse the test's writer or profile a real `.parquet`.) Confirm `./shape.exe profile some.parquet` prints a table with the parquet columns, and `./shape.exe schema some.parquet` emits a JSON Schema.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/source.go internal/cmd/profile.go internal/cmd/schema.go internal/cmd/diff.go internal/cmd/parquet_test.go
git commit -m "feat: enable Parquet for profile/schema/diff"
```

---

## Plan 5b self-review

Coverage: parquetreader package + registration + deep value conversion + stdin rejection + fixture round-trip (Task 1), command wiring + `--format` help + e2e (Task 2). Determinism: parquet rows are read in file order (row groups then rows), which is deterministic; the exact-mode profiler is order-independent below the cap, and any promoted top-K is stable for a fixed file. Value conversion reuses the Plan 5a `ToProfileValue`; nested groups/lists recurse via `convertDeep`.

Placeholder scan: none; every code step is complete and grounded in the empirically-verified parquet-go v0.30.1 API.

Type consistency: `stream` satisfies `readers.RecordStream`; `readers.Open(FormatParquet, ...)` uses the Task 1 factory; `convertDeep` leaves call `readers.ToProfileValue`.

Out of scope (later): SQLite (5c). Parquet DECIMAL/INT96 logical types (surface as string/native per the lib) are handled generically; explicit logical-type polishing is future. Streaming batch size (256) is a fixed default.

## Next plans
- Plan 5c: SQLite reader (modernc.org/sqlite, pure Go; `ORDER BY rowid` for deterministic promoted top-K; `--table` selection).
- Plans 6-7: Wails GUI; distribution.
