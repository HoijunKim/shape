package diff

import (
	"strconv"
	"strings"
	"testing"

	"github.com/hoijunkim/shape/internal/profile"
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

func result(src string, fields ...profile.FieldProfile) profile.ProfileResult {
	return profile.ProfileResult{Source: src, Records: 10, Fields: fields}
}

func changeFor(d DiffResult, path string) (Change, bool) {
	for _, c := range d.Changes {
		if c.Path == path {
			return c, true
		}
	}
	return Change{}, false
}

func TestDiffRemovedAddedChanged(t *testing.T) {
	old := result("old",
		fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 }),
		fp("email", func(f *profile.FieldProfile) { f.TypeDist[profile.KindString] = 1 }),
	)
	new := result("new",
		fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 0.5; f.TypeDist[profile.KindString] = 0.5 }),
		fp("nickname", func(f *profile.FieldProfile) { f.TypeDist[profile.KindString] = 1 }),
	)
	d := Diff(old, new)
	if d.Removed != 1 || d.Added != 1 || d.Changed != 1 {
		t.Fatalf("counts: removed=%d added=%d changed=%d, want 1/1/1", d.Removed, d.Added, d.Changed)
	}
	email, _ := changeFor(d, "email")
	if email.Kind != Removed || !email.Breaking {
		t.Errorf("email = %+v, want removed+breaking (was always-present)", email)
	}
	nick, _ := changeFor(d, "nickname")
	if nick.Kind != Added || nick.Breaking {
		t.Errorf("nickname = %+v, want added+safe", nick)
	}
	id, _ := changeFor(d, "id")
	if id.Kind != Changed || !id.Breaking {
		t.Errorf("id = %+v, want changed+breaking (type added)", id)
	}
	if d.Breaking != 2 {
		t.Errorf("breaking = %d, want 2 (email + id)", d.Breaking)
	}
}

func TestDiffOptionalFieldRemovalIsSafe(t *testing.T) {
	old := result("old", fp("opt", func(f *profile.FieldProfile) { f.PresenceRate = 0.4; f.TypeDist[profile.KindString] = 1 }))
	new := result("new", fp("keep", func(f *profile.FieldProfile) { f.TypeDist[profile.KindString] = 1 }))
	d := Diff(old, new)
	opt, _ := changeFor(d, "opt")
	if opt.Kind != Removed || opt.Breaking {
		t.Errorf("optional removal must be non-breaking, got %+v", opt)
	}
}

func TestDiffNoChangeOnIdenticalProfiles(t *testing.T) {
	p := result("same", fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 }))
	d := Diff(p, p)
	if len(d.Changes) != 0 || d.Breaking != 0 {
		t.Errorf("identical profiles must diff clean, got %+v", d)
	}
}

func TestDiffCaveats(t *testing.T) {
	old := profile.ProfileResult{Source: "old", Records: 5, Skipped: 3}
	new := profile.ProfileResult{Source: "new", Records: 5000}
	d := Diff(old, new)
	if len(d.Caveats) < 2 {
		t.Errorf("expected skipped + count-mismatch caveats, got %v", d.Caveats)
	}
}

func TestDiffEmptyBaselineCaveat(t *testing.T) {
	old := profile.ProfileResult{Source: "old", Records: 0}
	new := result("new", fp("id", func(f *profile.FieldProfile) { f.TypeDist[profile.KindInt] = 1 }))
	d := Diff(old, new)
	found := false
	for _, c := range d.Caveats {
		if strings.Contains(c, "0 records") {
			found = true
		}
	}
	if !found {
		t.Errorf("empty baseline must emit a caveat, got %v", d.Caveats)
	}
}

func TestDiffNumericFieldNoEnumChange(t *testing.T) {
	// A numeric low-cardinality field whose value set rotates is NOT an enum
	// and must not produce any change (both sides are "number").
	mk := func(src string, vals ...int64) profile.ProfileResult {
		f := fp("n", func(f *profile.FieldProfile) {
			f.TypeDist[profile.KindInt] = 1
			f.DistinctExact = true
			f.DistinctCount = len(vals)
			for _, v := range vals {
				f.TopValues = append(f.TopValues, profile.ValueCount{Value: strconv.FormatInt(v, 10), Count: 1})
			}
		})
		return result(src, f)
	}
	d := Diff(mk("old", 1, 2), mk("new", 3, 4))
	if ch, ok := changeFor(d, "n"); ok {
		t.Errorf("numeric field with rotated values must not diff (no enum), got %+v", ch)
	}
}

func TestDiffEnumTruncatedNotFlagged(t *testing.T) {
	// 11 distinct string values exceed the TopValues cap (10), so DistinctCount
	// != len(TopValues) and no enum verdict may be made.
	mk := func(src string, n int) profile.ProfileResult {
		f := fp("s", func(f *profile.FieldProfile) {
			f.TypeDist[profile.KindString] = 1
			f.DistinctExact = true
			f.DistinctCount = n
			for i := 0; i < n && i < 10; i++ {
				f.TopValues = append(f.TopValues, profile.ValueCount{Value: strconv.Itoa(i), Count: 1})
			}
		})
		return result(src, f)
	}
	d := Diff(mk("old", 11), mk("new", 11))
	if _, ok := changeFor(d, "s"); ok {
		t.Errorf("11-distinct field exceeds the TopValues cap; must not produce an enum change")
	}
}
