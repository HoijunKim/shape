// Package query's export layer: the streaming RowEncoders behind
// Engine.ExportQuery (spec §8). Every encoder takes the RAW projected values
// a Backend.Export hands it (see RowEncoder, backend.go) and writes them
// straight to an io.Writer, one row at a time, so an export costs bounded
// memory at any file size and never truncates a value the way a display Cell
// would.
package query

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
)

// ExportFormat is the wire format ExportQuery writes. The names are the
// strings the GUI sends; they are deliberately NOT readers.Format values --
// those describe what shape can READ (four formats, with json/ndjson and
// csv/tsv as sub-modes), while these describe what it can WRITE (five, with
// the sub-modes promoted to first-class choices, because "export as NDJSON"
// and "export as JSON array" are different user intents).
type ExportFormat string

const (
	ExportJSON    ExportFormat = "json"   // one JSON array
	ExportNDJSON  ExportFormat = "ndjson" // one JSON object per line
	ExportCSV     ExportFormat = "csv"
	ExportTSV     ExportFormat = "tsv"
	ExportParquet ExportFormat = "parquet"
)

// rowEncoder is the internal encoder contract: a RowEncoder that also has a
// tail to write. Close is where the JSON array's "]" and the delimited
// encoders' buffered bytes land, so ExportQuery must call it BEFORE flushing
// whatever buffered writer sits underneath (see ExportQuery's ordering note).
type rowEncoder interface {
	RowEncoder
	Close() error
}

// jsonEncoder writes projected rows as JSON objects, either as one array
// (array == true, ExportJSON) or one object per line (array == false,
// ExportNDJSON).
//
// The object framing is hand-rolled rather than delegated to
// json.Marshal(map[string]any) for three reasons, each pinned by a test:
//  1. ORDER: Go marshals map keys sorted, which would silently reorder the
//     columns the user picked in the transform panel.
//  2. MISSING: a map cannot express "this record does not have this path at
//     all" separately from "it has null" -- here a Missing value simply omits
//     its key.
//  3. ESCAPING: json.Encoder escapes <, > and & by default, which mangles
//     ordinary data (URLs, HTML snippets) in an exported file.
//
// Keys are Column.Path, never Column.Name: base columns are named by their
// LEAF (columns.go), so {"user":{"id":..},"order":{"id":..}} yields two
// columns both NAMED "id" whose paths are what actually distinguishes them.
type jsonEncoder struct {
	w     io.Writer
	cols  []Column
	array bool

	wrote   bool // at least one row has been written (array framing)
	line    bytes.Buffer
	scratch bytes.Buffer
	enc     *json.Encoder
}

// newJSONEncoder returns a jsonEncoder writing to w. cols fixes the key set
// and its order; array selects JSON-array framing over line framing.
func newJSONEncoder(w io.Writer, cols []Column, array bool) *jsonEncoder {
	e := &jsonEncoder{w: w, cols: cols, array: array}
	e.enc = json.NewEncoder(&e.scratch)
	e.enc.SetEscapeHTML(false)
	return e
}

// Encode writes one row. values is the caller's reused scratch buffer (see
// RowEncoder); nothing is retained past this call.
func (e *jsonEncoder) Encode(_ int64, values []any) error {
	e.line.Reset()
	if e.array {
		if e.wrote {
			e.line.WriteString(",\n")
		} else {
			e.line.WriteString("[\n")
		}
	}
	e.line.WriteByte('{')
	first := true
	for i, c := range e.cols {
		if i >= len(values) {
			break
		}
		v := values[i]
		if IsMissing(v) {
			continue // an absent path writes no key at all
		}
		if !first {
			e.line.WriteByte(',')
		}
		first = false
		if err := e.writeValue(c.Path); err != nil {
			return err
		}
		e.line.WriteByte(':')
		if err := e.writeValue(jsonSafe(v)); err != nil {
			return err
		}
	}
	e.line.WriteByte('}')
	if !e.array {
		e.line.WriteByte('\n')
	}
	if _, err := e.w.Write(e.line.Bytes()); err != nil {
		return err
	}
	e.wrote = true
	return nil
}

// writeValue appends v's JSON encoding to the pending line, without the
// trailing newline json.Encoder adds.
func (e *jsonEncoder) writeValue(v any) error {
	e.scratch.Reset()
	if err := e.enc.Encode(v); err != nil {
		return fmt.Errorf("query: export: encoding value: %w", err)
	}
	e.line.Write(bytes.TrimRight(e.scratch.Bytes(), "\n"))
	return nil
}

// Close writes the array terminator. A zero-row array export is "[]\n" --
// valid JSON -- rather than an empty file; a zero-row line export is genuinely
// empty, which is valid NDJSON.
func (e *jsonEncoder) Close() error {
	if !e.array {
		return nil
	}
	if !e.wrote {
		_, err := io.WriteString(e.w, "[]\n")
		return err
	}
	_, err := io.WriteString(e.w, "\n]\n")
	return err
}

