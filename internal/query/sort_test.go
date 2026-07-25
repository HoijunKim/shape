package query

import (
	"encoding/json"
	"testing"
)

func n(s string) json.Number { return json.Number(s) }

func sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	default:
		return 0
	}
}

func TestCompareValues_TotalOrderAcrossKinds(t *testing.T) {
	// Missing < null < bool < number < string, then within-kind.
	ordered := []any{
		Missing, nil,
		false, true,
		n("-5"), n("0"), n("2.5"), n("10"),
		"a", "b",
	}
	for i := 0; i < len(ordered); i++ {
		for j := 0; j < len(ordered); j++ {
			got := compareValues(ordered[i], ordered[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if sign(got) != want {
				t.Fatalf("compareValues(%v, %v) sign = %d, want %d", ordered[i], ordered[j], sign(got), want)
			}
		}
	}
}

func TestCompareValues_BigIntExactNotFloat(t *testing.T) {
	// 9007199254740993 and 9007199254740992 are indistinguishable as float64
	// but must order strictly. Mutation: compare via Float64() -> equal -> fails.
	if compareValues(n("9007199254740993"), n("9007199254740992")) <= 0 {
		t.Fatalf("9007199254740993 must sort AFTER 9007199254740992 (float64 collapses them)")
	}
	if compareValues(n("9007199254740992"), n("9007199254740993")) >= 0 {
		t.Fatalf("ordering must be strict and antisymmetric at 2^53")
	}
}

func TestCompareValues_IntVsFraction(t *testing.T) {
	if compareValues(n("2"), n("10")) >= 0 {
		t.Fatalf("2 < 10 numerically (not lexically)")
	}
	if compareValues(n("2.5"), n("2.49")) <= 0 {
		t.Fatalf("2.5 > 2.49")
	}
}

func TestCompareValues_Float64AndJSONNumberUnify(t *testing.T) {
	// The memory tier holds json.Number; Parquet DOUBLE / SQLite REAL hold
	// float64. The SAME logical value must compare equal across tiers, and a
	// float64 must order numerically against a json.Number (mutation: omit the
	// float64 case -> a float64 ranks as "unexpected" (rank 5, after strings)
	// and two floats compare mutually equal -> both assertions fail).
	if compareValues(float64(2.5), n("2.5")) != 0 {
		t.Fatalf("float64(2.5) must equal json.Number(\"2.5\") cross-tier")
	}
	if compareValues(float64(2.5), n("10")) >= 0 {
		t.Fatalf("float64(2.5) < json.Number(10) numerically")
	}
	if compareValues(float64(1.0), float64(2.0)) >= 0 {
		t.Fatalf("float64 values must order numerically among themselves")
	}
}
