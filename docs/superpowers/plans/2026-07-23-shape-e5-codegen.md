# E5: Codegen (jq + SQL) + SQL-WHERE Pushdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. Every task is TDD for its pure logic; the Svelte task adds a jsdom wiring test AND a `wails build`, not only a unit test.

**Goal:** Show the user the `jq` expression and the SQL query equivalent to the filter + transform they built by clicking - the power-user hook and a jq/SQL learning aid - and then *use* that same SQL to make SQLite sources fast.

**Architecture:** E5 is two things that share one operator vocabulary but must NOT share one string builder:

1. **Display codegen** (`internal/query/codegen.go`): a pure `Codegen(Filter, Transform, CodegenContext) (Generated, error)` returning readable jq and SQL with literals inlined. Golden-tested byte-for-byte, and **executed** (real SQLite; real `jq` where present). This is what the user copies.
2. **Execution pushdown** (`internal/query/sqlpushdown.go`): a conservative `sqlPushdown(...) (where string, args []any, exact bool)` producing a **parameterised** WHERE for `sqlBackend`. Different requirements (bound params, must refuse anything ambiguous), so a different builder.

Then the Wails binding and a read-only GUI panel.

**Tech Stack:** Go 1.25 stdlib + the already-vendored `modernc.org/sqlite`. Frontend: Svelte 3.49 + vitest, `ClipboardSetText` from the existing wails runtime. **No new dependency, cgo-free.**

**AUTHORITATIVE SOURCES:** engine spec `docs/superpowers/specs/2026-07-17-shape-engine-design.md` §7 (codegen), §4 (`sqlBackend`), §5 (filter semantics), §9 (determinism); product spec §3.6 + the §5 E5 line. **This plan knowingly supersedes spec §7 in four places** (decision 15) - every deviation is labelled, and a follow-up `fix(docs)` against the spec is part of Task 9 so E6 cannot reintroduce them.

> **Every empirical claim below was verified during plan review** against the vendored `modernc.org/sqlite` (SQLite 3.53.2) and real `jq` 1.7.1, with throwaway tests. Where a rule looks over-cautious, the evidence for it is cited inline: do not relax it without re-running that experiment.

## Decisions locked before this plan (do not relitigate)

1. **Display and execution are separate code paths.** Inlining literals is right for something a human copies; bound parameters are required for something the engine executes. One shared *vocabulary*, two builders. Never derive one from the other by string substitution.
2. **Pushdown is EXACT-OR-NOTHING.** If any part of the filter is not pushable, `sqlBackend` keeps exactly today's behavior (full cursor scan + the shared Go predicate). A superset WHERE would forbid pushing `LIMIT`/`OFFSET`/`COUNT(*)`, which is where the entire win lives.
3. **Pushability preconditions - ALL must hold, or `exact=false` for the WHOLE filter.** Each was verified; the evidence is the reason.

   | Precondition | Why (verified) |
   |---|---|
   | `len(segs)==1`, no `Elem` | SQLite rows are flat |
   | op ≠ `regex` | `no such function: REGEXP` |
   | `CaseInsensitive == false` | `lower('ÄÖÜ')`=`'ÄÖÜ'`; Go folds Unicode |
   | no `Filter.Negate` anywhere | `NOT(NULL)` is NULL, Go inverts false→true |
   | **column never yielded a raw `[]byte` or `time.Time`** | `sqlbackend.go:293` stores `readers.ToProfileValue(v)`, which rewrites `[]byte→string` and `time.Time→RFC3339Nano`. A DATE column stores `'2024-01-01'` but the Go value is `"2024-01-01T00:00:00Z"` → pushed `=` returns **0 rows where Go returns 1**. BLOBs sort above all TEXT by storage class: declared-TEXT column holding one blob + one text row, `= 'abc'` → 1 of 2, Go → 2. **A declared TEXT affinity is no protection.** |
   | **decltype not `DATE`/`DATETIME`/`TIMESTAMP`** | the driver's own conversion trigger; covers a column that is all-NULL at profile time (decltype alone cannot predict a BLOB, so both gates are needed) |
   | **no zero-term node** | see decision 14 |
   | **every string comparison renders `"col" COLLATE BINARY`** | decision 4 |
