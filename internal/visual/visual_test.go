package visual

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

var update = flag.Bool("update", false, "update golden files")

func goldenCheck(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run -update first): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch for %s; run: go test ./internal/visual -run %s -update", name, t.Name())
	}
}

// sampleProfile builds a multi-kind ProfileResult fixture covering, in
// path-sorted order: bool ("active"), high-cardinality/promoted string
// ("email"), mixed/type-drift ("id"), all-null ("last_login"),
// numeric-continuous/histogram ("latency_ms"), discrete-numeric/categorical
// ("rating"), enum string/categorical ("status"), an array container
// ("tags") and its "[]" element (enum-like categorical string). Counts are
// chosen to divide evenly so percentage rounding never introduces an
// apportionment artifact unrelated to the logic under test.
func sampleProfile() profile.ProfileResult {
	return profile.ProfileResult{
		Records: 1000,
		Skipped: 5,
		Fields: []profile.FieldProfile{
			{ // bool -> FormMeter
				Path:          "active",
				PresenceRate:  1.0,
				TypeDist:      map[profile.JSONKind]float64{profile.KindBool: 1.0},
				NullRate:      0,
				DistinctExact: true,
				Observations:  1000,
			},
			{ // high-cardinality string (promoted) -> FormHighCard
				Path:         "email",
				PresenceRate: 1.0,
				TypeDist: map[profile.JSONKind]float64{
					profile.KindString: 0.95, profile.KindNull: 0.05,
				},
				NullRate:      0.05,
				DistinctCount: 900,
				DistinctExact: false,
				TopValues: []profile.ValueCount{
					{Value: "a@x.com", Count: 12},
					{Value: "b@x.com", Count: 11},
					{Value: "c@x.com", Count: 10},
					{Value: "d@x.com", Count: 9},
					{Value: "e@x.com", Count: 8},
				},
				StrLenMin:    intp(6),
				StrLenMax:    intp(40),
				Observations: 1000,
			},
			{ // mixed / type drift -> FormTypeMix
				Path:         "id",
				PresenceRate: 1.0,
				TypeDist: map[profile.JSONKind]float64{
					profile.KindInt: 0.7, profile.KindString: 0.3,
				},
				NullRate: 0,
				Min:      fptr(100),
				Max:      fptr(900),
				Histogram: []profile.HistBin{
					{Value: 100, Count: 350},
					{Value: 900, Count: 350},
				},
				Median:        fptr(500),
				P95:           fptr(880),
				DistinctCount: 900,
				DistinctExact: true,
				Observations:  1000,
			},
			{ // all-null -> FormEmpty
				Path:          "last_login",
				PresenceRate:  1.0,
				TypeDist:      map[profile.JSONKind]float64{profile.KindNull: 1.0},
				NullRate:      1.0,
				DistinctExact: true,
				Observations:  1000,
			},
			{ // numeric-continuous -> FormHistogram
				Path:         "latency_ms",
				PresenceRate: 1.0,
				TypeDist: map[profile.JSONKind]float64{
					profile.KindFloat: 0.98, profile.KindNull: 0.02,
				},
				NullRate: 0.02,
				Min:      fptr(5),
				Max:      fptr(500),
				Histogram: []profile.HistBin{
					{Value: 10, Count: 100},
					{Value: 30, Count: 200},
					{Value: 60, Count: 250},
					{Value: 90, Count: 200},
					{Value: 150, Count: 120},
					{Value: 250, Count: 70},
					{Value: 400, Count: 30},
					{Value: 495, Count: 10},
				},
				Median:        fptr(70),
				P95:           fptr(400),
				DistinctCount: 643,
				DistinctExact: true,
				Observations:  1000,
			},
			{ // discrete numeric -> FormCategorical (kind "number")
				Path:         "rating",
				PresenceRate: 1.0,
				TypeDist:     map[profile.JSONKind]float64{profile.KindInt: 1.0},
				NullRate:     0,
				Min:          fptr(1),
				Max:          fptr(5),
				Histogram: []profile.HistBin{
					{Value: 1, Count: 50},
					{Value: 2, Count: 100},
					{Value: 3, Count: 150},
					{Value: 4, Count: 300},
					{Value: 5, Count: 400},
				},
				Median: fptr(4),
				P95:    fptr(5),
				TopValues: []profile.ValueCount{
					{Value: "5", Count: 400},
					{Value: "4", Count: 300},
					{Value: "3", Count: 150},
					{Value: "2", Count: 100},
					{Value: "1", Count: 50},
				},
				DistinctCount: 5,
				DistinctExact: true,
				Observations:  1000,
			},
			{ // enum string -> FormCategorical (kind "string"), enumLike
				Path:         "status",
				PresenceRate: 1.0,
				TypeDist: map[profile.JSONKind]float64{
					profile.KindString: 0.75, profile.KindNull: 0.25,
				},
				NullRate: 0.25,
				TopValues: []profile.ValueCount{
					{Value: "active", Count: 450},
					{Value: "pending", Count: 150},
					{Value: "closed", Count: 100},
					{Value: "cancelled", Count: 50},
				},
				DistinctCount: 4,
				DistinctExact: true,
				Observations:  1000,
			},
			{ // array container -> FormArray
				Path:          "tags",
				PresenceRate:  1.0,
				TypeDist:      map[profile.JSONKind]float64{profile.KindArray: 1.0},
				NullRate:      0,
				DistinctExact: true,
				Observations:  1000,
			},
			{ // array element (string, enum-like) -> FormCategorical
				Path:         "tags[]",
				PresenceRate: 1.0,
				TypeDist:     map[profile.JSONKind]float64{profile.KindString: 1.0},
				NullRate:     0,
				TopValues: []profile.ValueCount{
					{Value: "news", Count: 500},
					{Value: "sports", Count: 400},
					{Value: "tech", Count: 350},
					{Value: "music", Count: 300},
					{Value: "travel", Count: 300},
					{Value: "food", Count: 250},
					{Value: "health", Count: 250},
					{Value: "finance", Count: 150},
				},
				DistinctCount: 8,
				DistinctExact: true,
				Observations:  2500,
			},
		},
	}
}

