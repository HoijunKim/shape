package profile

import (
	"fmt"
	"math"
	"testing"
)

func TestHash64Deterministic(t *testing.T) {
	if hash64("shape") != hash64("shape") {
		t.Error("hash64 must be deterministic for the same input")
	}
	if hash64("a") == hash64("b") {
		t.Error("hash64 must distinguish different inputs")
	}
}

func TestHLLAccuracy(t *testing.T) {
	// n well above 2.5*2^12 (10240) so the raw estimator is in its unbiased range.
	for _, n := range []int{20000, 200000} {
		h := newHLL(12)
		for i := 0; i < n; i++ {
			h.add(fmt.Sprintf("value-%d", i))
		}
		est := h.estimate()
		rel := math.Abs(float64(est-n)) / float64(n)
		if rel > 0.05 { // ~3x the 1.6% standard error, generous
			t.Errorf("n=%d: estimate=%d rel-error=%.4f > 0.05", n, est, rel)
		}
	}
}

func TestHLLSameInputSameRegisters(t *testing.T) {
	build := func() *hll {
		h := newHLL(12)
		for i := 0; i < 5000; i++ {
			h.add(fmt.Sprintf("k-%d", i))
		}
		return h
	}
	a, b := build(), build()
	for i := range a.regs {
		if a.regs[i] != b.regs[i] {
			t.Fatalf("register %d differs: %d vs %d (non-deterministic)", i, a.regs[i], b.regs[i])
		}
	}
}
