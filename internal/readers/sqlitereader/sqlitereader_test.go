package sqlitereader

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/hoijunkim/shape/internal/readers"
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

func TestSQLiteReadOnlyPathWithHash(t *testing.T) {
	// A path with '#' must not corrupt the read-only URI. Create the fixture via
	// a plain (literal) path, then read it through the reader's file: URI.
	dir := t.TempDir()
	path := dir + "/track#1.sqlite"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE t(x INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES (1),(2)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	s, cleanup, err := readers.Open(readers.FormatSQLite, readers.Source{Path: path})
	if err != nil {
		t.Fatalf("open path with '#': %v", err)
	}
	defer cleanup()
	if got := len(drain(t, s)); got != 2 {
		t.Errorf("rows = %d, want 2 from a '#'-containing path", got)
	}
	// No stray truncated-path file should have been created.
	if _, err := os.Stat(dir + "/track"); err == nil {
		t.Error("a stray 'track' file was created - the URI was not encoded / not read-only")
	}
}
