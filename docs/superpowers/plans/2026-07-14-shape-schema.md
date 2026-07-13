# Shape Plan 2: `shape schema` - infer and export a JSON Schema

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `shape schema <file|->` subcommand that reconstructs a nested JSON Schema (Draft 2020-12) from Plan 1's flattened per-path profile and writes it to stdout or a file.

**Architecture:** A new pure package `internal/schema` turns a `profile.ProfileResult` into a schema tree (`buildTree`) and folds it into a `map[string]any` Draft 2020-12 schema (`Reconstruct`/`nodeToSchema`). The `shape schema` command profiles the input using a shared `profileSource` helper (extracted from Plan 1's profile command) and marshals the schema. Soundness first: only emit a keyword the profile can prove.

**Tech Stack:** Go, `spf13/cobra`, `encoding/json`, standard `testing`.

## Global Constraints

- Module path: `github.com/hoijun-kim/shape`. Go floor `go 1.23`.
- New package `internal/schema` may import `internal/profile`. It MUST NOT import `internal/readers`, `internal/render`, or `internal/cmd`.
- Plain ASCII hyphen `-` only in code and docs. Never the em dash, en dash, or middle dot.
- Commits: Conventional Commits, NO co-author trailer.
- TDD: failing test first, then minimal implementation, then green, then commit.
- The `$schema` value is exactly `https://json-schema.org/draft/2020-12/schema`.
- Soundness rules (never claim what the stats cannot prove): objects stay open (omit `additionalProperties`); `required` is parent-conditional presence == 1.0 and is suppressed inside array elements; `enum` only when `DistinctExact && DistinctCount == len(TopValues)`; NO `format`, NO `pattern`, NO numeric/length bounds, NO `minItems`/`maxItems`/`uniqueItems`/`multipleOf` in v1.
- Deterministic output: object property keys sorted; type arrays in fixed canonical order (`object, array, string, number, integer, boolean, null`); enum values sorted.

---

### Task 1: Path tree (parsePath + buildTree)

**Files:**
- Create: `internal/schema/tree.go`
- Test: `internal/schema/tree_test.go`

**Interfaces:**
- Consumes: `profile.FieldProfile` (Plan 1).
- Produces:
  - `type step struct { key string; isElem bool }`
  - `func parsePath(p string) []step` - `"$"`/`""` -> nil; split on `.`; a trailing `[]` on a segment becomes an `isElem` step after the key step.
  - `type node struct { props map[string]*node; elem *node; profile *profile.FieldProfile; underArr bool }`
  - `func newNode() *node`
  - `func buildTree(fields []profile.FieldProfile) *node` - threads each field down its parsed path; object keys become `props` children, `[]` becomes the single shared `elem` child; sets `underArr` on any node reached through an elem step; attaches `&field` to the terminal node's `profile`.

- [ ] **Step 1: Write the failing test**

`internal/schema/tree_test.go`:

```go
package schema

import (
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		in   string
		want []step
	}{
		{"$", nil},
		{"", nil},
		{"email", []step{{key: "email"}}},
		{"user.email", []step{{key: "user"}, {key: "email"}}},
		{"items[].price", []step{{key: "items"}, {isElem: true}, {key: "price"}}},
		{"[]", []step{{isElem: true}}},
		{"matrix[][]", []step{{key: "matrix"}, {isElem: true}, {isElem: true}}},
	}
	for _, c := range cases {
		got := parsePath(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parsePath(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parsePath(%q)[%d] = %v, want %v", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestBuildTree(t *testing.T) {
	fields := []profile.FieldProfile{
		{Path: "id"},
		{Path: "user"},
		{Path: "user.name"},
		{Path: "tags"},
		{Path: "tags[]"},
	}
	root := buildTree(fields)
	if root.props["id"] == nil || root.props["id"].profile == nil {
		t.Fatal("id not attached")
	}
	if root.props["user"].props["name"] == nil {
		t.Fatal("user.name not nested under user")
	}
	tags := root.props["tags"]
	if tags.elem == nil || tags.elem.profile == nil {
		t.Fatal("tags[] not attached to tags.elem")
	}
	if !tags.elem.underArr {
		t.Error("tags[] element should be marked underArr")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/schema/ -v`
Expected: FAIL (build error: undefined symbols).

- [ ] **Step 3: Write minimal implementation**

`internal/schema/tree.go`:

```go
package schema

import (
	"strings"

	"github.com/hoijun-kim/shape/internal/profile"
)

type step struct {
	key    string
	isElem bool
}

// parsePath turns a flattened profile path into ordered tree steps.
func parsePath(p string) []step {
	if p == "$" || p == "" {
		return nil
	}
	var out []step
	for _, seg := range strings.Split(p, ".") {
		n := 0
		for strings.HasSuffix(seg, "[]") {
			seg = seg[:len(seg)-2]
			n++
		}
		if seg != "" {
			out = append(out, step{key: seg})
		}
		for i := 0; i < n; i++ {
			out = append(out, step{isElem: true})
		}
	}
	return out
}

type node struct {
	props    map[string]*node
	elem     *node
	profile  *profile.FieldProfile
	underArr bool
}

func newNode() *node { return &node{props: map[string]*node{}} }

// buildTree threads every field down its parsed path into an intermediate tree.
func buildTree(fields []profile.FieldProfile) *node {
	root := newNode()
	for i := range fields {
		fp := &fields[i]
		cur := root
		under := false
		for _, s := range parsePath(fp.Path) {
			if s.isElem {
				if cur.elem == nil {
					cur.elem = newNode()
				}
				cur = cur.elem
				under = true
			} else {
				ch := cur.props[s.key]
				if ch == nil {
					ch = newNode()
					cur.props[s.key] = ch
				}
				cur = ch
			}
			cur.underArr = cur.underArr || under
		}
		cur.profile = fp
	}
	return root
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/schema/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/schema/tree.go internal/schema/tree_test.go
git commit -m "feat: parse flattened profile paths into a schema tree"
```

---

### Task 2: Structural schema (types, nesting, null unions, drift anyOf)

**Files:**
- Create: `internal/schema/schema.go`
- Test: `internal/schema/schema_test.go`

**Interfaces:**
- Consumes: `node`, `buildTree` (Task 1), `profile.ProfileResult`, `profile.FieldProfile`, `profile.JSONKind` consts (Plan 1).
- Produces:
  - `func Reconstruct(res profile.ProfileResult) map[string]any` - top-level schema with `$schema`.
  - `func nodeToSchema(n *node, records int) map[string]any`
  - internal helpers: `(*node).typeSet()`, `buildBranch`, `combine`, `canonicalTypes`, `sortedKeys`.

This task emits sound STRUCTURE: correct types, nested `properties`, array `items`, null unions, and `anyOf` for container/scalar drift. It does NOT yet emit `required` (Task 3) or `enum` (Task 4).

- [ ] **Step 1: Write the failing test**

`internal/schema/schema_test.go`:

```go
package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoijun-kim/shape/internal/profile"
)

// build profiles a set of NDJSON records and reconstructs their schema.
func build(t *testing.T, records ...string) map[string]any {
	t.Helper()
	p := profile.NewProfiler()
	for _, r := range records {
		d := json.NewDecoder(strings.NewReader(r))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			t.Fatalf("decode %q: %v", r, err)
		}
		p.AddRecord(v)
	}
	return Reconstruct(p.Result())
}

func props(t *testing.T, s map[string]any) map[string]any {
	t.Helper()
	p, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties in %v", s)
	}
	return p
}

func TestSchemaRootAndTypes(t *testing.T) {
	s := build(t, `{"name":"x","age":30}`)
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", s["$schema"])
	}
	if s["type"] != "object" {
		t.Errorf("root type = %v, want object", s["type"])
	}
	p := props(t, s)
	if p["name"].(map[string]any)["type"] != "string" {
		t.Errorf("name type = %v, want string", p["name"])
	}
	if p["age"].(map[string]any)["type"] != "integer" {
		t.Errorf("age type = %v, want integer", p["age"])
	}
}

func TestSchemaNullUnion(t *testing.T) {
	s := build(t, `{"e":"a@b.c"}`, `{"e":null}`)
	e := props(t, s)["e"].(map[string]any)
	ty, ok := e["type"].([]any)
	if !ok || len(ty) != 2 || ty[0] != "string" || ty[1] != "null" {
		t.Errorf("e type = %v, want [string null]", e["type"])
	}
}

func TestSchemaDriftTypeArray(t *testing.T) {
	s := build(t, `{"id":1}`, `{"id":"two"}`)
	id := props(t, s)["id"].(map[string]any)
	ty, ok := id["type"].([]any)
	if !ok || len(ty) != 2 || ty[0] != "string" || ty[1] != "integer" {
		t.Errorf("id type = %v, want [string integer] (canonical order)", id["type"])
	}
}

func TestSchemaNestedAndArray(t *testing.T) {
	s := build(t, `{"user":{"name":"x"},"tags":["a","b"]}`)
	p := props(t, s)
	user := p["user"].(map[string]any)
	if user["type"] != "object" || props(t, user)["name"].(map[string]any)["type"] != "string" {
		t.Errorf("user schema wrong: %v", user)
	}
	tags := p["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Fatalf("tags type = %v, want array", tags["type"])
	}
	if tags["items"].(map[string]any)["type"] != "string" {
		t.Errorf("tags items = %v, want string", tags["items"])
	}
}

func TestSchemaEmptyArrayNoItems(t *testing.T) {
	s := build(t, `{"tags":[]}`)
	tags := props(t, s)["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Fatalf("tags type = %v, want array", tags["type"])
	}
	if _, has := tags["items"]; has {
		t.Errorf("empty array must not emit items, got %v", tags["items"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/schema/ -run TestSchema -v`
Expected: FAIL (build error: `Reconstruct`/`nodeToSchema` undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/schema/schema.go`:

```go
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
		for _, k := range sortedKeys(n.props) {
			ps[k] = nodeToSchema(n.props[k], records)
		}
		return map[string]any{"type": "object", "properties": ps}
	case "array":
		b := map[string]any{"type": "array"}
		if n.elem != nil {
			b["items"] = nodeToSchema(n.elem, records)
		}
		return b
	default:
		return map[string]any{"type": t}
	}
}

