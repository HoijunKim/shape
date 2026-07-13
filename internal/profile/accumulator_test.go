package profile

import "testing"

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

func TestAccumulatorDistinctOverflow(t *testing.T) {
	a := newFieldAccumulator("s", 2)
	for _, v := range []string{"a", "b", "c", "d"} {
		a.AddValue(Observation{Path: "s", Kind: KindString, Str: v})
		a.MarkPresent()
	}
	fp := a.Result(4)
	if fp.DistinctExact {
		t.Errorf("expected distinct overflow to mark inexact")
	}
	if fp.StrLenMin == nil || *fp.StrLenMin != 1 {
		t.Errorf("str len min = %v, want 1", fp.StrLenMin)
	}
}
