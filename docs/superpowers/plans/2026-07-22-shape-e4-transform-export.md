# E4: Transform + Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. Every task is TDD for its pure logic; the Svelte-component tasks add a jsdom wiring test AND a `wails build` + real-binary drive, not only a unit test.

**Goal:** Reshape and take the data out. Choose/reorder/rename the columns you see, then export the **filtered + transformed** result to JSON, NDJSON, CSV, TSV, or Parquet - complete, never capped by the interactive tier, on files far larger than RAM.

**Architecture:** E4 is the first phase since E1 that **touches Go**. The four backends already implement `Backend.Export` (a full-file streaming pass through the shared `CompiledPlan`), and `CompileTransform`/`Project` already compile `Transform.Select`/`Drop` - but nothing writes a file: there are no `RowEncoder` implementations, no `Engine.ExportQuery`, and no `App` binding. E4 adds (1) a raw-value projection so export cannot truncate container values, (2) five streaming encoders, (3) `Engine.ExportQuery` with cancel + progress + atomic replace, (4) the Wails binding and save dialog, and (5) the frontend: a column/transform panel, an export dialog, and the store wiring that threads a live `Transform` into `QueryRows`.

**Tech Stack:** Go 1.25 (stdlib `encoding/json`, `encoding/csv`, `os`, `context`) + the already-vendored pure-Go `github.com/parquet-go/parquet-go v0.30.1`. Frontend: Svelte 3.49 + Vite 3 + TypeScript 4.6, vitest 0.34 + jsdom 29. **No new dependency of any kind, cgo-free, no DuckDB.**

**AUTHORITATIVE SOURCES:** product spec `docs/superpowers/specs/2026-07-17-shape-data-explorer-design.md` (§3.5 transform, §3.6 export, §5 the E4 phase line); engine spec `docs/superpowers/specs/2026-07-17-shape-engine-design.md` (§4 `Backend.Export`/`RowEncoder`, §6 the Transform model, §8 `ExportRequest`/`ExportResult`/progress+cancel, §10 "E4 (export)"). Existing code that E4 consumes as-is: `internal/query/transform.go` (`CompileTransform`, `CompiledPlan`), `internal/query/{memstore,rescan,sqlbackend,parquetbackend}.go` (`Export`), `internal/query/engine.go` (`begin`/`Cancel` registry), `gui/frontend/src/lib/explorer/store.ts` (the E3 filter/gen/supersession machinery E4 mirrors).

## Decisions locked before this plan (do not relitigate)

1. **Export streams RAW values, not `Row`/`Cell`.** `toCell` truncates container values to a 200-byte preview (`columns.go:170,243-248` - `previewCap`, `HasMore`) and renders numbers as `Str`+`Num`. Exporting through `Row` would silently write truncated JSON previews into the user's file. So `RowEncoder` becomes `Encode(index int64, values []any) error` and `CompiledTransform` gains `ProjectValues(rec any, dst []any) []any`. This is a **documented deviation** from engine spec §4's `Encode(row Row)`; the spec's intent ("export is never capped") is what forces it. The interactive `Query` path keeps using `Project`/`Cell` unchanged.
2. **Five formats:** `json` (one array), `ndjson`, `csv`, `tsv`, `parquet`. No XLSX, no compression, no split files.
3. **Missing vs null.** JSON/NDJSON: a *missing* path OMITS the key; an explicit null writes `null`. CSV/TSV: both write an empty field (the format cannot distinguish them - documented). Parquet: both write null (every column is `Optional`).
3b. **The encoders' input domain is small and already normalized.** Every reader funnels cell values through `readers.ToProfileValue` (`internal/readers/readers.go:96-134`), so a projected value is always one of `nil`, `bool`, `string`, `json.Number`, `float64`, `map[string]any`, `[]any` - never `int64`, `[]byte`, or `time.Time` (bytes become strings, times become RFC3339Nano strings, all integers become `json.Number`). Encoders are written against exactly that set; a `default:` branch that compact-JSONs anything unexpected is the only defensive handling required, and no test needs to fabricate exotic types.
4. **Number fidelity.** `json.Number` is written verbatim in JSON/NDJSON (`encoding/json` emits the exact literal) and via `.String()` in CSV/TSV, so 64-bit ints and high-precision decimals never round-trip through float64. Parquet coerces per column type (below).
5. **Non-finite floats.** JSON/NDJSON: `null` (JSON cannot represent NaN/±Inf; `json.Marshal` would otherwise ERROR and kill the whole export mid-file). CSV/TSV: the Go literal text `NaN`/`+Inf`/`-Inf`. Parquet: written as the IEEE double (Parquet supports them).
6. **Parquet keeps the output column ORDER.** `parquet.Group` is a `map[string]Node` whose `Fields()` sorts alphabetically (verified in `node.go`), so a plain Group would reorder the user's columns. A ~30-line ordered `Node`/`Field` wrapper fixes it - **spike-verified before this plan**: ordered schema written, nulls/missing handled, and the file re-opened through shape's own engine with `zeta, alpha, flag, score, blob` order and correct types intact.
7. **Parquet column types** come from the output `Column.Type`: `int`→INT64, `float`→DOUBLE, `bool`→BOOLEAN, `string`→UTF8; **everything else** (`object`, `array`, `null`, `mixed`, unknown/empty) → UTF8 holding compact JSON. A value that cannot be coerced into its column's type is written NULL and counted; `ExportResult.Warnings` reports the total. Silent-loss is not acceptable, a mid-export abort is worse.
8. **Duplicate output column names are rejected** for EVERY format, before a byte is written (JSON object keys and the Parquet schema map both collapse duplicates; CSV would silently emit two identical headers). The UI validates the same rule and blocks the export button.
9. **Atomic replace.** The encoder writes to a temp file in the destination directory, then `os.Rename`s over the target. Any error, or a cancel, removes the temp file and leaves the destination untouched. A half-written export must never appear at the path the user chose. **Verified on this machine before planning** (throwaway Go program, Windows 11 + Go 1.25): `os.Rename` over an *existing* destination succeeds and leaves no temp behind - but over a destination **held open by another handle** it fails with `Access is denied.`. So (a) the rename error must be reported as *"could not replace `<path>` - it may be open in another program"* rather than a raw OS string, and (b) `ExportQuery` rejects an `OutPath` that resolves to the currently-open source file (decision 9b below).
9b. **Never export onto the open source.** `ExportQuery` compares `filepath.Clean(OutPath)` against the handle's own source path (case-insensitively on Windows) and errors before opening anything. Exporting a file onto itself would destroy the data being streamed out of it and orphan the backend's open handle; on Windows the rename would fail anyway, leaving a confusing half-success.
10. **Progress is `shape:progress`, rows-only.** `Engine.ExportQuery` takes a Go-level `progress func(rows int64)` (never part of a DTO); `App` throttles it to ≥200 ms and emits the spec §8 event `shape:progress {requestId, scanned, total}` with `total: -1` (the matching-row total is genuinely unknown without a second full pass - do not fake it). Cancel reuses the existing `Cancel(requestID)` registry.
11. **Transform UI = select + reorder + rename only.** No derive/compute, no unnest, no group/aggregate (product spec §3.5 defers them explicitly), and **no flatten toggle**: the base `ColumnModel` is already flattened into dotted columns, and `Transform.FlattenObjects` is an accepted-but-inert field (`transform.go:33-40`) - E3's lesson says never surface a control that does nothing.
12. **Every emitted `ColumnSpec` carries an explicit `As`, and every writer keys on `Column.Path`.** `compileSelect` names an un-renamed selected column by its LEAF (`transform.go:184-187`: `columnName(spec.Path, segs)`), so selecting `user.name` without `As` would silently rename the column to `name` - and collide with `meta.name`. The transform panel therefore always sends `As` (defaulting to the base column's full dotted path). **The same leaf-naming applies to the IDENTITY path**, which no `As` can fix: base columns are named by leaf (`columns.go:581` → `columns.go:711-716`) and `Transform{}` propagates that verbatim (`transform.go:149-153`), so `{"user":{"id":1},"order":{"id":2}}` yields two base columns both *named* `id` while their *paths* stay unique. Therefore **JSON/NDJSON object keys, the CSV/TSV header, the Parquet schema field names, and the duplicate-name validation all key on `Column.Path`, never `Column.Name`** - under `Select` the two coincide anyway (`transform.go:189-191` sets `Path = Name = As`), so one rule covers both paths.
12b. **Known limitation (documented, not fixed in E4):** shape's own reader treats a literal `.` in a key as nesting (`profile/flatten.go:31`; `query/columns.go:420-426` concatenates and `parsePath`, `columns.go:44-82`, splits again), so re-opening a shape export whose column names contain a `.` shows those columns correctly typed but with every cell `missing`. Verified for NDJSON and Parquet during plan review. The exported file is correct for every other consumer; bracket-quoting on the READ side is an engine-wide path-grammar change, deferred to E6 alongside `Unflatten` (decision 14).
13. **Filters always address BASE paths.** The filter runs on the record, before projection; renames/selection must not change it. So `FilterBar`/`StructureMap` keep consuming `baseColumns` (the immutable `OpenResult.columns`), while `DataTable` renders the projected `columns`. This split is the store's job (Task 8).
14. **`Unflatten` is NOT in E4.** Engine spec §8's `ExportRequest.Unflatten` re-nests dotted names into objects; its collision semantics (a renamed column, a name that is a prefix of another) need their own design, and container VALUES already export as real nested JSON regardless. E4 ships no such field rather than shipping a dead one (decision 11's rule). Owner: E6 polish.
15. **No CLI `shape export`.** §5's E4 line is GUI-scoped; the CLI (`profile`/`schema`/`diff`) stays exactly as it is. `Engine.ExportQuery` is CLI-ready if a later phase wants it.

