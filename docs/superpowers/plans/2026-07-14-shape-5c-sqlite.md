# Shape Plan 5c: SQLite reader

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a SQLite reader (pure-Go, cgo-free `modernc.org/sqlite`) behind the `readers.RecordStream` contract, so `shape profile/schema/diff` work on `.sqlite`/`.db` files. File-only. Deterministic row order via `ORDER BY _rowid_`. Table selection via `--table` (default the sole user table).

**Architecture:** A new `internal/readers/sqlitereader` package opens the DB read-only through `database/sql` + the `modernc.org/sqlite` driver (registered as `"sqlite"`), chooses a table (the `--table` flag, or the single user table, else an error listing options), runs `SELECT * FROM <table> ORDER BY _rowid_` (falling back to no ORDER BY if the table has no rowid), scans each row into `map[string]any` via `readers.ToProfileValue`, and registers a `FormatSQLite` factory. `--table` is threaded through `profileSource`. Detection for `.sqlite`/`.db`/`SQLite format 3` magic already exists from Plan 5a.

**Tech Stack:** Go, `database/sql`, `modernc.org/sqlite` (pure Go, cgo-free), standard `testing`.

## Global Constraints

- Module `github.com/hoijun-kim/shape`. Adding `modernc.org/sqlite` may raise the `go` directive; allow it. Do NOT enable cgo - `modernc.org/sqlite` is a pure-Go/no-cgo port; the build must stay cgo-free (`CGO_ENABLED=0 go build` succeeds). Note: this is a large dependency (transpiled C runtime `modernc.org/libc`); binary size grows notably - accepted for v1 (single static binary preserved). Pin the driver's own required `modernc.org/libc` version (do not bump it independently).
- Package `internal/readers/sqlitereader` imports `internal/readers`, `database/sql`, and blank-imports `modernc.org/sqlite`; it MUST NOT import `internal/cmd`.
- Records handed to the profiler have scalar leaves in {nil, bool, string, json.Number, float64}; SQLite via database/sql yields int64/float64/string/[]byte/nil, all handled by `readers.ToProfileValue`.
- Determinism: query with `ORDER BY _rowid_` so promoted top-K (order-sensitive) is stable. Fall back to no ORDER BY only if the rowid query errors (WITHOUT ROWID tables / views) - documented as an accepted limitation for those.
- Table name is validated against the `sqlite_master` allowlist before interpolation (prevents SQL injection; identifiers cannot be parameterized).
- JSON/CSV/Parquet behavior unchanged (existing tests byte-identical). Plain ASCII hyphen `-` only. Conventional Commits, NO co-author trailer. TDD.
- SQLite is FILE-ONLY: the factory rejects `Source.Path == ""` (stdin).

---

### Task 1: sqlitereader package

**Files:**
- Modify: `go.mod`/`go.sum` (add modernc.org/sqlite)
- Create: `internal/readers/sqlitereader/sqlitereader.go`
- Test: `internal/readers/sqlitereader/sqlitereader_test.go`

- [ ] **Step 1: Add the dependency**

```bash
cd C:/Users/hoijun/Projects/shape
go get modernc.org/sqlite@latest
```

- [ ] **Step 2: Write the failing test**

`internal/readers/sqlitereader/sqlitereader_test.go`:

```go
package sqlitereader

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/hoijun-kim/shape/internal/readers"
)

func makeDB(t *testing.T, stmts ...string) string {
	t.Helper()
	path := t.TempDir() + "/t.sqlite"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

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

func TestSQLiteReadTable(t *testing.T) {
	path := makeDB(t,
		"CREATE TABLE users(id INTEGER, name TEXT, score REAL)",
		"INSERT INTO users VALUES (1,'alice',9.5),(2,'bob',3.0)",
	)
	s, cleanup, err := readers.Open(readers.FormatSQLite, readers.Source{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()
	rows := drain(t, s)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["id"] != json.Number("1") { // INTEGER -> int64 -> json.Number
		t.Errorf("id = %v (%T), want json.Number 1", rows[0]["id"], rows[0]["id"])
	}
	if rows[0]["name"] != "alice" {
		t.Errorf("name = %v, want alice", rows[0]["name"])
	}
	if rows[0]["score"] != float64(9.5) { // REAL -> float64
		t.Errorf("score = %v (%T), want 9.5", rows[0]["score"], rows[0]["score"])
	}
}

func TestSQLiteSingleTableDefault(t *testing.T) {
	path := makeDB(t, "CREATE TABLE only(x INTEGER)", "INSERT INTO only VALUES (1)")
	s, cleanup, err := readers.Open(readers.FormatSQLite, readers.Source{Path: path})
	if err != nil {
		t.Fatalf("single table should default: %v", err)
	}
	defer cleanup()
	if len(drain(t, s)) != 1 {
		t.Error("expected 1 row from the sole table")
	}
}

func TestSQLiteMultiTableRequiresChoice(t *testing.T) {
	path := makeDB(t, "CREATE TABLE a(x INTEGER)", "CREATE TABLE b(y INTEGER)")
	if _, _, err := readers.Open(readers.FormatSQLite, readers.Source{Path: path}); err == nil {
		t.Error("multiple tables must require --table")
	}
	s, cleanup, err := readers.Open(readers.FormatSQLite, readers.Source{Path: path, Table: "a"})
	if err != nil {
		t.Fatalf("explicit table a should work: %v", err)
	}
	cleanup()
	_ = s
}

func TestSQLiteRejectsStdin(t *testing.T) {
	if _, _, err := readers.Open(readers.FormatSQLite, readers.Source{Path: ""}); err == nil {
		t.Error("sqlite from stdin (empty path) must error")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/readers/sqlitereader/ -v`
