package profile

import (
	"sort"
	"strconv"
)

const (
	// hllPrecision sets the HyperLogLog register count (2^12 = 4096 registers,
	// ~1.6% standard error). DefaultExactCap must stay >= 2.5*2^hllPrecision.
	hllPrecision = 12
	// spaceSavingCap bounds the top-K heavy-hitter counters per promoted field.
	spaceSavingCap = 512
)

// ValueCount is a value and how often it was seen.
type ValueCount struct {
	Value string
	Count int
}

// FieldProfile is the accumulated profile for one path.
type FieldProfile struct {
	Path          string
	PresenceRate  float64
	TypeDist      map[JSONKind]float64
	NullRate      float64
	Min, Max      *float64
	DistinctCount int
	DistinctExact bool
	TopValues     []ValueCount
	StrLenMin     *int
	StrLenMax     *int
	Observations  int
}

type fieldAccumulator struct {
	path       string
	kindCounts map[JSONKind]int
	present    int
	obs        int
	haveNum    bool
	min, max   float64
	haveLen    bool
	lenMin     int
	lenMax     int
	counts     map[string]int // value key -> count; nil once promoted
	exactCap   int
	promoted   bool
	hll        *hll
	ss         *spaceSaving
}

func newFieldAccumulator(path string, exactCap int) *fieldAccumulator {
	return &fieldAccumulator{
		path:       path,
		kindCounts: map[JSONKind]int{},
		counts:     map[string]int{},
		exactCap:   exactCap,
	}
}

func (a *fieldAccumulator) MarkPresent() { a.present++ }

func (a *fieldAccumulator) AddValue(o Observation) {
	a.obs++
	a.kindCounts[o.Kind]++
	switch o.Kind {
	case KindInt, KindFloat:
		if !a.haveNum || o.Num < a.min {
			a.min = o.Num
		}
		if !a.haveNum || o.Num > a.max {
			a.max = o.Num
		}
		a.haveNum = true
		a.addCount(numKey(o.Num))
	case KindString:
		l := len(o.Str)
		if !a.haveLen || l < a.lenMin {
			a.lenMin = l
		}
		if !a.haveLen || l > a.lenMax {
			a.lenMax = l
		}
		a.haveLen = true
		a.addCount(o.Str)
	}
}

func (a *fieldAccumulator) addCount(key string) {
	if a.promoted {
		a.hll.add(key)
		a.ss.add(key)
		return
	}
	if _, ok := a.counts[key]; ok {
		a.counts[key]++
		return
	}
	if len(a.counts) >= a.exactCap { // a new key would exceed the exact cap
		a.promote()
		a.hll.add(key)
		a.ss.add(key)
		return
	}
	a.counts[key] = 1
}

// promote switches from the exact map to bounded sketches, seeded from the map,
// then frees the map. HLL seeding is order-independent (register max); the
// Space-Saving seed uses the sorted (count desc, value asc) entry order so it is
// deterministic despite Go's randomized map iteration.
func (a *fieldAccumulator) promote() {
	a.hll = newHLL(hllPrecision)
	a.ss = newSpaceSaving(spaceSavingCap)
	entries := topValues(a.counts, len(a.counts)) // all entries, sorted
	for _, e := range entries {
		a.hll.add(e.Value)
	}
	for i, e := range entries {
		if i >= spaceSavingCap {
			break
		}
		a.ss.seed(e.Value, e.Count)
	}
	a.promoted = true
	a.counts = nil
}

func (a *fieldAccumulator) Result(totalRecords int) FieldProfile {
	fp := FieldProfile{
		Path:         a.path,
		TypeDist:     map[JSONKind]float64{},
		Observations: a.obs,
	}
	if totalRecords > 0 {
		fp.PresenceRate = float64(a.present) / float64(totalRecords)
	}
	if a.obs > 0 {
		for k, c := range a.kindCounts {
			fp.TypeDist[k] = float64(c) / float64(a.obs)
		}
		fp.NullRate = float64(a.kindCounts[KindNull]) / float64(a.obs)
	}
	if a.haveNum {
		mn, mx := a.min, a.max
		fp.Min, fp.Max = &mn, &mx
	}
	if a.haveLen {
		mn, mx := a.lenMin, a.lenMax
		fp.StrLenMin, fp.StrLenMax = &mn, &mx
	}
	if a.promoted {
		top := a.ss.heavyTop(10)
		est := a.hll.estimate()
		if est < len(top) {
			est = len(top) // never report fewer distinct than the top values shown
		}
		fp.DistinctCount = est
		fp.DistinctExact = false
		fp.TopValues = top
	} else {
		fp.DistinctCount = len(a.counts)
		fp.DistinctExact = true
		fp.TopValues = topValues(a.counts, 10)
	}
	return fp
}

func topValues(counts map[string]int, k int) []ValueCount {
	vs := make([]ValueCount, 0, len(counts))
	for v, c := range counts {
		vs = append(vs, ValueCount{Value: v, Count: c})
	}
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Count != vs[j].Count {
			return vs[i].Count > vs[j].Count
		}
		return vs[i].Value < vs[j].Value
	})
	if len(vs) > k {
		vs = vs[:k]
	}
	return vs
}

func numKey(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
