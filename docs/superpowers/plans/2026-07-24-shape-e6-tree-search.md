# E6: Cell Value Tree View + Global Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. Every task is TDD for its pure logic; Svelte-component tasks add a jsdom wiring test AND a `wails build`, not only a unit test.

**Goal:** Two things the explorer still can't do. (1) See the WHOLE of a value the table can only preview: click a truncated object/array cell and expand its full nested structure. (2) Find a value without knowing which column it's in: a global search box that filters to rows where any field contains the text, no jq required.

**Architecture:** E6 touches Go again, but far less than E4/E5.

1. **`GetCell`** (`internal/query`): the engine already returns a `Cell` whose container preview is capped at `previewCap=200` (`columns.go`). A tree view needs the untruncated value, so add `Backend.GetCell(ctx, index, path) (json.RawMessage, error)` — fetch the record at an absolute ordinal, resolve the path, and marshal the full value. Read-only, no filter/transform, no scan of anything but the one record.
2. **Global search** (`internal/query`): a `Search` string threaded alongside `Filter` into `QueryRows`/`CountMatches`, compiled to a predicate that matches when ANY scalar leaf of a record contains the text (case-insensitive), AND-ed with the visual filter. It reuses the whole existing filter/count/window machinery — search is just an extra conjunct on the compiled predicate.
3. **Frontend**: a value-tree component (recursive, expand/collapse) shown in a panel/overlay when a container cell is clicked; a search box in the filter bar wired to the store; the codegen panel gains the search term where it belongs.

**Tech Stack:** Go 1.25 stdlib (`encoding/json`, `strings`); the vendored `modernc.org/sqlite` for the sqlite tier's GetCell. Frontend: Svelte 3.49 + vitest. **No new dependency, cgo-free.**

**AUTHORITATIVE SOURCES:** product spec `docs/superpowers/specs/2026-07-17-shape-data-explorer-design.md` §3.3 (tree view), §3.4 + §5 E6 line (global search), §8 (both themes). Engine spec `docs/superpowers/specs/2026-07-17-shape-engine-design.md` §8 (`GetCell`/`CellRequest` — the DTO is already specified), §5 (filter semantics the search predicate must be consistent with), §4 (per-backend record access). Existing code E6 builds on: `internal/query/columns.go` (`resolve`, `toCell`, `previewCap`, `Row.Index` = absolute record ordinal), `internal/query/filter.go` (`CompileFilter`, the compiled predicate), the four backends' record access (`memBackend.records`, `rescanBackend.scan`, `sqlBackend.scan`, `parquetBackend.scan`), `internal/query/engine.go` (the binding/DTO layer), `gui/frontend/src/lib/explorer/{CellView,DataTable,FilterBar,store}.*`.

## Decisions locked before this plan (do not relitigate)