// combine merges concrete-type branches and an optional null into one schema.
// Bare {"type": X} branches collapse into a single type array; anything richer
// (object/array/enum) is kept as its own branch under anyOf.
func combine(branches []map[string]any, nullable bool) map[string]any {
	if nullable {
		branches = append(branches, map[string]any{"type": "null"})
	}
	if len(branches) == 0 {
		return map[string]any{}
	}
	types := make([]string, 0, len(branches))
	allSimple := true
	for _, b := range branches {
		if len(b) == 1 {
			if t, ok := b["type"].(string); ok {
				types = append(types, t)
				continue
			}
		}
		allSimple = false
		break
	}
	if allSimple {
		if len(types) == 1 {
			return map[string]any{"type": types[0]}
		}
		set := map[string]bool{}
		for _, t := range types {
			set[t] = true
		}
		return map[string]any{"type": canonicalTypes(set)}
	}
	if len(branches) == 1 {
		return branches[0]
	}
	return map[string]any{"anyOf": branches}
}

func sortedKeys(m map[string]*node) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/schema/ -run TestSchema -v` then `go test ./internal/schema/ -count=1`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/schema/schema.go internal/schema/schema_test.go
git commit -m "feat: reconstruct structural JSON Schema from a profile tree"
```

---

### Task 3: Required properties (parent-conditional presence)