4. **Column collation is invisible and must be OVERRIDDEN, not detected.** `PRAGMA table_info` has no collation column (verified: `[cid name type notnull dflt_value pk]`). On `TEXT COLLATE NOCASE` holding `'Apple','apple','BANANA'`: `a = 'apple'` → **2 rows**, Go → 1. On `COLLATE RTRIM`: `a = 'abc'` → 2, Go → 1. So every pushed `=`/`<>`/`<`/`<=`/`>`/`>=`/`IN` renders the **column** operand as `sqliteQuoteIdent(name) + " COLLATE BINARY"`. **Placement is load-bearing:** `"a" COLLATE BINARY IN (?,?)` → 1 row (right); `"a" IN (?,?) COLLATE BINARY` → 3 rows (**a no-op** - it binds to the last list element). `instr()`, `IS NULL`/`IS NOT NULL` and numeric comparisons are collation-independent; emitting `COLLATE BINARY` on a numeric column is a verified no-op, so no type branch is needed.
5. **`lt`/`lte`/`gt`/`gte` with a STRING operand are NEVER pushable.** SQLite applies the *column's affinity to the bound parameter*: on an INTEGER-affinity column holding `'1.2.3','1.10.0'`, `x < '2'` → **0 rows**, Go → 2. `COLLATE BINARY` does not fix it (verified) - affinity and collation are different mechanisms. Costs nothing today: `filterModel.ts:211-215` only emits ordering ops with numeric operands.
6. **A numeric operand is pushable only when `math.Abs(v.Num) < 1<<53`,** for every op and every `in` element. `Value.Num` is float64; SQLite compares INTEGER against a bound REAL **exactly**. Verified: rows `9007199254740992`/`9007199254740993`, `n = ?` bound `f64(2^53)` → 1 row, Go → **2** (both round to 2^53). Strict `<`: 2^53 itself aliases. Bigint IDs are the ordinary case for a SQLite source. Operands are **always bound as float64**, never `int64(v.Num)` (which truncates 1.5→1 and overflows).
7. **Null semantics come from spec §5, which was chosen to make SQL guard-free.** Do not add `IS NOT NULL` guards - they change nothing and imply the rule is uncertain.
8. **Empty `in` list → jq `false` / SQL `1=0`**, plus a warning - never a syntax error.
9. **The panel is read-only.** Copy-to-clipboard only; no edit-and-run console.
10. **`Codegen` is pure**; the engine entry point only supplies context (format, table, columns) and must never scan.
11. **Generated SQL for a non-SQLite source is illustrative**, over a flat table `data`, prefixed with `-- illustrative: this source is <format>, not a database`. Never executed by shape.
12. **CI folding is ASCII-only in BOTH targets** (SQLite `lower()`, jq `ascii_downcase`) while the engine folds Unicode. Verified: `lower('İ')`=`'İ'`. Every `ci` condition emits a warning **and** an inline caveat comment, once per output - exactly as `regex` does. `COLLATE NOCASE` is ASCII-only too and buys nothing.
13. **jq's `|` is the lowest-precedence operator and rebinds `.`.** Any `|` inside a generated expression MUST be parenthesised: `(.p|type)`, `(.p|contains(V))` - never bare as an operand of `and`/`or`/`==`. Spec §7's `(.p|type=="string" and (.p|contains(V)))` parses as `.p | (type=="string" and (.p|contains(V)))` and dies with `Cannot index string with string "p"` - **exactly on the rows it was meant to match**, because `and` short-circuits.
14. **A zero-term node emits its combinator's identity, never an empty fragment.** `(1=1 AND ())` is a prepare-time syntax error, and silently dropping the fragment turns a childless OR from match-nothing into match-everything. jq `true`/SQL `1=1` for `and`; jq `false`/SQL `1=0` for `or` - matching `compileGroup` (`filter.go:174-197`). The omit-`select`/omit-`WHERE` test is `isEmptyFilter(f)`, **Negate included** (`Filter{Negate:true}` matches nothing). For `sqlPushdown` a zero-term node ⇒ `exact=false`.
15. **Labelled deviations from spec §7** (Task 9 files a `fix(docs)` for each): the `contains`/`regex` parenthesisation (13); type guards on `ne`/ordering ops (Task 2); `?`-suffixed paths plus `// false` so a sparse record cannot abort the stream (Task 2); `COLLATE BINARY` on pushed and SQLite-targeted comparisons (4).
16. **No `GetCell`, no tree view, no global search** - E6 owns those.

## Global Constraints

- **Zero new dependencies**, cgo-free, no DuckDB.
- **Golden strings byte-for-byte.** Never "contains".
- **Generated strings must EXECUTE, not merely parse.**
  - SQL: every generated query whose source `Filter` has no `OpRegex` condition (gate on the **AST**, not `strings.Contains(sql,"REGEXP")` - a `contains` on the literal `'REGEXP'` would wrongly skip) runs against a seeded in-memory SQLite and must return the **exact ordered rowid set**. `json_extract`/`json_each`/`EXISTS`/CI/empty-`in` all run fine on the vendored driver. The fixture must hold **valid JSON or NULL** in every json-reachable column (malformed non-NULL JSON raises `SQL logic error: malformed JSON` at iteration) and must be **discriminating** - per column a matching row, a non-matching row and a NULL row, or a `<`/`>` swap survives.
  - jq: **run real `jq`**, gated on `exec.LookPath("jq")` + `t.Skip`. `.github/workflows/ci.yml:8` runs `go test ./...` on `ubuntu-latest`, whose runner image ships `jq`, so this executes on every CI run despite no `jq` on this Windows box. Assert **stdout equality**, never exit status alone (a short-circuiting broken template exits 0).
