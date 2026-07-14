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
