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