- **Determinism:** no map iteration in any generated string.
- **Every new test must fail if the logic regresses** - state the mutation and confirm it kills the test.
- **Never `git add -A` in a task that ran `wails build`:** vite's `emptyDir` deletes the tracked `gui/frontend/dist/.gitkeep`, and committing that deletion breaks `//go:embed all:frontend/dist` on a fresh clone (reds ubuntu CI, which never runs `npm run build`). Run `git checkout -- gui/frontend/dist/.gitkeep` after every build and stage files explicitly. `go.mod`/`go.sum`/`wailsjs/runtime/*` showing as ` M` after a build is CRLF-only noise, not a gate failure.
- **Commits: Conventional Commits, lowercase imperative, NO co-author trailer.**
- Gates per task: Go - `go build ./... && go test ./... -count=1` (16 packages), gofmt clean. Frontend - `npm run check` 0 errors, `npm run test` green **by process exit code** (an unhandled rejection exits 1 while printing all-passed).

---

### Task 1: Path encoding, literals, and the number formatter

**Files:** Create `internal/query/codegen.go`, `internal/query/codegen_test.go`.

**Interfaces:** `jqPredicatePath(segs []Seg) string`; `jqProjectionPath(segs []Seg) string`; `sqlPathExpr(path string, segs []Seg, cols *ColumnModel, sqlite bool) (string, []string)` (returns the expression + any warnings); `jqLiteral(v Value) (string, error)`; `sqlLiteral(v Value) (string, error)`; `sqlNumber(f float64) (string, error)`; `sqlIdent(name string) string`.

- **jq predicate path:** identifier segment (`^[A-Za-z_][A-Za-z0-9_]*$`) → `.a`, else `.["escaped"]` via `json.Marshal`; **every segment is `?`-suffixed** (`.a?.b?`) so a scalar/missing ancestor yields empty instead of aborting the stream; `Elem` appends `[]?`. The caller wraps each condition in `(… ) // false` (Task 2) because `?` yields **empty**, not false, and empty propagates through `not`.
- **jq projection path:** same, but a path containing `Elem` is wrapped `[<path>][0]` - `Project` takes the first resolved value, whereas a bare `.tags[]` fans one record into N. Verified `[…][0]`: `["x","y"]`→`"x"`, `[]`→`null`, absent→`null`, `[false,…]`→`false` (preserved, unlike `// null`). Do not use `first(…)`.
- **SQL path, ORDERED lookup** (`codegen.go` is in package `query`, so `cols.byPath` is reachable): (1) the **full dotted path** is a key of `cols.byPath` → one quoted identifier `"user.name"` - correct for both targets, since `ColumnModel` keys on the full path and the illustrative `data` table is flat; (2) else non-SQLite → `sqlIdent(path)` anyway; (3) else SQLite and not a column → `json_extract(sqlIdent(segs[0]),'$.rest')` **plus a warning**. A single segment whose key contains a dot must produce `'$."a.b"'`, selecting `{"a.b":1}` - not `{"a":{"b":…}}`.
- **`sqlNumber`:** `json.Marshal` semantics - `'f',-1` when `abs==0 || 1e-6 <= abs < 1e21`, else `'e'`; **error on NaN/±Inf**. Verified: `1e6→1000000`, `1234567.5→1234567.5`, `1e-7→1e-7`, `1e21→1e+21`, `2^53→9007199254740992`. A bare `'g',-1` gives `1e+06`; a bare `'f',-1` gives a 301-char string for `1e300`; `FormatFloat(NaN,…)`→`"NaN"`, which SQLite rejects as `no such column: NaN`.

- [ ] **Step 1: Write failing tests**: each jq path form incl. a dotted key and a unicode key; `?`-suffixing present on every segment; the projection wrapper for an `Elem` path; the three SQL path branches (**no `"a"."b"` golden - that is invalid SQL**); `'$."a.b"'` for a dotted single key; `sqlIdent(`he said "hi"`)` → `"he said ""hi"""`; `sqlLiteral("O'Brien")` → `'O''Brien'`; `sqlNumber` for `1e6`, `1234567.5`, `1e-7`, `1e21`, `2^53`, and `NaN → error`.
- [ ] **Steps 2-4** (FAIL → implement → PASS + gofmt). **Step 5: Commit** - `feat(query): jq and sql path encoding and literal escaping`.

---

### Task 2: The jq program

**Files:** Modify `internal/query/codegen.go`, `codegen_test.go`.

**Interfaces:** `jqCondition`, `jqFilter`, `jqProgram(f Filter, t Transform, ctx CodegenContext) (string, []string, error)`.

Templates - **decision 13's parenthesisation is mandatory in every one**, and the ops whose Go rule is type-gated carry an explicit type guard (`typedEqual`, `filter.go:414-444`, makes a kind mismatch `false`, so Go's `ne` is **false** cross-type, while jq has a total order across types and would select `{"p":"hello"}` for `.p != 5`):