## Global Constraints

- **Zero new dependencies** - Go and frontend both. `go.mod` must not gain a `require` line; `gui/frontend/package.json` must not gain a `dependencies` block.
- **cgo-free.** `go build` must stay cgo-free; nothing may import a C-backed package.
- **Bounded memory at any file size.** Every encoder streams; the only buffering allowed is one row (JSON/CSV) or one bounded batch (Parquet, ≤1024 rows). No `[]Row` accumulation, no "collect then write". A 10 GB export must run in constant memory.
- **The Go predicate/projection stays the single source of truth.** E4 adds no second filtering path; `Export` keeps calling `p.Filter.Match` + the compiled transform, so exported rows are by construction identical to what the table shows.
- **`values []any` handed to `Encode` is a REUSED scratch buffer** - an encoder that buffers (Parquet) MUST copy what it needs. State this in the interface doc and test it (Task 4).
- **Svelte 3.49 only** - `export let`, `createEventDispatcher`, `$:`, `$explorer`. No runes.
- **Extract pure logic into `.ts` modules beside the components** (repo convention: `widths.ts`, `paging.ts`, `rowCount.ts`, `filterModel.ts`, each with a `.test.ts`). Components stay thin and are tested in jsdom for wiring only.
- **Every new test must fail if the logic it covers regresses.** For every concurrency, supersession, omission, truncation, or fidelity test - and any test whose assertion is not a direct one-to-one check of the value under test - state the exact mutation that breaks it and confirm that mutation actually kills the test (not a redundant guard elsewhere). Direct-assertion pure-logic tests are self-proving and need no mutation annotation. *(This repo has shipped cannot-fail tests in the majority of E2/E3 tasks; the mutation proof is the gate.)*
- **Commits: Conventional Commits, lowercase imperative subject, NO co-author trailer.** This overrides Claude Code's default trailer.
- Gates every task ends on: Go tasks - `go build ./... && go test ./... -count=1` (all 16 packages green) and `gofmt -l` clean. Frontend tasks - `cd gui/frontend && npm run check` (**0 errors**) and `npm run test` (green). Component tasks additionally `cd gui && wails build` and drive the real binary.
- **Bindings regeneration is EXPECTED in this phase** (unlike E3): after Task 6, `wails generate module` must be run and the resulting `gui/frontend/wailsjs/**` diff committed WITH the Go change that caused it. Never hand-edit generated files.

---

### Task 1: Raw-value projection - export stops truncating containers

**Files:** Modify `internal/query/transform.go` (add `ProjectValues`, `Len`), `internal/query/backend.go` (`RowEncoder` signature + doc), `internal/query/memstore.go`, `internal/query/rescan.go`, `internal/query/sqlbackend.go`, `internal/query/parquetbackend.go` (each `Export` loop), and their `_test.go` files (the `collectEncoder`-style doubles). Implements locked decision 1.

**Interfaces (produces):**
`var Missing any` + `func IsMissing(v any) bool` (the absent-path sentinel);
`func (ct *CompiledTransform) Len() int`;
`func (ct *CompiledTransform) ProjectValues(rec any, dst []any) []any`;
`type RowEncoder interface { Encode(index int64, values []any) error }` (changed).

**Why first:** every encoder in Tasks 2-4 consumes this, and it is the one change that decides whether E4's core promise ("export the real data") is true. `Project` classifies through `toCell`, which truncates `map`/`[]any` to a 200-byte preview and drops the rest (`columns.go:243-248`) - perfect for a table cell, silently lossy in a file.

Rules:
- `ProjectValues(rec, dst)` mirrors `Project`'s resolution exactly (`transform.go:249-263`): per output column, `resolve(rec, oc.segs)`; an empty set → `Missing`; otherwise the FIRST value, **unclassified and untruncated**. It reuses `dst` when `cap(dst) >= Len()` and allocates otherwise, returning the filled slice.
- `Missing` is an unexported zero-size type behind an exported `any` var so no reader value can ever equal it (`nil`, `false`, `""`, `0` are all real values that must stay distinguishable from an absent path).
- Each backend's `Export` allocates ONE `buf := make([]any, p.Transform.Len())` outside its loop and passes `p.Transform.ProjectValues(rec, buf)` to `enc.Encode(idx, ...)`. Nothing else in the four `Export` bodies changes (the cancel strides, scan callbacks, and error handling stay exactly as they are).
- `Project`, `Row`, `Cell`, and every `Query` path are UNTOUCHED.

- [ ] **Step 1: Write failing tests** - in `transform_test.go`: `ProjectValues` returns the raw `map[string]any` for a container column (assert it is the same map, `reflect.DeepEqual` against the source, NOT a string), returns `json.Number` unchanged for a numeric column (assert the concrete type via a type switch, so a float64 conversion fails the test), marks an absent path with `IsMissing` while a present JSON `null` is `nil` and NOT missing, and reuses the passed `dst` slice (assert same backing array via `&dst[0]`). In `memstore_test.go`: a record whose nested object's compact JSON exceeds 300 bytes exports the **whole** object - **mutation: in `ProjectValues`, replace `dst[i] = <resolved value>` with `dst[i] = toCell(<resolved value>).Str` (same signature, same call sites, still compiles) → the container arrives as the 200-byte preview (`columns.go:243-244`) and the whole-object assertion fails.** (Do NOT state the mutation as "revert `Export` to `enc.Encode(row)`": with the new interface that is a type error, so it proves nothing about the assertion.)
- [ ] **Step 2: Run - FAIL** (`go test ./internal/query -run 'ProjectValues|Export'`).
- [ ] **Step 3: Implement** per above; update the four `Export` bodies and the four test encoders.
- [ ] **Step 4: Run - PASS** + `go test ./... -count=1` (the whole repo, since `RowEncoder` is a public interface) + `gofmt -l`.
- [ ] **Step 5: Commit** - `refactor(query): export raw projected values instead of table cells`.

---

### Task 2: JSON and NDJSON encoders

**Files:** Create `internal/query/export.go`, `internal/query/export_test.go`. Implements locked decisions 2-5.

**Interfaces (produces):**
`type ExportFormat string` + consts `FormatJSONArray="json"`, `FormatNDJSON="ndjson"`, `FormatCSV="csv"`, `FormatTSV="tsv"`, `FormatParquet="parquet"`;
`func newJSONEncoder(w io.Writer, cols []Column, array bool) *jsonEncoder` (both modes, one type);
`func (e *jsonEncoder) Encode(index int64, values []any) error`; `func (e *jsonEncoder) Close() error`;
`func jsonSafe(v any) any` (the non-finite sanitizer, exported to the package for reuse by the CSV encoder).

