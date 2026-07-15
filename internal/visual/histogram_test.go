package visual

import (
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

func hbins(pairs ...float64) []profile.HistBin { // value,count,value,count,...
	var out []profile.HistBin
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, profile.HistBin{Value: pairs[i], Count: int(pairs[i+1])})
	}
	return out
}

func TestDisplayHistogramMassPreserved(t *testing.T) {
	fp := profile.FieldProfile{Min: fptr(0), Max: fptr(100), Histogram: hbins(1, 5, 25, 10, 50, 20, 99, 5)}
	h := displayHistogram(fp)
	if len(h.Bins) != DisplayBins {
		t.Fatalf("bins = %d, want %d", len(h.Bins), DisplayBins)
	}
	sum := 0
	for _, b := range h.Bins {
		sum += b.Count
	}
	if sum != 40 || h.Total != 40 {
		t.Errorf("sum=%d total=%d, want 40/40", sum, h.Total)
	}
}

func TestDisplayHistogramDegenerate(t *testing.T) {
	fp := profile.FieldProfile{Min: fptr(7), Max: fptr(7), Histogram: hbins(7, 9)}
	h := displayHistogram(fp)
	if len(h.Bins) != 1 || h.Bins[0].Count != 9 || h.BinWidth != 0 || h.Bins[0].Frac != 1 {
		t.Errorf("degenerate = %+v, want single full bar", h)
	}
}

func TestDisplayHistogramMaxClampsToLastBin(t *testing.T) {
	fp := profile.FieldProfile{Min: fptr(0), Max: fptr(20), Histogram: hbins(20, 3)} // value == max
	h := displayHistogram(fp)
	if h.Bins[DisplayBins-1].Count != 3 {
		t.Errorf("value==max landed in bin != last: %+v", h.Bins[DisplayBins-1])
	}
}
