package query

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// assertRowSetEqual compares two RowSets field by field, zeroing ONLY the two
// fields that legitimately differ between the pushed and the Go path:
// ElapsedMs (wall clock) and Scanned (whose whole point is to drop when the
// database does the filtering). Everything else -- Columns, every Row
// INCLUDING Row.Index, Offset, Total, TotalExact, Truncated, ColumnsTruncated,
// TotalPaths -- must match exactly, and any field added to RowSet later is
// compared automatically rather than silently skipped.
func assertRowSetEqual(t *testing.T, got, want RowSet, context string) {
	t.Helper()
	g, w := got, want
	g.ElapsedMs, w.ElapsedMs = 0, 0
	g.Scanned, w.Scanned = 0, 0
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("%s: pushed path differs from the Go path\npushed: %+v\ngo:     %+v", context, g, w)
	}
}

// pushdownFixture opens one sqlite fixture twice: once normally (pushdown
// live) and once with the seam disabled (the pre-E5 Go path), so every case
// can be run both ways and compared.
func pushdownFixture(t *testing.T, createSQL string, inserts ...string) (pushed, gopath *sqlBackend) {
	t.Helper()
	path := makeSQLiteFixture(t, createSQL, inserts...)
	for _, target := range []**sqlBackend{&pushed, &gopath} {
		sb, err := newSQLBackend(context.Background(), path, "")
		if err != nil {
			t.Fatalf("newSQLBackend: %v", err)
		}
		t.Cleanup(func() { _ = sb.Close() })
		*target = sb
	}
	gopath.disablePushdown = true
	return pushed, gopath
}

