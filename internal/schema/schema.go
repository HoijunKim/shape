package schema

import (
	"sort"

	"github.com/hoijun-kim/shape/internal/profile"
)

const draft = "https://json-schema.org/draft/2020-12/schema"

// typeOrder is the canonical, deterministic ordering for a type array.
var typeOrder = []string{"object", "array", "string", "number", "integer", "boolean", "null"}

// Reconstruct builds a Draft 2020-12 JSON Schema describing one record of res.
func Reconstruct(res profile.ProfileResult) map[string]any {
	s := nodeToSchema(buildTree(res.Fields), res.Records)
	s["$schema"] = draft
	return s
}

// nodeToSchema folds a tree node into a JSON Schema subschema.
func nodeToSchema(n *node, records int) map[string]any {
	concrete, nullable := n.typeSet()
	branches := make([]map[string]any, 0, len(concrete))
	for _, t := range concrete {
		branches = append(branches, buildBranch(t, n, records, len(concrete) == 1))
	}
	return combine(branches, nullable)
}

// typeSet returns this node's concrete (non-null) JSON Schema types in canonical
// order, plus whether null was observed. Structure (properties/elem) unions with
// the profile's TypeDist; integer collapses into number when both appear.
func (n *node) typeSet() (types []string, nullable bool) {
	set := map[string]bool{}
	if n.profile != nil {
		for k, frac := range n.profile.TypeDist {
			if frac <= 0 {
				continue
			}
			switch k {
			case profile.KindNull:
				nullable = true
			case profile.KindBool:
				set["boolean"] = true
			case profile.KindInt:
				set["integer"] = true
			case profile.KindFloat:
				set["number"] = true
			case profile.KindString:
				set["string"] = true
			case profile.KindArray:
				set["array"] = true
			case profile.KindObject:
				set["object"] = true
			}
		}
		if n.profile.NullRate > 0 {
			nullable = true
		}
	}
	if len(n.props) > 0 {
		set["object"] = true
	}
	if n.elem != nil {
		set["array"] = true
	}
	if set["integer"] && set["number"] {
		delete(set, "integer")
	}
	return canonicalTypes(set), nullable
}

// canonicalTypes returns the set's members in fixed typeOrder.
func canonicalTypes(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for _, t := range typeOrder {
		if set[t] {
			out = append(out, t)
		}
	}
	return out
}

// buildBranch builds one concrete-type subschema. sole reports whether this is
// the node's only concrete type (used by later tasks to gate enum).
func buildBranch(t string, n *node, records int, sole bool) map[string]any {
	switch t {
	case "object":
		ps := map[string]any{}
		var required []string
		selfPresent := presentCount(n, records)
		for _, k := range sortedKeys(n.props) {
			child := n.props[k]
			ps[k] = nodeToSchema(child, records)
			if !n.underArr && records > 0 && presentCount(child, records) >= selfPresent {
				required = append(required, k)
			}
		}
		b := map[string]any{"type": "object", "properties": ps}
		if len(required) > 0 {
			b["required"] = required // already in sorted-key order
		}
		return b
	case "array":
		b := map[string]any{"type": "array"}
		if n.elem != nil {
			b["items"] = nodeToSchema(n.elem, records)
		}
		return b
	case "string":
		b := map[string]any{"type": "string"}
		if sole && n.profile != nil && enumOK(n.profile) {
			b["enum"] = enumValues(n.profile)
		}
		return b
	default:
		return map[string]any{"type": t}
	}
}

// enumOK reports whether the profiler retained the field's COMPLETE distinct
// value set, so an enum can be listed soundly.
func enumOK(fp *profile.FieldProfile) bool {
	return fp.DistinctExact && fp.DistinctCount > 0 &&
		fp.DistinctCount == len(fp.TopValues)
}

// enumValues returns the sorted distinct string values as schema enum members.
func enumValues(fp *profile.FieldProfile) []any {
	vals := make([]string, 0, len(fp.TopValues))
	for _, v := range fp.TopValues {
		vals = append(vals, v.Value)
	}
	sort.Strings(vals)
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}

// combine merges concrete-type branches and an optional null into one schema.
// Bare {"type": X} branches collapse into a single type array. A single
// enum-bearing branch collapses alongside them too: null is added to the enum
// members whenever null is one of the collapsed types, so the schema does not
// reject the null values that justified the type union. Anything richer
// (object/array) is kept as its own branch under anyOf.
func combine(branches []map[string]any, nullable bool) map[string]any {
	if nullable {
		branches = append(branches, map[string]any{"type": "null"})
	}
	if len(branches) == 0 {
		return map[string]any{}
	}
	types := make([]string, 0, len(branches))
	var enum []any
	allSimple := true
	for _, b := range branches {
		t, hasType := b["type"].(string)
		if !hasType {
			allSimple = false
			break
		}
		want := 1
		if e, ok := b["enum"].([]any); ok && enum == nil {
			enum = e
			want = 2
		}
		if len(b) != want {
			allSimple = false
			break
		}
		types = append(types, t)
	}
	if allSimple {
		if enum != nil {
			for _, t := range types {
				if t == "null" {
					enum = append(enum, nil)
				}
			}
		}
		if len(types) == 1 {
			out := map[string]any{"type": types[0]}
			if enum != nil {
				out["enum"] = enum
			}
			return out
		}
		set := map[string]bool{}
		for _, t := range types {
			set[t] = true
		}
		ordered := canonicalTypes(set)
		arr := make([]any, len(ordered))
		for i, t := range ordered {
			arr[i] = t
		}
		out := map[string]any{"type": arr}
		if enum != nil {
			out["enum"] = enum
		}
		return out
	}
	if len(branches) == 1 {
		return branches[0]
	}
	return map[string]any{"anyOf": branches}
}

// presentCount rounds a node's observed presence to a record count. The
// synthetic root object (nil profile) is present in every record.
func presentCount(n *node, records int) int {
	if n.profile == nil {
		return records
	}
	return int(n.profile.PresenceRate*float64(records) + 0.5)
}

func sortedKeys(m map[string]*node) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
