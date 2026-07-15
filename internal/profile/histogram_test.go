package profile

import "testing"

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
