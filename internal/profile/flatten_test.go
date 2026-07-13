package profile

import (
	"sort"
	"testing"
)

func collect(t *testing.T, s string) map[string]JSONKind {
	t.Helper()
	got := map[string]JSONKind{}
	Flatten(decode(t, s), func(o Observation) { got[o.Path] = o.Kind })
	return got
}

func TestFlattenNestedPaths(t *testing.T) {
	got := collect(t, `{"email":"a@b.c","user":{"name":"x"},"tags":["p","q"]}`)
	want := map[string]JSONKind{
		"email":     KindString,
		"user":      KindObject,
		"user.name": KindString,
		"tags":      KindArray,
		"tags[]":    KindString,
	}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for p, k := range want {
		if got[p] != k {
			t.Errorf("path %q kind = %s, want %s", p, got[p], k)
		}
	}
}

func TestFlattenScalarAndValues(t *testing.T) {
	var nums []float64
	Flatten(decode(t, `{"a":42,"b":"hi"}`), func(o Observation) {
		if o.Kind == KindInt {
			nums = append(nums, o.Num)
		}
		if o.Path == "b" && o.Str != "hi" {
			t.Errorf("b.Str = %q, want hi", o.Str)
		}
	})
	sort.Float64s(nums)
	if len(nums) != 1 || nums[0] != 42 {
		t.Errorf("int values = %v, want [42]", nums)
	}
}

func TestFlattenRootScalar(t *testing.T) {
	got := collect(t, `7`)
	if got["$"] != KindInt {
		t.Errorf("root scalar kind = %s, want int", got["$"])
	}
}
