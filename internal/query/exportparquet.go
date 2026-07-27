package query

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// parquetBatchRows is how many rows the encoder buffers before handing a batch
// to the writer. It bounds memory (the whole point of a streaming export) while
// still giving parquet-go enough rows per call to be efficient.
const parquetBatchRows = 1024

// orderedGroup is a parquet.Group whose Fields() come back in an EXPLICIT
// order instead of sorted by name.
//
// parquet.Group is a map[string]Node and its own Fields() sorts alphabetically
// (parquet-go node.go), and a schema's field order is the file's column order
// -- so a plain Group would silently reorder the columns the user arranged in
// the transform panel. Everything except Fields() is inherited from the
// embedded Group.
type orderedGroup struct {
	parquet.Group
	order []string
}

func (g orderedGroup) Fields() []parquet.Field {
	fields := make([]parquet.Field, 0, len(g.order))
	for _, name := range g.order {
		fields = append(fields, &orderedField{Node: g.Group[name], name: name})
	}
	return fields
}

// orderedField is orderedGroup's parquet.Field: a Node plus the name it has in
// its parent.
//
// Value mirrors parquet-go's own unexported groupField.Value for the map case
// (unwrap an interface, then index the map). The writer does not consult it on
// the map[string]any path this encoder uses -- it goes through
// writeValue/reflect directly -- but parquet.Field requires it, and a Field
// that returned a zero Value would break any future path that does call it.
type orderedField struct {
	parquet.Node
	name string
}

func (f *orderedField) Name() string { return f.name }

func (f *orderedField) Value(base reflect.Value) reflect.Value {
	if base.Kind() == reflect.Interface {
		if base.IsNil() {
			return reflect.ValueOf(nil)
		}
		if base = base.Elem(); base.Kind() == reflect.Pointer && base.IsNil() {
			return reflect.ValueOf(nil)
		}
	}
	if base.Kind() != reflect.Map {
		return reflect.Value{}
	}
	return base.MapIndex(reflect.ValueOf(&f.name).Elem())
}

// parquetEncoder writes projected rows to a Parquet file whose schema is built
// at runtime from the output columns (spec §8's export formats).
//
// *** Every value is stored in the row map behind a freshly allocated POINTER.
// This is not a style choice. *** For an OPTIONAL leaf written through a
// map[string]any row, parquet-go decides nullness with
// `isNullValue -> value.IsZero()` (column_buffer_reflect.go), and the group
// writer hands it the unwrapped concrete value -- so a bare int64(0),
// float64(0), false or "" would be written as NULL, indistinguishable from a
// real null. A pointer is only "zero" when it is nil, which is exactly the
// semantics we want: nil pointer / absent key = null, non-nil = a value, even
// a zero one.
type parquetEncoder struct {
	w    *parquet.GenericWriter[any]
	cols []Column

	batch  []any
	nulled map[string]int64 // column path -> values that failed coercion
}

// newParquetEncoder returns an encoder writing a Parquet file to w whose
// columns are exactly cols, in that order. Column.Type picks each leaf's
// physical type; every column is OPTIONAL, so missing/null/uncoercible values
// have somewhere to go.
//
// Duplicate column paths are rejected here: the schema is keyed by name, so a
// duplicate would silently collapse two columns into one. ExportQuery validates
// the same rule earlier and with a better message; this is the backstop that
// makes the loss impossible rather than merely unlikely.
func newParquetEncoder(w io.Writer, cols []Column) (*parquetEncoder, error) {
	if len(cols) == 0 {
		return nil, fmt.Errorf("query: parquet export: no columns to write")
	}
	group := parquet.Group{}
	order := make([]string, 0, len(cols))
	for _, c := range cols {
		if _, dup := group[c.Path]; dup {
			return nil, fmt.Errorf("query: parquet export: duplicate column %q", c.Path)
		}
		group[c.Path] = parquetNodeFor(c.Type)
		order = append(order, c.Path)
	}
	schema := parquet.NewSchema("shape", orderedGroup{Group: group, order: order})
	return &parquetEncoder{
		w:      parquet.NewGenericWriter[any](w, schema),
		cols:   cols,
		batch:  make([]any, 0, parquetBatchRows),
		nulled: map[string]int64{},
	}, nil
}