// TestSQLBackend_PushedPathMatchesTheGoPath is E5's central safety proof: an
// optimisation that changes RESULTS is worse than no optimisation, so every
// pushable filter is run BOTH ways against the same fixture and the RowSets
// must be identical.
//
// The fixture is deliberately hostile. On a dense, contiguous, all-TEXT table
// every wrong implementation survives.
func TestSQLBackend_PushedPathMatchesTheGoPath(t *testing.T) {
	pushed, gopath := pushdownFixture(t,
		`CREATE TABLE t (
			id INTEGER,
			name TEXT COLLATE NOCASE,
			pad TEXT COLLATE RTRIM,
			score REAL,
			big INTEGER
		)`,
		// Row 3 is deleted below, so the rowids are SPARSE: an implementation
		// that uses offset+i or a naive rowid-1 diverges here and only here.
		`INSERT INTO t VALUES (1,'Apple','abc   ',1.5, 9007199254740992)`,
		`INSERT INTO t VALUES (2,'apple','abc',2.5, 9007199254740993)`,
		`INSERT INTO t VALUES (3,'gone','gone',0, 0)`,
		`INSERT INTO t VALUES (4,'BANANA','zz',3.5, 42)`,
		`INSERT INTO t VALUES (5,'cherry','q',4.5, 7)`,
		`DELETE FROM t WHERE id = 3`,
	)

	cases := []struct {
		name string
		f    Filter
		w    Window
	}{
		{"numeric eq", oneCond(Condition{Path: "id", Op: OpEq, Value: num(2)}), Window{Limit: 10}},
		{"numeric range", oneCond(Condition{Path: "id", Op: OpGte, Value: num(2)}), Window{Limit: 10}},
		{"numeric range windowed", oneCond(Condition{Path: "id", Op: OpGte, Value: num(1)}), Window{Offset: 1, Limit: 2}},
		// A NOCASE column: SQL '=' would match both 'Apple' and 'apple'
		// without COLLATE BINARY, while the engine is byte-exact.
		{"string eq on a NOCASE column", oneCond(Condition{Path: "name", Op: OpEq, Value: str("apple")}), Window{Limit: 10}},
		{"string in on a NOCASE column", oneCond(Condition{Path: "name", Op: OpIn, Value: inList(str("apple"), str("BANANA"))}), Window{Limit: 10}},
		// An RTRIM column: SQL '=' would match the padded value too.
		{"string eq on an RTRIM column", oneCond(Condition{Path: "pad", Op: OpEq, Value: str("abc")}), Window{Limit: 10}},
		{"contains", oneCond(Condition{Path: "name", Op: OpContains, Value: str("an")}), Window{Limit: 10}},
		{"isnull", oneCond(Condition{Path: "name", Op: OpIsNull}), Window{Limit: 10}},
		{"notnull", oneCond(Condition{Path: "id", Op: OpNotNull}), Window{Limit: 10}},
		{"float compare", oneCond(Condition{Path: "score", Op: OpGt, Value: num(2)}), Window{Limit: 10}},
		{"and group", Filter{Combinator: And, Conditions: []Condition{
			{Path: "id", Op: OpGte, Value: num(2)},
			{Path: "name", Op: OpContains, Value: str("a")},
		}}, Window{Limit: 10}},
		{"or group", Filter{Combinator: Or, Conditions: []Condition{
			{Path: "id", Op: OpEq, Value: num(1)},
			{Path: "id", Op: OpEq, Value: num(5)},
		}}, Window{Limit: 10}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cf, err := CompileFilter(tc.f, pushed.Columns())
			if err != nil {
				t.Fatalf("CompileFilter: %v", err)
			}
			// PER-CASE precondition: without it, a case that silently fails to
			// push still "passes" (both runs took the Go path and are
			// trivially equal), and an aggregate counter elsewhere would hide
			// it.
			if _, _, exact := pushed.pushdownFor(cf); !exact {
				t.Fatalf("case %q is not pushable against the real fixture -- this row proves nothing", tc.name)
			}

			plan, err := CompilePlan(tc.f, Transform{}, pushed.Columns())
			if err != nil {
				t.Fatalf("CompilePlan: %v", err)
			}
			goPlan, err := CompilePlan(tc.f, Transform{}, gopath.Columns())
			if err != nil {
				t.Fatalf("CompilePlan: %v", err)
			}

			for _, wantTotal := range []bool{false, true} {
				gotRS, err := pushed.Query(context.Background(), plan, tc.w, wantTotal)
				if err != nil {
					t.Fatalf("pushed Query: %v", err)
				}
				wantRS, err := gopath.Query(context.Background(), goPlan, tc.w, wantTotal)
				if err != nil {
					t.Fatalf("go Query: %v", err)
				}
				assertRowSetEqual(t, gotRS, wantRS, fmt.Sprintf("wantTotal=%v", wantTotal))
			}

			gotN, gotExact, err := pushed.Count(context.Background(), cf)
			if err != nil {
				t.Fatalf("pushed Count: %v", err)
			}
			wantN, wantExact, err := gopath.Count(context.Background(), cf)
			if err != nil {
				t.Fatalf("go Count: %v", err)
			}
			if gotN != wantN || gotExact != wantExact {
				t.Fatalf("Count = (%d,%v), Go path = (%d,%v)", gotN, gotExact, wantN, wantExact)
			}

			gotEnc, wantEnc := &collectEncoder{}, &collectEncoder{}
			if _, err := pushed.Export(context.Background(), plan, gotEnc); err != nil {
				t.Fatalf("pushed Export: %v", err)
			}
			if _, err := gopath.Export(context.Background(), goPlan, wantEnc); err != nil {
				t.Fatalf("go Export: %v", err)
			}
			if !reflect.DeepEqual(gotEnc.rows, wantEnc.rows) {
				t.Fatalf("Export rows differ\npushed: %+v\ngo:     %+v", gotEnc.rows, wantEnc.rows)
			}
		})
	}
}