| Op | jq |
|---|---|
| eq | `(.p != null and .p == V)` - jq `==` is already type-aware |
| ne / lt / lte / gt / gte | guard on the **operand** kind, which is what the Go predicate branches on: `((.p\|type)=="number" and .p != V)`, `…=="string" and .p < V`, `…=="boolean" and .p != V` |
| contains | `((.p\|type=="string") and (.p\|contains(V)))`; CI → `((.p\|type=="string") and ((.p\|ascii_downcase)\|contains(<V downcased>)))` |
| regex | `((.p\|type=="string") and (.p\|test(R)))`; CI → `test(R;"i")` |
| eq/ne CI | `((.p\|type=="string") and ((.p\|ascii_downcase) == (V\|ascii_downcase)))` - the guard is mandatory, `ascii_downcase` **errors** on a non-string and an error inside `select()` aborts the program |
| in | `(.p as $x\|any(V1,V2,…;.==$x))`; empty → `false` |
| isnull / notnull | `(.p == null)` / `(.p != null)` |
| bool | `(.p == true)` / `(.p == false)` |
| `[]`+op | `any(.tags[]?; ((type)=="number") and . > V)` - the `?` is required (`any(.tags[];…)` on `{"other":1}` → `Cannot iterate over null`, exit 5, killing the whole stream) and is safe because `any()` maps an empty generator to `false`, exactly the Go result |

