// Package query: sqlBackend (spec §4, docs/superpowers/specs/2026-07-17-
// shape-engine-design.md) implements Backend for a FormatSQLite source by
// pushing PROJECTION + WINDOW + ORDER to SQLite over a read-only
// modernc.org/sqlite connection, then applying the plan's shared Go
// predicate over the returned rows for FILTER correctness. Full SQL WHERE
// pushdown (codegen'd from Filter) landed in E5 (sqlpushdown.go); at the
// E1 baseline, row-level correctness always comes from the same
// CompiledFilter/CompiledTransform pair mem/rescan/parquet backends use, so
// results are identical across every backend by construction (spec §9).
package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hoijunkim/shape/internal/profile"
	"github.com/hoijunkim/shape/internal/readers"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver (cgo-free)
)

// var _ Backend ensures sqlBackend satisfies the Backend interface at
// compile time (mirrors memstore.go/rescan.go's compile-time check).
var _ Backend = (*sqlBackend)(nil)

// sqlBackend is the SQLite-native Backend (spec §4). It keeps ONE read-only
// connection open for the handle's whole lifetime (immutable=1, so SQLite
// can skip its usual change-detection locking -- safe because shape never
// writes to a source it is exploring).
//
// Since E5 it also pushes an EXACT filter's WHERE to SQLite (sqlpushdown.go),
// but only where that cannot change results: see pushdownFor and the per-
// method gates on Query/Count/Export. Row-level correctness still comes from
// the shared Go predicate on every path the pushdown declines.
//
// Query splits on whether the compiled filter is empty:
//   - EMPTY filter: SQL LIMIT/OFFSET is pushed straight to SQLite (bound
//     params) -- native random access, no O(offset) cost, spec §4's
//     headline sqlBackend win.
//   - NON-EMPTY filter: LIMIT/OFFSET cannot be pushed (the Go predicate
//     filters AFTER any SQL-side windowing would have applied, so a plain
//     SQL LIMIT/OFFSET over the UNFILTERED table would return the wrong
//     rows). Instead every row is streamed via a single _rowid_-ordered
//     cursor, the Go predicate decides membership, and Offset/Limit are
//     applied to the MATCH sequence (skip the first Offset matches, take
//     the next Limit) -- byte-for-byte the same algorithm rescanBackend.Query
//     uses (rescan.go), which is required: the cross-backend row-identity
//     invariant (spec §9) means a filtered window must return the SAME rows
//     regardless of which backend served it.
type sqlBackend struct {
	db       *sql.DB
	table    string
	cols     []string // PRAGMA table_info order: SQLite's real column order, also the SELECT list
	hasRowID bool     // false for WITHOUT ROWID tables/views (no _rowid_ pseudo-column)

	// denseRowIDs is true when _rowid_ runs 1..N with no gaps, so a row's
	// scan ordinal is exactly rowid-1. Only then can a pushed WHERE window
	// reproduce Row.Index, which is the ABSOLUTE scan ordinal (columns.go)
	// and is rendered as the table's row-number gutter. Probed once at open;
	// sound for the handle's lifetime because the connection is immutable=1.
	denseRowIDs bool

	// noPush marks columns whose raw driver value differs from what the
	// engine compares against, so nothing about them may be pushed to SQL:
	// scan() stores readers.ToProfileValue(v), which rewrites []byte to a
	// string and time.Time to RFC3339Nano. A DATE column therefore holds
	// '2024-01-01' in SQLite while the engine sees "2024-01-01T00:00:00Z",
	// and a pushed `=` returns zero rows where the Go predicate returns one.
	// BLOBs are worse: storage-class ordering puts every BLOB above every
	// TEXT, and a declared TEXT affinity does not convert them.
	noPush map[string]bool

	// probeRaw is true only during the constructor's profiling pass, when
	// scan() inspects each RAW driver value to populate noPush. It is not
	// guarded by a mutex because it is set and cleared inside newSQLBackend,
	// before the handle is ever registered and therefore before any
	// concurrent Query/Count/Export can run.
	probeRaw bool

	// disablePushdown is a TEST seam: with it set, every pushable filter
	// takes the Go path instead, so a test can run the same query both ways
	// and compare. Production never sets it.
	disablePushdown bool

	cm   *ColumnModel
	prof profile.ProfileResult
}