// TestSQLBackend_BigIntegersAreNotPushed covers the 2^53 boundary end to end:
// the two `big` values differ by one but share a float64, so a pushed
// comparison would match a different set than the Go predicate. The planner
// must refuse it, and the result must therefore still be the Go answer.
func TestSQLBackend_BigIntegersAreNotPushed(t *testing.T) {
	pushed, gopath := pushdownFixture(t,
		`CREATE TABLE t (id INTEGER, big INTEGER)`,
		`INSERT INTO t VALUES (1, 9007199254740992)`,
		`INSERT INTO t VALUES (2, 9007199254740993)`,
	)
	f := oneCond(Condition{Path: "big", Op: OpEq, Value: num(9007199254740992)})
	cf, err := CompileFilter(f, pushed.Columns())
	if err != nil {
		t.Fatalf("CompileFilter: %v", err)
	}
	if _, _, exact := pushed.pushdownFor(cf); exact {
		t.Fatalf("a 2^53 operand was pushed; SQLite compares it exactly and the engine does not")
	}

	plan, _ := CompilePlan(f, Transform{}, pushed.Columns())
	goPlan, _ := CompilePlan(f, Transform{}, gopath.Columns())
	got, err := pushed.Query(context.Background(), plan, Window{Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want, err := gopath.Query(context.Background(), goPlan, Window{Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	assertRowSetEqual(t, got, want, "2^53")
}

// TestSQLBackend_TaintedColumnsAreNotPushed covers the storage-mismatch gate
// on a real table: a BLOB column and a DATE column both hold values the driver
// rewrites before the engine ever sees them.
func TestSQLBackend_TaintedColumnsAreNotPushed(t *testing.T) {
	pushed, gopath := pushdownFixture(t,
		`CREATE TABLE t (id INTEGER, b BLOB, d DATE, plain TEXT)`,
		`INSERT INTO t VALUES (1, x'6162', '2024-01-01', 'keep')`,
		`INSERT INTO t VALUES (2, x'6364', '2024-01-02', 'drop')`,
	)

	if !pushed.taintedColumns()["b"] {
		t.Fatalf("a BLOB column was not flagged: %v", pushed.taintedColumns())
	}
	if !pushed.taintedColumns()["d"] {
		t.Fatalf("a DATE column was not flagged: %v", pushed.taintedColumns())
	}
	if pushed.taintedColumns()["plain"] {
		t.Fatalf("an ordinary TEXT column must not be flagged")
	}

	// The values shape SHOWS for these columns are the converted ones, so a
	// filter built from the grid uses them -- and must still return the Go
	// answer rather than SQLite's.
	for _, tc := range []struct {
		name string
		f    Filter
	}{
		{"blob eq", oneCond(Condition{Path: "b", Op: OpEq, Value: str("ab")})},
		{"date eq", oneCond(Condition{Path: "d", Op: OpEq, Value: str("2024-01-01T00:00:00Z")})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cf, err := CompileFilter(tc.f, pushed.Columns())
			if err != nil {
				t.Fatalf("CompileFilter: %v", err)
			}
			if _, _, exact := pushed.pushdownFor(cf); exact {
				t.Fatalf("a tainted column was pushed")
			}
			plan, _ := CompilePlan(tc.f, Transform{}, pushed.Columns())
			goPlan, _ := CompilePlan(tc.f, Transform{}, gopath.Columns())
			got, err := pushed.Query(context.Background(), plan, Window{Limit: 10}, true)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			want, err := gopath.Query(context.Background(), goPlan, Window{Limit: 10}, true)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			assertRowSetEqual(t, got, want, tc.name)
		})
	}
}

// TestSQLBackend_WithoutRowIDKeepsTheGoWindow: with no _rowid_ there is no
// ORDER BY, so a pushed WHERE lets SQLite pick an index and return a
// DIFFERENT window for the same LIMIT. The rows must still come from the Go
// cursor -- while the total may still be pushed, since a COUNT is
// order-independent.
func TestSQLBackend_WithoutRowIDKeepsTheGoWindow(t *testing.T) {
	pushed, gopath := pushdownFixture(t,
		`CREATE TABLE t (k TEXT PRIMARY KEY, v INTEGER) WITHOUT ROWID;
		 CREATE INDEX iv ON t(v)`,
		`INSERT INTO t VALUES ('b', 9)`,
		`INSERT INTO t VALUES ('c', 1)`,
		`INSERT INTO t VALUES ('a', 5)`,
		`INSERT INTO t VALUES ('e', 3)`,
		`INSERT INTO t VALUES ('d', 7)`,
	)
	if pushed.hasRowID {
		t.Fatalf("fixture precondition: the table must be WITHOUT ROWID")
	}

	f := oneCond(Condition{Path: "v", Op: OpGte, Value: num(1)})
	plan, _ := CompilePlan(f, Transform{}, pushed.Columns())
	goPlan, _ := CompilePlan(f, Transform{}, gopath.Columns())

	for _, w := range []Window{{Limit: 2}, {Offset: 1, Limit: 2}, {Limit: 10}} {
		got, err := pushed.Query(context.Background(), plan, w, true)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		want, err := gopath.Query(context.Background(), goPlan, w, true)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		assertRowSetEqual(t, got, want, fmt.Sprintf("window %+v", w))
	}
}

// TestSQLBackend_HandBuiltCompiledFilterIsNeverPushed: a CompiledFilter built
// by hand (this package's own tests decorate predicates that way) carries no
// source AST. Treating that as the zero Filter would push a WHERE-less query
// and return EVERY row.
func TestSQLBackend_HandBuiltCompiledFilterIsNeverPushed(t *testing.T) {
	pushed, _ := pushdownFixture(t,
		`CREATE TABLE t (id INTEGER)`,
		`INSERT INTO t VALUES (1)`,
		`INSERT INTO t VALUES (2)`,
	)
	hand := &CompiledFilter{pred: func(rec any) bool {
		m, _ := rec.(map[string]any)
		return fmt.Sprint(m["id"]) == "1"
	}}
	if _, _, exact := pushed.pushdownFor(hand); exact {
		t.Fatalf("a hand-built CompiledFilter was pushed")
	}
	n, _, err := pushed.Count(context.Background(), hand)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count = %d, want 1 -- the predicate must still decide", n)
	}
}

// TestSQLBackend_PushdownActuallyRuns guards against the whole feature being
// silently dead: the seam must change how much work the Go side does.
func TestSQLBackend_PushdownActuallyRuns(t *testing.T) {
	var inserts []string
	for i := 1; i <= 200; i++ {
		inserts = append(inserts, fmt.Sprintf(`INSERT INTO t VALUES (%d)`, i))
	}
	pushed, gopath := pushdownFixture(t, `CREATE TABLE t (id INTEGER)`, inserts...)

	f := oneCond(Condition{Path: "id", Op: OpLte, Value: num(3)})
	plan, _ := CompilePlan(f, Transform{}, pushed.Columns())
	goPlan, _ := CompilePlan(f, Transform{}, gopath.Columns())

	got, err := pushed.Query(context.Background(), plan, Window{Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want, err := gopath.Query(context.Background(), goPlan, Window{Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	assertRowSetEqual(t, got, want, "selective filter")

	// The Go path streams all 200 rows; the pushed path only sees the 3 that
	// matched. Mutation that must break it: force exact=false -> both scan
	// everything and the numbers are equal.
	if !(got.Scanned < want.Scanned) {
		t.Fatalf("pushed Scanned = %d, Go Scanned = %d -- the pushdown is not actually running",
			got.Scanned, want.Scanned)
	}
}

// TestSQLBackend_PushedWindowKeepsAbsoluteRowIndex exercises queryPushed
// itself, which the sparse-rowid fixture above deliberately cannot: there,
// denseRowIDs is false and the window falls back to the Go cursor, so a wrong
// ordinal inside queryPushed would sit in dead code.
//
// Here the table IS dense and the filter matches every OTHER row, so the
// matched rows' absolute ordinals (0,2,4,...) differ from their positions
// within the match sequence (0,1,2,...) -- the exact distinction Row.Index
// carries and the row-number gutter renders.
//
// Mutation that must break it: project with offset+i instead of rowid-1.
func TestSQLBackend_PushedWindowKeepsAbsoluteRowIndex(t *testing.T) {
	var inserts []string
	for i := 1; i <= 10; i++ {
		inserts = append(inserts, fmt.Sprintf(`INSERT INTO t VALUES (%d, %d)`, i, i%2))
	}
	pushed, gopath := pushdownFixture(t, `CREATE TABLE t (id INTEGER, odd INTEGER)`, inserts...)
	if !pushed.denseRowIDs {
		t.Fatalf("fixture precondition: rowids must be dense so queryPushed actually runs")
	}

	f := oneCond(Condition{Path: "odd", Op: OpEq, Value: num(1)})
	cf, err := CompileFilter(f, pushed.Columns())
	if err != nil {
		t.Fatalf("CompileFilter: %v", err)
	}
	if _, _, exact := pushed.pushdownFor(cf); !exact {
		t.Fatalf("precondition: this filter must be pushable")
	}
	plan, _ := CompilePlan(f, Transform{}, pushed.Columns())
	goPlan, _ := CompilePlan(f, Transform{}, gopath.Columns())

	for _, w := range []Window{{Limit: 10}, {Offset: 1, Limit: 2}, {Offset: 3, Limit: 5}} {
		got, err := pushed.Query(context.Background(), plan, w, true)
		if err != nil {
			t.Fatalf("pushed Query: %v", err)
		}
		want, err := gopath.Query(context.Background(), goPlan, w, true)
		if err != nil {
			t.Fatalf("go Query: %v", err)
		}
		assertRowSetEqual(t, got, want, fmt.Sprintf("dense window %+v", w))

		// Spelled out, so the intent survives even if RowSet grows a field:
		// the indexes are the ABSOLUTE ordinals of the matching rows.
		for i, r := range got.Rows {
			wantIdx := (w.Offset + int64(i)) * 2 // rows 0,2,4,... are the odd ones
			if r.Index != wantIdx {
				t.Fatalf("window %+v row %d Index = %d, want %d (absolute ordinal, not match position)",
					w, i, r.Index, wantIdx)
			}
		}
	}
}

// TestSQLBackend_NonRoundTrippingColumnKeepsTheGoAnswer is the I1 regression at
// the backend level: a real SQLite column named "x." would, without the
// round-trip guard, be pushed as a quoted identifier matching the real column,
// while the Go predicate resolves the map key "x" and finds nothing -- so the
// pushed path returned rows the Go path did not (verified: pushed=1, go=0).
func TestSQLBackend_NonRoundTrippingColumnKeepsTheGoAnswer(t *testing.T) {
	pushed, gopath := pushdownFixture(t,
		`CREATE TABLE t ("x." TEXT, v INTEGER)`,
		`INSERT INTO t VALUES ('a', 1)`,
		`INSERT INTO t VALUES ('b', 2)`,
		`INSERT INTO t VALUES ('a', 3)`,
	)
	f := oneCond(Condition{Path: "x.", Op: OpEq, Value: str("a")})
	cf, err := CompileFilter(f, pushed.Columns())
	if err != nil {
		t.Fatalf("CompileFilter: %v", err)
	}
	if _, _, exact := pushed.pushdownFor(cf); exact {
		t.Fatalf(`the column "x." was pushed; its name does not round-trip through parsePath`)
	}
	plan, _ := CompilePlan(f, Transform{}, pushed.Columns())
	goPlan, _ := CompilePlan(f, Transform{}, gopath.Columns())
	got, err := pushed.Query(context.Background(), plan, Window{Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	want, err := gopath.Query(context.Background(), goPlan, Window{Limit: 10}, true)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	assertRowSetEqual(t, got, want, `column "x."`)
}

// TestSQLBackend_PushedExportAndWindowAreRowidOrdered closes the I3/M9 test
// gaps: the central proof runs on a SPARSE fixture, where denseRowIDs is false
// and neither exportPushed nor the pushed Query window is reached, so their
// ORDER BY _rowid_ was unpinned. This fixture is DENSE with a secondary index
// whose scan order differs from rowid order, so dropping the ORDER BY (or
// emitting a wrong ordinal) reorders the result and diverges from the Go path.
func TestSQLBackend_PushedExportAndWindowAreRowidOrdered(t *testing.T) {
	// v runs opposite to rowid, so an index scan on v would reverse the rows.
	var inserts []string
	for i := 1; i <= 10; i++ {
		inserts = append(inserts, fmt.Sprintf(`INSERT INTO t VALUES (%d, %d)`, i, 100-i))
	}
	pushed, gopath := pushdownFixture(t,
		`CREATE TABLE t (id INTEGER, v INTEGER);
		 CREATE INDEX iv ON t(v)`, inserts...)
	if !pushed.hasRowID || !pushed.denseRowIDs {
		t.Fatalf("fixture precondition: the table must be dense WITH-ROWID so the pushed paths run")
	}

	f := oneCond(Condition{Path: "v", Op: OpLte, Value: num(100)}) // matches all, but via a pushed WHERE
	cf, err := CompileFilter(f, pushed.Columns())
	if err != nil {
		t.Fatalf("CompileFilter: %v", err)
	}
	if _, _, exact := pushed.pushdownFor(cf); !exact {
		t.Fatalf("precondition: the filter must be pushable")
	}
	plan, _ := CompilePlan(f, Transform{}, pushed.Columns())
	goPlan, _ := CompilePlan(f, Transform{}, gopath.Columns())

	// Windowed Query must return the rowid-ordered window, not the index order.
	for _, w := range []Window{{Limit: 3}, {Offset: 2, Limit: 3}, {Limit: 10}} {
		got, err := pushed.Query(context.Background(), plan, w, true)
		if err != nil {
			t.Fatalf("pushed Query: %v", err)
		}
		want, err := gopath.Query(context.Background(), goPlan, w, true)
		if err != nil {
			t.Fatalf("go Query: %v", err)
		}
		assertRowSetEqual(t, got, want, fmt.Sprintf("window %+v", w))
	}

	// Export must stream in rowid order and carry rowid-1 as the index.
	gotEnc, wantEnc := &collectEncoder{}, &collectEncoder{}
	if _, err := pushed.Export(context.Background(), plan, gotEnc); err != nil {
		t.Fatalf("pushed Export: %v", err)
	}
	if _, err := gopath.Export(context.Background(), goPlan, wantEnc); err != nil {
		t.Fatalf("go Export: %v", err)
	}
	if !reflect.DeepEqual(gotEnc.rows, wantEnc.rows) {
		t.Fatalf("pushed Export differs from the Go path\npushed: %+v\ngo:     %+v", gotEnc.rows, wantEnc.rows)
	}
}