Expected: FAIL (package/factory missing).

- [ ] **Step 4: Write minimal implementation**

`internal/readers/sqlitereader/sqlitereader.go`:

```go
package sqlitereader

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hoijun-kim/shape/internal/readers"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver (cgo-free)
)

var _ readers.RecordStream = (*stream)(nil)

func init() {
	readers.Register(readers.FormatSQLite, open)
}

// open reads a table from a SQLite file read-only. stdin is rejected.
func open(s readers.Source) (readers.RecordStream, func() error, error) {
	if s.Path == "" {
		return nil, nil, fmt.Errorf("sqlite cannot be read from stdin; provide a file path")
	}
	db, err := sql.Open("sqlite", "file:"+s.Path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, nil, err
	}
	table, err := chooseTable(db, s.Table)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	// ORDER BY _rowid_ makes row order deterministic (so promoted top-K is
	// stable); fall back to unordered for WITHOUT ROWID tables / views.
	rows, err := db.Query("SELECT * FROM " + quoteIdent(table) + " ORDER BY _rowid_")
	if err != nil {
		rows, err = db.Query("SELECT * FROM " + quoteIdent(table))
		if err != nil {
			db.Close()
			return nil, nil, fmt.Errorf("query %s: %w", table, err)
		}
	}
	cols, err := rows.Columns()
	if err != nil {
		rows.Close()
		db.Close()
		return nil, nil, err
	}
	st := &stream{rows: rows, cols: cols}
	cleanup := func() error { return errors.Join(rows.Close(), db.Close()) }
	return st, cleanup, nil
}

// chooseTable resolves which table to read: the requested one (validated), the
// sole user table, or an error listing the options.
func chooseTable(db *sql.DB, want string) (string, error) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", err
		}
		tables = append(tables, n)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if want != "" {
		for _, t := range tables {
			if t == want {
				return t, nil
			}
		}
		return "", fmt.Errorf("table %q not found; available: %s", want, strings.Join(tables, ", "))
	}
	switch len(tables) {
	case 0:
		return "", fmt.Errorf("no user tables in the database")
	case 1:
		return tables[0], nil
	default:
		return "", fmt.Errorf("multiple tables; choose one with --table: %s", strings.Join(tables, ", "))
	}
}

// quoteIdent safely quotes a SQLite identifier (doubles embedded quotes).
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

type stream struct {
	rows *sql.Rows
	cols []string
}

func (s *stream) Next() (any, error) {
	if !s.rows.Next() {
		if err := s.rows.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	vals := make([]any, len(s.cols))
	ptrs := make([]any, len(s.cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := s.rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	row := make(map[string]any, len(s.cols))
	for i, c := range s.cols {
		row[c] = readers.ToProfileValue(vals[i])
	}
	return row, nil
}

func (s *stream) Skipped() int { return 0 }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/readers/sqlitereader/ -count=1`
Expected: PASS.

- [ ] **Step 6: Confirm cgo-free build**