// newSQLBackend opens path read-only (mirrors internal/readers/sqlitereader's
// connection approach: sqliteReadonlyURI below is the same "file:...?mode=ro
// &immutable=1" construction, mirrored locally rather than exported
// cross-package -- see the task report for why), resolves table with the
// SAME default rule sqlitereader/the CLI use (the explicit table if given,
// else the sole user table, else an error listing the choices), discovers
// its columns via PRAGMA table_info -- SQLite's real, natural column order,
// passed to buildColumnModel as sourceOrder (unlike the JSON/CSV ingest
// path, which has no true source order to offer, spec §3) -- and runs one
// full pass over every row to build the ColumnModel/ProfileResult (spec §4:
// "you may run the existing profiler over the rows via the sqlitereader
// stream, OR a lighter per-column pass"; this runs the real profiler, for
// consistency with mem/rescan's sidebar structure map).
//
// That full pass is cancellable: it runs through sb.scan(ctx, ...), which
// checks ctx every cancelCheckStride rows exactly like every other scan this
// backend runs (Query/Count/Export), so a ctx that dies during this initial
// profiling pass aborts newSQLBackend with an error rather than running to
// completion uncancellably.
func newSQLBackend(ctx context.Context, path, table string) (*sqlBackend, error) {
	if path == "" {
		return nil, fmt.Errorf("query: sqlite cannot be read from stdin; provide a file path")
	}
	db, err := sql.Open("sqlite", sqliteReadonlyURI(path))
	if err != nil {
		return nil, fmt.Errorf("query: open sqlite %s: %w", path, err)
	}

	tbl, err := sqliteChooseTable(db, table)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("query: %s: %w", path, err)
	}
	cols, err := sqliteTableColumns(db, tbl)
	if err != nil {
		db.Close()
		return nil, err
	}
	if len(cols) == 0 {
		db.Close()
		return nil, fmt.Errorf("query: table %q has no columns", tbl)
	}

	sb := &sqlBackend{
		db:       db,
		table:    tbl,
		cols:     cols,
		hasRowID: sqliteHasRowID(db, tbl),
		noPush:   map[string]bool{},
	}
	if sb.hasRowID {
		sb.denseRowIDs = sqliteDenseRowIDs(db, tbl)
	}
	// Declared type is the driver's own conversion trigger, and it covers a
	// column that is entirely NULL at profile time (where the value probe
	// below sees nothing). It cannot predict a BLOB, so both gates are needed.
	for _, c := range sqliteConvertedDecltypeColumns(db, tbl) {
		sb.noPush[c] = true
	}

	disc := newColumnDiscoverer()
	prof := profile.NewProfiler()
	sb.probeRaw = true // only during this pass; see scan()
	if serr := sb.scan(ctx, func(_ int64, rec any) (bool, error) {
		disc.Observe(rec)
		prof.AddRecord(rec)
		return false, nil
	}); serr != nil {
		sb.probeRaw = false
		db.Close()
		return nil, fmt.Errorf("query: profile sqlite %s: %w", path, serr)
	}

	sb.probeRaw = false

	profResult := prof.Result()
	sb.prof = profResult
	sb.cm = buildColumnModel(disc, profResult, cols)
	return sb, nil
}

// --- connection/table helpers ------------------------------------------------
//
// These mirror internal/readers/sqlitereader/sqlitereader.go's
// readonlyURI/chooseTable/quoteIdent verbatim (same read-only+immutable URI
// construction, same default-table rule) but are kept local to this package
// rather than promoted to an exported cross-package helper: sqlitereader's
// versions are unexported today, and duplicating ~20 lines is a smaller,
// safer blast radius for this task than changing sqlitereader's public
// surface (noted in the task report as a judgment call).

// sqliteReadonlyURI builds a read-only SQLite file URI, percent-encoding the
// URI-special characters so a path containing '#', '%', or '?' cannot
// corrupt the query string (which would silently drop mode=ro).
func sqliteReadonlyURI(path string) string {
	enc := strings.NewReplacer("%", "%25", "#", "%23", "?", "%3f").Replace(path)
	return "file:" + enc + "?mode=ro&immutable=1"
}

// sqliteQuoteIdent safely quotes a SQLite identifier (doubles embedded quotes).
func sqliteQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// sqliteChooseTable resolves which table to read: the requested one
// (validated), the sole user table, or an error listing the options.
func sqliteChooseTable(db *sql.DB, want string) (string, error) {
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
		return "", fmt.Errorf("multiple tables; choose one with Table: %s", strings.Join(tables, ", "))
	}
}

