package profile

import (
	"fmt"
	"testing"
)

func TestAccumulatorTypeDistAndNull(t *testing.T) {
	a := newFieldAccumulator("x", 1000)
	a.AddValue(Observation{Path: "x", Kind: KindInt, Num: 1})
	a.MarkPresent()
	a.AddValue(Observation{Path: "x", Kind: KindString, Str: "oops"})
	a.MarkPresent()
	a.AddValue(Observation{Path: "x", Kind: KindNull})
	a.MarkPresent()
	fp := a.Result(4) // 4 records total, present in 3

	if fp.PresenceRate < 0.749 || fp.PresenceRate > 0.751 {
		t.Errorf("presence = %v, want 0.75", fp.PresenceRate)
	}
	if fp.NullRate < 0.332 || fp.NullRate > 0.334 {
		t.Errorf("null rate = %v, want ~0.333", fp.NullRate)
	}
	if fp.TypeDist[KindInt] < 0.332 || fp.TypeDist[KindString] < 0.332 {
		t.Errorf("type dist = %v, want int and string each ~0.333", fp.TypeDist)
	}
}

func TestAccumulatorNumericRangeAndTop(t *testing.T) {
	a := newFieldAccumulator("n", 1000)
	for _, v := range []float64{5, 1, 9, 5, 5} {
		a.AddValue(Observation{Path: "n", Kind: KindInt, Num: v})
		a.MarkPresent()
	}
	fp := a.Result(5)
	if fp.Min == nil || *fp.Min != 1 || fp.Max == nil || *fp.Max != 9 {
		t.Fatalf("min/max = %v/%v, want 1/9", fp.Min, fp.Max)
	}
	if len(fp.TopValues) == 0 || fp.TopValues[0].Value != "5" || fp.TopValues[0].Count != 3 {
		t.Errorf("top value = %v, want 5 x3", fp.TopValues)
	}
	if fp.DistinctCount != 3 || !fp.DistinctExact {
		t.Errorf("distinct = %d exact=%v, want 3 exact", fp.DistinctCount, fp.DistinctExact)
	}
}

func TestAccumulatorPromotionBoundary(t *testing.T) {
	a := newFieldAccumulator("s", 8) // exact up to 8 distinct
	for i := 0; i < 8; i++ {
		a.AddValue(Observation{Path: "s", Kind: KindString, Str: fmt.Sprintf("v%d", i)})
		a.MarkPresent()
	}
	fp := a.Result(8)
	if !fp.DistinctExact || fp.DistinctCount != 8 {
		t.Fatalf("at cap: exact=%v count=%d, want true/8", fp.DistinctExact, fp.DistinctCount)
	}
	if fp.StrLenMin == nil || *fp.StrLenMin != 2 { // "v0".."v7" all length 2
		t.Errorf("string length must stay exact, got %v", fp.StrLenMin)
	}
	// a 9th distinct value exceeds the cap -> promote.
	a.AddValue(Observation{Path: "s", Kind: KindString, Str: "v8"})
	a.MarkPresent()
	if a.counts != nil {
		t.Error("exact map must be freed after promotion")
	}
	fp2 := a.Result(9)
	if fp2.DistinctExact {
		t.Error("after exceeding cap the field must be approximate (DistinctExact=false)")
	}
	if fp2.StrLenMin == nil || *fp2.StrLenMin != 2 {
		t.Errorf("string length must stay exact after promotion, got %v", fp2.StrLenMin)
	}
}

func TestAccumulatorPromotedKeepsHeavyHitter(t *testing.T) {
	a := newFieldAccumulator("s", 16)
	for i := 0; i < 100; i++ {
		a.AddValue(Observation{Path: "s", Kind: KindString, Str: "HEAVY"})
		a.MarkPresent()
	}
	for i := 0; i < 200; i++ { // unique tail forces promotion
		a.AddValue(Observation{Path: "s", Kind: KindString, Str: fmt.Sprintf("tail-%d", i)})
		a.MarkPresent()
	}
	fp := a.Result(300)
	if fp.DistinctExact {
		t.Fatal("should have promoted")
	}
	if len(fp.TopValues) == 0 || fp.TopValues[0].Value != "HEAVY" {
		t.Errorf("promoted top value should be HEAVY, got %v", fp.TopValues)
	}
	if fp.DistinctCount < len(fp.TopValues) {
		t.Errorf("DistinctCount %d must not be below len(TopValues) %d", fp.DistinctCount, len(fp.TopValues))
	}
}
