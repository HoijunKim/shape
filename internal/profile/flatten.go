package profile

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Observation is one path node seen in one record.
type Observation struct {
	Path string
	Kind JSONKind
	Num  float64 // valid when Kind is KindInt or KindFloat
	Str  string  // valid when Kind is KindString
}

// Flatten walks a decoded record and emits an Observation per path node.
// If the record itself is an array, its container observation is emitted
// at path "$" and its elements are emitted under path "[]".
func Flatten(record any, emit func(Observation)) {
	walk("", record, emit)
}

func walk(path string, v any, emit func(Observation)) {
	switch t := v.(type) {
	case map[string]any:
		if path != "" {
			emit(Observation{Path: path, Kind: KindObject})
		}
		for k, cv := range t {
			child := k
			if path != "" {
				child = path + "." + k
			}
			walk(child, cv, emit)
		}
	case []any:
		emit(Observation{Path: rootOr(path), Kind: KindArray})
		elem := "[]"
		if path != "" {
			elem = path + "[]"
		}
		for _, cv := range t {
			walk(elem, cv, emit)
		}
	default:
		emit(scalarObservation(rootOr(path), t))
	}
}

func rootOr(path string) string {
	if path == "" {
		return "$"
	}
	return path
}

func scalarObservation(path string, v any) Observation {
	switch t := v.(type) {
	case nil:
		return Observation{Path: path, Kind: KindNull}
	case bool:
		return Observation{Path: path, Kind: KindBool}
	case string:
		return Observation{Path: path, Kind: KindString, Str: t}
	case json.Number:
		if strings.ContainsAny(t.String(), ".eE") {
			f, _ := t.Float64()
			return Observation{Path: path, Kind: KindFloat, Num: f}
		}
		i, _ := t.Int64()
		return Observation{Path: path, Kind: KindInt, Num: float64(i)}
	case float64:
		return Observation{Path: path, Kind: KindFloat, Num: t}
	default:
		return Observation{Path: path, Kind: KindString, Str: fmt.Sprintf("%v", t)}
	}
}
