package readers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// RecordStream yields one decoded record at a time until io.EOF. A record is
// what the profiler consumes via profile.Flatten: a map[string]any, []any, or a
// scalar whose value is already profiler-compatible (nil, bool, string,
// json.Number, or float64).
type RecordStream interface {
	Next() (any, error)
	Skipped() int
}

// Format identifies an input format.
type Format string

const (
	FormatJSON    Format = "json"
	FormatCSV     Format = "csv"
	FormatParquet Format = "parquet"
	FormatSQLite  Format = "sqlite"
)

// Source is a resolved input handle. Path is "" for stdin. Reader is set for
// streamable formats; file-only formats (parquet/sqlite) use Path.
type Source struct {
	Path      string
	Reader    io.Reader
	Peek      []byte
	RawFormat string
	CSVRaw    bool
}

// Factory builds a RecordStream and its cleanup for a Source.
type Factory func(Source) (RecordStream, func() error, error)

var registry = map[Format]Factory{}

// Register wires a format's factory; called from each reader package's init().
func Register(f Format, mk Factory) { registry[f] = mk }

// Open builds the stream for a format, or errors if the format is unregistered.
func Open(f Format, s Source) (RecordStream, func() error, error) {
	mk, ok := registry[f]
	if !ok {
		return nil, nil, fmt.Errorf("unsupported format %q (not built in)", f)
	}
	return mk(s)
}

// DetectFormat chooses a reader Format from an explicit flag, then the path
// extension, then a content peek.
func DetectFormat(path, formatFlag string, peek []byte) Format {
	switch formatFlag {
	case "json", "ndjson":
		return FormatJSON
	case "csv":
		return FormatCSV
	case "parquet":
		return FormatParquet
	case "sqlite":
		return FormatSQLite
	}
	l := strings.ToLower(path)
	switch {
	case strings.HasSuffix(l, ".csv"), strings.HasSuffix(l, ".tsv"):
		return FormatCSV
	case strings.HasSuffix(l, ".parquet"), strings.HasSuffix(l, ".pqt"):
		return FormatParquet
	case strings.HasSuffix(l, ".sqlite"), strings.HasSuffix(l, ".sqlite3"), strings.HasSuffix(l, ".db"):
		return FormatSQLite
	case strings.HasSuffix(l, ".json"), strings.HasSuffix(l, ".ndjson"), strings.HasSuffix(l, ".jsonl"):
		return FormatJSON
	}
	if bytes.HasPrefix(peek, []byte("PAR1")) {
		return FormatParquet
	}
	if bytes.HasPrefix(peek, []byte("SQLite format 3\x00")) {
		return FormatSQLite
	}
	return FormatJSON
}

// ToProfileValue maps a native typed cell value to the profiler-compatible set
// {nil, bool, string, json.Number, float64}. Integers become json.Number (the
// only route to KindInt); float32 becomes float64 (KindFloat); bytes/time
// become strings.
func ToProfileValue(v any) any {
	switch t := v.(type) {
	case nil, bool, string, float64, json.Number:
		return t
	case int:
		return json.Number(strconv.FormatInt(int64(t), 10))
	case int8:
		return json.Number(strconv.FormatInt(int64(t), 10))
	case int16:
		return json.Number(strconv.FormatInt(int64(t), 10))
	case int32:
		return json.Number(strconv.FormatInt(int64(t), 10))
	case int64:
		return json.Number(strconv.FormatInt(t, 10))
	case uint:
		return json.Number(strconv.FormatUint(uint64(t), 10))
	case uint8:
		return json.Number(strconv.FormatUint(uint64(t), 10))
	case uint16:
		return json.Number(strconv.FormatUint(uint64(t), 10))
	case uint32:
		return json.Number(strconv.FormatUint(uint64(t), 10))
	case uint64:
		return json.Number(strconv.FormatUint(t, 10))
	case float32:
		return float64(t)
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", t)
	}
}
