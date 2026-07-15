package visual

import (
	"math"
	"strconv"
	"strings"
)

func fmtPct(f float64) string { return strconv.Itoa(int(f*100+0.5)) + "%" }

func fmtInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func fmtDistinct(n int, exact bool) string {
	if exact {
		return fmtInt(n)
	}
	return "~" + fmtInt(n)
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// trim1 formats x to one decimal and drops a trailing ".0".
func trim1(x float64) string {
	s := strconv.FormatFloat(x, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// fmtNum formats a numeric value for display. Non-finite -> em-dash. Compact
// SI-ish suffix at/above 1e4; integer-valued prints grouped; else 2 decimals
// with trailing zeros trimmed.
func fmtNum(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "—"
	}
	abs := math.Abs(f)
	switch {
	case abs >= 1e12:
		return trim1(f/1e12) + "T"
	case abs >= 1e9:
		return trim1(f/1e9) + "B"
	case abs >= 1e6:
		return trim1(f/1e6) + "M"
	case abs >= 1e4:
		return trim1(f/1e3) + "K"
	}
	if f == math.Trunc(f) {
		return fmtInt(int(f))
	}
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// deriveFormat maps a filename/label extension to a format display label.
func deriveFormat(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".csv"):
		return "CSV"
	case strings.HasSuffix(l, ".tsv"):
		return "TSV"
	case strings.HasSuffix(l, ".parquet"), strings.HasSuffix(l, ".pqt"):
		return "Parquet"
	case strings.HasSuffix(l, ".sqlite"), strings.HasSuffix(l, ".sqlite3"), strings.HasSuffix(l, ".db"):
		return "SQLite"
	case strings.HasSuffix(l, ".ndjson"), strings.HasSuffix(l, ".jsonl"):
		return "NDJSON"
	case strings.HasSuffix(l, ".json"):
		return "JSON"
	case name == "":
		return "—"
	default:
		if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
			return strings.ToUpper(name[i+1:])
		}
		return "—"
	}
}