// sqliteTableColumns returns table's column names via PRAGMA table_info, in
// SQLite's natural (CREATE TABLE) column order.
func sqliteTableColumns(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query("PRAGMA table_info(" + sqliteQuoteIdent(table) + ")")
	if err != nil {
		return nil, fmt.Errorf("query: PRAGMA table_info(%s): %w", table, err)
	}
	defer rows.Close()

	resultCols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	nameIdx := -1
	for i, c := range resultCols {
		if c == "name" {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		return nil, fmt.Errorf("query: PRAGMA table_info(%s): no \"name\" column in result", table)
	}

	var names []string
	for rows.Next() {
		vals := make([]any, len(resultCols))
		ptrs := make([]any, len(resultCols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		name, _ := vals[nameIdx].(string)
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

// sqliteHasRowID reports whether table supports the _rowid_ pseudo-column
// (false for WITHOUT ROWID tables and views), mirroring sqlitereader's
// probe-then-fallback: attempt a zero-row _rowid_ query and treat a query
// error (SQLite fails "no such column: _rowid_" at prepare/step time, never
// deferred to iterating a LIMIT-0 result) as "no _rowid_".
func sqliteHasRowID(db *sql.DB, table string) bool {
	rows, err := db.Query("SELECT _rowid_ FROM " + sqliteQuoteIdent(table) + " LIMIT 0")
	if err != nil {
		return false
	}
	rows.Close()
	return true
}

// --- shared SQL builders ------------------------------------------------------

// columnList renders s.cols as a comma-joined, quoted SELECT list.
func (s *sqlBackend) columnList() string {
	parts := make([]string, len(s.cols))
	for i, c := range s.cols {
		parts[i] = sqliteQuoteIdent(c)
	}
	return strings.Join(parts, ", ")
}

// selectSQL is the base "SELECT <cols> FROM <table>[ ORDER BY _rowid_]"
// every cursor scan and windowed query starts from (spec §4).
func (s *sqlBackend) selectSQL() string {
	q := "SELECT " + s.columnList() + " FROM " + sqliteQuoteIdent(s.table)
	if s.hasRowID {
		q += " ORDER BY _rowid_"
	}
	return q
}

// scan runs the full, unwindowed selectSQL query and streams every row
// through fn, in _rowid_ order, checking ctx for cancellation every
// cancelCheckStride rows (the same constant/discipline rescanBackend.scan
// uses, rescan.go) -- the one shared loop newSQLBackend (initial
// profiling), Query's filtered path, Count's filtered path, and Export all
// build on.
func (s *sqlBackend) scan(ctx context.Context, fn scanFunc) error {
	rows, err := s.db.QueryContext(ctx, s.selectSQL())
	if err != nil {
		return fmt.Errorf("query: sqlBackend: query %s: %w", s.table, err)
	}
	defer rows.Close()

	vals := make([]any, len(s.cols))
	ptrs := make([]any, len(s.cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var idx int64
	for rows.Next() {
		if idx%cancelCheckStride == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("query: sqlBackend: scan %s: %w", s.table, err)
		}
		rec := make(map[string]any, len(s.cols))
		for i, c := range s.cols {
			if s.probeRaw {
				switch vals[i].(type) {
				case []byte, time.Time:
					// The conversion below is about to make this value
					// something SQLite never stored; remember that.
					s.noPush[c] = true
				}
			}
			rec[c] = readers.ToProfileValue(vals[i])
		}
		stop, ferr := fn(idx, rec)
		if ferr != nil {
			return ferr
		}
		if stop {
			return nil
		}
		idx++
	}
	return rows.Err()
}

// queryWindowSQL pushes LIMIT/OFFSET straight to SQL with bound params (the
// empty-filter fast path, spec §4): only ever called when the compiled
// filter matches everything, so no Go-side filtering is needed -- SQLite's
// own window IS the query's answer.
func (s *sqlBackend) queryWindowSQL(ctx context.Context, offset int64, limit int) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, s.selectSQL()+" LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query: sqlBackend: windowed query %s: %w", s.table, err)
	}
	defer rows.Close()

	vals := make([]any, len(s.cols))
	ptrs := make([]any, len(s.cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var recs []map[string]any
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("query: sqlBackend: scan %s: %w", s.table, err)
		}
		rec := make(map[string]any, len(s.cols))
		for i, c := range s.cols {
			rec[c] = readers.ToProfileValue(vals[i])
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return recs, nil
}

// rowCountSQL runs SELECT COUNT(*) -- always exact for a SQLite table (spec
// §4: "Count runs SELECT COUNT(*) ... (exact)").
func (s *sqlBackend) rowCountSQL(ctx context.Context) (int64, error) {
	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+sqliteQuoteIdent(s.table))
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("query: sqlBackend: count %s: %w", s.table, err)
	}
	return n, nil
}

// --- Backend interface ---------------------------------------------------

// taintedColumns reports which columns hold values SQLite stores differently
// from what shape shows -- see the noPush field.
func (s *sqlBackend) taintedColumns() map[string]bool { return s.noPush }

// sqliteDenseRowIDs reports whether _rowid_ runs 1..N with no gaps, which is
// what makes rowid-1 equal to a row's scan ordinal. O(1): both MIN and MAX
// come straight off the rowid B-tree.
func sqliteDenseRowIDs(db *sql.DB, table string) bool {
	row := db.QueryRow("SELECT COUNT(*)=MAX(_rowid_)-MIN(_rowid_)+1 AND MIN(_rowid_)=1 FROM " + sqliteQuoteIdent(table))
	var dense sql.NullBool
	if err := row.Scan(&dense); err != nil {
		return false
	}
	// An empty table scans NULL (MIN/MAX over no rows): nothing to window, so
	// treating it as non-dense costs nothing.
	return dense.Valid && dense.Bool
}

// sqliteConvertedDecltypeColumns lists columns whose DECLARED type makes the
// driver convert their values (DATE/DATETIME/TIMESTAMP become time.Time).
func sqliteConvertedDecltypeColumns(db *sql.DB, table string) []string {
	rows, err := db.Query("PRAGMA table_info(" + sqliteQuoteIdent(table) + ")")
	if err != nil {
		return nil
	}
	defer rows.Close()
	resultCols, err := rows.Columns()
	if err != nil {
		return nil
	}
	nameIdx, typeIdx := -1, -1
	for i, c := range resultCols {
		switch c {
		case "name":
			nameIdx = i
		case "type":
			typeIdx = i
		}
	}
	if nameIdx < 0 || typeIdx < 0 {
		return nil
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(resultCols))
		ptrs := make([]any, len(resultCols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return out
		}
		name, _ := vals[nameIdx].(string)
		decl, _ := vals[typeIdx].(string)
		switch strings.ToUpper(strings.TrimSpace(decl)) {
		case "DATE", "DATETIME", "TIMESTAMP":
			out = append(out, name)
		}
	}
	return out
}

// pushdownFor returns the parameterised WHERE for p's filter, or exact=false
// when anything about it cannot be pushed safely (sqlpushdown.go). A nil
// source AST -- a hand-built CompiledFilter, which this package's tests use to
// decorate predicates -- is "unknown", never "match everything".
func (s *sqlBackend) pushdownFor(cf *CompiledFilter) (string, []any, bool) {
	if s.disablePushdown || cf == nil || cf.src == nil {
		return "", nil, false
	}
	if isEmptyFilter(*cf.src) {
		return "", nil, false // the unfiltered fast path already handles this
	}
	return sqlPushdown(*cf.src, s.cm, s.noPush)
}

// pushedCountSQL runs COUNT(*) with a pushed WHERE.
func (s *sqlBackend) pushedCountSQL(ctx context.Context, where string, args []any) (int64, error) {
	q := "SELECT COUNT(*) FROM " + sqliteQuoteIdent(s.table) + " WHERE " + where
	var n int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("query: sqlBackend: pushed count %s: %w", s.table, err)
	}
	return n, nil
}

// pushedWindow runs the pushed WHERE with SQL LIMIT/OFFSET and returns each
// row together with its rowid, so the caller can reproduce Row.Index.
func (s *sqlBackend) pushedWindow(ctx context.Context, where string, args []any, offset int64, limit int) ([]int64, []map[string]any, error) {
	q := "SELECT _rowid_, " + s.columnList() + " FROM " + sqliteQuoteIdent(s.table) +
		" WHERE " + where + " ORDER BY _rowid_ LIMIT ? OFFSET ?"
	qargs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, q, qargs...)
	if err != nil {
		return nil, nil, fmt.Errorf("query: sqlBackend: pushed window %s: %w", s.table, err)
	}
	defer rows.Close()

	vals := make([]any, len(s.cols)+1)
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var ids []int64
	var recs []map[string]any
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, fmt.Errorf("query: sqlBackend: scan %s: %w", s.table, err)
		}
		id, _ := vals[0].(int64)
		rec := make(map[string]any, len(s.cols))
		for i, c := range s.cols {
			rec[c] = readers.ToProfileValue(vals[i+1])
		}
		ids = append(ids, id)
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return ids, recs, nil
}