**Files:**
- Modify: `internal/schema/schema.go` (object branch in `buildBranch`; add `presentCount`)
- Test: `internal/schema/schema_test.go` (add cases)

**Interfaces:**
- Consumes: everything from Task 2.
- Produces: `func presentCount(n *node, records int) int` and a `required` array on object schemas.

Rule: a child key is `required` on its parent object iff the child is present in every record the parent is present, i.e. `presentCount(child) >= presentCount(parent)` (child presence is a subset of parent presence, so this holds only on equality). Suppress `required` entirely when the object node is `underArr` (per-element presence is unknown inside arrays), and when `records == 0`.

- [ ] **Step 1: Add failing tests**

Append to `internal/schema/schema_test.go`:

```go
func TestSchemaRequiredParentConditional(t *testing.T) {
	// id present in all 3; email present in 2 of 3 -> only id required.
	s := build(t,
		`{"id":1,"email":"a@b.c"}`,
		`{"id":2,"email":null}`,
		`{"id":3}`,
	)
	req := toStrings(s["required"])
	if !contains(req, "id") {
		t.Errorf("required = %v, want id", req)
	}
	if contains(req, "email") {
		t.Errorf("required = %v, must not contain email (present 2 of 3)", req)
	}
}

func TestSchemaRequiredSuppressedInArray(t *testing.T) {
	// item objects live under items[]; their fields must never be required.
	s := build(t, `{"items":[{"sku":"a"},{"sku":"b"}]}`)
	items := props(t, s)["items"].(map[string]any)
	elem := items["items"].(map[string]any)
	if _, has := elem["required"]; has {
		t.Errorf("array-element object must not carry required, got %v", elem["required"])
	}
}

func toStrings(v any) []string {
	out := []string{}
	if arr, ok := v.([]string); ok {
		return arr
	}
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/schema/ -run 'TestSchemaRequired' -v`
Expected: FAIL (no `required` emitted yet).

