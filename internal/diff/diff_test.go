package diff

import (
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

func fp(path string, mut func(*profile.FieldProfile)) profile.FieldProfile {
	f := profile.FieldProfile{Path: path, PresenceRate: 1.0, TypeDist: map[profile.JSONKind]float64{}}
	if mut != nil {
		mut(&f)
	}
	return f
}

func TestTypeChangeAddedIsBreaking(t *testing.T) {
	a := fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 })
	b := fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 0.5; f.TypeDist[profile.KindString] = 0.5 })
	d, ok := typeChange(a, b)
	if !ok || !d.Breaking {
		t.Fatalf("adding a string type must be breaking, got %+v ok=%v", d, ok)
	}
}

func TestTypeChangeDroppedIsSafe(t *testing.T) {
	a := fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 0.5; f.TypeDist[profile.KindString] = 0.5 })
	b := fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 })
	d, ok := typeChange(a, b)
	if !ok || d.Breaking {
		t.Fatalf("dropping a type must be non-breaking, got %+v ok=%v", d, ok)
	}
}

func TestTypeChangeIntFloatSame(t *testing.T) {
	a := fp("n", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 })
	b := fp("n", func(f *profile.FieldProfile) { f.TypeDist[profile.KindFloat] = 1 })
	if _, ok := typeChange(a, b); ok {
		t.Fatal("int vs float must not register as a type change (both are number)")
	}
}

func TestPresenceBecameOptionalIsBreaking(t *testing.T) {
	a := fp("x", func(f *profile.FieldProfile) { f.PresenceRate = 1.0 })
	b := fp("x", func(f *profile.FieldProfile) { f.PresenceRate = 0.5 })
	d, ok := presenceChange(a, b)
	if !ok || !d.Breaking {
		t.Fatalf("always-present -> optional must be breaking, got %+v ok=%v", d, ok)
	}
}

func TestPresenceBecameRequiredIsSafe(t *testing.T) {
	a := fp("x", func(f *profile.FieldProfile) { f.PresenceRate = 0.5 })
	b := fp("x", func(f *profile.FieldProfile) { f.PresenceRate = 1.0 })
	d, ok := presenceChange(a, b)
	if !ok || d.Breaking {
		t.Fatalf("optional -> always-present must be non-breaking, got %+v ok=%v", d, ok)
	}
}

func closedString(path string, vals ...string) profile.FieldProfile {
	return fp(path, func(f *profile.FieldProfile) {
		f.TypeDist[profile.KindString] = 1
		f.DistinctExact = true
		f.DistinctCount = len(vals)
		for _, v := range vals {
			f.TopValues = append(f.TopValues, profile.ValueCount{Value: v, Count: 1})
		}
	})
}

func TestEnumGainedMemberIsBreaking(t *testing.T) {
	a := closedString("s", "open", "closed")
	b := closedString("s", "open", "pending")
	d, ok := enumChange(a, b)
	if !ok || !d.Breaking {
		t.Fatalf("enum gaining a member must be breaking, got %+v ok=%v", d, ok)
	}
}

func TestEnumLostMemberIsSafe(t *testing.T) {
	a := closedString("s", "open", "closed", "pending")
	b := closedString("s", "open", "closed")
	d, ok := enumChange(a, b)
	if !ok || d.Breaking {
		t.Fatalf("enum losing a member must be non-breaking, got %+v ok=%v", d, ok)
	}
}

func TestEnumSuppressedWhenIncomplete(t *testing.T) {
	// DistinctExact false on one side -> no enum verdict at all.
	a := closedString("s", "open", "closed")
	b := closedString("s", "open", "pending")
	b.DistinctExact = false
	if _, ok := enumChange(a, b); ok {
		t.Fatal("enum change must be suppressed when a side is not a proven complete set")
	}
}