**Consumes:** Task 1's `Missing`/`IsMissing`.

**Why hand-rolled framing:** `json.Marshal(map[string]any)` sorts keys alphabetically, which would silently reorder the user's chosen columns, and it errors on NaN/±Inf, which would abort a multi-GB export at row 900,000. So the encoder writes `{`, then per column `<marshaled Column.Path>:<marshaled value>`, and skips missing keys - order-preserving, allocation-light, and non-finite-safe. **The key is `Column.Path`, not `Column.Name`** (decision 12): base columns are leaf-named and two nested paths can share a leaf.

Rules:
- Array mode: `[\n` on first write, `,\n` between rows, `\n]\n` on `Close` - and an **empty result must still produce `[]\n`**, not an empty file.
- Line mode (NDJSON): one compact object per line, `\n`-terminated, no wrapper; an empty result is a 0-byte file.
- `SetEscapeHTML(false)` on the internal `json.Encoder` (a data export must not turn `<` into `<`), and strip the trailing newline `Encoder.Encode` appends.
- `jsonSafe` walks `map[string]any`/`[]any` recursively and replaces any non-finite `float64` with `nil`; scalars pass through (`json.Number`, `string`, `bool`, `nil`, `[]byte`→`string`). It only ALLOCATES when it actually rewrites something (return the input unchanged otherwise), so the common path stays free.
- `Missing` values are skipped entirely (no key emitted). `nil` writes `null`.

- [ ] **Step 1: Write failing tests** in `export_test.go` covering: column ORDER preserved when names are reverse-alphabetical (`{"zeta":1,"alpha":2}` - **mutation: build a `map[string]any` and `json.Marshal` it → keys come out `alpha,zeta` and the test fails**); a missing value omits its key while an explicit nil writes `null` (assert the exact bytes); `json.Number("123456789012345678901")` round-trips as that exact literal (a float64 path would print `1.2345678901234568e+20`); NaN/+Inf inside a nested array become `null` and `Encode` returns no error (**mutation: drop `jsonSafe` → `Encode` errors and the test fails**); `<a>&b` is written literally, not escaped; array mode with zero rows produces exactly `[]\n`; array mode with two rows produces valid JSON that `json.Unmarshal`s back into a 2-element slice; NDJSON mode produces exactly two `\n`-terminated lines and no wrapper; a container value longer than `previewCap` is written in FULL (the Task-1 promise, asserted at the encoder boundary too).
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** `export.go`.
- [ ] **Step 4: Run - PASS** + `go test ./internal/query -count=1` + `gofmt -l`.
- [ ] **Step 5: Commit** - `feat(query): streaming json and ndjson export encoders`.

---

### Task 3: CSV and TSV encoder

**Files:** Modify `internal/query/export.go`, `internal/query/export_test.go`.

**Interfaces (produces):** `func newDelimitedEncoder(w io.Writer, cols []Column, comma rune) *delimitedEncoder` with `Encode`/`Close`.

**Consumes:** Task 2's `jsonSafe`; Task 1's `Missing`.

Rules (each cell → exactly one string, `encoding/csv` owns quoting):
- Header row = the output columns' `Path` values (decision 12), written on construction... **no**: written lazily on the first `Encode` OR on `Close`, so a zero-row export still emits the header exactly once. (A header written in the constructor would be duplicated by a retry and lost on an aborted open - lazy-once is the only shape that is right in both cases.)
- `Missing` and `nil` → `""` (documented collapse, decision 3).
- `json.Number` → `.String()` (exact literal); `float64` → `strconv.FormatFloat(v,'g',-1,64)`, which yields `NaN`/`+Inf`/`-Inf` for non-finites (decision 5); `bool` → `true`/`false`; `string` → itself; `[]byte` → `string`; `map`/`[]any` → compact JSON of `jsonSafe(v)`.
- TSV is the same encoder with `comma='\t'`; `csv.Writer` refuses `\r`/`\n`/`"` as `Comma` so the two are the only delimiters offered.
- `Close` flushes and returns `csv.Writer.Error()`.

- [ ] **Step 1: Write failing tests**: header written once for a zero-row export (**mutation: write the header in the constructor and also on first row → the zero-row test still passes but the two-row test sees a duplicated header; both directions asserted**); a value containing the delimiter, a quote, and a newline is quoted/escaped by `encoding/csv` and re-parses via `csv.NewReader` back to the identical string; missing and nil both produce an empty field while the literal string `""` is preserved as an empty field too (documented ambiguity - assert the bytes so the collapse is deliberate, not accidental); a nested object cell becomes compact JSON on ONE line; `json.Number` exactness; TSV uses tabs and a value containing a tab gets quoted.
- [ ] **Step 2: Run - FAIL.** **Step 3: Implement.** **Step 4: Run - PASS** + `gofmt -l`.
- [ ] **Step 5: Commit** - `feat(query): streaming csv and tsv export encoder`.

---

### Task 4: Parquet encoder with an ordered dynamic schema

**Files:** Create `internal/query/exportparquet.go`, `internal/query/exportparquet_test.go`. Implements locked decisions 6-7.

**Interfaces (produces):**
`func newParquetEncoder(w io.Writer, cols []Column) (*parquetEncoder, error)` with `Encode`/`Close`/`Warnings() []string`;
internal `orderedGroup`/`orderedField` (the `parquet.Node`/`parquet.Field` wrappers that preserve column order).

**Consumes:** Task 1's `Missing`; Task 2's `jsonSafe` (for the JSON-in-a-string columns).

**Spike-verified API (do not re-derive):** `parquet.NewGenericWriter[any](w, parquet.NewSchema("row", node))` accepts `[]any` batches of `map[string]any` rows; the schema's `Fields()` order is the file's column order. Absent keys and `nil` values write NULL - **and so does any Go zero value passed by value**: parquet-go's `isNullValue` ends in `default: return value.IsZero()` (`column_buffer_reflect.go:43,73-75`) and the `map[string]any` group writer hands it the unwrapped concrete value (`column_buffer_reflect.go:616-622`), so a bare `int64(0)`, `float64(0)`, `false` or `""` is silently written as NULL. `NewGenericWriter[map[string]any]` does not avoid this (`writer.go:115` only special-cases struct types) and `orderedField.Value` is not called on this path, so the wrapper cannot compensate. **This was reproduced empirically during plan review** - a row `{"i":int64(0),"f":float64(0),"b":false,"s":""}` read back as four nulls; the same row written as `*int64/*float64/*bool/*string` read back correctly. `parquet.Group`'s own `Fields()` sorts by name (`node.go`), hence the wrapper: `orderedGroup{Group, order []string}` overrides `Fields()` to walk `order`, and `orderedField{Node, name}` implements `Name()` plus `Value(base reflect.Value)` = the interface-unwrap + `base.MapIndex` path copied from parquet-go's own unexported `groupField.Value`. Everything else (`Type`, `Optional`, `GoType`, …) is inherited from the embedded `Group`.

Rules:
- Schema field names are the output columns' `Path` values (decision 12), in that order.
- Node per column type (decision 7): `int`→`parquet.Optional(parquet.Int(64))`, `float`→`parquet.Optional(parquet.Leaf(parquet.DoubleType))`, `bool`→`parquet.Optional(parquet.Leaf(parquet.BooleanType))`, everything else→`parquet.Optional(parquet.String())`.
- Coercion per value: INT64 ← `json.Number`(ParseInt), `int64`, `float64` with an exact integral value; DOUBLE ← `json.Number`(ParseFloat), `float64`, `int64`; BOOLEAN ← `bool`; STRING ← `string`, `[]byte`, else compact JSON of `jsonSafe(v)`. **A value that fails its column's coercion writes NULL and increments a per-column counter**; `Warnings()` returns at most one summary line naming the affected columns and the total.
- **Every successfully coerced value is stored in the row map as a freshly allocated POINTER** (`*int64`, `*float64`, `*bool`, `*string`) - `v := coerced; m[name] = &v` **inside** the per-row loop (a pointer hoisted out of the loop corrupts the whole 1024-row batch exactly like retaining `values` would). This is what defeats the zero-value-is-null trap above; it is not optional. Only `Missing`, an explicit `nil`, and a failed coercion leave the key absent. `[]byte` coerces to `*string` too, so the rule is uniform.
- Batching: accumulate up to 1024 `map[string]any` rows, then `w.Write(batch)`; `Close` flushes the tail then closes the writer. **Each row's map is built fresh** - the `values` slice is reused by the caller (Global Constraints), so retaining it would corrupt every buffered row.
- Duplicate names cannot reach here (Task 5 validates first), but the constructor still returns an error on a duplicate - a map-keyed schema would silently drop a column, and that must be loud.