1. **`GetCell`'s index is the ABSOLUTE record ordinal** (`Row.Index`), NOT a filtered/windowed position. The table already carries `Row.Index` (`columns.go`, rendered as the row-number gutter), so a cell the user clicked hands its own index straight back — no filter/transform context needed, and the result is stable regardless of what filter is active. This is what spec §8's `CellRequest.Index` means.
2. **`GetCell` returns the FULL value, untruncated, as `json.RawMessage`.** It is the escape hatch from `previewCap`. It marshals through the same non-finite-safe path `toCell`/`compactJSON` use (`sanitizeValue`, `columns.go`) so a NaN/±Inf deep in the value cannot make it error. A path that resolves to no value returns JSON `null` with a distinguishing flag, not an error (missing is normal).
3. **`GetCell` reads exactly one record.** memBackend indexes `records[N]` in O(1); the streaming/sql/parquet backends scan/seek to record N and stop. It never applies a filter or transform, never returns more than one value, and an out-of-range index is an error (the UI only ever asks for an index it just rendered).
4. **Global search is a SEPARATE predicate AND-ed with the filter, not a synthetic filter condition.** A 512-column OR-of-contains would be unreadable in codegen and O(columns) per record; instead search compiles to one predicate that walks a record's scalar leaves once. It is threaded as a `Search string` field on `QueryRequest`/`CountRequest`, compiled in the engine, and combined with the visual filter's predicate by logical AND.
5. **Search matches SCALAR LEAF VALUES, case-insensitively (Unicode), never keys.** A record matches when any string/number/bool leaf, rendered as text, contains the (lowercased) query. Keys are structure, not data — searching them would surface `"name"` for a query of `nam`. Numbers are matched on their exact source literal (`json.Number` text), bools on `true`/`false`, null is not matched (there is nothing to contain). Folding is `strings.ToLower` (Unicode), consistent with the filter's `contains` operator (`filter.go`), NOT ASCII-only.
6. **An empty search string is a no-op** — the same match-all as no search. It must produce byte-identical `QueryRows`/`CountMatches` behavior to today, so a blank box never changes results or costs a predicate.
7. **Search participates in the count and the cache exactly like the filter.** The match count reflects filter AND search; the `FilterKey`/bitset/count caches key on both, so two different searches never share a cached count. (This falls out of compiling search INTO the same predicate the caches already key on — see Task 3.)
8. **Search is NOT pushed to SQLite.** The E5 pushdown is filter-only and exact-or-nothing; a leaf-walk contains has no faithful SQL translation (it would have to `instr` every column with the collation/BLOB caveats E5 documented). On the sqlite tier a searched query keeps the Go predicate path. (Codegen MAY show an illustrative multi-column SQL with a caveat — see Task 5 — but the engine never executes it.)
9. **Search appears in codegen** as the jq/SQL equivalent, so "copy the query" stays honest: jq `select(.. | strings | ascii_downcase | contains(q))` style (or an explicit any-over-leaves form the review will pin), SQL as an illustrative OR-of-`instr` across columns with a caveat that shape walks leaves generically. Consistency with §8 decision: the search term is part of "the current query".
10. **The tree view is READ-ONLY.** It displays a value; it does not edit, filter-from, or export a sub-value. Copy-the-value is the only affordance (via `ClipboardSetText`, the E5 idiom).
11. **Themes already work** (`App.svelte`'s `toggleTheme` + `prefers-color-scheme`); E6 does NOT rebuild them. The "polish" of the spec's E6 line that IS engineering — making the new tree/search surfaces theme-correct in both modes — is in scope; the launch artifacts (README GIF, screenshots, a recorded demo) are NOT: they require driving the GUI interactively, which is a human task, and are called out as such in Task 9, not attempted.

## Global Constraints

- **Zero new dependencies**, cgo-free, no DuckDB.
- **`GetCell` and search change results for NOBODY who does not use them.** An unsearched, non-GetCell path must be byte-identical to today: every existing `internal/query` and frontend test must still pass unchanged. (Task 1/3 add a field and a method; they must not alter any existing code path's behavior.)
- **The search predicate is consistent with the filter engine's value model** (`{nil, bool, string, json.Number, float64}` + nested `map`/`[]any`, per `readers.ToProfileValue`): it walks exactly those, and its folding matches the filter's `contains`.
- **Determinism:** the leaf walk visits values in a fixed order (it only needs "any match", so order affects nothing observable, but the walk itself must not depend on Go map iteration for correctness — a match is a match regardless of order; state this so a reviewer does not flag map iteration).
- **Every new test must fail if the logic it covers regresses** — state the exact mutation and confirm it kills the test. This repo has shipped cannot-fail tests in every phase; the mutation proof is the gate.
- **Bindings regeneration is expected** (Task 6/7 add DTOs); run `wails generate module` and commit the diff WITH the Go change. Never `git add -A` after `wails build` (it deletes the tracked `gui/frontend/dist/.gitkeep`); run `git checkout -- gui/frontend/dist/.gitkeep` and stage explicitly.
- **Commits: Conventional Commits, lowercase imperative subject, NO co-author trailer.**
- Gates per task: Go — `go build ./... && go test ./... -count=1` (16 packages) and gofmt clean. Frontend — `npm run check` (0 errors) and `npm run test` (green by process exit code). Component tasks add `cd gui && wails build`.

---

### Task 1: `Backend.GetCell` + `resolveFull`

**Files:** Modify `internal/query/backend.go` (interface + doc), `internal/query/columns.go` (a full-value marshaller beside `toCell`), each backend's `.go` + `_test.go`. Implements spec §8's per-backend value access and decisions 1-3.

**Interfaces (produces):**
`GetCell(ctx context.Context, index int64, segs []Seg) (json.RawMessage, bool, error)` on `Backend` (the `bool` is `found` — false when the path resolves to no value, distinct from a value that IS json `null`);
`func fullCellJSON(v any) (json.RawMessage, error)` in columns.go (marshals a resolved value non-finite-safely, reusing `sanitizeValue`).

- Each backend fetches record `index`: memBackend `records[index]` (bounds-checked); rescan/sql/parquet scan/seek to `index` and stop at it (early-return from the existing scan callback). An index past EOF is an error.
- `resolve(record, segs)` (columns.go) already returns the value set for a path; `GetCell` takes the FIRST value (matching `toCell`/`Project`'s first-value rule) and marshals it in full — no `previewCap`, no `Str` truncation. Empty set → `found=false`, `null`, no error.
- `fullCellJSON` marshals via `json.Marshal`, falling back to `sanitizeValue` on a non-finite error exactly as `compactJSON` does (`columns.go`), so a value with a NaN deep inside serializes rather than erroring.

- [ ] **Step 1: Write failing tests** per backend (reuse each backend's existing fixture helpers): `GetCell` on a container path returns the WHOLE value — build a fixture whose object's compact JSON exceeds `previewCap` and assert the returned bytes parse back to the full object, not a 200-byte prefix (**mutation: have GetCell return `toCell(v).Str` → the test sees a truncated string and fails**); a scalar path returns that scalar; a missing path returns `found=false` and `null`; an explicit JSON null returns `found=true` and `null` (the found flag is what distinguishes them); an out-of-range index errors; a value containing a NaN deep inside still marshals (no error). Cross-backend: the same logical record at the same index returns byte-identical JSON from mem and rescan (the row-identity invariant, spec §9).
- [ ] **Steps 2-4** (FAIL → implement → PASS + `go test ./... -count=1` + gofmt). **Step 5: Commit** — `feat(query): GetCell returns a cell's full untruncated value`.

---

### Task 2: `Engine.GetCell` + Wails binding

**Files:** Modify `internal/query/engine.go` (`CellRequest` DTO + `Engine.GetCell`), `engine_test.go`, `gui/app.go` (+ `app_test.go`); regenerate bindings.

**Interfaces (produces):**
```go
type CellRequest struct { Handle string `json:"handle"`; Index int64 `json:"index"`; Path string `json:"path"` }
type CellResult struct { Value json.RawMessage `json:"value"`; Found bool `json:"found"` }
func (e *Engine) GetCell(ctx context.Context, req CellRequest) (CellResult, error)
func (a *App) GetCell(req query.CellRequest) (query.CellResult, error)
```

- `Engine.GetCell` looks up the handle, compiles `req.Path` to `[]Seg` **via the validating `resolveSegs`/`validatePath` helper (`filter.go` ~241-297), NOT the raw `parsePath` (`columns.go` ~46)** (plan review §10). `parsePath` has no error return and — the review noted — can loop on a malformed path; `resolveSegs` validates against the ColumnModel and returns an error, which is exactly the "a path that does not parse is an error" behavior this method needs. This also resolves a dotted/bracket key the same way the filter engine (and thus the table's columns) does. Then calls `backend.GetCell` and wraps the result.
- `App.GetCell` is a pass-through like `CountMatches` (a ctx is fine — GetCell is instant but reads a record, so give it `a.reqCtx()`).

- [ ] **Step 1: Write failing tests**: `Engine.GetCell` on a real handle returns the full value for a nested path; unknown handle errors; `Found` distinguishes missing from null; `App.GetCell` forwards the request verbatim (spy engine, like `codegenSpyEngine`). **Wails: `wails generate module`, commit the DTO diff with the Go change; `npm run check` proves the regenerated `CellRequest`/`CellResult` compile.**
- [ ] **Steps 2-4.** **Step 5: Commit** — `feat(gui): GetCell binding and DTOs`.

---

### Task 3: The search predicate + threading into Query/Count

**Files:** Create `internal/query/search.go`, `search_test.go`. Modify `internal/query/filter.go` or `transform.go` (where `CompiledPlan`/`CompiledFilter` compose — the search predicate must combine with the filter's), `engine.go` (`QueryRequest.Search`/`CountRequest.Search` + compile + thread), each backend if the predicate is applied there (prefer: fold search INTO the `CompiledFilter` so backends need zero changes). Implements decisions 4-7.

**Interfaces (produces):**
`func compileSearch(query string) func(rec any) bool` (nil for an empty query — match-all);
`CompileFilterWithSearch(f Filter, search string, cm) (*CompiledFilter, error)` composing both predicates and folding the search into the canonical key.

**The key correctness pins:**
- Search AND filter: a record matches iff the filter predicate AND the search predicate both hold. An empty search leaves the filter predicate byte-identical (decision 6).
- The compiled key (`CompiledFilter.Key()`, used by every cache) MUST incorporate the search string, or two searches share a cached bitset/count (decision 7). Fold the search into `canonicalFilterKey` (hash the search text alongside the filter JSON).
- **`CompileFilterWithSearch` MUST set the returned `CompiledFilter.src = nil` whenever `search != ""`.** This is the load-bearing correction the plan review caught (Critical): mem/rescan/parquet apply `cf.pred` and honor the folded search for free, but **`sqlBackend`'s pushdown gate reads only `cf.src`** (the filter AST — `sqlbackend.go`'s `pushdownFor`), NOT `cf.pred`. A pushable filter (`age>30`, `exact=true`) plus a search would otherwise run `queryPushed`/`pushedCountSQL`/`exportPushed`, none of which call `Match`, **silently dropping the search on the entire SQLite tier**. A nil `src` makes `pushdownFor` return `exact=false`, so `sqlBackend` falls back to `queryFiltered`/the Go-scan Count/Export, all of which apply the composed `pred`. `isMatchAllFilter` keys on `pred` (non-nil), so the unfiltered fast path is also correctly avoided. When `search == ""`, the result is byte-identical to `CompileFilter` (`src = &f` preserved), so pure-filter pushdown is unaffected (decision 6). This contradicts "zero backend change" for the `src` field — nulling `src` is precisely the mechanism that delivers decision 8's "the sqlite tier keeps the Go predicate path."
- Leaf walk: `matchesSearch(rec, lowered)` recurses through `map[string]any`/`[]any`, testing each scalar leaf: `string` → `strings.ToLower(s)` contains; `json.Number` → its `.String()` contains (lowercasing is a no-op but keep it uniform); `bool` → `"true"`/`"false"` contains; `float64` (from a reader that emits it) → its shortest representation; `nil` → never. Keys are never tested. First match short-circuits.

- [ ] **Step 1: Write failing tests** in `search_test.go`: `matchesSearch` finds a value nested two levels deep; finds inside an array element; is case-insensitive both ways (query `AbC` matches value `xabcy`, query `abc` matches value `XABC`) with a Unicode case (query `İ` vs value containing `i̇`, or `MÜNCHEN` vs `münchen` — one the ASCII fold would miss, matching the filter's `contains` behavior on the PURE Go predicate); does NOT match a key name; matches a number's literal (`42` matches value `1042`) and a bool; never matches null; an empty query returns a nil predicate (**mutation: return a non-nil always-false predicate for "" → an unsearched query returns nothing and every existing Query test fails**). Then engine-level: a `QueryRows` with `Search` narrows the rows AND the count; search AND filter compose (a row matching the search but not the filter is excluded); two different searches do not share a cached count (**mutation: drop the search from the canonical key → the second search returns the first's count**); an empty search is byte-identical to no search.
- [ ] **Step 1b (the Critical guard): a `sqlBackend`-tier test.** Build a SQLite fixture with DENSE rowids, apply an exact-pushable filter (`age>30`) AND a search that excludes some visually-matching rows, and assert `Query` rows, `Count`, AND `Export` (via a `collectEncoder`) each reflect filter-AND-search, not filter-only. **Mutation: leave `src` pointing at the visual filter (do not null it when `search != ""`) → all three assertions fail** (the pushed path ignores the search). Without this the entire-tier search-drop re-lands green — no other test exercises `sqlBackend` + search.
- [ ] **Step 1c:** the DTO adders regenerate bindings — see Step 4.
- [ ] **Step 2: search must reach EXPORT too** (plan review Important — else a searched export writes filter-only rows, disagreeing with the table, the dialog's "N matching rows", and the copied jq/SQL). Add `Search string json:"search"` to `ExportRequest`; `ExportQuery` compiles via `CompileFilterWithSearch(req.Filter, req.Search, …)` rather than through `CompilePlan`'s filter-only path (thread the composed `*CompiledFilter` into the plan, or add a search-aware `CompilePlan` variant). Test: a searched export contains only filter-AND-search rows (depends on Step 1b's `src=nil` so `exportPushed` cannot bypass it). `store.ts`'s `runExport` threads `currentSearch` in Task 5.
- [ ] **Steps 3-4** (implement → PASS + `go test ./... -count=1`). **Step 4b: regenerate bindings.** `QueryRequest.Search`/`CountRequest.Search`/`ExportRequest.Search` are Wails-bound request DTOs, so run `wails generate module` and commit the `models.ts` diff WITH this Go change (the Global-Constraints note that "Tasks 2/3/4 add DTOs" — Task 3 is one of them). **Step 5: Commit** — `feat(query): global search predicate combined with the filter`.

---

### Task 4: Search codegen

**Files:** Modify `internal/query/codegen.go`/`codegenjq.go`/`codegensql.go` (+ tests). Implements decision 9.

- **`CodegenRequest` gains `Search string json:"search"`** (plan review Important — without it the term never reaches `Engine.Codegen`→`CodegenContext`, so Task 5's "codegen shows the search" and its mock-last-arg assertion are unimplementable). `Engine.Codegen` sets `req.Search` into the `CodegenContext`; `jqProgram`/`sqlQuery` AND a search clause when it is non-empty. Regenerate bindings and commit the `models.ts` diff with this change (Step 3b).
- **jq form — the review pinned these against real jq 1.7.1, do not deviate:** `select(([.. | select(type=="string" or type=="number" or type=="boolean") | tostring | ascii_downcase] | any(contains(q))) and <filter>)`, where `q` is the query **lowercased at generation time** (without it `ABC` won't match `abc` even in ASCII). It MUST include number and bool leaves, not just `strings` (decision 5 matches all scalar leaves). Combined with the filter by `and` (jq `and` binds tighter than the outer `select(...)` parens — safe, unlike E5's `//` trap; verify anyway).
- **This jq form is NOT exactly equivalent to the engine, and the plan admits it rather than pretending:** (a) `ascii_downcase` folds ASCII only while the engine folds Unicode (`strings.ToLower`) — value `MÜNCHEN` vs query `münchen` diverges; (b) `tostring` on a number canonicalises it (`1e3`→`"1E+3"`) and cannot reproduce `json.Number`'s source literal, so a search for `1e3` matches the engine but not jq. Both are the SAME class of divergence E5 already discloses. So the search jq codegen MUST append `warnCaseInsensitive` (exactly as `OpContains` does, `codegenjq.go`) AND a `# note:` that numeric leaves are canonicalised by jq and may not match shape's source-literal search for exponent/odd-decimal numbers. Do NOT "fix" it by canonicalising numbers in `compileSearch` too — that would break decision 5's no-float-loss source-literal match.
- SQL: illustrative only (decision 8) — an `OR`-of-`instr(lower("col"),lower(Q))` across the real columns, with a `-- note:` that shape searches every leaf generically and this SQL only covers the top-level columns, PLUS `warnCaseInsensitive` (SQLite `lower()` is ASCII-only too, `codegensql.go`). Never executed.
- The search term is escaped exactly like any other string literal (`jqLiteral`/`sqlValueLiteral`).

- [ ] **Step 1: Write failing tests**: a golden jq program for a search (byte-for-byte, including the lowercased query and the caveat comment) AND a real-jq execution (gated on `exec.LookPath("jq")` + `t.Skip`, as E5's jq tests) over a fixture that MUST contain (i) an exponent numeric leaf where `json.Number.String()` differs from jq `tostring` (**use `1e3`** — verified `1e3`→`"1E+3"`; the test **asserts the KNOWN divergence**: the engine matches a `1e3` search, jq does not, mirroring how E5 pins the OpContains ASCII limit rather than hiding it); (ii) a bool-only match (jq must match `true`); (iii) a non-ASCII case-fold string (value `MÜNCHEN` vs query `münchen`) where again the engine matches and jq does not, **asserted as a divergence**. Over the AGREEING subset (string + integer leaves) jq and `compileSearch` select the same records. A golden SQL with the illustrative + case-insensitive caveats; search combined with a filter (both clauses present, AND-ed); an empty search adds nothing to either program. **Mutation: emit the search clause without the `and <filter>` → the combined test fails.** **Mutation: use `strings` instead of the three-type select → the bool/number test fails.**
- [ ] **Step 3b: regenerate bindings** for `CodegenRequest.Search` and commit the `models.ts` diff with the Go change. Add an **Engine-level** test that `Engine.Codegen` with non-empty `req.Search` yields jq/SQL containing the search clause (**mutation: drop the `req.Search`→ctx assignment → the test fails** — the pure-function and JS-mock tests do not otherwise guard this wiring).
- [ ] **Steps 2-4.** **Step 5: Commit** — `feat(query): jq and sql codegen for global search`.

---

### Task 5: Store — thread search, and drive GetCell

**Files:** Modify `gui/frontend/src/lib/explorer/store.ts` (+ `store.test.ts`). Implements the search lifecycle and the cell-fetch action.

**Interfaces (produces):**
`explorer.setSearch(q: string): void` (debounced like `setFilter`, and it must reset/supersede exactly like a filter change — bump `gen`, cancel in-flight, reset total, re-count on the non-memory tiers);
`ExplorerState.search: string`;
`explorer.getCell(index: number, path: string): Promise<{ value: unknown; found: boolean }>` (calls the binding; no store state beyond what the caller needs — the tree overlay owns its own open/loading/error state, the store just forwards).

- `setSearch` threads the term into `QueryRows`/`CountMatches` (the store already sends `filter`; add `search`). It reuses the SAME debounce+gen+count discipline `setFilter` established — do NOT invent a second path; ideally search and filter share one "requery" internal so the gen/count/reset logic exists once.
- **The count-reset predicate must key on "filter OR search active", NOT on filter alone** (plan review §7). `setFilter`'s reset today is `total: active ? -1 : baseTotal` / `totalExact: active ? false : baseTotalExact` (`store.ts` ~429) and it re-counts only `if (active && tier !== "memory")`. If search shares this path, `active` must mean **the filter is non-empty OR the search is non-empty**. Otherwise clearing the FILTER while a SEARCH is still live restores `baseTotal` (the whole-file count) and skips the re-count — the status bar would read "~726,181 rows" while the table shows only the search hits. Symmetrically, clearing the SEARCH while a filter is live must keep the filtered count, not baseTotal. Only when BOTH are empty does the total return to `baseTotal`/`baseTotalExact` and the re-count get skipped. (This is the store analogue of decision 6: empty-BOTH is the true no-op, not empty-either.)
- Refresh codegen on a search change too (E5's `refreshCodegen`), since search is part of the query.
- `getCell` is a thin async wrapper; a failed fetch rejects and the caller shows an error — it does not wipe table state.

- [ ] **Step 1: Write failing tests** (extend `store.test.ts`, mock the bridge incl. `GetCell`): `setSearch("x")` sends `search:"x"` on the next `QueryRows`; a non-memory tier re-counts on search change; `setSearch("")` when no filter is active restores the unsearched `baseTotal` (like clearing a filter); **clearing the filter while a search is still active does NOT restore `baseTotal` and DOES re-count on a non-memory tier (mutation: reset/skip on `filterActive` alone → total snaps back to the whole-file count and the assertion fails);** search change refreshes codegen (assert the `Codegen` mock's last arg carries the search); the gen guard supersedes a stale searched page exactly as the filter test does (**mutation: don't bump gen in setSearch → a stale page lands**); `getCell` forwards index+path and returns the binding's result.
- [ ] **Steps 2-4** + `npm run check`. **Step 5: Commit** — `feat(gui): thread global search through the store and add getCell`.

---

### Task 6: The value tree component

**Files:** Create `gui/frontend/src/lib/explorer/ValueTree.svelte`, `ValueTree.test.ts`, and a small pure `valueTree.ts` (+ test) if the value→node shaping is non-trivial. Implements spec §3.3 and decision 10.

- Recursive expand/collapse over an arbitrary JSON value (object/array/scalar), via `<svelte:self>` like `TreeNode.svelte`. Objects show `key: value`, arrays show `[i]: value`, scalars render with the same kind coloring `CellView` uses (reuse `kindToken.ts`). Deep values collapse by default past the first level or two; a large array shows a count and lazy-expands.
- A **Copy** button copies the value's JSON (`ClipboardSetText`).
- It renders a value it is GIVEN (from `getCell`); it does not fetch. It handles `found:false` (show "no value"), a scalar (no expand affordance), and a huge value (cap rendered children with a "N more" note — never freeze the webview on a 100k-element array).

- [ ] **Step 1: Build it**, then `ValueTree.test.ts` (jsdom): an object renders its keys; expanding a nested object reveals its children; a scalar renders without an expander; Copy calls `ClipboardSetText` with the exact JSON (**mutation: copy a `.toString()`'d value → the assertion on exact JSON fails**); a large array renders a capped set plus a "more" note (**mutation: render all elements → the count assertion fails**); `found:false` shows the empty state.
- [ ] **Step 2: BUILD.** `wails build`; the tree is not mounted until Task 7, so the live drive defers there. `npm run check` 0 errors is the hard gate.
- [ ] **Step 3: Commit** — `feat(gui): recursive value tree component`.

---

### Task 7: Wire it all in — cell click, search box, shell

**Files:** Modify `gui/frontend/src/lib/explorer/DataTable.svelte` (a container cell dispatches an expand event with its index+path), `Explorer.svelte` (own the tree overlay, call `getCell`, mount `ValueTree`), `FilterBar.svelte` (the search box), `CellView.svelte` (a cursor/affordance on expandable cells), and their tests.

- **Cell click:** a container cell (`object`/`array`) gets an "expand" affordance; clicking it dispatches `expandCell {index, path}` (index from `Row.Index`, path from the column). `Explorer` handles it: `getCell` → open the `ValueTree` overlay with the value. NOTE (plan review): `DataTable` today dispatches ONLY `focus`, and ONLY from the header `<button>` — data cells have no click handler and there is no row/cell seed in the table (seeding lives in `StructureMap`). So there is no competing handler for a cell click to bubble into; a `stopPropagation` guard is only needed IF this task also introduces a competing cell/row handler (it does not). Do not write a "does not also fire focus" test — it cannot fail.
- **Search box: rendered INDEPENDENT of `filterOpen`** (plan review Important). `FilterBar` gates its whole body behind `{#if open}` (`FilterBar.svelte`), which is false by default — a search box inside it is invisible until the user opens the visual filter, i.e. a global search you cannot see. Either hoist the search box into its own always-on slim bar in Explorer's `ready` branch, OR restructure `FilterBar` so the search input renders OUTSIDE `{#if open}` while the condition builder stays gated. `on:input` → debounced `explorer.setSearch`, clearable, a magnifier affordance. Its debounce needs its OWN `onDestroy` cancel (a second one alongside `FilterBar`'s existing `debouncedApply` teardown — progress.md records this exact stale-debounce-fires-against-file-B recurrence across E3/E4).
- **Empty state:** add a `searchActive`-aware empty state so "No rows match your search" is distinct from "No rows in this file" — `Explorer.svelte` currently branches only on `filterActive`.
- **Overlay:** a focus-trapped `role="dialog"` like E4's ExportDialog (reuse that pattern), Esc/backdrop to close, showing the `ValueTree` + Copy. Loading and error states while `getCell` is in flight.

- [ ] **Step 1: Build it**, then tests: `DataTable.test.ts` — clicking a container cell's expand affordance dispatches `expandCell` exactly once with `{index: row.index, path: col.path}` (**mutation: omit the dispatch, or swap index/path → the test fails**; do NOT assert "does not fire focus" — no such path exists); `Explorer.test.ts` — an `expandCell` event calls `getCell` and mounts the tree with its value; `FilterBar.test.ts` (or the search bar's own test) — the search input IS present in the DOM with `open={false}` (**a real gate: fails if the box is hidden behind the filter toggle**), typing calls `setSearch` after the debounce with the text, clearing calls `setSearch("")`, and unmounting after arming the debounce does NOT fire a stale `setSearch` (**mutation: drop the search debounce's `onDestroy` cancel → the stale search applies after unmount**).
- [ ] **Step 2: BUILD + RUN.** `wails build`, `git checkout -- gui/frontend/dist/.gitkeep`; open `gui/testdata/nested.ndjson`, click a nested object cell → the tree opens showing the full value; type a value in the search box → rows narrow, count updates, Code panel shows the search. **Look at it.**
- [ ] **Step 3: Commit** — `feat(gui): cell value tree overlay and global search box`.

---

### Task 8: Theme-correctness of the new surfaces

**Files:** Modify the E6 components' styles as needed; a jsdom or visual check. Implements the engineering half of the spec's "both themes" E6 line (decision 11).

- The value tree, the search box, and the overlay must read correctly in BOTH light and dark, using the existing `app.css` tokens (never a hardcoded color) exactly as the E4/E5 panels do. No new theme machinery — just token discipline.
- [ ] **Step 1:** Audit every color/background/border in the new components: each is a `var(--…)` token, none is a literal hex/rgb. Build and view in both themes (toggle via the header). **Look at both.**
- [ ] **Step 2: Commit** — `style(gui): theme-correct the tree and search surfaces` (only if changes were needed; otherwise fold the audit note into Task 7).

---

### Task 9: Full-stack verification, docs, and the launch handoff

**Files:** Modify `gui/README.md`, `README.md`. No source changes unless verification finds a defect — if it does, fix it as its own `fix(...)` commit and say so.

- [ ] **Step 1: Gates.** `npm run check` 0 errors; `npm run test` green (state the count; it must exceed E5's 305); `go test ./... -count=1` 16 packages; gofmt clean; `go.mod`/`go.sum` unchanged; no `dependencies` block; `wails build` succeeds then `git checkout -- gui/frontend/dist/.gitkeep`; `wails generate module` diff empty after the binding commits.
- [ ] **Step 2: The checks jsdom cannot do.** On a large rescan-tier NDJSON: search for a value known to be deep in a nested object and confirm the count matches a manual grep; `GetCell` on a row far into the file returns the right value (record N is reached without loading the whole file — watch RSS). Re-open a value's copied JSON to confirm it is complete. Execute the generated search jq/SQL if `jq`/`sqlite3` are available.
- [ ] **Step 3: The launch handoff (NOT done here — explicitly a human task).** The spec's E6 "launch" items — a README GIF, screenshots, a recorded demo — require driving the GUI interactively and recording it, which cannot be automated from this environment. Document precisely what to capture (the wow: drag a messy file → browse → click-to-expand a nested value → search → export with the jq/SQL shown) and where it goes (README top), so a human can produce them in one sitting. Do not fake them.
- [ ] **Step 4: Commit** — `docs: document the value tree and global search`.

---

## Self-Review

**Coverage (E6 from product spec §3.3/§3.4 + §5/§8):** `Backend.GetCell` full-value access (T1) · `Engine.GetCell` + binding (T2) · the global-search predicate combined with the filter and the caches (T3) · search codegen (T4) · store threading + `getCell` (T5) · the value tree component (T6) · cell-click + search-box + overlay wiring (T7) · theme-correctness (T8) · verification + docs + the launch handoff (T9).

**Explicitly NOT in this plan, with owners:** derive/compute, unnest arrays, group/aggregate transforms (product spec §3.5 "later") · editing a value from the tree (decision 10 — read-only) · pushing search to SQLite (decision 8) · `Unflatten` on export (E4 decision 14, still deferred) · the README GIF / screenshots / recorded demo (T9 Step 3 — a human task, this environment cannot drive+record the GUI) · a determinate GetCell progress bar (a single-record fetch needs none).

**Correctness checks (each mutation-proven):** GetCell returns the full value, not a preview (T1) · GetCell's `found` flag distinguishes missing from null (T1) · an unsearched/non-GetCell path is byte-identical to today (T1/T3) · search matches leaves not keys, case-insensitively, consistent with the filter's contains (T3) · an empty search is a true no-op (T3) · two searches never share a cached count (T3, mutation drops search from the canonical key) · the generated search jq selects exactly what `compileSearch` does, proven by real jq (T4) · search threads through the store's gen/count/reset discipline like a filter (T5) · Copy puts the exact JSON on the clipboard (T6) · a huge value does not render unbounded (T6) · the expand click dispatches `expandCell` with the right index+path (T7) · the search box renders with `open={false}` — not hidden behind the filter toggle (T7) · the search debounce cancels on unmount (T7).

**The risk this plan is built around:** search must not become a second, divergent filter path. Decisions 4-7 collapse it INTO the existing `CompiledFilter`/cache/count machinery — one predicate, AND-ed, keyed together — so there is no separate code path to keep consistent, and the "empty search == no search" invariant makes it free for everyone who does not use it.