func TestFromProfileGolden(t *testing.T) {
	res := sampleProfile() // build the multi-kind fixture (defined in this file)
	vm := FromProfile(res, Options{Name: "sample.ndjson"})
	b, err := json.MarshalIndent(vm, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	goldenCheck(t, "fromprofile.golden", b)
}

// TestFromProfileDualConsumer checks that every FieldCard carries the base
// payload both GUI and HTML-report consumers need regardless of chart form:
// Stats and Badges are always populated. TypeMix is checked too, except for
// FormEmpty cards (all-null / no-observation fields): typeMix(fp) legitimately
// returns nil there (design §5 - there is no non-null type distribution to
// show, and the 1-NullRate divide-by-zero guard makes this the only sound
// behavior), so requiring it universally would contradict already-committed,
// reviewed Task 4 behavior.
func TestFromProfileDualConsumer(t *testing.T) {
	vm := FromProfile(sampleProfile(), Options{Name: "sample.ndjson"})
	for _, c := range vm.Fields {
		if c.Stats == nil || len(c.Badges) == 0 {
			t.Errorf("card %q missing base payload: stats=%v badges=%d", c.Path, c.Stats, len(c.Badges))
		}
		if c.Form != FormEmpty && c.TypeMix == nil {
			t.Errorf("card %q missing typeMix (form=%s)", c.Path, c.Form)
		}
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"$":           "$",
		"id":          "id",
		"user.tags":   "tags",
		"user.tags[]": "tags[]",
		"a.b[]":       "b[]",
		"tags[]":      "tags[]",
		"a.b.c":       "c",
	}
	for in, want := range cases {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}