Run: `CGO_ENABLED=0 go build ./...`
Expected: succeeds (modernc.org/sqlite is pure Go).

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/readers/sqlitereader/sqlitereader.go internal/readers/sqlitereader/sqlitereader_test.go
git commit -m "feat: SQLite reader via pure-Go modernc.org/sqlite"
```

---

### Task 2: wire SQLite into the commands + `--table`

**Files:**
- Modify: `internal/cmd/source.go` (blank-import sqlitereader; `profileSource` gains a `table` argument)
- Modify: `internal/cmd/profile.go`, `internal/cmd/schema.go`, `internal/cmd/diff.go` (add `--table` flag; widen `--format` help; pass `table` through)
- Test: `internal/cmd/sqlite_test.go`

**Interfaces:** `profileSource(src, format string, csvRaw bool, table string) (profile.ProfileResult, error)`.

- [ ] **Step 1: Write the failing test**

`internal/cmd/sqlite_test.go`:

```go
package cmd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

func makeSQLite(t *testing.T, stmts ...string) string {
	t.Helper()
	path := t.TempDir() + "/t.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	return path
}

func TestProfileSQLite(t *testing.T) {
	path := makeSQLite(t,
		"CREATE TABLE t(id INTEGER, tag TEXT)",
		"INSERT INTO t VALUES (1,'a'),(2,'b'),(3,'a')",
	)
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

func TestProfileSQLiteTableFlag(t *testing.T) {
	path := makeSQLite(t,
		"CREATE TABLE a(x INTEGER)", "INSERT INTO a VALUES (1),(2)",
		"CREATE TABLE b(y INTEGER)", "INSERT INTO b VALUES (10)",
	)
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "--json", "--table", "b", path})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (%s)", err, out.String())
	}
	var res map[string]any
	json.Unmarshal(out.Bytes(), &res)
	if res["records"].(float64) != 1 {
		t.Errorf("table b records = %v, want 1", res["records"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run TestProfileSQLite -v`
Expected: FAIL (sqlite not registered / `--table` flag missing).

- [ ] **Step 3: Implement**

In `internal/cmd/source.go`:
1. Add a blank import: `_ "github.com/hoijun-kim/shape/internal/readers/sqlitereader" // register sqlite`.
2. Change `profileSource`'s signature to `func profileSource(src, format string, csvRaw bool, table string) (profile.ProfileResult, error)` and set `source.Table = table` alongside the existing `source.CSVRaw = csvRaw`.

In each of `internal/cmd/profile.go`, `internal/cmd/schema.go`, `internal/cmd/diff.go`:
1. Add `var table string` alongside the other flag vars.
2. Register: `cmd.Flags().StringVar(&table, "table", "", "SQLite table to read (default: the sole user table)")`.
3. Widen the `--format` help to `"input format: auto|json|ndjson|csv|parquet|sqlite"`.
4. Pass `table` in the `profileSource(...)` call(s): profile.go/schema.go -> `profileSource(args[0], format, csvRaw, table)`; diff.go -> both calls become `profileSource(args[0], format, csvRaw, table)` and `profileSource(args[1], format, csvRaw, table)`.

- [ ] **Step 4: Run tests + build**

Run:
```bash
go test ./... -count=1
CGO_ENABLED=0 go build ./...
go build -o shape.exe .
```
Expected: all PASS; both builds succeed.

- [ ] **Step 5: Manual smoke check**

Create a `.db` with a table and run `./shape.exe profile file.db`, `./shape.exe schema file.db`, and (with 2 tables) confirm `./shape.exe profile file.db` errors asking for `--table`, while `--table <name>` works.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/source.go internal/cmd/profile.go internal/cmd/schema.go internal/cmd/diff.go internal/cmd/sqlite_test.go
git commit -m "feat: enable SQLite for profile/schema/diff with --table selection"
```

---

## Plan 5c self-review

Coverage: sqlitereader package + registration + table selection + read-only open + ORDER BY rowid determinism + value conversion + stdin rejection (Task 1), command wiring + `--table` + `--format` help + e2e (Task 2). Determinism: `ORDER BY _rowid_` fixes row order so the order-sensitive promoted top-K is reproducible; the fallback (no rowid) is a documented limitation. Value conversion reuses `readers.ToProfileValue` (int64/float64/[]byte/string/nil all covered). Table name is allowlisted against `sqlite_master`, preventing injection.

Placeholder scan: none; every code step is complete. `database/sql` + `modernc.org/sqlite` is a standard, stable API.

Type consistency: `stream` satisfies `readers.RecordStream`; `readers.Open(FormatSQLite, ...)` uses the Task 1 factory; `profileSource(src, format, csvRaw, table)` matches its call sites.

Out of scope (later): profiling multiple/all tables in one run; views (readable only if the rowid fallback path works); `WITHOUT ROWID` determinism; gating the heavy modernc dependency behind a build tag for a leaner default binary (accepted unconditionally in v1).

## Next plans
- Plans 6-7: Wails desktop GUI; distribution (GitHub Action / Homebrew / npm).
