package query

import (
	"context"
	"math/rand"
	"testing"
)

type parityFloatRow struct {
	F float64 `parquet:"f"`
}

// TestSortParity_FloatColumnMemVsParquet is the E9 cross-tier guarantee for the
// float64 path (plan-review Critical #1): the memory tier reads an NDJSON float
// as json.Number, while Parquet reads the SAME logical value from a DOUBLE
// column as float64. The comparator unifies them, so sorting on that column must
// yield a BYTE-IDENTICAL window (same Row.Index sequence) on both tiers. If
// float64 were dropped from the comparator, the two tiers would diverge here.
func TestSortParity_FloatColumnMemVsParquet(t *testing.T) {
	const nrec = 200
	r := rand.New(rand.NewSource(7))
	perm := r.Perm(nrec)

	maps := make([]map[string]any, nrec)
	prows := make([]parityFloatRow, nrec)
	for i := 0; i < nrec; i++ {
		v := float64(perm[i]) + 0.5
		maps[i] = map[string]any{"f": v}
		prows[i] = parityFloatRow{F: v}
	}

	engMem, hMem, _ := openExportFixture(t, maps, 0) // memory tier: f is json.Number
	pb := newTestParquetBackend(t, prows, 0)         // parquet: f is float64

	win := Window{Offset: 40, Limit: 25}
	sort := SortSpec{Path: "f", Desc: true}

	rsMem, err := engMem.QueryRows(context.Background(), QueryRequest{Handle: hMem, Offset: win.Offset, Limit: win.Limit, Sort: sort})
	if err != nil {
		t.Fatal(err)
	}

	pp := compilePlan(t, Filter{}, Transform{}, pb.Columns())
	cs, err := CompileSort(sort, pb.Columns())
	if err != nil {
		t.Fatal(err)
	}
	pp.Sort = cs
	rsPq, err := pb.Query(context.Background(), pp, win, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(rsMem.Rows) != len(rsPq.Rows) || len(rsMem.Rows) == 0 {
		t.Fatalf("window sizes: mem %d vs parquet %d (want equal, non-zero)", len(rsMem.Rows), len(rsPq.Rows))
	}
	for i := range rsMem.Rows {
		if rsMem.Rows[i].Index != rsPq.Rows[i].Index {
			t.Fatalf("row %d: mem Index %d != parquet Index %d -- float64 (parquet) and json.Number (mem) must sort identically (comparator unification)", i, rsMem.Rows[i].Index, rsPq.Rows[i].Index)
		}
	}
}
