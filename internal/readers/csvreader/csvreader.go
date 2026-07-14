package csvreader

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/hoijun-kim/shape/internal/readers"
)

var _ readers.RecordStream = (*stream)(nil)

func init() {
	readers.Register(readers.FormatCSV, open)
}

func open(s readers.Source) (readers.RecordStream, func() error, error) {
	comma := ','
	l := strings.ToLower(s.Path)
	if strings.HasSuffix(l, ".tsv") || strings.HasSuffix(l, ".tab") {
		comma = '\t'
	}
	return newStream(s.Reader, s.CSVRaw, comma), func() error { return nil }, nil
}

type stream struct {
	r       *csv.Reader
	header  []string
	raw     bool
	started bool
	skipped int
}

func newStream(rd io.Reader, raw bool, comma rune) *stream {
	c := csv.NewReader(rd)
	c.Comma = comma
	c.FieldsPerRecord = -1 // tolerate ragged rows
	return &stream{r: c, raw: raw}
}

func (s *stream) Next() (any, error) {
	if !s.started {
		h, err := s.r.Read()
		if err != nil {
			return nil, err // io.EOF on an empty file
		}
		s.header = h
		s.started = true
	}
	rec, err := s.r.Read()
	if err != nil {
		// A CSV parse error (e.g. an unterminated quote) desyncs the whole
		// stream; unlike NDJSON we cannot reliably resync per line, so surface
		// the error and abort rather than silently skip.
		return nil, err
	}
	row := make(map[string]any, len(s.header))
	for i, col := range s.header {
		cell := ""
		if i < len(rec) {
			cell = rec[i]
		}
		if s.raw {
			if cell == "" {
				row[col] = nil
			} else {
				row[col] = cell
			}
		} else {
			row[col] = inferValue(cell)
		}
	}
	return row, nil
}

func (s *stream) Skipped() int { return s.skipped }

// inferValue applies the default CSV type-inference policy.
func inferValue(cell string) any {
	switch {
	case cell == "":
		return nil
	case cell == "true":
		return true
	case cell == "false":
		return false
	case isIntLiteral(cell):
		return json.Number(cell)
	case isFloatLiteral(cell):
		return json.Number(cell)
	default:
		return cell
	}
}

// isIntLiteral accepts a strict decimal integer, rejecting leading-zero codes
// ("007") and very long digit strings (identifiers) so they stay strings.
func isIntLiteral(s string) bool {
	body := s
	if strings.HasPrefix(body, "-") {
		body = body[1:]
	}
	if body == "" || len(body) > 15 {
		return false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	if len(body) > 1 && body[0] == '0' {
		return false
	}
	return true
}

// isFloatLiteral accepts a real decimal/scientific float (must contain '.' or
// an exponent, and not be NaN/Inf).
func isFloatLiteral(s string) bool {
	if strings.Contains(s, "_") {
		return false
	}
	if !strings.ContainsAny(s, ".eE") {
		return false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return true
}
