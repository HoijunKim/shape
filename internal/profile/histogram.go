package profile

import (
	"math"
	"sort"
)

// histMaxBins bounds the streaming histogram's bin count per numeric field.
// Like hll and spaceSaving, this keeps a numeric field's memory bounded no
// matter how many distinct values stream through.
const histMaxBins = 64

// HistBin is one histogram bin: a centroid value and how many observations
// merged into it. Bins are always kept sorted by Value ascending.
type HistBin struct {
	Value float64 `json:"value"`
	Count int     `json:"count"`
}

// numHistogram is a bounded streaming histogram (Ben-Haim & Tom-Tov, "A
// Streaming Parallel Decision Tree Algorithm"). It keeps at most maxBins
// (centroid, count) bins sorted by centroid; when an insertion would exceed the
// cap it merges the adjacent pair with the smallest gap into their
// count-weighted mean. Single pass, bounded memory - the same family as hll and
// spaceSaving.
type numHistogram struct {
	maxBins int
	bins    []HistBin // sorted by Value asc
	total   int
}

func newNumHistogram(maxBins int) *numHistogram {
	return &numHistogram{maxBins: maxBins, bins: make([]HistBin, 0, maxBins+1)}
}

// add records one numeric observation.
func (h *numHistogram) add(x float64) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return
	}
	h.total++
	i := sort.Search(len(h.bins), func(j int) bool { return h.bins[j].Value >= x })
	if i < len(h.bins) && h.bins[i].Value == x {
		h.bins[i].Count++
		return
	}
	h.bins = append(h.bins, HistBin{})
	copy(h.bins[i+1:], h.bins[i:])
	h.bins[i] = HistBin{Value: x, Count: 1}
	if len(h.bins) > h.maxBins {
		h.mergeClosest()
	}
}

// mergeClosest merges the adjacent bin pair with the smallest centroid gap,
// breaking ties toward the leftmost pair so merging is deterministic.
func (h *numHistogram) mergeClosest() {
	best := 0
	bestGap := h.bins[1].Value - h.bins[0].Value
	for i := 1; i < len(h.bins)-1; i++ {
		if gap := h.bins[i+1].Value - h.bins[i].Value; gap < bestGap {
			bestGap = gap
			best = i
		}
	}
	a, b := h.bins[best], h.bins[best+1]
	c := a.Count + b.Count
	h.bins[best] = HistBin{
		Value: (a.Value*float64(a.Count) + b.Value*float64(b.Count)) / float64(c),
		Count: c,
	}
	h.bins = append(h.bins[:best+1], h.bins[best+2:]...)
}

// snapshot returns a copy of the current bins (sorted by Value asc).
func (h *numHistogram) snapshot() []HistBin {
	out := make([]HistBin, len(h.bins))
	copy(out, h.bins)
	return out
}

// quantile returns an approximate q-quantile (q in 0..1) of the observed
// values, interpolating linearly across the cumulative counts positioned at bin
// centroids (each bin's mass is treated as centered on its centroid). Returns
// NaN when empty; clamps q<=0 to the smallest centroid and q>=1 to the largest.
func (h *numHistogram) quantile(q float64) float64 {
	if len(h.bins) == 0 {
		return math.NaN()
	}
	if len(h.bins) == 1 || q <= 0 {
		return h.bins[0].Value
	}
	if q >= 1 {
		return h.bins[len(h.bins)-1].Value
	}
	target := q * float64(h.total)
	cum := 0.0 // running count strictly before the current bin
	prevCenter := 0.0
	prevVal := h.bins[0].Value
	for i, b := range h.bins {
		center := cum + float64(b.Count)/2
		if center >= target {
			if i == 0 {
				return b.Value
			}
			frac := (target - prevCenter) / (center - prevCenter)
			return prevVal + frac*(b.Value-prevVal)
		}
		prevCenter = center
		prevVal = b.Value
		cum += float64(b.Count)
	}
	return h.bins[len(h.bins)-1].Value
}
