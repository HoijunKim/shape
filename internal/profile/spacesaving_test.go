package profile

import (
	"fmt"
	"testing"
)

func TestSpaceSavingHeavyHitters(t *testing.T) {
	s := newSpaceSaving(64)
	for i := 0; i < 1000; i++ {
		s.add("heavy-a")
		s.add("heavy-b")
	}
	for i := 0; i < 500; i++ {
		s.add("heavy-c")
	}
	for i := 0; i < 5000; i++ { // long unique tail
		s.add(fmt.Sprintf("noise-%d", i))
	}
	got := map[string]bool{}
	for _, v := range s.top(3) {
		got[v.Value] = true
	}
	for _, h := range []string{"heavy-a", "heavy-b", "heavy-c"} {
		if !got[h] {
			t.Errorf("heavy hitter %q missing from top-3: %v", h, s.top(3))
		}
	}
}

func TestSpaceSavingDeterministicEviction(t *testing.T) {
	s := newSpaceSaving(2)
	s.add("b") // count 1
	s.add("a") // count 1, full; a and b both count 1
	s.add("c") // must evict min by (count asc, value asc) -> "a"
	if _, ok := s.counters["a"]; ok {
		t.Error("min counter (smallest value at min count) 'a' should have been evicted")
	}
	if _, ok := s.counters["c"]; !ok {
		t.Error("new key 'c' should be present after eviction")
	}
}

func TestSpaceSavingTopOrdering(t *testing.T) {
	s := newSpaceSaving(8)
	s.add("x")
	s.add("x")
	s.add("y")
	top := s.top(2)
	if top[0].Value != "x" || top[0].Count != 2 || top[1].Value != "y" {
		t.Errorf("top ordering wrong: %v", top)
	}
}

func TestSpaceSavingSeed(t *testing.T) {
	s := newSpaceSaving(8)
	s.seed("v", 42)
	if s.counters["v"].count != 42 || s.counters["v"].err != 0 {
		t.Errorf("seed should set exact count and zero error, got %+v", s.counters["v"])
	}
}