// Columns returns the base ColumnModel built from PRAGMA table_info's
// column order (sourceOrder) joined with the profiling pass's type info.
func (s *sqlBackend) Columns() *ColumnModel { return s.cm }

// Profile returns the sidebar structure map computed by newSQLBackend's
// one-time profiling pass.
func (s *sqlBackend) Profile() profile.ProfileResult { return s.prof }

// RowCount returns SQLite's exact COUNT(*) (spec §4/§8: always exact for
// sqlBackend). A query failure -- including a cancelled ctx, which
// rowCountSQL's QueryRowContext rejects immediately -- reports (0,false)
// rather than panicking (RowCount never errors per the Backend interface's
// signature).
func (s *sqlBackend) RowCount(ctx context.Context) (n int64, exact bool) {
	n, err := s.rowCountSQL(ctx)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Query implements the split described on the sqlBackend doc comment:
// isMatchAllFilter (rescan.go) selects the SQL-LIMIT/OFFSET fast path or the
// Go-residual cursor-scan path.
func (s *sqlBackend) Query(ctx context.Context, p *CompiledPlan, w Window, wantTotal bool) (RowSet, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return RowSet{}, err
	}
	if p == nil {
		return RowSet{}, fmt.Errorf("query: sqlBackend.Query: nil CompiledPlan")
	}

	limit := w.Limit
	if limit < 0 {
		limit = 0
	}
	offset := w.Offset
	if offset < 0 {
		offset = 0
	}

	if p.Sort != nil {
		return s.querySorted(ctx, p, offset, limit, w, start)
	}

	if isMatchAllFilter(p.Filter) {
		return s.queryUnfiltered(ctx, p, w, offset, limit, wantTotal, start)
	}
	// A filter SQLite can evaluate exactly lets the window and the count go
	// to the database instead of streaming every row through Go. The row
	// window additionally needs dense rowids, because Row.Index is the
	// ABSOLUTE scan ordinal and only rowid-1 reproduces it; the COUNT does
	// not, so a non-dense table still gets the cheaper total below.
	if where, args, exact := s.pushdownFor(p.Filter); exact {
		if s.hasRowID && s.denseRowIDs {
			return s.queryPushed(ctx, p, w, offset, limit, wantTotal, where, args, start)
		}
		if wantTotal {
			return s.queryFilteredWithPushedTotal(ctx, p, w, offset, limit, where, args, start)
		}
	}
	return s.queryFiltered(ctx, p, w, offset, limit, wantTotal, start)
}