- [ ] **Step 3: Implement**

In `internal/schema/schema.go`, replace the `case "object":` block in `buildBranch` with:

```go
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
```

Add this helper to `internal/schema/schema.go`:

```go
// presentCount rounds a node's observed presence to a record count. The
// synthetic root object (nil profile) is present in every record.
func presentCount(n *node, records int) int {
	if n.profile == nil {
		return records
	}
	return int(n.profile.PresenceRate*float64(records) + 0.5)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/schema/ -count=1`
Expected: PASS (all schema tests).

- [ ] **Step 5: Commit**

```bash
git add internal/schema/schema.go internal/schema/schema_test.go
git commit -m "feat: emit required from parent-conditional presence"
```

---

### Task 4: Guarded enum for closed string sets

**Files:**
- Modify: `internal/schema/schema.go` (add a `string` case to `buildBranch`; add `enumOK`/`enumValues`)
- Test: `internal/schema/schema_test.go` (add cases)

**Interfaces:**
- Consumes: everything from Tasks 2-3.
- Produces: `func enumOK(fp *profile.FieldProfile) bool`, `func enumValues(fp *profile.FieldProfile) []any`, and an `enum` keyword on qualifying single-string leaves.

Rule: emit `enum` only when the leaf's sole concrete type is `string` (`sole == true`) AND `fp.DistinctExact && fp.DistinctCount > 0 && fp.DistinctCount == len(fp.TopValues)` (the profiler retained the complete distinct set). Members are the `TopValues` strings, sorted. Never emit enum on a drifting or high-cardinality field.

- [ ] **Step 1: Add failing tests**

Append to `internal/schema/schema_test.go`:

```go
func TestSchemaEnumClosedSet(t *testing.T) {
	// status has 2 distinct values across 4 records -> closed set -> enum.
	s := build(t,
		`{"status":"open"}`,
		`{"status":"closed"}`,
		`{"status":"open"}`,
		`{"status":"closed"}`,
	)
	st := props(t, s)["status"].(map[string]any)
	en := toStrings(st["enum"])
	if len(en) != 2 || en[0] != "closed" || en[1] != "open" {
		t.Errorf("enum = %v, want [closed open] sorted", st["enum"])
	}
}

func TestSchemaNoEnumOnDrift(t *testing.T) {
	// id drifts int/string -> never an enum.
	s := build(t, `{"id":1}`, `{"id":"two"}`)
	id := props(t, s)["id"].(map[string]any)
	if _, has := id["enum"]; has {
		t.Errorf("drifting field must not get enum, got %v", id["enum"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/schema/ -run 'TestSchemaEnum|TestSchemaNoEnum' -v`
Expected: FAIL (no `enum` emitted yet).

- [ ] **Step 3: Implement**

In `internal/schema/schema.go`, add a `case "string":` to `buildBranch` BEFORE the `default:` case:

