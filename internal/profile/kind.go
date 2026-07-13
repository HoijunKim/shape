package profile

import (
	"encoding/json"
	"strings"
)

// JSONKind is the classified kind of a decoded JSON value.
type JSONKind string

const (
	KindNull   JSONKind = "null"
	KindBool   JSONKind = "bool"
	KindInt    JSONKind = "int"
	KindFloat  JSONKind = "float"
	KindString JSONKind = "string"
	KindArray  JSONKind = "array"
	KindObject JSONKind = "object"
)

// KindOf classifies a value produced by encoding/json with UseNumber().
func KindOf(v any) JSONKind {
	switch t := v.(type) {
	case nil:
		return KindNull
	case bool:
		return KindBool
	case string:
		return KindString
	case json.Number:
		if strings.ContainsAny(t.String(), ".eE") {
			return KindFloat
		}
		return KindInt
	case float64: // fallback if a caller decoded without UseNumber
		return KindFloat
	case []any:
		return KindArray
	case map[string]any:
		return KindObject
	default:
		return KindString
	}
}