- **Every condition is wrapped `(<expr>) // false`** so a `?`-yielded empty becomes false rather than propagating (bare `?` + `not` emits nothing where Go's `Negate` keeps the record).
- Groups/negate: `(… and …)` / `(… or …)` / `(… | not)`, always parenthesised. Zero-term node → `true`/`false` per decision 14.
- Projection: `{ "as1": .p1 }` in slice order, key via `json.Marshal(out)` where `out = spec.As` or `columnName(...)` (matching `compileSelect`); `Drop` → `del(.d1,.d2)`.
- Full program: `select(<F>) | <P>`; `select` omitted when `isEmptyFilter(f)`, `| <P>` omitted for an identity transform - a match-all identity query is `.`, not `select(true) | .`. A `# ` invocation note is prepended per format (`.[] |` for a JSON array, plain for NDJSON, "convert to JSON first / use the SQL" for csv/parquet/sqlite).
- CI caveat comment once per output (decision 12).

- [ ] **Step 1: Write failing tests**: byte-for-byte goldens per op incl. all four CI variants; AND/OR/nested/negate; the three zero-term cases (`Filter{Negate:true}`, childless `and` group, childless `or` group, plus a childless OR nested under an AND with one real condition - under a "silently skip" mis-implementation the AND case is *accidentally* right, so both are required); empty filter emits no `select(`; the `Select` golden uses a **reverse-alphabetical** fixture (`zeta` before `alpha`) and its mutation is *build the projection from a map and emit its keys **sorted*** (a bare `for k := range m` on 2 entries reverses only ~1/8 of the time and reads as "does not kill"); goldens for `Drop` too. **Real-jq execution:** an equivalence proof - one NDJSON fixture carrying, per op, a value of every wrong kind (string/number/bool/array/object/null/missing), asserting the emitted record set equals what `CompileFilter(...).Match` selects over the same decoded records; the `contains`/`regex`/CI fixtures must include a **string** value, one matching and one not (a non-string fixture short-circuits and hides the broken template); a sparse `[]`-path case over `[{id:1,tags:["x"]},{id:2,other:1},{id:3,tags:["x"]}]` asserting **both** id 1 and id 3 are emitted; a `Select{{Path:"tags[]",As:"tag"}}` case proving 3 records in → 3 out; a `Select` aliased `he said "hi"`.
- **Mutations:** revert either `contains`/`regex` template to the spec form → the exec test fails; drop the `ne` type guard → jq emits the string record Go does not; drop the `?` from `[]` → id 3 is lost; drop `"i"` from CI `test()` → golden fails; emit the CI caveat per condition → the two-CI-condition golden fails.
- [ ] **Steps 2-4.** **Step 5: Commit** - `feat(query): jq program generation`.

---

### Task 3: The SQL query (display)

**Files:** Modify `internal/query/codegen.go`, `codegen_test.go`.

| Op | SQL |
|---|---|
| eq / ne | `"p" COLLATE BINARY = V` / `<> V` (collation only when `ctx` targets SQLite; the illustrative `data` table has no declared collation) |
| lt/lte/gt/gte | `"p" COLLATE BINARY < V` … |
| contains | `instr("p",V)>0`; CI → `instr(lower("p"),lower(V))>0` - `instr` is collation-blind (verified) so no `COLLATE` |
| eq/ne CI | `lower("p") = lower(V)` / `<>` |
| regex | `"p" REGEXP 'R'` + caveat; **never executed** |
| in | `"p" COLLATE BINARY IN (V1,…)` - **left operand**; empty → `1=0` |
| isnull / notnull | `"p" IS NULL` / `"p" IS NOT NULL` |
| bool | `"p" = 1` / `"p" = 0` |
| `[]`+op | `EXISTS(SELECT 1 FROM json_each("tags") j WHERE j.value <op> V)` |

- Projection: `SELECT "p1" AS "as1", …` with the alias through `sqlIdent`; `Drop` → enumerated kept columns; else `SELECT *`. Zero-term node → `1=1`/`1=0`; `WHERE` omitted when `isEmptyFilter(f)`.
- Warnings + `-- note:` lines for: regex (once), CI (once, decision 12), a `json_extract` fallback path, and **a condition on a blob-/time-tainted column** (decision 3) - otherwise the copied SQL returns 0 rows against the user's own database and reads as a shape bug.
- Known accepted limitation, recorded as a `-- note:`: the display SQL has no type guard for `ne`/ordering (an untyped column's `"p" <> 5` returns `'hello'`), unlike the jq output.

- [ ] **Step 1: Write failing tests**: goldens per op incl. CI and `COLLATE BINARY` placement; AND/OR/nested/negate; the zero-term set; projections incl. a quoted alias; the illustrative prefix per non-SQLite format and its absence for SQLite; each caveat exactly once. **Executability per Global Constraints** (exact ordered rowid set, discriminating fixture, gate on the AST for regex). **Mutations:** move `COLLATE BINARY` to the right of `IN` → the NOCASE fixture returns 3 instead of 1; swap `instr` arguments → the execution assertion fails; drop `sqlIdent` from the alias → `near "hi": syntax error`.
- [ ] **Steps 2-4.** **Step 5: Commit** - `feat(query): sql query generation`.

---

### Task 4: `Codegen` entry point, DTOs, and the engine's source metadata

**Files:** Modify `internal/query/codegen.go`, `codegen_test.go`, **`internal/query/engine.go`, `engine_test.go`**.

The engine cannot answer "what format / which table" today: it keeps only `backends` + `sources map[string]string`, `format` is a local in `OpenSource` that goes into `OpenResult` and is discarded, `req.Table` is never retained, and `Backend` exposes neither. Without this Task 2's invocation note and Task 3's illustrative prefix cannot be produced at all.

1. `sources` becomes `map[string]sourceMeta` with `{path, format, rawFormat, table string}`; keep `sourcePath(handle)` as a thin accessor so `ExportQuery`'s overwrite guard and its tests are untouched.
2. Populate in `OpenSource`: `format` is already in scope; `rawFormat = req.Format` (the user's override - `readers.Format` cannot express "ndjson"); table via a same-package assertion `if sb, ok := backend.(*sqlBackend); ok { meta.table = sb.table }` - the **resolved** table, never `req.Table`, which is usually `""`.
3. **`register`'s meta write must be UNCONDITIONAL** - today `if path != ""` skips it, so a pathless backend silently loses its format and table too.
4. `CloseSource` must delete from the new map.
5. `CodegenContext.Format` is `rawFormat` when it is `"ndjson"`, else `format`. Do **not** leak "ndjson" into `OpenResult.Format` (`engine_test.go:63-64` asserts otherwise).

`Generated{JQ, SQL string; Warnings []string}`; `CodegenRequest{Handle, Filter, Transform}`; `Engine.Codegen(req) (Generated, error)`.

- [ ] **Step 1: Write failing tests**: (a) a sqlite handle opened with an **empty** `Table` reports the auto-resolved name (**mutation: store `req.Table` → fails**); (b) a memory-tier **CSV** handle reports `"csv"` and a **JSON** handle `"json"` (**mutation: hardcode `Format:""` → fails**; no type-switch workaround covers this); (c) `Format:"ndjson"` reports `"ndjson"` and its jq program has no `.[] |` prefix; unknown handle errors; warnings deduplicated; **`Engine.Codegen` never scans - mutation: have it call `backend.Count` → a counting fake records a call and the test fails**.
- [ ] **Steps 2-4.** **Step 5: Commit** - `feat(query): Codegen entry point and engine source metadata`.

---

### Task 5: The pushdown planner

**Files:** Create `internal/query/sqlpushdown.go`, `sqlpushdown_test.go`; **modify `internal/query/filter.go` (+ `filter_test.go`)**.

**Step 0 - the AST must reach the call sites.** `CompiledFilter` is `{pred, key}`; `CompiledPlan` drops `CompilePlan`'s `f Filter`; `Query` gets a `*CompiledPlan` and `Count` a `*CompiledFilter`. Add an unexported **pointer** field `src *Filter` to `CompiledFilter`, set only by `CompileFilter` (both returns). `sqlbackend.go` is in package `query`, so `p.Filter.src` is read directly - no exported signature change, no `Backend` change. **Pointer, not value, is load-bearing:** the zero `Filter` IS match-all, so a hand-built `&CompiledFilter{pred: …}` would present as "empty filter" and push a WHERE-less query returning every row - and that exact pattern exists three times already (`engine_test.go:407-419`, `:541-556`, `:1017-1029`). `src == nil` ⇒ unknown ⇒ Go path.

**Interface:** `sqlPushdown(f Filter, cols *ColumnModel, noPush map[string]bool) (where string, args []any, exact bool)` - every precondition from decisions 3-6, `COLLATE BINARY` on the left operand of every string comparison, numeric args always `float64`.

- [ ] **Step 1: Write failing tests** - a table-driven matrix against **ONE** explicitly built `ColumnModel` containing `id`(int), `name`(string), `score`(float), `flag`(bool), `tags`(array), `meta`(mixed), **and a real column whose Path literally contains a dot, `user.name`** (from `CREATE TABLE t ("user.name" TEXT, …)`), plus a `nosuch` unknown path. The dotted-path row **must** use `user.name`, or `len(segs)==1` is not the thing rejecting it and the mutation cannot flip it (`buildColumnModel` excludes every `Elem` path anyway, so `tags[]` is rejected as unknown regardless).
  Pushable rows: numeric eq/ne/lt/lte/gt/gte with `|Num| < 2^53`; string eq/ne/contains/in; isnull/notnull; AND groups; OR groups with all children pushable. **Not** pushable: regex; `ci:true` **for each of eq, ne, contains, regex separately** (a per-op implementation could satisfy a single "allow ci" mutation); a dotted path; an `Elem` path; string `lt` (decision 5); numeric operand `9007199254740992` (kills a `>=` off-by-one) while `2^52` stays pushable; `in` with any element ≥ 2^53 or of a mismatched type; a `mixed`/`object`/`array`/`null` column; any `Negate`; an OR with one non-pushable child; a zero-term node; **any op on a blob-tainted or DATE-decltype column**.
  Assert the exact WHERE text AND the args slice for every pushable row.
  **Note in the plan and in the test file:** `bool` is unreachable for `sqlBackend` (driver returns `int64` → `json.Number` → `dominantKind` = `"int"`), so that row is a unit-level guard for a future backend and has **no** Task 6 row-equality counterpart. State which rows do (int/float/string comparisons, `in`, isnull/notnull, groups).
  **Mutations:** allow `ci` (per-op); allow a dotted path; allow string ordering; allow `Negate`; allow an OR with a non-pushable child; delete the 2^53 gate; bind numerics as `int64` (the 1.5-operand case); move `COLLATE BINARY` right of `IN`; `src *Filter` → `src Filter` (the hand-built decorator pushes an empty WHERE and returns every row).
- [ ] **Steps 2-4.** **Step 5: Commit** - `feat(query): conservative sql pushdown planner`.

---

### Task 6: Wire pushdown into `sqlBackend`

**Files:** Modify `internal/query/sqlbackend.go`, `sqlbackend_test.go`.

**The taint probe.** Add `noPush []bool` (len == `len(s.cols)`) + `rawProbed bool`. Inside `scan`'s row loop where `vals[i]` is still raw (`sqlbackend.go:288-294`), flag any column that yields `[]byte` or `time.Time`; set `rawProbed` after `newSQLBackend`'s profiling pass returns - the guard is required because `scan` is shared by `Query`/`Count`/`Export` and must not mutate state concurrently. The flags are **complete, not sampled** (the profiling pass visits every row; the connection is `immutable=1`). Belt-and-braces, free: `sqliteTableColumns` already reads PRAGMA `table_info` and discards `type` - also flag decltype `DATE`/`DATETIME`/`TIMESTAMP`, which covers an all-NULL column (decltype alone cannot predict a BLOB).

**The pushdown is NOT one switch - it splits per method:**

| Method | Push when |
|---|---|
| `Count`, and `Query`'s `wantTotal` `COUNT(*)` | `exact` - **unconditionally**, no rowid requirement. Set-based, order-independent, no ordinal. **This is the verified win.** |
| `Query`'s row window | `exact && s.hasRowID && dense` - select `_rowid_` alongside `s.cols` and project `Project(rec, rowid-1)`; otherwise keep `queryFiltered` for rows while still pushing the count |
| `Export` | `exact && s.hasRowID` - no ordinal needed (all three encoders take `Encode(_ int64, …)`; pin that with an assertion so a future encoder cannot quietly start reading it) |

Why the extra gates: (a) **`Row.Index` is the absolute scan ordinal, not the match ordinal** (`queryFiltered` counts every row; `columns.go:187` documents it; `DataTable.svelte:300` renders it; `sqlbackend_test.go:781` already pins it against memBackend) - the natural `offset+i` is wrong, and `_rowid_-1` is wrong too after deletions (verified `COUNT=8 MIN=1 MAX=10`). Hence the **dense-rowid probe** run once at open beside the existing `hasRowID` probe: `SELECT COUNT(*)=MAX(_rowid_)-MIN(_rowid_)+1 AND MIN(_rowid_)=1 FROM t` (O(1) via the rowid B-tree, sound because `immutable=1`). Do **not** use `ROW_NUMBER() OVER (ORDER BY _rowid_)` - verified it forces `SCAN t` plus a co-routine subquery, costing more than the scan it replaces. (b) **Without `_rowid_` there is no ORDER BY at all**, and adding a WHERE lets SQLite switch index: verified on `WITHOUT ROWID` + a secondary index, `SELECT …` → `[b c a]` but `… WHERE "k" >= 'a'` → `[a b c]` - a different LIMIT window, silently. (c) `Scanned` on the pushed path is `offset + int64(len(rows))`, the `queryUnfiltered` convention ("rows SQLite handed back"); document it.

Reuse `sqliteQuoteIdent` and splice the WHERE into `selectSQL()`'s shape (keeping `ORDER BY _rowid_` behind `s.hasRowID`) rather than building a new string. Update the file header, which still says pushdown is deferred to E5.

- [ ] **Step 1: Write failing tests.** The load-bearing one is **row-equality between the two paths**, via a helper `assertRowSetEqual(t, got, want)` that copies both, zeroes **only** `ElapsedMs` and `Scanned`, then `reflect.DeepEqual`s the whole struct - so `Columns`, every `Row` **including `Row.Index`**, `Offset`, `Total`, `TotalExact`, `Truncated`, `ColumnsTruncated`, `TotalPaths` are covered and any future field is compared automatically. Assert `Scanned` separately (`== offset+len(rows)` on the pushed path; `pushedScanned < goScanned` on a selective fixture, proving the win is real).
  **Per-case precondition, not an aggregate counter:** before each pushable row, `where, args, exact := sqlPushdown(f, sb.Columns(), sb.noPushSet()); if !exact { t.Fatalf("case %q is not pushable against the real fixture - this row proves nothing", name) }`. A single aggregate "pushdown was used" counter makes every case unfalsifiable (a case that silently fails to push still passes row-equality because both runs took the Go path). Do not phrase it as "delta exactly 1" - a pushed `Query` with `wantTotal` issues two statements. Apply the precondition to the `Count` and `Export` forks too.
  **Fixtures - on today's dense contiguous fixtures every wrong implementation survives:** matches that do not start at row 0 and are not contiguous; a **sparse-rowid** fixture (insert 40, `DELETE` two mid-table); a `WITHOUT ROWID` fixture with an index whose order differs from PK order, run with `Limit: 2`; **new column types**: `b BLOB` (`x'6162'`), a declared-`TEXT` column holding one BLOB and one TEXT row, `ts DATETIME`, `name TEXT COLLATE NOCASE` (`'Apple','apple','BANANA'`), `pad TEXT COLLATE RTRIM` (`'abc   ','abc'`), and an int column holding **both** `9007199254740992` and `9007199254740993`. Without new column *types* the deep-equality test is vacuous for entire bug classes.
  **Mutations:** emit `offset+i` as the row index → fails; emit `_rowid_-1` → fails only on the sparse fixture; remove the `hasRowID` gate → the WITHOUT ROWID window is wrong; remove the raw-kind gate → the BLOB `eq` case flips to pushable and diverges; remove `COLLATE BINARY` → the NOCASE case returns 2 where Go returns 1.
- [ ] **Steps 2-4** + `go test ./... -count=1`. **Step 5: Commit** - `feat(query): push exact filters down to sqlite`.

---

### Task 7: Wails binding

**Files:** Modify `gui/app.go`, `gui/app_test.go`; regenerate `gui/frontend/wailsjs/**`.

`func (a *App) Codegen(req query.CodegenRequest) (query.Generated, error)`; `sourceEngine` gains the same. Straight pass-through, **no ctx parameter** (codegen is pure and instant, and matching `*query.Engine`'s signature keeps `var _ sourceEngine = (*query.Engine)(nil)` compiling).

- [ ] **Step 1: Write failing tests** with a NEW spy mirroring `exportSpyEngine` - `codegenSpyEngine{*query.Engine; gotReq; res; err}`. The existing fakes override only `OpenSource`/`ExportQuery`, so an un-overridden `Codegen` would hit the real engine and error on an unknown handle.
- [ ] **Steps 2-3.** **Step 4:** `go test ./...` + `wails generate module` (bindings diff EXPECTED; commit in the same commit) + `npm run check` 0 errors.
- [ ] **Step 5: Commit** - `feat(gui): Codegen binding`.

---

### Task 8: The codegen panel

**Files:** Create `CodegenPanel.svelte`, `CodegenPanel.test.ts`. Modify `store.ts` (+ `store.test.ts`), `Explorer.svelte`, **`Explorer.test.ts`, `ExportDialog.test.ts`, `FilterBar.test.ts`**, `App.svelte`, `lib/Header.svelte`.

**The four extra test files are not optional:** their `vi.mock` factories for `wailsjs/go/main/App` are exhaustive and have no `Codegen`, and vitest 0.34.6 throws `No "Codegen" export is defined on the mock` on first property access. Add `Codegen: vi.fn(() => Promise.resolve({ jq: ".", sql: "SELECT * FROM data", warnings: [] }))` - a bare `vi.fn()` resolves `undefined` and `g.jq` still throws. (`TransformPanel.test.ts` needs nothing: it stubs `setTransform` and never opens a file.)

**Store:** `ExplorerState.codegen: Generated | null`, `codegenError: string`, both added to the `empty` const so `close()` clears them; action `refreshCodegen()` called at the end of `setFilter`/`setTransform` and in `open()` (after the existing `myGen !== gen` block). It captures `const myGen = gen` **synchronously before the await** and re-checks before **both** the success and error writes - `gen` is the complete key, not `handle`, which none of the triggers changes, and Wails runs each binding call in its own goroutine so resolution order is not call order. Success writes `{codegen, codegenError:""}` in one update; failure writes `codegenError` alone, **leaving the last good output**. No clear-at-start (E4's `runExport` pattern would blank the panel on every debounced keystroke) and no loading flag.

**Panel:** two labelled read-only monospace blocks with a **Copy** each via `ClipboardSetText`, plus warnings. `CodegenPanel.test.ts` must `vi.mock("../../../wailsjs/runtime", () => ({ ClipboardSetText: vi.fn(), EventsOn: vi.fn(() => () => {}) }))` - the real module dereferences `window.runtime`, absent in jsdom, and the spy is what the exact-text assertion reads.

**Wiring (specified, not inferred - E5's only new user-facing path, and there is no `Header.test.ts`/`App.test.ts` at all today):** `Header.svelte` gains `export let codeOpen = false`, `toggleCode: void` in the dispatcher generic, and a `Code` button with `aria-pressed={codeOpen}`; `App.svelte` gains `let codeOpen = false`, passes `{codeOpen}`, `on:toggleCode={() => (codeOpen = !codeOpen)}`, and `bind:codeOpen` on `<Explorer>`. Because three docked panels can now stack (45vh each), cap the panel column with `max-height` and let it scroll.

- [ ] **Step 1: Build it**, then tests: the panel renders the store's jq and SQL; Copy calls `ClipboardSetText` with the **exact** string (mutation: include the label → fails); warnings render; an empty filter shows a valid identity program; the store refreshes after `setFilter` **and** after `setTransform` - asserting on `vi.mocked(Codegen).mock.calls.at(-1)?.[0]` carrying the new transform (the idiom at `store.test.ts:582`), because asserting only on `$explorer.codegen` passes with a constant mock; a codegen error surfaces **without wiping the last good output**; an `Explorer.test.ts` case that the Code button toggles the panel and reflects `aria-pressed`.
- [ ] **Step 2: BUILD.** `wails build`, then `git checkout -- gui/frontend/dist/.gitkeep`; `npm run check` 0 errors; `npm run test` green by exit code.
- [ ] **Step 3: Commit** - `feat(gui): jq and sql codegen panel`.

---

### Task 9: Full-stack verification and docs

**Files:** Modify `gui/README.md`, `README.md`, **and `docs/superpowers/specs/2026-07-17-shape-engine-design.md` §7** (decision 15's four deviations, as a separate `fix(docs)` commit).

- [ ] **Step 1: Gates.** `npm run check` 0 errors; `npm run test` green by exit code (state the count; must exceed E4's 294); `go test ./... -count=1` 16 packages; gofmt clean; `go.mod`/`go.sum` unchanged; no `dependencies` block; `wails build` succeeds then `git checkout -- gui/frontend/dist/.gitkeep`; `wails generate module` diff empty after Task 7.
- [ ] **Step 2: Execute what the user would copy.** Against a real SQLite fixture: build a filter in the GUI, copy the generated SQL, run it through a Go harness, and compare to the status bar's count - **restricted to filters the plan says agree** (no regex, no CI, no ordering-on-string, no tainted column). For a filter that deliberately diverges, assert the caveat comment is present instead. Same for the jq program if `jq` is on the machine; if it is not, say so rather than claiming it was checked.
- [ ] **Step 3: Prove the acceleration.** Time `Count` on a >100k-row SQLite fixture with a pushable filter, pushed vs seam-disabled, and record both numbers. Note in one line that a non-BINARY column keeps correctness but forfeits its index (SEARCH → SCAN) - the accepted trade.
- [ ] **Step 4: Commit** - `docs: document jq and sql codegen` + `fix(docs): correct engine spec §7 codegen templates`.

---

## Self-Review

**Coverage:** path/literal/number encoding (T1) · the jq program (T2) · the SQL query (T3) · `Codegen` + engine metadata (T4) · the pushdown planner incl. the `src *Filter` plumbing (T5) · `sqlBackend` per-method pushdown with row-equality proof (T6) · binding (T7) · panel + wiring (T8) · verification + docs + spec fix (T9).

**Explicitly NOT in this plan:** tree view, `GetCell`, global search, launch polish (E6) · an editable SQL console (decision 9) · Parquet pushdown (row-group pruning is its acceleration and E1 ships it) · `Unflatten` (E4 decision 14).

**Correctness checks (each mutation-proven):** goldens byte-for-byte and **executed**, not merely parsed (T1-T3) · jq programs run through real jq on CI, asserting stdout equality against `CompileFilter.Match` (T2) · `Engine.Codegen` never scans (T4) · the pushability matrix refuses `ci` per-op, dotted paths, string ordering, ≥2^53 operands, `Negate`, mixed types, tainted columns, zero-term nodes (T5) · a hand-built `CompiledFilter` cannot push (T5) · **the pushed path returns `RowSet`s deep-equal to the Go path including `Row.Index`**, on sparse-rowid, WITHOUT ROWID, BLOB, DATE, NOCASE, RTRIM and 2^53 fixtures (T6) · each pushable case asserts its own pushability first, so no row is vacuous (T6) · Copy puts the exact program on the clipboard, and codegen refreshes on both triggers asserted by request argument (T8).

**The risk this plan is built around:** pushdown is the first optimisation here that can silently change *results*. Decisions 2-6 exist to make that structurally impossible - exact-or-nothing, type-gated, collation-forced, magnitude-bounded, taint-probed - and T6 proves it by deep-equality against the path it replaces. **Every one of those five gates was added because an experiment showed the naive version returning different rows.**