```go
	case "string":
		b := map[string]any{"type": "string"}
		if sole && n.profile != nil && enumOK(n.profile) {
			b["enum"] = enumValues(n.profile)
		}
		return b
```

Add these helpers to `internal/schema/schema.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/schema/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/schema/schema.go internal/schema/schema_test.go
git commit -m "feat: emit guarded enum for closed string value sets"
```

---

### Task 5: Wire the `shape schema` command

**Files:**
- Create: `internal/cmd/source.go` (shared `profileSource`; move `openSource` + `bytesReader` here)
- Delete: `internal/cmd/bytesreader.go`
- Modify: `internal/cmd/profile.go` (use `profileSource`; drop the inline read loop and `openSource`)
- Create: `internal/cmd/schema.go`
- Modify: `internal/cmd/root.go` (register `newSchemaCmd`)
- Test: `internal/cmd/schema_test.go`

**Interfaces:**
- Consumes: `profile`, `jsonreader`, `schema.Reconstruct`.
- Produces:
  - `func profileSource(src, format string) (profile.ProfileResult, error)` - open + detect + stream + profile, `Source` set.
  - `func newSchemaCmd() *cobra.Command` - `shape schema <file|-> [-o file] [--format ...]`, prints the schema JSON or writes it to `-o`.

- [ ] **Step 1: Create the shared source helper**

`internal/cmd/source.go`:

```go
package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers/jsonreader"
)

// profileSource opens src (a file path or "-"), detects the format, streams the
// records, and returns the assembled profile with Source set.
func profileSource(src, format string) (profile.ProfileResult, error) {
	r, peek, closeFn, err := openSource(src)
	if err != nil {
		return profile.ProfileResult{}, err
	}
	defer closeFn()

	stream := jsonreader.New(r, jsonreader.DetectMode(src, format, peek))
	p := profile.NewProfiler()
	for {
		rec, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return profile.ProfileResult{}, fmt.Errorf("read %s: %w", src, err)
		}
		p.AddRecord(rec)
	}
	p.AddSkipped(stream.Skipped())
	res := p.Result()
	res.Source = src
	return res, nil
}

// openSource opens a file path or stdin ("-"), returning the reader, a peek of
// the first bytes (for format detection), and a close function.
func openSource(src string) (io.Reader, []byte, func(), error) {
	if src == "-" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(os.Stdin, buf)
		peek := buf[:n]
		combined := io.MultiReader(bytes.NewReader(peek), os.Stdin)
		return combined, peek, func() {}, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, nil, nil, err
	}
	peek := make([]byte, 512)
	n, _ := f.Read(peek)
	peek = peek[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, nil, err
	}
	return f, peek, func() { f.Close() }, nil
}
```

- [ ] **Step 2: Delete the old bytesreader helper**

Run:
```bash
git rm internal/cmd/bytesreader.go
```
(`bytes.NewReader` is now used directly in `source.go`.)

- [ ] **Step 3: Refactor profile.go to use profileSource**

Replace the entire contents of `internal/cmd/profile.go` with:

```go
package cmd

import (
	"github.com/hoijun-kim/shape/internal/render"
	"github.com/spf13/cobra"
)

func newProfileCmd() *cobra.Command {
	var asJSON bool
	var format string

	cmd := &cobra.Command{
		Use:   "profile <file|->",
		Short: "Profile the shape of a JSON or NDJSON input",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := profileSource(args[0], format)
			if err != nil {
				return err
			}
			if asJSON {
				return render.JSON(cmd.OutOrStdout(), res)
			}
			render.Table(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	cmd.Flags().StringVar(&format, "format", "auto", "input format: auto|json|ndjson")
	return cmd
}
```

- [ ] **Step 4: Write the failing schema-command test**

`internal/cmd/schema_test.go`:

```go
package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
)

func runSchema(t *testing.T, args ...string) map[string]any {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"schema"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (out: %s)", err, out.String())
	}
	var s map[string]any
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	return s
}

func TestSchemaCommand(t *testing.T) {
	s := runSchema(t, "testdata/sample.ndjson")
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", s["$schema"])
	}
	if s["type"] != "object" {
		t.Errorf("root type = %v, want object", s["type"])
	}
	p, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties: %v", s)
	}
	for _, k := range []string{"id", "email", "tags"} {
		if _, has := p[k]; !has {
			t.Errorf("missing property %q", k)
		}
	}
	// id drifts int/string; tags is an array of strings.
	if _, ok := p["id"].(map[string]any)["type"].([]any); !ok {
		t.Errorf("id should have a union type, got %v", p["id"])
	}
	if p["tags"].(map[string]any)["type"] != "array" {
		t.Errorf("tags type = %v, want array", p["tags"])
	}
}
```

(The fixture `internal/cmd/testdata/sample.ndjson` already exists from Plan 1.)

- [ ] **Step 5: Run test to verify it fails**

Run: `go test ./internal/cmd/ -run TestSchemaCommand -v`
Expected: FAIL (build error: `schema` subcommand not registered).

- [ ] **Step 6: Implement the schema command**

`internal/cmd/schema.go`:

```go
package cmd

import (
	"encoding/json"
	"os"

	"github.com/hoijun-kim/shape/internal/schema"
	"github.com/spf13/cobra"
)

func newSchemaCmd() *cobra.Command {
	var out string
	var format string

	cmd := &cobra.Command{
		Use:   "schema <file|->",
		Short: "Infer a JSON Schema (Draft 2020-12) from a JSON or NDJSON input",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := profileSource(args[0], format)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(schema.Reconstruct(res), "", "  ")
			if err != nil {
				return err
			}
			b = append(b, '\n')
			if out != "" {
				return os.WriteFile(out, b, 0o644)
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "write the schema to a file instead of stdout")
	cmd.Flags().StringVar(&format, "format", "auto", "input format: auto|json|ndjson")
	return cmd
}
```

In `internal/cmd/root.go`, register the command next to the existing `AddCommand`:

```go
	root.AddCommand(newProfileCmd())
	root.AddCommand(newSchemaCmd())
	return root
```

- [ ] **Step 7: Run the whole suite and build**

Run:
```bash
go test ./... -count=1
go build -o shape.exe .
```
Expected: all tests PASS; binary builds.

- [ ] **Step 8: Manual smoke check**

Run: `go run . schema internal/cmd/testdata/sample.ndjson`
Expected: a Draft 2020-12 schema with a root `object`, `properties` for `id`/`email`/`tags`, `id` a union type, `email` `["string","null"]`, `tags` an array of strings, `required` containing `id` and `tags` (not `email`).

- [ ] **Step 9: Commit**

```bash
git add internal/cmd/source.go internal/cmd/profile.go internal/cmd/schema.go internal/cmd/root.go internal/cmd/schema_test.go
git commit -m "feat: wire shape schema command with a shared profile source helper"
```

---

## Plan 2 self-review

Coverage: reconstruct nested schema (Tasks 1-2), null unions + drift anyOf (Task 2), required parent-conditional (Task 3), guarded enum (Task 4), `shape schema` command + `-o` + shared `profileSource` (Task 5). Soundness constraints (open objects, no format/bounds/array-length, enum only on proven-complete sets) are enforced by omission and are covered by tests (`TestSchemaEmptyArrayNoItems`, `TestSchemaNoEnumOnDrift`, `TestSchemaRequiredParentConditional`, `TestSchemaRequiredSuppressedInArray`).

Placeholder scan: none; every code step is complete.

Type consistency: `node`/`step`/`parsePath`/`buildTree` (Task 1) are used unchanged by `nodeToSchema`/`typeSet`/`buildBranch`/`combine` (Task 2); `presentCount` (Task 3) and `enumOK`/`enumValues` (Task 4) extend the same file; `buildBranch`'s `sole` parameter is introduced in Task 2 and first consumed in Task 4; `profileSource` (Task 5) matches the reader/profiler signatures from Plan 1.

Out of scope (later): `--closed` (additionalProperties:false opt-in), numeric/length bounds behind a flag, format detection (needs profiler support), numeric enums. These are intentionally omitted for soundness/scope.

## Next plans
- Plan 3: `shape diff A B` + `--fail-on breaking` CI contract.
- Plans 4-7 as listed in the Plan 1 roadmap.