// jsonSafe returns v with everything encoding/json cannot represent replaced:
// a non-finite float64 (NaN, ±Inf) becomes nil, and []byte becomes a string
// rather than base64. Readers normalize cells to
// {nil,bool,string,json.Number,float64} plus nested map/slice
// (readers.ToProfileValue), so in practice only the float case fires -- but it
// fires on data shape's own Parquet and SQLite readers can produce, and
// json.Marshal ERRORS on those values, which would abort an export mid-file
// rather than degrade one cell.
//
// The input is never mutated: when a rewrite is needed a copy is built, and
// when it is not (the overwhelmingly common case) v is returned as-is, so the
// hot path allocates nothing.
func jsonSafe(v any) any {
	if !needsJSONRewrite(v) {
		return v
	}
	switch t := v.(type) {
	case float64:
		return nil // non-finite: needsJSONRewrite only says yes for those
	case []byte:
		return string(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = jsonSafe(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = jsonSafe(val)
		}
		return out
	default:
		return v
	}
}

// needsJSONRewrite reports whether jsonSafe would change v, so the common
// (clean) case can return the original value untouched.
func needsJSONRewrite(v any) bool {
	switch t := v.(type) {
	case float64:
		return math.IsNaN(t) || math.IsInf(t, 0)
	case []byte:
		return true
	case map[string]any:
		for _, val := range t {
			if needsJSONRewrite(val) {
				return true
			}
		}
		return false
	case []any:
		for _, val := range t {
			if needsJSONRewrite(val) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// delimitedEncoder writes projected rows as CSV (comma) or TSV (tab) via
// encoding/csv, which owns all quoting/escaping decisions -- a value
// containing the delimiter, a quote, or a newline round-trips through
// csv.Reader unchanged.
//
// Every value becomes exactly one string (see delimitedCell). CSV cannot
// express structure, so containers are compact JSON in a single field, and
// missing / null / "" all collapse to an empty field -- a documented,
// unavoidable loss of the missing-vs-null distinction that JSON, NDJSON and
// Parquet all keep.
type delimitedEncoder struct {
	w      *csv.Writer
	cols   []Column
	header bool     // header row already written
	rec    []string // reused per row
}

// newDelimitedEncoder returns an encoder writing to w with the given field
// delimiter (',' for CSV, '\t' for TSV; encoding/csv rejects '"', '\r' and
// '\n', which is why only those two are offered).
func newDelimitedEncoder(w io.Writer, cols []Column, comma rune) *delimitedEncoder {
	cw := csv.NewWriter(w)
	cw.Comma = comma
	return &delimitedEncoder{w: cw, cols: cols, rec: make([]string, len(cols))}
}

// writeHeader writes the header row exactly once. It is called lazily -- from
// the first Encode AND from Close -- so a zero-row export still describes its
// own shape (a header-only file) instead of being empty, while a many-row
// export never repeats it. Header cells are Column.Path, matching the JSON
// encoders' key choice (base columns are leaf-NAMED, so only the path is
// unique).
func (e *delimitedEncoder) writeHeader() error {
	if e.header {
		return nil
	}
	e.header = true
	for i, c := range e.cols {
		e.rec[i] = c.Path
	}
	return e.w.Write(e.rec)
}

// Encode writes one row. values is the caller's reused scratch buffer (see
// RowEncoder); csv.Writer copies what it needs, so nothing is retained.
func (e *delimitedEncoder) Encode(_ int64, values []any) error {
	if err := e.writeHeader(); err != nil {
		return err
	}
	for i := range e.cols {
		if i >= len(values) {
			e.rec[i] = ""
			continue
		}
		e.rec[i] = delimitedCell(values[i])
	}
	return e.w.Write(e.rec)
}

// Close writes a header for an otherwise empty export, then flushes.
func (e *delimitedEncoder) Close() error {
	if err := e.writeHeader(); err != nil {
		return err
	}
	e.w.Flush()
	return e.w.Error()
}

// delimitedCell renders one projected value as a single CSV/TSV field.
//
// Numbers keep their EXACT source literal (json.Number is already the text the
// file contained, so 64-bit ints and long decimals do not degrade through
// float64). A float64 -- which only reaches here from Parquet/SQLite -- is
// formatted with 'g'/-1, the shortest representation that round-trips, and
// non-finite values render as Go's NaN/+Inf/-Inf text rather than an empty
// field, so they stay visible rather than looking like missing data.
func delimitedCell(v any) string {
	if IsMissing(v) || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []byte:
		return string(t)
	default:
		return compactExportJSON(v)
	}
}

// compactExportJSON renders v as single-line JSON for a text field, with HTML
// escaping OFF (a data export must not turn "<x>" into "<x>") and
// non-finite floats nulled (jsonSafe), so it can never fail mid-row. It is the
// container representation for CSV/TSV cells and for Parquet's JSON-in-a-
// string columns.
func compactExportJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(jsonSafe(v)); err != nil {
		return ""
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}