// parquetNodeFor maps an engine Column.Type to an optional Parquet leaf.
// Anything that is not a plain scalar -- object, array, mixed (a drifting
// column), null, or an unknown/empty type -- becomes a UTF8 column holding
// compact JSON, so no value is ever unrepresentable.
func parquetNodeFor(colType string) parquet.Node {
	switch colType {
	case "int":
		return parquet.Optional(parquet.Int(64))
	case "float":
		return parquet.Optional(parquet.Leaf(parquet.DoubleType))
	case "bool":
		return parquet.Optional(parquet.Leaf(parquet.BooleanType))
	default:
		return parquet.Optional(parquet.String())
	}
}

// Encode buffers one row, flushing a full batch to the writer. values is the
// caller's reused scratch buffer (see RowEncoder), so every value is copied
// into a fresh per-row map here -- retaining the slice would make every
// buffered row alias the last one.
func (e *parquetEncoder) Encode(_ int64, values []any) error {
	row := make(map[string]any, len(e.cols))
	for i, c := range e.cols {
		if i >= len(values) {
			break
		}
		v := values[i]
		if IsMissing(v) || v == nil {
			continue // absent key => null, for both cases
		}
		coerced, ok := parquetValue(c.Type, v)
		if !ok {
			e.nulled[c.Path]++
			continue
		}
		row[c.Path] = coerced
	}
	e.batch = append(e.batch, row)
	if len(e.batch) >= parquetBatchRows {
		return e.flush()
	}
	return nil
}

// flush writes the buffered batch and resets it.
func (e *parquetEncoder) flush() error {
	if len(e.batch) == 0 {
		return nil
	}
	if _, err := e.w.Write(e.batch); err != nil {
		return fmt.Errorf("query: parquet export: %w", err)
	}
	e.batch = e.batch[:0]
	return nil
}

// Close flushes the tail batch and finalizes the file (footer + metadata).
func (e *parquetEncoder) Close() error {
	if err := e.flush(); err != nil {
		return err
	}
	if err := e.w.Close(); err != nil {
		return fmt.Errorf("query: parquet export: closing writer: %w", err)
	}
	return nil
}

// Warnings reports values that could not be represented in their column's
// Parquet type and were written as null -- the honest half of "pick a type per
// column": a drifting column exports as text and loses nothing, but a column
// the profiler typed as int cannot hold "n/a", and silently nulling it would be
// invisible data loss.
func (e *parquetEncoder) Warnings() []string {
	if len(e.nulled) == 0 {
		return nil
	}
	paths := make([]string, 0, len(e.nulled))
	var total int64
	for p, n := range e.nulled {
		paths = append(paths, p)
		total += n
	}
	sort.Strings(paths) // deterministic message, no map-iteration dependence
	return []string{fmt.Sprintf(
		"%d value(s) did not fit their Parquet column type and were written as null (%s) - export as JSON or NDJSON to keep them",
		total, strings.Join(paths, ", "),
	)}
}

// parquetValue coerces one projected value into the Go type its column's
// Parquet leaf expects, returning it BEHIND A POINTER (see parquetEncoder's
// doc comment: a bare zero value would be written as null). ok is false when
// the value cannot be represented in that column's type, which the caller
// records as a nulled value rather than failing the whole export.
func parquetValue(colType string, v any) (any, bool) {
	switch colType {
	case "int":
		switch t := v.(type) {
		case json.Number:
			n, err := strconv.ParseInt(t.String(), 10, 64)
			if err != nil {
				return nil, false
			}
			return &n, true
		case int64:
			n := t
			return &n, true
		case float64:
			if math.IsNaN(t) || math.IsInf(t, 0) || t != math.Trunc(t) {
				return nil, false // a fractional/non-finite value is not an int64
			}
			n := int64(t)
			return &n, true
		default:
			return nil, false
		}
	case "float":
		switch t := v.(type) {
		case json.Number:
			f, err := strconv.ParseFloat(t.String(), 64)
			if err != nil {
				return nil, false
			}
			return &f, true
		case float64:
			f := t // NaN/±Inf are representable in parquet, unlike JSON
			return &f, true
		case int64:
			f := float64(t)
			return &f, true
		default:
			return nil, false
		}
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, false
		}
		return &b, true
	default: // UTF8: strings verbatim, everything else as compact JSON
		switch t := v.(type) {
		case string:
			s := t
			return &s, true
		case []byte:
			s := string(t)
			return &s, true
		default:
			s := compactExportJSON(v)
			return &s, true
		}
	}
}
