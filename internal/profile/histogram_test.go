package profile

import (
	"math"
	"testing"
)

func TestHistogramExactBelowCap(t *testing.T) {
	h := newNumHistogram(64)
	for _, v := range []float64{30, 10, 20, 10} {
		h.add(v)
	}
	bins := h.snapshot()
	// 3 distinct values -> 3 bins, no merge, sorted ascending.
	if len(bins) != 3 {
		t.Fatalf("bins = %d, want 3", len(bins))
	}
	if bins[0].Value != 10 || bins[0].Count != 2 {
		t.Errorf("bins[0] = %+v, want {10, 2}", bins[0])
	}
	if bins[1].Value != 20 || bins[1].Count != 1 {
		t.Errorf("bins[1] = %+v, want {20, 1}", bins[1])
	}
	if bins[2].Value != 30 || bins[2].Count != 1 {
		t.Errorf("bins[2] = %+v, want {30, 1}", bins[2])
	}
	if h.total != 4 {
		t.Errorf("total = %d, want 4", h.total)
	}
}

func TestHistogramBoundedBins(t *testing.T) {
	h := newNumHistogram(64)
	for i := 0; i < 10000; i++ {
		h.add(float64(i))
	}
	bins := h.snapshot()
	if len(bins) > 64 {
		t.Fatalf("bins = %d, want <= 64 (bounded memory)", len(bins))
	}
	if h.total != 10000 {
		t.Errorf("total = %d, want 10000", h.total)
	}
	// total count across bins must equal observations (merging preserves mass).
	sum := 0
	for _, b := range bins {
		sum += b.Count
	}
	if sum != 10000 {
		t.Errorf("sum of bin counts = %d, want 10000", sum)
	}
	// bins stay sorted ascending.
	for i := 1; i < len(bins); i++ {
		if bins[i].Value < bins[i-1].Value {
			t.Fatalf("bins not sorted at %d: %v", i, bins)
		}
	}
}

func TestHistogramSnapshotIsCopy(t *testing.T) {
	h := newNumHistogram(64)
	h.add(1)
	snap := h.snapshot()
	snap[0].Count = 999
	if h.snapshot()[0].Count != 1 {
		t.Error("snapshot must return a copy, not the live slice")
	}
}

func TestHistogramQuantileEmpty(t *testing.T) {
	h := newNumHistogram(64)
	if q := h.quantile(0.5); !math.IsNaN(q) {
		t.Errorf("quantile of empty = %v, want NaN", q)
	}
}

func TestHistogramQuantileSingleValue(t *testing.T) {
	h := newNumHistogram(64)
	for i := 0; i < 5; i++ {
		h.add(42)
	}
	if q := h.quantile(0.5); q != 42 {
		t.Errorf("median = %v, want 42", q)
	}
	if q := h.quantile(0.95); q != 42 {
		t.Errorf("p95 = %v, want 42", q)
	}
}

func TestHistogramQuantileClamp(t *testing.T) {
	h := newNumHistogram(64)
	for _, v := range []float64{10, 20, 30} {
		h.add(v)
	}
	if q := h.quantile(0); q != 10 {
		t.Errorf("q(0) = %v, want 10 (min)", q)
	}
	if q := h.quantile(1); q != 30 {
		t.Errorf("q(1) = %v, want 30 (max)", q)
	}
}

func TestHistogramIgnoresNonFinite(t *testing.T) {
	h := newNumHistogram(64)
	for _, v := range []float64{1, 2, math.NaN(), 3, math.Inf(1), 0.5, math.Inf(-1)} {
		h.add(v)
	}
	if h.total != 4 { // only 1,2,3,0.5 are finite
		t.Errorf("total = %d, want 4 (non-finite excluded)", h.total)
	}
	bins := h.snapshot()
	sum := 0
	for i, b := range bins {
		sum += b.Count
		if math.IsNaN(b.Value) || math.IsInf(b.Value, 0) {
			t.Errorf("bin %d has non-finite value %v", i, b.Value)
		}
		if i > 0 && bins[i].Value < bins[i-1].Value {
			t.Fatalf("bins not sorted at %d: %v", i, bins)
		}
	}
	if sum != 4 {
		t.Errorf("sum of bin counts = %d, want 4", sum)
	}
	if q := h.quantile(0.5); math.IsNaN(q) || math.IsInf(q, 0) {
		t.Errorf("median = %v, want finite", q)
	}
}

func TestHistogramQuantileUniformAccuracy(t *testing.T) {
	h := newNumHistogram(64)
	for i := 0; i < 10000; i++ {
		h.add(float64(i)) // uniform 0..9999
	}
	med := h.quantile(0.5)
	if med < 4900 || med > 5100 { // within ~1% of true median 4999.5
		t.Errorf("median = %v, want ~4999.5 (+/-100)", med)
	}
	p95 := h.quantile(0.95)
	if p95 < 9400 || p95 > 9600 { // within ~1% of true p95 ~9499
		t.Errorf("p95 = %v, want ~9499 (+/-100)", p95)
	}
}
