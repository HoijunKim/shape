package profile

import "sort"

// ssCounter is one monitored value. err is the maximum overestimate of count.
type ssCounter struct {
	value string
	count int
	err   int
}

// spaceSaving is a bounded top-K heavy-hitter sketch (Metwally et al.). Any
// value whose true frequency exceeds N/cap is guaranteed to be retained.
type spaceSaving struct {
	cap      int
	counters map[string]*ssCounter
}

func newSpaceSaving(cap int) *spaceSaving {
	return &spaceSaving{cap: cap, counters: make(map[string]*ssCounter, cap)}
}

// seed inserts a value with a known exact count (used at promotion, err=0).
func (s *spaceSaving) seed(value string, count int) {
	s.counters[value] = &ssCounter{value: value, count: count}
}

func (s *spaceSaving) add(key string) {
	if c, ok := s.counters[key]; ok {
		c.count++
		return
	}
	if len(s.counters) < s.cap {
		s.counters[key] = &ssCounter{value: key, count: 1}
		return
	}
	m := s.min()
	delete(s.counters, m.value)
	m.value, m.err, m.count = key, m.count, m.count+1
	s.counters[key] = m
}

// min returns the counter with the smallest count, breaking ties by smallest
// value string so eviction is a deterministic pure function of counter state.
func (s *spaceSaving) min() *ssCounter {
	var m *ssCounter
	for _, c := range s.counters {
		if m == nil || c.count < m.count || (c.count == m.count && c.value < m.value) {
			m = c
		}
	}
	return m
}

// top returns the k highest-count values, sorted count desc then value asc.
func (s *spaceSaving) top(k int) []ValueCount {
	vs := make([]ValueCount, 0, len(s.counters))
	for _, c := range s.counters {
		vs = append(vs, ValueCount{Value: c.value, Count: c.count})
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