- [ ] **Step 1: Write failing tests** covering: **column order preserved** - build columns `zeta,alpha,flag,score` and assert the written file's schema field order matches, then re-open the file through `NewEngine().OpenSource` and assert `res.Columns` paths in that same order (**mutation: use a plain `parquet.Group` instead of `orderedGroup` → order comes back alphabetical and the test fails**); nulls and missing both read back as `CellNull`; **a row whose `int`/`float`/`bool`/`string` columns all hold the Go ZERO value (`0`, `0.0`, `false`, `""`) reads back as those exact values, not `CellNull`** - compare `Cell` struct fields, since `Num`/`Bool` are `omitempty` and marshaled JSON hides the difference (**mutation: store the coerced value bare instead of behind a pointer → all four read back `CellNull` and the test fails**); this stays a SEPARATE test from the nulls/missing one; an `int` column receiving a non-numeric string writes NULL and surfaces a warning naming that column (**mutation: drop the counter → `Warnings()` is empty and the test fails**); a container column round-trips as compact JSON text; a `float` column keeps NaN (assert `math.IsNaN` after re-read); the buffered-batch copy is real - encode 3 rows through ONE reused `[]any` buffer that the test mutates between calls, and assert all three rows read back distinct (**mutation: store `values` directly in the row map → all rows carry the last value and the test fails**); >1024 rows spanning two batches all land.
- [ ] **Step 2: Run - FAIL.** **Step 3: Implement.** **Step 4: Run - PASS** + `go test ./internal/query -count=1` + `gofmt -l`.
- [ ] **Step 5: Commit** - `feat(query): parquet export encoder with ordered dynamic schema`.

---

### Task 5: `Engine.ExportQuery` - DTOs, atomic replace, cancel, progress

**Files:** Modify `internal/query/export.go` (or a new `internal/query/exportquery.go`), `internal/query/engine_test.go` (or `export_test.go`). Implements engine spec §8's `ExportRequest`/`ExportResult` and locked decisions 8-10.

**Interfaces (produces):**
```go
type ExportRequest struct {
	RequestID string    `json:"requestId,omitempty"`
	Handle    string    `json:"handle"`
	Filter    Filter    `json:"filter"`
	Transform Transform `json:"transform"`
	Format    string    `json:"format"`   // json|ndjson|csv|tsv|parquet
	OutPath   string    `json:"outPath"`
}
type ExportResult struct {
	OutPath   string   `json:"outPath"`
	RowsOut   int64    `json:"rowsOut"`
	BytesOut  int64    `json:"bytesOut"`
	ElapsedMs int64    `json:"elapsedMs"`
	Warnings  []string `json:"warnings,omitempty"`
}
func (e *Engine) ExportQuery(ctx context.Context, req ExportRequest, progress func(rows int64)) (ExportResult, error)
```

**Consumes:** Tasks 2-4's encoders; `CompilePlan`; `Backend.Export`; `e.begin`/`Engine.Cancel` (`engine.go:58-96`).