// queryUnfiltered is the empty-filter fast path: LIMIT/OFFSET pushed to SQL
// (bound params), native random access -- no scan of skipped rows at all.
func (s *sqlBackend) queryUnfiltered(ctx context.Context, p *CompiledPlan, w Window, offset int64, limit int, wantTotal bool, start time.Time) (RowSet, error) {
	recs, err := s.queryWindowSQL(ctx, offset, limit)
	if err != nil {
		return RowSet{}, err
	}
	rows := make([]Row, len(recs))
	for i, rec := range recs {
		rows[i] = p.Transform.Project(rec, offset+int64(i))
	}

	rs := RowSet{
		Columns:   p.Transform.Columns(),
		Rows:      rows,
		Offset:    w.Offset,
		Scanned:   offset + int64(len(rows)),
		Truncated: len(rows) < limit,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	if wantTotal {
		// s.rowCountSQL(ctx), not s.RowCount(ctx): RowCount collapses any
		// error (including a cancelled ctx) to (0,false), but a
		// cancelled/timed-out COUNT here must propagate ctx.Err() itself out
		// of Query, the same way queryWindowSQL's own ctx-bound query already
		// does above.
		n, err := s.rowCountSQL(ctx)
		if err != nil {
			return RowSet{}, err
		}
		rs.Total = n
		rs.TotalExact = true
	} else {
		rs.Total = -1
		rs.TotalExact = false
	}
	return rs, nil
}

// querySorted serves a sorted window with the shared Go comparator over the
// _rowid_-ordered cursor scan (which yields idx == the absolute scan ordinal,
// exactly what getCell/edit/stats key on). This is EXACT and cross-tier
// identical, and it deliberately does NOT push ORDER BY to SQLite: a pushed
// reorder would reproduce Row.Index only as rowid-1 on a dense-rowid table and
// would silently stamp the sorted RANK otherwise (the E9 plan-review C2). The
// SQL ORDER BY pushdown is a follow-up perf optimization, not correctness.
// Row.Index STAYS the absolute ordinal (Project(rec, m.idx)).
func (s *sqlBackend) querySorted(ctx context.Context, p *CompiledPlan, offset int64, limit int, w Window, start time.Time) (RowSet, error) {
	type ir struct {
		idx int64
		rec any
	}
	var matches []ir
	err := s.scan(ctx, func(idx int64, rec any) (bool, error) {
		if p.Filter.Match(rec) {
			matches = append(matches, ir{idx, rec})
		}
		return false, nil
	})
	if err != nil {
		return RowSet{}, err
	}
	sort.SliceStable(matches, func(a, b int) bool {
		return p.Sort.Less(matches[a].rec, matches[a].idx, matches[b].rec, matches[b].idx)
	})
	lo := offset
	if lo > int64(len(matches)) {
		lo = int64(len(matches))
	}
	hi := offset + int64(limit)
	if hi > int64(len(matches)) {
		hi = int64(len(matches))
	}
	rows := make([]Row, 0, hi-lo)
	for _, m := range matches[lo:hi] {
		rows = append(rows, p.Transform.Project(m.rec, m.idx))
	}
	return RowSet{
		Columns:    p.Transform.Columns(),
		Rows:       rows,
		Offset:     w.Offset,
		Scanned:    int64(len(matches)),
		Truncated:  hi < int64(len(matches)),
		ElapsedMs:  time.Since(start).Milliseconds(),
		Total:      int64(len(matches)),
		TotalExact: true,
	}, nil
}

// queryFiltered is the non-empty-filter path (spec §4's critical subtlety):
// LIMIT/OFFSET cannot be pushed to SQL because the Go predicate filters
// AFTER any SQL-side window would already have applied. Instead every row is
// streamed via one _rowid_-ordered cursor; Offset/Limit apply to the MATCH
// sequence, not raw row position -- the SAME algorithm as
// rescanBackend.Query (rescan.go), required for the cross-backend row-
// identity invariant (spec §9). !wantTotal early-stops once the window is
// full (mirrors rescanBackend); wantTotal forces a full scan, and -- unlike
// rescanBackend, which never asserts Query-time exactness even on a full
// scan -- sqlBackend's matched count from an uninterrupted full table scan
// IS the exact filtered total (no sampling/estimation anywhere in this
// backend), so TotalExact is true whenever wantTotal is true.
func (s *sqlBackend) queryFiltered(ctx context.Context, p *CompiledPlan, w Window, offset int64, limit int, wantTotal bool, start time.Time) (RowSet, error) {
	rows := make([]Row, 0, limit)
	var scanned int64
	var matched int64

	err := s.scan(ctx, func(idx int64, rec any) (bool, error) {
		scanned = idx + 1
		if p.Filter.Match(rec) {
			matched++
			if matched > offset && int64(len(rows)) < int64(limit) {
				rows = append(rows, p.Transform.Project(rec, idx))
			}
		}
		if !wantTotal && int64(len(rows)) >= int64(limit) {
			return true, nil // early-stop: window full, caller does not need a total
		}
		return false, nil
	})
	if err != nil {
		return RowSet{}, err
	}

	rs := RowSet{
		Columns:   p.Transform.Columns(),
		Rows:      rows,
		Offset:    w.Offset,
		Scanned:   scanned,
		Truncated: int64(len(rows)) < int64(limit),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	if wantTotal {
		rs.Total = matched
		rs.TotalExact = true
	} else {
		rs.Total = -1
		rs.TotalExact = false
	}
	return rs, nil
}

// queryPushed serves a window entirely from SQLite: the WHERE, the ordering
// and the LIMIT/OFFSET all run in the database, and Go only projects the rows
// that come back.
//
// Row.Index is rowid-1, NOT offset+i: Index is the absolute record ordinal
// (columns.go) that the table renders as its row-number gutter, and the
// caller has already checked denseRowIDs, which is exactly the condition
// under which those two agree.
func (s *sqlBackend) queryPushed(ctx context.Context, p *CompiledPlan, w Window, offset int64, limit int, wantTotal bool, where string, args []any, start time.Time) (RowSet, error) {
	ids, recs, err := s.pushedWindow(ctx, where, args, offset, limit)
	if err != nil {
		return RowSet{}, err
	}
	rows := make([]Row, 0, len(recs))
	for i, rec := range recs {
		rows = append(rows, p.Transform.Project(rec, ids[i]-1))
	}

	rs := RowSet{
		Columns: p.Transform.Columns(),
		Rows:    rows,
		Offset:  w.Offset,
		// "rows SQLite handed back", the same convention queryUnfiltered
		// uses -- not "rows examined", which the database no longer reports.
		Scanned:   offset + int64(len(rows)),
		Truncated: int64(len(rows)) < int64(limit),
		ElapsedMs: time.Since(start).Milliseconds(),
		Total:     -1,
	}
	if wantTotal {
		n, cerr := s.pushedCountSQL(ctx, where, args)
		if cerr != nil {
			return RowSet{}, cerr
		}
		rs.Total = n
		rs.TotalExact = true
	}
	return rs, nil
}

// queryFilteredWithPushedTotal is the middle case: the filter is exact but the
// table cannot supply a usable ordinal (no rowid, or gaps in it), so the rows
// still come from the Go cursor while the TOTAL -- which is order-independent
// and needs no ordinal -- is a single COUNT(*).
func (s *sqlBackend) queryFilteredWithPushedTotal(ctx context.Context, p *CompiledPlan, w Window, offset int64, limit int, where string, args []any, start time.Time) (RowSet, error) {
	rs, err := s.queryFiltered(ctx, p, w, offset, limit, false, start)
	if err != nil {
		return RowSet{}, err
	}
	n, cerr := s.pushedCountSQL(ctx, where, args)
	if cerr != nil {
		return RowSet{}, cerr
	}
	rs.Total = n
	rs.TotalExact = true
	return rs, nil
}

// Count returns the exact number of records matching f: SELECT COUNT(*) for
// an empty/match-all filter (spec §4's fast path), else a full cancellable
// cursor scan applying the Go predicate (spec §4: "non-empty -> stream+Go-
// count (exact, cancellable)").
func (s *sqlBackend) Count(ctx context.Context, f *CompiledFilter) (total int64, exact bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if isMatchAllFilter(f) {
		n, cerr := s.rowCountSQL(ctx)
		if cerr != nil {
			return 0, false, cerr
		}
		return n, true, nil
	}
	// The aggregate is where the pushdown pays: set-based, order-independent
	// and needing no ordinal, so it applies whenever the filter is exact --
	// no rowid requirement at all.
	if where, args, exact := s.pushdownFor(f); exact {
		n, cerr := s.pushedCountSQL(ctx, where, args)
		if cerr != nil {
			return 0, false, cerr
		}
		return n, true, nil
	}
	var matched int64
	scanErr := s.scan(ctx, func(_ int64, rec any) (bool, error) {
		if f.Match(rec) {
			matched++
		}
		return false, nil
	})
	if scanErr != nil {
		return 0, false, scanErr
	}
	return matched, true, nil
}

// Export streams every matching, projected row through enc via the same
// full cursor scan Count/queryFiltered use, in _rowid_ order, regardless of
// any interactive-tier window (spec §4/§8: export is never capped).
func (s *sqlBackend) Export(ctx context.Context, p *CompiledPlan, enc RowEncoder) (rows int64, err error) {
	if p == nil {
		return 0, fmt.Errorf("query: sqlBackend.Export: nil CompiledPlan")
	}
	if enc == nil {
		return 0, fmt.Errorf("query: sqlBackend.Export: nil RowEncoder")
	}
	// The pushed path needs BOTH gates, for two different reasons:
	//   - hasRowID, because without a rowid there is no ORDER BY at all and a
	//     WHERE lets SQLite switch to an index scan, reordering the result;
	//   - denseRowIDs, because Encode's index is the absolute record ordinal
	//     and rowid-1 only equals it when the rowids have no gaps. Today's
	//     encoders happen to ignore that argument, but a path that quietly
	//     emits a different ordinal than every other backend is a trap for
	//     the first one that does not.
	if where, args, exact := s.pushdownFor(p.Filter); exact && s.hasRowID && s.denseRowIDs {
		return s.exportPushed(ctx, p, enc, where, args)
	}
	var n int64
	buf := make([]any, p.Transform.Len()) // reused per record; see RowEncoder
	scanErr := s.scan(ctx, func(idx int64, rec any) (bool, error) {
		if !p.Filter.Match(rec) {
			return false, nil
		}
		if err := enc.Encode(idx, p.Transform.ProjectValues(rec, buf)); err != nil {
			return true, err
		}
		n++
		return false, nil
	})
	if scanErr != nil {
		return n, scanErr
	}
	return n, nil
}

// exportPushed streams every matching row from a single WHERE-filtered,
// rowid-ordered cursor.
func (s *sqlBackend) exportPushed(ctx context.Context, p *CompiledPlan, enc RowEncoder, where string, args []any) (int64, error) {
	q := "SELECT _rowid_, " + s.columnList() + " FROM " + sqliteQuoteIdent(s.table) +
		" WHERE " + where + " ORDER BY _rowid_"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("query: sqlBackend: pushed export %s: %w", s.table, err)
	}
	defer rows.Close()

	vals := make([]any, len(s.cols)+1)
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	buf := make([]any, p.Transform.Len())
	var n int64
	for rows.Next() {
		if n%cancelCheckStride == 0 {
			if err := ctx.Err(); err != nil {
				return n, err
			}
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, fmt.Errorf("query: sqlBackend: scan %s: %w", s.table, err)
		}
		id, _ := vals[0].(int64)
		rec := make(map[string]any, len(s.cols))
		for i, c := range s.cols {
			rec[c] = readers.ToProfileValue(vals[i+1])
		}
		if err := enc.Encode(id-1, p.Transform.ProjectValues(rec, buf)); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, nil
}

// GetCell returns the full value at segs in the record at absolute ordinal
// index. It fetches exactly that one row with the same LIMIT/OFFSET-over-
// selectSQL idiom queryWindowSQL uses (SELECT ... ORDER BY _rowid_ LIMIT 1
// OFFSET index), so index addresses the same row the table rendered at that
// Row.Index in EVERY Query path (queryWindowSQL's offset+i, queryFiltered's
// scan idx, and -- on a dense-rowid table -- queryPushed's rowid-1). An index
// past the last row is an error.
func (s *sqlBackend) GetCell(ctx context.Context, index int64, segs []Seg) (json.RawMessage, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if index < 0 {
		return nil, false, fmt.Errorf("query: sqlBackend.GetCell: negative index %d", index)
	}
	rec, ok, err := s.recordAt(ctx, index)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, fmt.Errorf("query: sqlBackend.GetCell: index %d past end of table", index)
	}
	return resolveFullCell(rec, segs)
}

// recordAt fetches the single record at absolute scan ordinal index -- the
// index-th row in _rowid_ order. found is false when index is past the last
// row (no error: EOF is the caller's out-of-range signal).
func (s *sqlBackend) recordAt(ctx context.Context, index int64) (map[string]any, bool, error) {
	rows, err := s.db.QueryContext(ctx, s.selectSQL()+" LIMIT 1 OFFSET ?", index)
	if err != nil {
		return nil, false, fmt.Errorf("query: sqlBackend: getcell query %s: %w", s.table, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, false, fmt.Errorf("query: sqlBackend: getcell %s: %w", s.table, err)
		}
		return nil, false, nil
	}
	vals := make([]any, len(s.cols))
	ptrs := make([]any, len(s.cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, false, fmt.Errorf("query: sqlBackend: getcell scan %s: %w", s.table, err)
	}
	rec := make(map[string]any, len(s.cols))
	for i, c := range s.cols {
		rec[c] = readers.ToProfileValue(vals[i])
	}
	return rec, true, nil
}

// StreamRecords streams every row as a flat map[string]any (SQLite rows are
// tabular), in _rowid_ order, via the shared scan loop.
func (s *sqlBackend) StreamRecords(ctx context.Context, fn func(index int64, rec any) error) error {
	return s.scan(ctx, func(idx int64, rec any) (bool, error) {
		return false, fn(idx, rec)
	})
}

// Close closes the read-only connection. Safe to call once; further calls
// to the other Backend methods afterward are not guaranteed to work (same
// contract as Backend.Close's doc comment, backend.go).
func (s *sqlBackend) Close() error {
	return s.db.Close()
}
