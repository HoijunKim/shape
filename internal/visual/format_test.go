package visual

import "testing"

func TestFmtPct(t *testing.T) {
	cases := map[float64]string{0: "0%", 0.5: "50%", 0.976: "98%", 1: "100%", 0.024: "2%"}
	for in, want := range cases {
		if got := fmtPct(in); got != want {
			t.Errorf("fmtPct(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFmtInt(t *testing.T) {
	cases := map[int]string{0: "0", 5: "5", 1000: "1,000", 12480: "12,480", -12480: "-12,480", 1000000: "1,000,000"}
	for in, want := range cases {
		if got := fmtInt(in); got != want {
			t.Errorf("fmtInt(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFmtNum(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"}, {42, "42"}, {3.14159, "3.14"}, {3.5, "3.5"}, {1500, "1,500"},
		{12000, "12K"}, {1500000, "1.5M"}, {2000000000, "2B"}, {3.5e12, "3.5T"},
	}
	for _, c := range cases {
		if got := fmtNum(c.in); got != c.want {
			t.Errorf("fmtNum(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFmtNumNonFinite(t *testing.T) {
	for _, v := range []float64{nan(), inf(1), inf(-1)} {
		if got := fmtNum(v); got != "—" {
			t.Errorf("fmtNum(%v) = %q, want em-dash", v, got)
		}
	}
}

func TestFmtDistinct(t *testing.T) {
	if got := fmtDistinct(48210, true); got != "48,210" {
		t.Errorf("exact = %q, want 48,210", got)
	}
	if got := fmtDistinct(48210, false); got != "~48,210" {
		t.Errorf("approx = %q, want ~48,210", got)
	}
}

func TestSafeDiv(t *testing.T) {
	if got := safeDiv(1, 0); got != 0 {
		t.Errorf("safeDiv(1,0) = %v, want 0", got)
	}
	if got := safeDiv(3, 6); got != 0.5 {
		t.Errorf("safeDiv(3,6) = %v, want 0.5", got)
	}
}

func TestDeriveFormat(t *testing.T) {
	cases := map[string]string{
		"a.csv": "CSV", "b.tsv": "TSV", "c.parquet": "Parquet", "d.sqlite": "SQLite",
		"e.ndjson": "NDJSON", "f.jsonl": "NDJSON", "g.json": "JSON", "": "—",
	}
	for in, want := range cases {
		if got := deriveFormat(in); got != want {
			t.Errorf("deriveFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

// nan/inf/half-up helpers local to the test (avoid importing math at top just for these).
func nan() float64 { var z float64; return z / z }
func inf(s int) float64 {
	z := 0.0
	if s < 0 {
		return -1 / z
	}
	return 1 / z
}