Flow (mirroring `QueryRows`' shape so the registry/cancel behavior is identical):
1. `lookup(req.Handle)` → `CompilePlan(req.Filter, req.Transform, backend.Columns())`.
2. **Validate before touching the filesystem:** known `Format`; non-empty `OutPath`; `OutPath` is not the open source itself (decision 9b); `len(cols) > 0`; **no duplicate output column `Column.Path` values** (decisions 8 + 12 - the same key space the encoders write, so validator and encoders cannot disagree) - each an error naming the offender.
3. `ctx, release := e.begin(ctx, req.RequestID); defer release()` - cancel works exactly like a query's.
4. `os.CreateTemp(filepath.Dir(outPath), ".shape-export-*")`; wrap in a byte-counting `io.Writer` (and a `bufio.Writer` for the text formats); build the encoder for the format. Stack order is `file ← counter ← bufio ← encoder`, so the counter sees only bytes that reached the file. `Close()` on every encoder must be safe to call before the error path's `os.Remove` (step 6).
5. `backend.Export(ctx, plan, countingEncoder{enc, progress})` - the wrapper counts rows and calls `progress` every 4096 rows (nil-safe).
6. On ANY error (including `ctx.Err()`): close + `os.Remove(temp)`, return the error. **The destination is never created or modified on a failed/cancelled export.**
6b. **After `backend.Export` returns nil, re-check `ctx.Err()`** (the ctx from step 3); if non-nil, close the encoder, `os.Remove(temp)` and return the error **without renaming**. The four backends only observe cancellation at a 1024/4096-record stride (`memstore.go:224`, `rescan.go:131`), so a source shorter than one stride finishes cleanly *after* a cancel and would otherwise rename a file the user cancelled. `Engine.OpenSource` already carries the identical re-check for the identical reason (`engine.go:331-334`).
7. On success: `enc.Close()` (this is what emits the JSON array's `\n]\n`, the lazy CSV header, `csv.Writer.Flush`, and the Parquet footer) → **then** flush the `bufio.Writer` if one was interposed (text formats only; guard on nil) → read `BytesOut` off the counting writer (only valid after that flush) → `Sync` + close the temp → `os.Rename(temp, outPath)`. Flushing *before* `Close()` strands every encoder's tail inside the `bufio.Writer`, which `os.File.Sync/Close` does not drain. If the rename fails, remove the temp and return a wrapped error.
8. Return rows/bytes/elapsed + the encoder's warnings.

**Fixtures (house pattern - there is NO `internal/query/testdata/` directory):** every fixture is generated fresh into `t.TempDir()` by the existing helpers - `writeNDJSONFile` (`rescan_test.go:20`), `writeCSVFile` (`parquetbackend_test.go:593`), `writeParquetFixture[T]` (`parquetbackend_test.go:34`), and the sqlite builder at `sqlbackend_test.go:33`. Reuse them; do not add a `testdata/` directory (`parquetbackend_test.go:31-33` documents that choice).
**Forcing the rescan tier in a test:** `OpenRequest{BudgetMB: 1}` - `budgetBytesOf` (`source.go:33-38`) only defaults to 512 when `BudgetMB <= 0`, so a 1 MB budget downgrades even a small generated NDJSON fixture to `rescanBackend`. That is what makes the "export is never capped" test reachable without a giant file.

- [ ] **Step 1: Write failing tests**: exporting an unfiltered NDJSON fixture to NDJSON yields byte-identical logical rows (parse both sides and compare); a FILTERED export writes exactly the matching rows and `RowsOut` equals `CountMatches` for the same filter - assert on a **rescan-tier** handle (`BudgetMB: 1`, above) too, proving export is not capped by the interactive tier (**mutation: make `Export` stop at the memory-tier row cap → the rescan counts diverge and the test fails**); a `Select` transform with renames writes those column names in that order; **an IDENTITY export (`Transform{}`) of records containing both `user.id` and `order.id` succeeds and writes the keys `"user.id"` and `"order.id"`** (**mutation: key the encoder/validator on `Column.Name` → both columns are named `id`, the duplicate check rejects the default export, and the test fails**); an unknown format, an empty `OutPath`, an `OutPath` equal to the open source, an empty column set, and duplicate output names each return an error **and create no file at the destination** (assert `os.Stat` is `IsNotExist`, and for the self-export case that the SOURCE file's bytes are unchanged); **on-disk round-trips through the real `ExportQuery`** (Tasks 2/3 encode into a bare `io.Writer` and cannot see the bufio/Close ordering): a `json` export of ≥1 row `json.Unmarshal`s FROM THE FILE into a slice of the expected length; a zero-row `json` export is exactly `[]\n` on disk; a small (<4 KiB) `csv` export re-parses from the FILE via `csv.NewReader` into header + all rows; a zero-row `csv` export is exactly the header line - **mutation: swap `Close()` and the flush back → the json file is unterminated, both zero-row files are 0 bytes, and the small csv file is empty; all four fail**; **cancellation, in two distinct cases** - **(a)** a gate that returns a sentinel ERROR after the first row → the error surfaces, no destination file, no `.shape-export-*` temp (**mutation: drop the `os.Remove` cleanup → the temp survives**); **(b)** the gate unblocks with **nil** after `Engine.Cancel(requestID)` on a fixture **smaller than 1024 records** → `ExportQuery` returns the context error, `os.Stat(outPath)` is `IsNotExist`, and `filepath.Glob` finds no temp (**mutation: delete step 6b's post-`Export` `ctx.Err()` re-check → the rename lands and the test fails**). The gate must NOT itself return `ctx.Err()`, which would make (b) pass without the re-check; implement it as a fake `Backend` registered via `e.register` (`engine.go:433`), since `ExportQuery` builds its own encoder and none can be injected from outside. Also: an export over an EXISTING destination replaces it (and a failed one leaves the original bytes intact); `progress` is called at least once for a >4096-row source and never after the call returns.
- [ ] **Step 2: Run - FAIL.** **Step 3: Implement.** **Step 4: Run - PASS** + `go test ./... -count=1` + `gofmt -l`.
- [ ] **Step 5: Commit** - `feat(query): ExportQuery with atomic replace, cancel and progress`.

---

### Task 6: Wails binding - `App.ExportQuery`, save dialog, progress events

**Files:** Modify `gui/app.go`, `gui/app_test.go`; regenerate `gui/frontend/wailsjs/**` (`wails generate module`). Implements engine spec §8's binding surface.

**Interfaces (produces):**
`func (a *App) ExportQuery(req query.ExportRequest) (query.ExportResult, error)`;
`func (a *App) SaveFileDialog(defaultName, format string) (string, error)` (native picker with a per-format filter; `""` when cancelled);
`sourceEngine` gains `ExportQuery(ctx context.Context, req query.ExportRequest, progress func(int64)) (query.ExportResult, error)`. *(Extending that interface does NOT break the existing test fake: `gatedOpenEngine` (`app_test.go:126-131`) EMBEDS `*query.Engine`, so it inherits the new method for free - just keep the interface signature byte-identical to `*query.Engine`'s.)*

**Verified API surface (do not re-derive):** `wr.SaveDialogOptions{DefaultDirectory, DefaultFilename, Title, Filters []wr.FileFilter, …}` and `wr.FileFilter{DisplayName, Pattern}` where `Pattern` is a semicolon-separated extension list; `wr.EventsEmit(ctx, name string, optionalData ...any)`.

Plus a **test seam mirroring `openGate`** (`app.go:46-55`) - nil by default, so the `&App{eng: …}` literals in `app_test.go` keep working:
```go
// emit, if non-nil, replaces the Wails event emitter. Production never sets it.
emit func(event string, data map[string]any)

func (a *App) emitEvent(event string, data map[string]any) {
    if a.emit != nil { a.emit(event, data); return }
    if a.ctx != nil { wr.EventsEmit(a.ctx, event, data) } // nil until startup
}
```
Without it the throttle is untestable: `a.ctx` is written only by `startup` (`app.go:61`), the Go tests never call it (`app.go:88-96`), so a raw `wr.EventsEmit` path emits ZERO events with or without the throttle - and a fake ctx is impossible (`getEvents` demands a `frontend.Events` from a wails-internal package `gui` cannot import, `pkg/runtime/runtime.go:46-58`).

Rules:
- `ExportQuery` passes `a.reqCtx()` and a progress closure that **throttles to ≥200 ms** and calls `a.emitEvent("shape:progress", map[string]any{"requestId": req.RequestID, "scanned": rows, "total": int64(-1)})` (decision 10).
- `SaveFileDialog` maps format → `wr.SaveDialogOptions{DefaultFilename, Filters: []wr.FileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}, …}}`. It does NOT write anything; the write is `ExportQuery`'s job. (`SaveText`, the existing schema-export path, stays untouched.)
- The existing one-open-source ownership (`a.handle`, `resolveOpenSeq`) is NOT touched: export takes an explicit handle from the caller exactly like `QueryRows` does.

- [ ] **Step 1: Write failing tests** in `app_test.go` with a fake `sourceEngine` (house pattern): `ExportQuery` forwards the request verbatim and returns the engine's result; with `a.emit` set to a counting func, 100 progress ticks within a few ms produce **≤2** calls (**mutation: drop the throttle → 100 calls and the test fails**); with BOTH `a.emit` and `a.ctx` nil, `emitEvent` is a silent no-op (**mutation: call `wr.EventsEmit` unconditionally → wails' `getEvents` `log.Fatalf`s on a nil ctx and the test binary dies**).
- [ ] **Step 2: Run - FAIL.** **Step 3: Implement.**
- [ ] **Step 4: Run - PASS** + `go test ./... -count=1` + `cd gui && wails generate module` - **the `wailsjs/**` diff is expected here**; inspect it (new `ExportQuery`/`SaveFileDialog` in `App.d.ts`/`App.js`, new `ExportRequest`/`ExportResult` in `models.ts`) and commit it in the SAME commit. Then `cd gui/frontend && npm run check` (0 errors) to prove the regenerated types compile against the existing frontend.
- [ ] **Step 5: Commit** - `feat(gui): ExportQuery binding, save dialog and progress events`.

---

### Task 7: The transform draft model

**Files:** Create `gui/frontend/src/lib/explorer/transformModel.ts`, `gui/frontend/src/lib/explorer/transformModel.test.ts`; extend `gui/frontend/src/lib/explorer/types.ts` with `Transform`/`ColumnSpec`/`ExportRequest`/`ExportResult` re-exports. Implements locked decisions 11-12.

**Interfaces (produces):**
`interface DraftColumn { path: string; name: string; visible: boolean }` (`path` = the BASE column path, immutable; `name` = the output name, defaulting to `path`);
`function draftFromColumns(cols: Column[]): DraftColumn[]`;
`function moveColumn(draft: DraftColumn[], index: number, delta: -1 | 1): DraftColumn[]`;
`function isIdentityDraft(draft: DraftColumn[], cols: Column[]): boolean`;
`function draftErrors(draft: DraftColumn[]): string[]` (duplicate output names, blank names, nothing visible);
`function buildTransform(draft: DraftColumn[], cols: Column[]): Transform`;
`function projectedColumns(draft: DraftColumn[], cols: Column[]): Column[]` (what the table will show - used by the store to keep page arithmetic consistent BEFORE the first projected page lands).

**Consumes:** `Column` from `types.ts`.

Rules:
- **`Transform` is a generated TS *class*** (`wailsjs/go/models.ts:419-450`: required `flattenObjects: boolean` + a `convertValues()` method), so no object literal satisfies it and `npm run check` (0 errors) is this task's gate. Build with a local `interface PlainTransform { select?: { path: string; as: string }[]; drop?: string[] }` and cast at **both** exits - `return {} as unknown as Transform` (key-free, so the wire request stays byte-identical to today's `store.ts:195`) and `return { select: specs } as unknown as Transform` - the same idiom as `store.ts:8-15` / `filterModel.ts:167-171`. Do NOT "fix" it by adding `flattenObjects: false`; that still fails on the missing `convertValues`. `Column`/`ColumnSpec` literals need no cast.
- `buildTransform` returns `{}` (identity - no `select`, no `drop`) when `isIdentityDraft` (every base column visible, original order, no rename). Identity must be byte-identical to today's request so the un-transformed path stays exactly as E2/E3 shipped it (and keeps `ColumnsTruncated` meaningful - `engine.go:404-412` switches on `Select` being empty).
- Otherwise it emits `select: [{path, as}]` for the visible columns in draft order, **always with `as`** (decision 12), where `as = name`.
- `projectedColumns` returns, for each visible draft entry, the base `Column` with `path`/`name` replaced by the output name and `index` renumbered - matching `compileSelect`'s output exactly (`transform.go:184-193`), so the optimistic column set the store shows equals the one page 0 returns.
- `draftErrors` catches what decision 8 rejects server-side, so the UI never fires a doomed export: duplicate names (case-sensitive - Parquet/JSON keys are), blank/whitespace names, zero visible columns. Because `draft.name` defaults to the base `path` and is emitted as `As` (which the engine copies into the output `Column.Path`, `transform.go:189-191`), this validates **the same key space** the encoders and Task 5's validator use - the two can never disagree.

- [ ] **Step 1: Write failing tests**: `draftFromColumns` seeds `name === path` and `visible: true` for every column; `buildTransform` on an untouched draft returns `{}` with NO `select` key (**mutation: always emit a full `select` → the assertion that `select` is undefined fails**); hiding one column emits a `select` of the rest, each with `as` set (assert `as` present on EVERY entry - decision 12's trap: a missing `as` silently renames `user.name`→`name`); reordering emits `select` in the new order; renaming emits `{path:"user.name", as:"Name"}`; `moveColumn` at the boundaries is a no-op and never mutates its input (assert the input array is unchanged); `draftErrors` flags duplicate names, a blank name, and an all-hidden draft, and returns `[]` for a valid draft; `projectedColumns` matches the rename/order and keeps `type` from the base column.
- [ ] **Step 2: Run - FAIL** (`npm run test -- transformModel`). **Step 3: Implement.** **Step 4: Run - PASS** + `npm run check`.
- [ ] **Step 5: Commit** - `feat(gui): transform draft model and Transform coercion`.

---

### Task 8: Store - thread the transform, split base/projected columns, run the export

**Files:** Modify `gui/frontend/src/lib/explorer/store.ts`, `gui/frontend/src/lib/explorer/store.test.ts`. Implements locked decision 13 and the export lifecycle.

**Interfaces (produces):**
`ExplorerState.baseColumns: Column[]` (immutable per open - the filter/sidebar/transform-panel source);
`ExplorerState.columns` becomes the **projected** set (what `DataTable` renders);
`ExplorerState.transformActive: boolean`;
`explorer.setTransform(t: Transform, projected: Column[]): void`;
`ExplorerState.exporting: boolean`, `exportRows: number` (progress), `exportError: string`, `exportResult: ExportResult | null`;
`explorer.runExport(format: string, outPath: string): Promise<void>`; `explorer.cancelExport(): void`; `explorer.dismissExport(): void`.

**Consumes:** Task 7's `Transform`/`projectedColumns`; Task 6's `ExportQuery` binding + the `shape:progress` event via `wailsjs/runtime`'s `EventsOn`.

Rules:
- `open()` sets BOTH `baseColumns` and `columns` from `res.columns`, and resets `currentTransform = {}`. `close()` resets the same way. Every existing E2/E3 consumer of `columns` that means "the file's structure" (`FilterBar`, `StructureMap`'s `columnPaths`) moves to `baseColumns` in Task 11 - the store just provides both.
- `ensurePages` sends `transform: currentTransform` instead of `{} as any` (`store.ts:195`). `currentTransform` is typed `Transform` and is initialised/reset with Task 7's cast (`{} as unknown as Transform`) - today's line only compiles because `{} as any` bypasses the class type, and that escape hatch disappears once a typed `Transform` is threaded.
- `setTransform(t, projected)` mirrors `setFilter` **exactly** (`store.ts:332-386`): `currentTransform = t`; `++gen`; cancel + clear `inflight`; `cache.clear()`; then ONE `update` that sets `columns: projected`, `transformActive`, `version: 0`, `resetToken: +1`, `pageError: ""`; then `void ensurePages(0, 0)`. **Setting `columns` synchronously in that same update is load-bearing:** `ensurePages` computes `pageRowsFor(s.columns.length)` (`store.ts:168`) BEFORE the fetch, so a stale column count would page the new projection with the old page size and desync every cached page from `rowAt`'s arithmetic.
- `setTransform` does NOT touch `total`/`matchCount`/`counting`: a projection changes columns, never which records match. (Do not copy `setFilter`'s total-reset - it would blank a correct count for no reason.) **To make that safe, first decouple the count's supersession key from the page generation:** `startCount`'s two exits (`store.ts:310`, `:316`) `return` on `genAtStart !== gen` *before* any `counting: false` write, and its `finally` only nulls `countReqId` - so `setTransform`'s `++gen` would strand an in-flight count at `counting: true` with no writer left, and `rowCount.ts:53` would render `counting…` forever over a known `matchCount`. `setFilter` escapes this only because it separately cancels/nulls `countReqId` (`:348`) and writes `counting: false` (`:374`), neither of which `setTransform` may copy. Fix in ~5 lines: add `let countGen = 0;` beside `gen` (`store.ts:71`), bump it exactly where `countReqId` is reset today (`open()` :109-110, `close()` :288-289, `setFilter()` :348), change `startCount`'s two guards from `genAtStart !== gen` to `genAtStart !== countGen`, and `setFilter`'s call site (`:384`) to pass `countGen`. `setTransform` then bumps only `gen`, and the in-flight count survives the projection and resolves normally. Do NOT instead cancel-and-restart the count (it throws away a full scan on a large file), and do NOT drop `++gen` from `setTransform` (it deletes the belt-and-suspenders guard `store.ts:295-303` documents).
- `runExport(format, outPath)`: builds `{requestId: 'x'+(++seq), handle, filter: currentFilter, transform: currentTransform, format, outPath}`, sets `exporting: true, exportRows: 0, exportError: "", exportResult: null`, awaits `ExportQuery`, and - **guarded by the same `myGen !== gen` + own-request-id pattern as every other async path** - writes the result or the error. `finally` clears `exporting` only if this export still owns `exportReqId`.
- `cancelExport()` writes the terminal state **synchronously**, mirroring `cancelCount` (`store.ts:326-328`) - it cannot rely on the rejected promise, because nulling `exportReqId` makes `runExport`'s own guards reject its catch/finally:
  ```ts
  function cancelExport(): void {
    if (!exportReqId) return;               // nothing in flight: leave a done/failed dialog alone
    void Cancel(exportReqId).catch(() => {});
    exportReqId = null;
    update((s) => ({ ...s, exporting: false, exportError: "cancelled" }));
  }
  ```
  The early return matters: an unconditional write would flip an already-finished dialog into "failed" on a late Esc. `runExport`'s catch/finally stay pure no-ops for a superseded request.
- Progress: `runExport` subscribes **lazily, per export - never at module scope**: `const off = EventsOn("shape:progress", (p) => { if (p?.requestId === exportReqId) update(s => ({...s, exportRows: p.scanned})); });`, with `off()` in the same `finally` that clears `exporting` (`runtime.d.ts:41` declares the `() => void` return). The `requestId` check is load-bearing: a stale/foreign event must not move the bar. **A module-scope call throws while `store.ts` is being evaluated** - node env: `window` is undefined; jsdom: `window.runtime` is undefined (`wailsjs/runtime/runtime.js:39-44`) - breaking `DataTable.test.ts`, `Explorer.test.ts` and `FilterBar.test.ts`, none of which mock the runtime. Do NOT reach for a `typeof window` guard instead: `store.test.ts` runs in the NODE environment, so the guard would suppress the subscription there and make the foreign-requestId test unable to fail.

- [ ] **Step 1: Write failing tests** in `store.test.ts` (extend the existing Wails mock with `ExportQuery`; mock `../../../wailsjs/runtime` for `EventsOn`): `ensurePages` sends the current transform (assert the mock's `transform`, not `{}`); `setTransform` updates `columns` **synchronously before** the refetch and leaves `baseColumns` untouched; the page-size half needs a **300-column** base fixture (`pageRowsFor(300) === 100`) projected down to 1 visible column - then assert the `QueryRows` call made **after** `setTransform` carries `limit: 200, offset: 0` (not the first call overall: `open()`'s own `ensurePages` legitimately fetches at `limit: 100`). **Mutation: set `columns` from the landed RowSet → the post-transform fetch still uses 100 and the assertion fails.** Below 151 columns this assertion CANNOT fail - `paging.ts:9-13` clamps `pageRowsFor` to 200 for every count ≤150, a trap this repo already documented at `Explorer.test.ts:246-255` - and `store.test.ts:35-40`'s `openResult()` helper hardcodes `columns: []`, so it needs a columns parameter. Next: a stale in-flight page from before `setTransform` never lands (resolve the old promise after the transform and assert `rowAt` never shows it - **mutation: remove `inflight.clear()`; that alone kills it, since `ensurePages`' `todo` filter (`store.ts:175`) then skips page 0 as already in flight and the stale promise passes the `:190` guard**); `setTransform` does NOT reset `total` (assert it survives); **with a rescan-tier filtered count in flight, `setTransform` then resolving that count sets `counting: false` and a real `matchCount`** (**mutation: guard `startCount` on `gen` instead of `countGen` → `counting` stays true forever and the test fails**); `runExport` resolves into `exportResult` and clears `exporting`; a `shape:progress` event with the live requestId updates `exportRows` while one with a foreign id does NOT (**mutation: drop the id check → the foreign event moves the bar and the test fails**); `cancelExport` calls `Cancel` with the export's requestId and **synchronously** leaves `exporting === false` with `exportError` containing "cancelled" - then settle the gated `ExportQuery` promise LATE and assert nothing changes (no `exportResult`, `exportError` still "cancelled"); that late-settle assertion is the load-bearing check for the id guard, since `cancelExport` does not bump `gen`; the progress subscription is unsubscribed after `runExport` settles (**mutation: drop `off()` → a second export delivers each progress event twice and the call-count assertion fails**); `open()` resets `currentTransform` so the next file's first `QueryRows` sends `{}`.
- [ ] **Step 2: Run - FAIL.** **Step 3: Implement.** **Step 4: Run - PASS** + `npm run check`.
- [ ] **Step 5: Commit** - `feat(gui): thread a transform through the store and run exports`.

---

### Task 9: The transform (columns) panel

**Files:** Create `gui/frontend/src/lib/explorer/TransformPanel.svelte`, `gui/frontend/src/lib/explorer/TransformPanel.test.ts`.

**Interfaces (produces):** props `{ columns: Column[]; open: boolean }` (columns = `baseColumns`); event `errors` (`string[]`, dispatched on every draft change **including the return to valid**, un-debounced - Task 11 routes it to the export dialog's `disabledReason`); owns a local `DraftColumn[]`; on any change calls `explorer.setTransform(buildTransform(draft, columns), projectedColumns(draft, columns))` through a 250 ms debounce (Task 3's existing `debounce.ts`), with `onDestroy(() => debounced.cancel())` - the exact teardown trap E3's F1 found (a debounce armed against file A firing after file B opens).

**Layout** (mirrors `FilterBar`'s bar, same tokens): a header row with "Columns", a "Reset" button, and a live `N of M shown` count; then a scrollable list, one row per base column: a visibility checkbox, ↑/↓ reorder buttons (disabled at the ends), a `KindChip` for the type, and a rename `<input>` seeded with the path. Errors from `draftErrors` render inline in `--status-critical` and - critically - the panel still applies the VALID part? **No:** while `draftErrors` is non-empty the panel does NOT call `setTransform` at all (a duplicate/blank name has no correct projection to apply) and shows the message. Export is blocked by the same errors in Task 10.
Form controls reuse the hand-styled `input`/`select` rules ConditionRow established (`ConditionRow.svelte` - no new tokens in `app.css`).

- [ ] **Step 1: Build it**, then `TransformPanel.test.ts` (jsdom, real store, mocked bridge): unchecking a column calls `setFilter`-style `QueryRows` with a `select` omitting it after `advanceTimersByTime(250)`; ↑ on the second row swaps the first two `select` entries; renaming emits `as`; a duplicate rename shows the error AND fires NO `setTransform` (**mutation: apply the draft regardless of `draftErrors` → a `QueryRows` call appears and the test fails**); clearing that duplicate re-dispatches `errors` with `[]` (**mutation: dispatch only when non-empty → the Export button latches disabled forever after one bad rename**); "Reset" restores identity (assert the emitted transform is `{}` - no `select` key); `$destroy()` after arming the debounce fires nothing (**mutation: drop `onDestroy` → the stale apply fires**).
- [ ] **Step 2: BUILD.** `cd gui && wails build` must succeed; `npm run check` (0 errors) and `npm run test` are the hard gates here. This component is not mounted anywhere until Task 11, so an interactive drive is impossible - it is deferred to Task 11 Step 2 (same sequencing E3 used for `ConditionRow`).
- [ ] **Step 3: Commit** - `feat(gui): column transform panel`.

---

### Task 10: The export dialog

**Files:** Create `gui/frontend/src/lib/explorer/ExportDialog.svelte`, `gui/frontend/src/lib/explorer/ExportDialog.test.ts`.

**Interfaces (produces):** props `{ open: boolean; disabledReason: string }`; events `close`. Owns: a format `<select>` (JSON array / NDJSON / CSV / TSV / Parquet), a filename derived from the source (`data.ndjson` → `data-export.csv`), a summary line ("exports the current filter and column selection: ~N rows × M columns"), a **Choose file…** button calling `SaveFileDialog(defaultName, format)`, then Export → `explorer.runExport(format, path)`.

States (all four must be reachable and tested): idle · exporting (`{exportRows} rows written…` + Cancel) · done (`{rowsOut} rows · {bytesOut} → {outPath}`, plus any `warnings` rendered as a caution line) · failed (`exportError` + Retry). A cancelled export lands in "failed" with the word cancelled - never a silent close.
The dialog is a plain focus-trapped `<div role="dialog" aria-modal="true">` with Esc-to-close and a backdrop click (no library, decision: zero deps). Esc during an in-flight export cancels it rather than closing behind the user's back.

- [ ] **Step 1: Build it**, then `ExportDialog.test.ts`: choosing CSV and a path calls `runExport("csv", path)`; `exportRows` updates render the live count; Cancel calls `cancelExport`; the done state shows rows/bytes/path; a `warnings` array renders (the Parquet null-coercion line - the honest half of decision 7; **mutation: drop the warnings render → the test fails**); `disabledReason` (routed from the panel's `errors` event, Task 11) disables Export and shows why - this guard is **advisory**; Task 5's server-side validation is what actually makes a bad export impossible; if the test drives the real `explorer.runExport` rather than spying on it, it must `vi.mock("../../../wailsjs/runtime", () => ({ EventsOn: vi.fn(() => () => {}) }))`; Esc while exporting cancels rather than closes (**mutation: make Esc always close → the cancel spy is not called**).
- [ ] **Step 2: BUILD.** `cd gui && wails build` must succeed; `npm run check` (0 errors) and `npm run test` are the hard gates. Like Task 9, this dialog is not mounted until Task 11 - the interactive export drive happens there.
- [ ] **Step 3: Commit** - `feat(gui): export dialog`.

---

### Task 11: Shell wiring - header buttons, panel mounting, base-column split

**Files:** Modify `gui/frontend/src/App.svelte` (own `columnsOpen`/`exportOpen`), `gui/frontend/src/lib/Header.svelte` (Columns + Export buttons), `gui/frontend/src/lib/explorer/Explorer.svelte` (mount both panels; switch the filter/sidebar to `baseColumns`), `gui/frontend/src/lib/explorer/StatusBar.svelte` (show the projection), and their tests.

Rules:
- **Ownership mirrors E3's `filterOpen` exactly** (`App.svelte:71-87`): `Header` and `Explorer` are SIBLINGS, so `columnsOpen`/`exportOpen` live in `App.svelte`, are passed down, and `Explorer` takes them as **bindable** props (the export dialog closes itself).
- `Header.actions` becomes: theme · **Columns** (`aria-pressed={columnsOpen}`) · **Filter** · Open · **Schema** (the existing `export` event, relabelled from "Export schema" - it exports the JSON Schema, not data) · **Export** (primary, opens the E4 dialog). Do not delete the schema path; it is existing, working behavior.
- `Explorer.svelte`: `<FilterBar columns={$explorer.baseColumns} …>` and `columnPaths = new Set($explorer.baseColumns.map(c => c.path))` (decision 13 - filters and the structure map address the SOURCE, not the projection), while `<DataTable columns={$explorer.columns} …>` keeps the projection. `<TransformPanel columns={$explorer.baseColumns} open={columnsOpen} />` mounts beside `FilterBar`; `<ExportDialog bind:open={exportOpen} disabledReason={…} />` mounts last.
- `StatusBar` gains two props, `transformActive` and `baseColumnCount`, passed from `Explorer.svelte` as `$explorer.transformActive` / `$explorer.baseColumns.length`. When `transformActive`, `columnsText` reads `showing {columnCount} of {baseColumnCount} columns`, reusing the truncation slot's STYLING only - it must NOT derive N from `totalPaths`, which `engine.go:404-412` sets to `len(rs.Columns)` under any `Select` (so a 3-of-12 projection would read "3 of 3"). Branch order: `transformActive` → `columnsTruncated` → plain. Unchanged when `transformActive` is false.
- **`disabledReason` needs a route** (the panel owns the draft; the dialog is its sibling): `TransformPanel` dispatches an `errors` event (`string[]`) on EVERY draft change **including the transition back to valid** (`[]`) and **un-debounced** - the 250 ms debounce covers `setTransform` only, so the Export guard cannot lag the panel's own inline message. `Explorer.svelte` holds `let transformErrors: string[] = []`, wires `on:errors={(e) => transformErrors = e.detail}`, and passes `disabledReason={transformErrors[0] ?? ""}` (note the type change: `string[]` → `string`), exactly how E3 routes `seedFilter` (`Explorer.svelte:52-57`).

- [ ] **Step 1: Build it**, then tests: `Explorer.test.ts` - with a projection applied, `FilterBar` still offers the BASE columns (**mutation: pass `columns` instead of `baseColumns` → a hidden column disappears from the filter's column select and the test fails**) and the sidebar still un-dims a hidden column's row; `Header.test.ts`/`App` wiring - the Columns button toggles the panel and reflects `aria-pressed`; the Export button opens the dialog; the Schema button still dispatches the old `export` event (**mutation: reroute Schema to the new dialog → the legacy assertion fails**); `StatusBar.test.ts` - with `transformActive: true, columnCount: 3, baseColumnCount: 12, totalPaths: 3, columnsTruncated: false` the text reads "showing 3 of 12 columns" (**mutation: derive N from `totalPaths` → "3 of 3" and the test fails**), and every existing `transformActive: false` fixture is unchanged; a duplicate rename in the panel leaves the dialog's Export button disabled (the `errors` route).
- [ ] **Step 2: BUILD + RUN - the deferred drives from Tasks 9 and 10 land here.** Full loop on the real binary with `gui/testdata/nested.ndjson`: open → filter → hide a column, reorder two, rename one (the table header and rows must follow and **the row count must NOT change**) → export to all five formats → re-open each exported file in shape. Column ORDER and row COUNT must match the panel; **columns whose name contains a `.` will show `missing` cells** (decision 12b's documented limitation) - confirm that is the ONLY discrepancy. **Look at it.**
- [ ] **Step 3: Commit** - `feat(gui): wire the columns panel and export dialog into the shell`.

---

### Task 12: Full-stack verification, large-file drive, docs

**Files:** Modify `gui/README.md` and the root `README.md` (document transform + export). No source changes unless verification finds a defect - if it does, fix it here as its own `fix(...)` commit and say so.

- [ ] **Step 1: Gates.** `cd gui/frontend && npm run check` (0 errors) and `npm run test` (green - state the count; it must exceed E3's 232); `go test ./... -count=1` (16 packages); `gofmt -l` clean; `go.mod` unchanged (`git diff --exit-code go.mod go.sum`); no `dependencies` block in `package.json`; `cd gui && wails build` succeeds; `wails generate module && git diff --exit-code gui/frontend/wailsjs/` is EMPTY (Task 6 already committed the regenerated bindings - a non-empty diff here means they drifted).
- [ ] **Step 2: The large-file drive (the check jsdom cannot do).** Generate a >300 MB NDJSON fixture (rescan tier). Filter it, hide half the columns, export to NDJSON and to Parquet. Verify: the progress count climbs; Cancel mid-export leaves **no** destination file and **no** `.shape-export-*` temp; a completed export's row count equals `CountMatches` for the same filter; the exported Parquet re-opens in shape with the columns in the panel's order; **a record carrying `0` / `false` / an empty string survives the Parquet round-trip as those values, not as nulls** (decision 7's pointer rule, live); memory stays bounded (watch RSS - a full-file buffer would be obvious). **If any of these fails, fix it and re-run rather than reporting success.**
- [ ] **Step 3: Screenshots** of the columns panel and the export dialog's done state. **Look at them.**
- [ ] **Step 4: Commit** - `docs: document column transform and data export`.

---

## Self-Review

**Coverage (E4 from product spec §3.5/§3.6 + §5):** raw-value projection so export is lossless (T1) · JSON/NDJSON (T2) · CSV/TSV (T3) · Parquet with ordered dynamic schema (T4) · `ExportQuery` with validation, atomic replace, cancel, progress (T5) · Wails binding + save dialog + throttled `shape:progress` (T6) · transform draft model (T7) · store: transform threading, base/projected column split, export lifecycle (T8) · columns panel (T9) · export dialog (T10) · shell wiring (T11) · full-stack + large-file verification and docs (T12).

**Explicitly NOT in this plan, with owners:** `Unflatten` re-nesting on export (E6 polish, decision 14) · derive/compute, unnest arrays, group/aggregate (product spec §3.5 "later") · jq/SQL codegen for the current filter+transform (E5) · SQL `WHERE` pushdown (E5) · the nested tree view and global search (E6) · a CLI `shape export` (decision 15) · a determinate export progress BAR (rows-only is honest; a percentage needs a match total nobody has without a second full pass).

**Placeholder note:** none. Every task carries its own tests with stated mutations. The one API risk (dynamic ordered Parquet writing) was spike-verified against `parquet-go v0.30.1` before this plan was written, including a round-trip back through shape's own engine - Task 4 re-encodes that spike as real tests rather than trusting it.

**Type consistency:** `Missing`/`ProjectValues`/`RowEncoder` (T1) are consumed by every encoder (T2-T4) and by `ExportQuery` (T5). `ExportRequest`/`ExportResult` are defined once in Go (T5), cross the binding (T6), and are re-exported into `types.ts` (T7) - never redeclared by hand. `DraftColumn`/`buildTransform`/`projectedColumns` (T7) feed the store (T8) and the panel (T9). `baseColumns` vs `columns` (T8) is consumed by `FilterBar`/`StructureMap` vs `DataTable` respectively (T11). The `shape:progress` payload shape is fixed in T6 and read in T8.

**Correctness checks (each with an explicit, mutation-proven test):** containers export in full, never as a 200-byte preview (T1, mutation reverts to `Project`) · JSON key order follows the user's column order and NaN cannot abort an export (T2) · a CSV header is written exactly once, including for a zero-row export (T3) · Parquet preserves column order (T4, mutation uses a plain `parquet.Group`), copies the reused value buffer (T4), and reports coerced-to-null values instead of hiding them (T4) · a failed or cancelled export leaves no destination file and no temp litter (T5) · export is never capped by the interactive tier - rescan-tier `RowsOut` equals `CountMatches` (T5) · duplicate output names are rejected before any bytes are written (T5) and blocked in the UI (T7/T9) · progress emission is throttled and nil-ctx-safe (T6) · the identity draft emits `{}` so the un-transformed request stays byte-identical (T7) · `columns` is updated synchronously with the transform so page arithmetic never desyncs (T8) · a stale pre-transform page cannot land (T8) · a foreign `shape:progress` event cannot move the bar (T8) · a debounce armed against file A cannot fire after file B opens (T9) · filters and the structure map keep addressing base paths under a projection (T11).
