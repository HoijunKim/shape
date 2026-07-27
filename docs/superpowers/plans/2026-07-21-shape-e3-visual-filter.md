# E3: Visual Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. Every task is TDD for its pure logic; the Svelte-component tasks add a jsdom wiring test AND a `wails build` + screenshot check, not only a unit test.

**Goal:** Filter the open data file without jq - a flat AND/OR condition builder with type-aware operators, seeded by clicking a field in the structure map, showing live filtered rows plus a cancellable exact count.

**Architecture:** E3 is **frontend-only** - the `internal/query` engine already compiles and applies `QueryRequest.Filter` (E1) and already exposes a cancellable `CountMatches` (E2), and the generated Wails bindings + `models.ts` Filter AST already match `filter.go` byte-for-byte. The work is: three pure `.ts` modules (the operator vocabulary, the Filter-building/coercion logic, a debounce helper), a store extension that threads a live `Filter` into `QueryRows` and drives a superseded-safe `CountMatches`, and a Svelte filter bar mounted between the table and the status bar. No Go changes, no new runtime dependency.

**Tech Stack:** Svelte 3.49 + Vite 3 + TypeScript 4.6, vitest 0.34 + jsdom 29 (both existing devDependencies). Zero runtime dependencies. The engine's `Filter`/`Condition`/`Value` DTOs and the `QueryRows`/`CountMatches`/`Cancel` bindings are consumed as-is.

**AUTHORITATIVE SOURCES:** product spec `docs/superpowers/specs/2026-07-17-shape-data-explorer-design.md` (§3.4 the condition-builder feature, §5 the E3 phase line); engine spec `docs/superpowers/specs/2026-07-17-shape-engine-design.md` (§5 the filter semantics table - the UI must build only conditions that mean what the user sees). The Filter AST lives in `internal/query/filter.go:13-78`; do not change it.

**Decisions locked before this plan (do not relitigate):**
- **Flat AND/OR only.** One top-level `combinator` (`"and"`/`"or"`) over a flat list of conditions. No nested `Groups`, no per-group `Negate` in the UI. The engine supports both; E3 emits neither, so `Filter.groups`/`Filter.negate` are always absent. Nested groups are a later add with zero engine/DTO change.
- **Global search is E6, not E3.** The product-spec §3.4 sentence bundles "a global search box" into the filter feature, but the §5 E3 phase line omits it and §6 (E6) explicitly owns "global search". **E3 ships zero free-text/global search** - every condition names a column.
- **The count affordance is indeterminate.** No `shape:progress` event exists in the Go code (it is E4-scoped), so there is no "scanned X of ~N" data. E3 shows a `counting…` label plus a Cancel button that calls `Cancel(countReqId)`. Do not invent a progress bar.
- **Click-to-seed from the sidebar is in scope.** Clicking a small filter affordance on a structure-map row adds one condition for that column, pre-seeded with a type-appropriate default operator, and moves keyboard focus to the value input. The existing field-click (scroll-the-column-into-view + focus) is unchanged; the seed is a distinct affordance, not an overload of the `focus` event.

## Global Constraints

- **Frontend-only. Zero Go changes.** Do not touch `internal/query/`, `gui/*.go`, or anything under `gui/frontend/wailsjs/**` (generator output). If a task seems to need an engine change, it is wrong - stop and report. (Recon: engine is E3-ready.)
- **Zero runtime dependencies.** Nothing may enter a `dependencies` block in `gui/frontend/package.json` (there is none today). Hand-roll everything; no combobox/select/debounce library. (Spec §4 reuse mandate; E2 posture.)
- **Svelte 3.49 only** - `export let`, `createEventDispatcher`, `$:`, `$explorer`. No runes, no Svelte 4/5 syntax.
- **Build only VALID conditions** (engine spec §5, `filter.go:287-410`): `isnull`/`notnull` carry NO value; `contains`/`regex` read `Value.Str` only; `in` reads `Value.List` (each element Kind-tagged); `bool` reads `Value.Bool` only; `eq`/`ne`/`lt`/`lte`/`gt`/`gte` need `Value.Kind` set to `"string"`/`"number"`/`"bool"` AND the matching one of `str`/`num`/`bool`. A wrong `Kind` yields zero matches, not an error. **A malformed regex makes `CompileFilter` ERROR and rejects the whole `QueryRows`/`CountMatches` request** - so a regex condition must be validated before it is sent. An incomplete condition (empty value where a value is required) must be OMITTED from the emitted Filter, never sent half-built.
- **Filter changes must be generation-guarded.** The store's supersede guard checks `myGen !== gen` (`store.ts:148,193`); `gen` bumps today only in `open()`/`close()`. A filter change that clears the cache without bumping `gen` lets an in-flight OLD-filter `QueryRows` land in the NEW filter's page slot → wrong rows. `setFilter` MUST bump `gen`. (Recon GAP 2 - the single most important correctness item.)
- **Numbers still render `Cell.Str`, never `Cell.Num`** and every render constraint E2 pinned still holds - E3 changes the query, not the cell renderer.
- **Extract pure logic into `.ts` modules beside the components** (repo convention: `widths.ts`, `paging.ts`, `rowCount.ts` each with a `.test.ts`). The operator vocabulary, Filter construction/coercion, and debounce are pure `.ts` unit-tested directly; the `.svelte` files stay thin and are tested in jsdom for wiring only.
- **Every new test must fail if the logic it covers regresses.** For every concurrency, supersession, or omission test - and any test whose assertion is not a direct one-to-one check of the value under test - state the exact mutation that breaks it, and confirm that mutation actually kills the test (not a redundant guard elsewhere). Direct-assertion pure-logic tests (e.g. `operatorsForType("int")` returns a fixed list) are self-proving and need no mutation annotation. (This repo shipped tests that could not fail in six of nine E2 tasks; the mutation proof is the gate for exactly the tests most prone to that failure.)
- **Commits: Conventional Commits, lowercase imperative subject, NO co-author trailer.** This overrides Claude Code's default trailer.
- Gates every task ends on: `cd gui/frontend && npm run check` (**0 errors**) and `npm run test` (green). Component tasks additionally `cd gui && wails build` and drive the real binary.

---

### Task 1: Type re-exports and the operator vocabulary

**Files:** Modify `gui/frontend/src/lib/explorer/types.ts` (re-export `Filter`/`Condition`/`Value`/`CountRequest`). Create `gui/frontend/src/lib/explorer/operators.ts`, `gui/frontend/src/lib/explorer/operators.test.ts`. Implements the type→operator mapping (engine spec §5 semantics table + `filter.go:15-28`).

**Interfaces (produces):**
`type OpId = "eq"|"ne"|"lt"|"lte"|"gt"|"gte"|"contains"|"regex"|"in"|"isnull"|"notnull"|"bool"`;
`type ValueArity = "none" | "text" | "number" | "bool" | "list"`;
`interface OpSpec { id: OpId; label: string; arity: ValueArity; ci: boolean }`;
`function operatorsForType(colType: string): OpSpec[]`;
`function defaultOpForType(colType: string): OpId`;
`const OP_LABELS: Record<OpId, string>`.

**Why a vocabulary module:** which operators a column offers, and what value shape each needs, is E3-owned (in no spec verbatim) and is the thing most likely to silently emit an invalid or zero-matching condition. Isolating it in a pure module makes every rule a direct unit test.

The type→operator mapping (from `Column.type` ∈ `int|float|string|bool|object|array|null|"mixed"`, `columns.go:573-587`):
- **numeric** (`int`, `float`): `eq, ne, lt, lte, gt, gte, in, isnull, notnull` - all value-bearing ops use `arity:"number"` except `in` (`"list"`) and `isnull`/`notnull` (`"none"`).
- **string**: `eq, ne, contains, regex, in, isnull, notnull` - `eq`/`ne`/`contains`/`regex` are `arity:"text"` and `ci:true` (case-insensitive toggle offered); `in` is `"list"`; `isnull`/`notnull` are `"none"`.
- **bool**: `bool, isnull, notnull` - `bool` is `arity:"bool"`; the rest `"none"`.
- **container / mixed** (`object`, `array`, `null`, `mixed`): `isnull, notnull` only - comparison ops skip non-scalar/null values (`filter.go:344-368`), so nothing else is meaningful.

`defaultOpForType`: numeric → `"gte"`, string → `"contains"`, bool → `"bool"`, container/mixed → `"isnull"`.

- [ ] **Step 1: Write failing tests** in `operators.test.ts` (header `// @vitest-environment jsdom` is unnecessary here - pure logic - omit it) covering:
  - `operatorsForType("int")` and `("float")` each return exactly the 9 numeric op ids in the order above; `in` has `arity:"list"`, `gte` has `arity:"number"`, `isnull` has `arity:"none"`.
  - `operatorsForType("string")` returns the 7 string op ids; `contains` has `arity:"text"` and `ci:true`; `regex` has `ci:true`; `isnull` has `ci:false`.
  - `operatorsForType("bool")` returns exactly `["bool","isnull","notnull"]`; `bool` has `arity:"bool"`.
  - `operatorsForType("object")`, `("array")`, `("mixed")`, `("null")` each return exactly `["isnull","notnull"]`.
  - `operatorsForType("wat")` (unknown) falls back to `["isnull","notnull"]` (safe: only null-ops are always valid).
  - `defaultOpForType`: `"int"`→`"gte"`, `"string"`→`"contains"`, `"bool"`→`"bool"`, `"object"`→`"isnull"`.
  - `OP_LABELS` has an entry for all 12 `OpId`s (assert `Object.keys(OP_LABELS).length === 12`) - a compile-time-adjacent guard so a new op cannot ship label-less.
  - Every `OpSpec.id` returned by `operatorsForType` over all input types is a key of `OP_LABELS` (no orphan op).
- [ ] **Step 2: Run - FAIL** (`cd gui/frontend && npm run test -- operators`).
- [ ] **Step 3: Implement** `operators.ts`. Build one internal `const OPS: Record<OpId, OpSpec>` as the single source, then `operatorsForType` returns ordered slices of its ids per the mapping above; `OP_LABELS` derives from `OPS`. Human labels: `eq`→"=", `ne`→"≠", `lt`→"<", `lte`→"≤", `gt`→">", `gte`→"≥", `contains`→"contains", `regex`→"matches regex", `in`→"in list", `isnull`→"is null", `notnull`→"is not null", `bool`→"is".
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(gui): filter operator vocabulary and type re-exports`.

Also in this task, add to `types.ts` after the existing re-exports:
```ts
export type Filter = query.Filter;
export type Condition = query.Condition;
export type Value = query.Value;
export type CountRequest = query.CountRequest;
```
(These already exist in `wailsjs/go/models.ts` namespace `query` - a re-export only, matching how `CountResult` is already re-exported at `types.ts:16`.)

---

### Task 2: The filter model - a UI draft that compiles to a valid engine Filter

**Files:** Create `gui/frontend/src/lib/explorer/filterModel.ts`, `gui/frontend/src/lib/explorer/filterModel.test.ts`. Implements the engine spec §5 per-op value rules and the empty/invalid-condition omission rule.

**Interfaces (produces):**
`interface DraftCondition { id: number; path: string; type: string; op: OpId; text: string; num: string; bool: boolean; list: string[]; ci: boolean }`;
`interface FilterDraft { combinator: "and" | "or"; conditions: DraftCondition[] }`;
`function emptyDraft(): FilterDraft`;
`function newCondition(id: number, path: string, colType: string): DraftCondition`;
`function conditionError(c: DraftCondition): string`  (returns `""` when valid or when the condition is empty-and-omittable);
`function isConditionComplete(c: DraftCondition): boolean`;
`function buildFilter(draft: FilterDraft): Filter`  (omits incomplete conditions; returns the match-all empty Filter `{combinator:"and"}` when nothing complete remains).

**Consumes:** Task 1's `OpId`, `operatorsForType`, `defaultOpForType`; `Filter`/`Condition`/`Value` from `types.ts`.

**Why a draft layer separate from the engine `Filter`:** the UI holds strings (a numeric input is a string mid-typing; a regex is a string that is invalid mid-typing), one editable row carries fields for every arity so switching operators keeps context, and rows need stable ids for Svelte `{#each}` keying. `buildFilter` is the single coercion point that turns that editable draft into exactly the JSON the engine accepts, dropping anything not yet complete. Keeping it pure makes every §5 value rule a direct assertion.

Coercion rules `buildFilter`/`newCondition` must implement (each traced to `filter.go`):
- `newCondition(id, path, colType)`: `op = defaultOpForType(colType)`, `text:"" num:"" bool:false list:[] ci:false`, `type: colType`.
- `isConditionComplete`: `none`-arity ops (`isnull`/`notnull`) are always complete; `text` ops need `text !== ""`; `number` ops need `num` parse to a finite number; `bool` ops are always complete (`false` is a real value); `list` ops need at least one non-empty entry.
- `conditionError`: only `regex` can be genuinely invalid - return a message when `op==="regex"` and `new RegExp(text)` throws (guard the `RegExp` construction in try/catch; RE2 vs JS regex differ but JS `RegExp` catches the common syntax errors that would also fail Go's `regexp.Compile`, which is the point - reject before firing). For a `number` op whose `num` is non-empty but not finite, return "not a number". Everything else → `""`.
- `buildFilter` per op, for each COMPLETE condition:
  - `isnull`/`notnull` → `{path, op}` (no `value`, no `ci`).
  - `contains`/`regex` → `{path, op, value:{kind:"string", str:text}, ...(ci?{ci:true}:{})}`.
  - string `eq`/`ne` → `{path, op, value:{kind:"string", str:text}, ...(ci?{ci:true}:{})}`.
  - numeric `eq`/`ne`/`lt`/`lte`/`gt`/`gte` → `{path, op, value:{kind:"number", num:Number(num)}}`.
  - `in` → `{path, op, value:{kind:"string", list: list.filter(x=>x!=="").map(x => coerce(x))}}` where `coerce` tags each element by the column type: numeric column → `{kind:"number", num:Number(x)}`, else `{kind:"string", str:x}`. (Outer `Value.Kind` is ignored by the engine for `in`, `filter.go:319-338`; set `"string"` harmlessly.)
  - `bool` → `{path, op, value:{kind:"bool", bool}}`.
  - Set `Filter.combinator` from the draft; **omit `groups` and `negate` entirely** (flat-only). Set `combinator` explicitly even for a single condition (empty string would default to AND, but be explicit - Global Constraints).

- [ ] **Step 1: Write failing tests** in `filterModel.test.ts` covering:
  - `emptyDraft()` → `{combinator:"and", conditions:[]}`; `buildFilter(emptyDraft())` → `{combinator:"and"}` (match-all, no conditions key or empty - assert `buildFilter(emptyDraft()).conditions === undefined || length 0`).
  - `newCondition(1,"age","int").op === "gte"`; `newCondition(2,"name","string").op === "contains"`.
  - `isConditionComplete`: an `isnull` condition with empty everything is complete; a `contains` with `text:""` is NOT; a `gte` with `num:""` is NOT, with `num:"18"` IS; a `gte` with `num:"abc"` is NOT; a `bool` op is complete regardless of `bool`; an `in` with `list:["",""]` is NOT, with `list:["a"]` IS.
  - `buildFilter` omits incomplete conditions: a draft with one complete `notnull` and one empty `contains` yields a Filter with exactly one condition (the `notnull`).
  - `buildFilter` numeric `gte` → `{path:"age", op:"gte", value:{kind:"number", num:18}}` (assert `num` is the JS number 18, not the string).
  - `buildFilter` string `contains` with `ci:true` → `value:{kind:"string", str:"foo"}` AND `ci:true`; with `ci:false` → NO `ci` key present.
  - `buildFilter` `isnull` → `{path, op:"isnull"}` with NO `value` key.
  - `buildFilter` `in` on a numeric column with `list:["1","2"]` → `value.list` = `[{kind:"number",num:1},{kind:"number",num:2}]` (each element Kind-tagged `"number"`, per `filter.go:319-338`). Also assert empty list entries are dropped: `list:["a","",""]` on a string column → `value.list` = `[{kind:"string",str:"a"}]`.
  - `buildFilter` `bool` → `{path, op:"bool", value:{kind:"bool", bool:false}}` when `bool:false` (assert the value IS emitted - `false` is a real operand).
  - `conditionError`: `regex` with `text:"("` (unbalanced) returns a non-empty string; `regex` with `text:"^a.*"` returns `""`; `gte` with `num:"abc"` returns "not a number"; `contains` with `text:""` returns `""` (empty is omittable, not an error).
  - `buildFilter` sets `combinator:"or"` when the draft's is `"or"`, and never sets `groups` or `negate` (assert both keys absent).
- [ ] **Step 2: Run - FAIL** (`npm run test -- filterModel`).
- [ ] **Step 3: Implement** `filterModel.ts` per the rules above.
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(gui): filter draft model and engine-Filter coercion`.

---

### Task 3: A debounce helper

**Files:** Create `gui/frontend/src/lib/explorer/debounce.ts`, `gui/frontend/src/lib/explorer/debounce.test.ts`.

**Interfaces (produces):** `function debounce<A extends any[]>(fn: (...a: A) => void, ms: number): { call: (...a: A) => void; flush: () => void; cancel: () => void }`.

**Why its own module + a fake-timer test:** no debounce exists anywhere in the frontend today, and E3 needs it in two places (page-0 refetch and the heavier count). A pure module with vitest fake timers pins the timing behavior without a component.

- [ ] **Step 1: Write failing tests** in `debounce.test.ts` using `vi.useFakeTimers()` covering:
  - Calling `.call(1)` then `.call(2)` within `ms` fires `fn` once with `2` after `ms` (assert `fn` not called before `vi.advanceTimersByTime(ms-1)`, called once with `[2]` after the full `ms`).
  - `.cancel()` before the timer elapses fires `fn` zero times.
  - `.flush()` fires `fn` immediately with the latest args and clears the pending timer (a subsequent `advanceTimers` does not fire again).
  - A `.call` after a completed fire schedules a fresh fire (not swallowed).
- [ ] **Step 2: Run - FAIL** (`npm run test -- debounce`).
- [ ] **Step 3: Implement** with a single `setTimeout` handle, latest-args capture, `clearTimeout` on cancel/flush/re-call.
- [ ] **Step 4: Run - PASS.**
- [ ] **Step 5: Commit** - `feat(gui): debounce helper`.

---

### Task 4: Store - thread a live filter into QueryRows and reset on change

**Files:** Modify `gui/frontend/src/lib/explorer/store.ts`. Create/extend `gui/frontend/src/lib/explorer/store.test.ts` (there is an existing `store.test.ts` from E2 - add to it). Implements recon GAPs 2/3/9.

**Interfaces (produces):**
`explorer.setFilter(f: Filter): void` (new action);
`ExplorerState.filterActive: boolean` (new field - true when a non-empty filter is applied, so the UI/empty-state can distinguish "no rows in file" from "no rows match filter");
`ExplorerState.resetToken: number` (new field - bumped on filter change so `DataTable` scrolls back to row 0).

**Consumes:** Task 2's `Filter` type; Task 2's notion of an empty (match-all) Filter.

**Why:** today `ensurePages` sends `filter: {} as any` (`store.ts:145`) - a hardcoded match-all. E3 makes the store hold a current filter and apply it. The three correctness must-dos from recon: (2) bump `gen` on filter change so an in-flight old-filter page can't land in the new filter's cache slot; (3) reset `total:-1, totalExact:false` on filter change so the stale UNFILTERED total isn't shown as the filtered count (on memory tier the exact filtered total returns on page 0 anyway; on rescan/sqlite/parquet it stays unknown until CountMatches, Task 5); (9) the store cannot move the viewport - `DataTable` owns scroll - so a `resetToken` signals it.

Changes:
- Add a module-scoped `let currentFilter: Filter = { combinator: "and" };` beside `gen` (`store.ts:51`).
- `ExplorerState` gains `filterActive: boolean` and `resetToken: number`; `empty` sets both to `false`/`0`.
- `open()` and `close()` reset `currentFilter = { combinator: "and" }` (so a new file starts unfiltered) - add to the `cache.clear()` blocks (`store.ts:63`, `:237`).
- `ensurePages`'s `QueryRows` call (`store.ts:145`) sends `filter: currentFilter` instead of `{} as any`.
- New `setFilter(f: Filter)`:
  ```ts
  function setFilter(f: Filter): void {
    currentFilter = f;
    const active = !!(f.conditions && f.conditions.length > 0);
    // Bump gen so any in-flight OLD-filter QueryRows is superseded and cannot
    // cache.set() into the NEW filter's page slot (recon GAP 2). Cancel and
    // clear inflight the same way a superseding scroll does, and clear the
    // page cache so no old-filter rows survive.
    ++gen;
    for (const [, reqId] of inflight) { void Cancel(reqId).catch(() => {}); }
    inflight.clear();
    cache.clear();
    update((s) => ({
      ...s,
      filterActive: active,
      // Reset the total so the stale unfiltered count is not shown as the
      // filtered count (recon GAP 3). On the memory tier page 0 immediately
      // re-fills it exactly; on other tiers it stays -1 (=> "counting...")
      // until Task 5's CountMatches finalizes it.
      total: -1,
      totalExact: false,
      version: 0,
      resetToken: s.resetToken + 1, // DataTable scrolls to row 0 (recon GAP 9)
      pageError: "",
    }));
    void ensurePages(0, 0);
  }
  ```
  Note `ensurePages` captures `myGen = gen` at its top (`store.ts:117`) AFTER the `++gen` above, so its own fetches use the new generation and the old ones (which captured the prior `gen`) are correctly rejected by the `myGen !== gen` guard.
- Return `setFilter` from the action set (`store.ts:241`).
- **`version: 0` reset caveat:** the empty-state check in `Explorer.svelte:106` is `total === 0 && (totalExact || version > 0)`. Resetting `version:0` and `total:-1` means that check is false during the refetch, so no false "empty" flashes; when page 0 lands it bumps `version` and reconciles `total`. Good - but `filterActive` must let a genuinely-empty filtered result read "No rows match filter" rather than "No rows in this file" (wired in Task 6).

- [ ] **Step 1: Write failing tests** in `store.test.ts` (mock the Wails bridge as the existing tests do - `vi.mock("../../../wailsjs/go/main/App", ...)` with `QueryRows`/`Cancel`/`CloseSource`/`OpenSource`; add `CountMatches` to the mock now so Task 5 needs no re-mock) covering:
  - After `open()` lands (mock `OpenSource` → a memory-tier `OpenResult`, `QueryRows` → a `RowSet` with `total:100, totalExact:true`), calling `setFilter({combinator:"and", conditions:[{path:"age", op:"gte", value:{kind:"number", num:18}}]})` calls `QueryRows` again with `filter` deep-equal to that filter (assert the mock's last call's `filter`, not `{}`).
  - `setFilter` bumps `resetToken` (read via `get(explorer)`).
  - `setFilter` with a non-empty filter sets `filterActive:true`; `setFilter({combinator:"and"})` (no conditions) sets `filterActive:false`.
  - **The GAP-2 regression test:** simulate an in-flight old-filter page - make `QueryRows` return a promise you resolve manually; call `ensurePages(0,0)` (starts page-0 fetch under gen G), then `setFilter(...)` (bumps to gen G+1 and starts a new fetch), then resolve the FIRST (old) promise with a distinctive RowSet; assert the cache/state did NOT adopt the old RowSet (its rows never become visible via `rowAt`). **Mutation that must break it: remove BOTH `++gen` AND `inflight.clear()` from `setFilter`.** Either guard alone still supersedes the stale page - `++gen` via the `myGen !== gen` clause (`store.ts:148`), and `inflight.clear()` + the fresh monotonic reqId via the `inflight.get(page) !== reqId` clause (because `setFilter` synchronously calls `ensurePages`, which registers `inflight.set(0, newReqId)` before the test resolves the old page-0 promise). Only removing both lets the old page land in the new filter's slot and fail the assertion. `++gen` is retained regardless for defense-in-depth and cross-file consistency with `open()`/`close()`.
  - `setFilter` resets `total` to `-1` and `totalExact` to `false` in the store state synchronously (before any refetch resolves).
  - `open()` resets `currentFilter` to match-all: after a filter is set, `open()` a new file and assert the next `QueryRows` sends an empty `{combinator:"and"}` filter.
- [ ] **Step 2: Run - FAIL** (`npm run test -- store`).
- [ ] **Step 3: Implement** per above.
- [ ] **Step 4: Run - PASS + `npm run check`.**
- [ ] **Step 5: Commit** - `feat(gui): thread a live filter through the store`.

---

### Task 5: Store - cancellable live filtered count via CountMatches

**Files:** Modify `gui/frontend/src/lib/explorer/store.ts`, `gui/frontend/src/lib/explorer/store.test.ts`. Implements recon GAPs 1/6 (the counting affordance) - the exact filtered count on the tiers where `QueryRows(wantTotal:false)` cannot give one.

**Interfaces (produces):**
`ExplorerState.counting: boolean` (a CountMatches request is in flight);
`ExplorerState.matchCount: number` (-1 = unknown; the exact filtered count when known);
`ExplorerState.matchExact: boolean`;
`explorer.cancelCount(): void` (the Cancel button behind the `counting…` affordance).

**Consumes:** Task 4's `setFilter` and `currentFilter`; `CountMatches`/`Cancel` bindings; `CountRequest`/`CountResult` from `types.ts`.

**Why:** on `rescan`/`sqlite`/`parquet`, a filtered `QueryRows(wantTotal:false)` returns `Total:-1` and `reconcileEof` can only derive the count by scrolling to the filtered tail. `CountMatches` is the eager, exact, cancellable finalizer (it runs a full residual scan and returns `exact:true` on all four tiers - `sqlbackend.go:497` residual, `memstore.go:190`, etc.). On the **memory tier**, `QueryRows` already returns the exact filtered `Total` on page 0, so E3 SKIPS `CountMatches` there (it would be a redundant full re-scan). The count is superseded-safe with its own request id: a slow count for filter A must never overwrite filter B's count.

Changes:
- Module-scoped `let countReqId: string | null = null;`.
- `ExplorerState` gains `counting:boolean`, `matchCount:number` (init `-1`), `matchExact:boolean`; `empty` sets `false`/`-1`/`false`.
- New internal `startCount(handle: string, filter: Filter, genAtStart: number)` (the memory-tier skip is decided by the caller in `setFilter`, not inside `startCount`):
  ```ts
  async function startCount(handle: string, filter: Filter, genAtStart: number): Promise<void> {
    const reqId = `c${++seq}`;
    countReqId = reqId;
    update((s) => ({ ...s, counting: true }));
    try {
      const res: CountResult = await CountMatches({ requestId: reqId, handle, filter } as any);
      if (countReqId !== reqId || genAtStart !== gen) return; // superseded by a newer filter
      update((s) => ({ ...s, matchCount: res.total, matchExact: res.exact, counting: false,
                              total: res.total, totalExact: res.exact }));
    } catch (e) {
      if (countReqId !== reqId || genAtStart !== gen) return;
      // A cancelled or failed count is not a page error; just stop counting.
      update((s) => ({ ...s, counting: false }));
    } finally {
      if (countReqId === reqId) countReqId = null;
    }
  }
  ```
- `cancelCount()`: `if (countReqId) { void Cancel(countReqId).catch(()=>{}); countReqId = null; } update(s => ({...s, counting:false}));`.
- In `setFilter` (Task 4): after bumping `gen` and clearing, cancel any prior count AND null its id (`if (countReqId) { void Cancel(countReqId).catch(()=>{}); countReqId = null; }` - matching how `open()`/`close()` null it; do NOT leave `countReqId` set, or a cleared-then-late-resolving count is rejected only by the `genAtStart` half of the guard), reset `matchCount:-1, matchExact:false, counting:false` in the same `update`, and - **only when the active filter is non-empty AND `s.tier !== "memory"`** - call `void startCount(s.handle, f, gen)`. On memory tier or an empty filter, do not count (page 0 or the unfiltered seed already has it).
- `open()`/`close()` reset `countReqId = null`.
- Return `cancelCount` from the action set.

- [ ] **Step 1: Write failing tests** in `store.test.ts` covering:
  - On a **rescan-tier** handle (mock `OpenResult.tier === "rescan"`), `setFilter(nonEmpty)` calls `CountMatches` once with the filter; when it resolves `{total:42, exact:true}`, state has `matchCount:42, matchExact:true, counting:false, total:42, totalExact:true`.
  - On a **memory-tier** handle, `setFilter(nonEmpty)` does NOT call `CountMatches` (assert the mock's call count stays 0). Mutation that must break it: drop the `tier !== "memory"` guard → the mock is called and the assertion fails.
  - `counting` is `true` synchronously after `setFilter` on a rescan tier (before the count resolves) and `false` after.
  - **Count supersession:** issue `setFilter(A)` on a rescan tier (count promise A pending), then `setFilter(B)` (count promise B), then resolve A with `{total:1}` and B with `{total:2}`; final `matchCount` is `2`, never flickers to `1` (resolve A last to make the race real). Mutation that must break it: remove the `countReqId !== reqId` guard → A's late resolution overwrites B.
  - `cancelCount()` while counting calls `Cancel(countReqId)` and sets `counting:false`.
  - `setFilter` back to an empty filter clears `matchCount` to `-1` and does not start a count.
  - **Cleared-then-late-count regression (the `genAtStart` guard is load-bearing):** on a rescan tier, `setFilter(A)` with a manually-resolved `CountMatches` promise, then `setFilter({combinator:"and"})` (clear), THEN resolve A's promise with `{total:1, exact:true}`; assert `matchCount` stays `-1` and `total` is NOT `1` (A's stale count must not land on the now-unfiltered state). **Mutation that must break it: drop the `genAtStart !== gen` half of `startCount`'s guard** - with `countReqId` nulled on clear (above), only `genAtStart` rejects this write, so without it A's count lands on the cleared state. This is the discriminating test the `countReqId`-only supersession test (the A→B case above) cannot provide.
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement** per above.
- [ ] **Step 4: Run - PASS + `npm run check`.**
- [ ] **Step 5: Commit** - `feat(gui): cancellable live filtered count`.

---

### Task 6: The condition row component

**Files:** Create `gui/frontend/src/lib/explorer/ConditionRow.svelte`, `gui/frontend/src/lib/explorer/ConditionRow.test.ts`. Renders and edits one `DraftCondition`.

**Interfaces (produces):** `ConditionRow` props `{ condition: DraftCondition; columns: Column[] }`; events `change` (detail: the updated `DraftCondition`), `remove` (detail: `{ id: number }`).

**Consumes:** Task 1 `operatorsForType`/`OpSpec`; Task 2 `DraftCondition`/`conditionError`; `Column`/`KindChip` for the type badge; `app.css` tokens.

**Row layout** (one flex row, `--surface-1` face): a column `<select>` (options are `columns.map(c => c.path)`; changing it MUST update the draft's `type` to the new column's `type`, reset `op` to `defaultOpForType(newColType)`, and clear the values - `type` is load-bearing: `buildFilter`'s `in`-list element coercion tags each element by `condition.type`, so a stale `type` after a column change mis-tags the list, e.g. numeric elements sent as strings that then zero-match), an operator `<select>` (options from `operatorsForType(col.type)`), then an arity-driven value control:
- `arity:"none"` → nothing.
- `arity:"text"` → `<input type="text">` (+ a `ci` toggle `<button aria-pressed>` when the `OpSpec.ci` is true), monospace via `.mono`.
- `arity:"number"` → `<input type="text" inputmode="decimal">` (text, not `type=number`, so mid-typing states are the draft's problem not the browser's).
- `arity:"bool"` → a two-state `<select>` `true`/`false`.
- `arity:"list"` → a comma-splitting `<input>` whose value maps to `list` (split on `,`, trim); show the parsed chip count.
Plus a remove `<button aria-label="Remove condition">✕</button>`. When `conditionError(condition) !== ""`, show it inline with `--status-critical` and mark the input `aria-invalid`.

**Form-control styling:** no input/select tokens exist in `app.css` - hand-style `input`/`select` to mirror the global `button` rule (`app.css:145-175`): `border:1px solid var(--border)`, `border-radius:var(--radius-sm)`, `background:var(--surface-1)`, `color:var(--text-primary)`, `padding:var(--space-2) var(--space-3)`, `:focus-visible` → `outline:2px solid var(--accent); outline-offset:-2px`. Native `<select>`/`<input>` are focusable - no `role`/`tabindex` needed.

- [ ] **Step 1: Build it**, then a jsdom wiring test `ConditionRow.test.ts` (mount the real component per the house pattern - `new ConditionRow({target, props})`, `await tick()`) covering:
  - Changing the operator `<select>` to `isnull` hides the value input (assert no text input in the DOM) and a subsequent `change` event's detail has `op:"isnull"`.
  - Typing in the value input for a `contains` op emits `change` with `text` updated.
  - Changing the column `<select>` from a string column to an int column resets `op` to `"gte"` AND sets `type` to `"int"` in the emitted `change` (mutation: drop the op-reset-on-column-change → op stays `contains`, invalid for int, test fails; separately assert the emitted `type` is `"int"` so a stale `type` - which would mis-tag an `in`-list - is caught).
  - A `regex` op with an unbalanced-paren value renders the `conditionError` text and sets `aria-invalid` on the input.
  - The remove button emits `remove` with the row's `id`.
- [ ] **Step 2: BUILD + RUN + SCREENSHOT.** `cd gui && wails build`; the app can't drive the row alone yet (Task 7 mounts the bar) - so verify the row visually via the preview path if one exists, else defer the screenshot to Task 7 and note it. `npm run check` 0 errors is the hard gate here.
- [ ] **Step 3: Commit** - `feat(gui): condition row editor`.

---

### Task 7: The filter bar and its store wiring

**Files:** Create `gui/frontend/src/lib/explorer/FilterBar.svelte`, `gui/frontend/src/lib/explorer/FilterBar.test.ts`. Modify `gui/frontend/src/App.svelte` (own `filterOpen`, wire the header toggle, pass it to Explorer), `gui/frontend/src/lib/explorer/Explorer.svelte` (accept `filterOpen` as a bindable prop, mount the bar between `.content` and `<StatusBar>`), `gui/frontend/src/lib/Header.svelte` (a Filter toggle button). Implements the §3.4 builder + §5 phase line.

**Layout ownership note (verified against the repo):** `Header` and `Explorer` are SIBLINGS under `App.svelte` (`App.svelte:71-87`) - `Explorer` does NOT render `Header`. So `filterOpen` must live in **`App.svelte`**, which routes the header's toggle event and passes the flag down to `Explorer`. Do not try to hold `filterOpen` in `Explorer` and wire `on:toggleFilter` there - `Explorer` never mounts `Header` and the event has no path to it.

**Interfaces (produces):** `FilterBar` props `{ columns: Column[]; open: boolean }`; it owns a local `FilterDraft`, renders `{#each draft.conditions}` as `ConditionRow`s plus an AND/OR `<select>`, an "+ Add condition" button, and a "Clear" button; on any change it debounces (Task 3) and calls `explorer.setFilter(buildFilter(draft))`.

**Consumes:** Task 2 `emptyDraft`/`newCondition`/`buildFilter`; Task 3 `debounce`; Task 6 `ConditionRow`; the `explorer` store's `setFilter`.

**Wiring:**
- Local `let draft = emptyDraft();` and `let nextId = 1;`. A `change` from a `ConditionRow` replaces that row in `draft.conditions` by `id`; `remove` filters it out; "+ Add condition" pushes `newCondition(nextId++, columns[0].path, columns[0].type)`; the combinator `<select>` sets `draft.combinator`; "Clear" resets `draft = emptyDraft()`.
- After every mutation, `rebuild()`: `const built = buildFilter(draft); debouncedApply.call(built);` where `debouncedApply = debounce((f) => explorer.setFilter(f), 250)`. Because `buildFilter` omits incomplete/invalid conditions, a half-typed regex or empty value never fires a bad request - it simply isn't in the built filter yet.
- **The count/apply split:** `setFilter` already debounced at 250ms handles both the page-0 refetch and (Task 5) the count. One debounce is enough for E3 - do not over-engineer a second interval.
- **Teardown safety (review F1):** `import { onDestroy } from "svelte"; onDestroy(() => debouncedApply.cancel());`. Opening a second file dips the store `status` `ready → opening`, which unmounts this `{#if status==="ready"}` FilterBar and later mounts a fresh empty one. Without cancelling the pending debounce, FilterBar A's armed 250ms timer survives its own destroy and fires `explorer.setFilter(f_A)` against file B's handle - filtering B by A's condition while the new bar shows nothing. The `{#if}` teardown runs in a Svelte microtask flush, well before the 250ms macrotask timer, so `cancel()` reliably beats the stale fire.
- The bar is only shown when its `open` prop is true; toggled by a `<button>` in `Header.svelte` (event `toggleFilter`), with `filterOpen` state held in `App.svelte` (see the ownership note above - Header and Explorer are siblings under App).

**App.svelte changes:** add `let filterOpen = false;`. Render `<Header ... filterOpen={filterOpen} on:toggleFilter={() => filterOpen = !filterOpen} />` and `<Explorer ... bind:filterOpen />` (alongside the existing `on:open`). `App.svelte` is the only file mounting both `Header` and `Explorer` (`App.svelte:71-87`), so it is the only place the toggle event and the flag can meet.

**Explorer.svelte changes:** add `export let filterOpen = false;` (a **bindable prop**, not a local `let` - Task 9's seed sets it and it must propagate up through `bind:filterOpen` to Header's `aria-pressed`). Mount `{#if $explorer.status === "ready"}<FilterBar columns={$explorer.columns} open={filterOpen} />{/if}` as a flex child of `.explorer` BETWEEN `.content` and `<StatusBar>` (spans full width under the sidebar+table, above the status bar - the locked layout decision). Do NOT wire `on:toggleFilter` here.

**Header.svelte changes:** add `export let filterOpen = false;` and a `Filter` `<button>` in `.actions` (before Open), `on:click={() => dispatch("toggleFilter")}`, `aria-pressed={filterOpen}`, `title="Filter"` - a plain button matching the existing ones. Do NOT disable it while opening; it is inert until `status==="ready"` because the bar only mounts then.

- [ ] **Step 1: Build it**, then `FilterBar.test.ts` (jsdom, mock the Wails bridge; the store is the real singleton) covering:
  - Adding a condition and typing a complete value results in `explorer.setFilter` being called (spy on the store, or assert `QueryRows` mock called with a non-empty filter after the debounce - use `vi.useFakeTimers()` + `vi.advanceTimersByTime(250)`).
  - Two conditions + the combinator set to `"or"` build a Filter with `combinator:"or"` and two conditions.
  - "Clear" calls `setFilter` with a match-all (empty) filter and empties the rows.
  - A half-typed `regex` (invalid) condition present alongside a complete `notnull` fires `setFilter` with ONLY the `notnull` condition (invalid one omitted) - mutation: make `buildFilter` include incomplete conditions → the invalid regex reaches the filter and the assertion (one condition) fails.
  - **Teardown cancels the pending debounce (review F1):** add a condition and type a complete value (arming the 250ms debounce), then `$destroy()` the component (simulating the `ready → opening` unmount), then `vi.advanceTimersByTime(250)`; assert `explorer.setFilter` / the `QueryRows` mock is NOT called with the stale filter. Mutation that must break it: remove the `onDestroy(() => debouncedApply.cancel())` → the stale `setFilter` fires after destroy.
- [ ] **Step 2: BUILD + RUN + SCREENSHOT (the wow check).** `cd gui && wails build`, open `gui/testdata/nested.ndjson`, toggle the filter bar, add `user.age >= 20`, watch the rows filter and the status count update. Open a rescan-tier fixture (a large generated NDJSON) and confirm the count shows `counting…` then an exact number, and Cancel stops it. **Look at the screenshots.** A filter that doesn't change the rows, a count stuck on the unfiltered number, or a half-typed regex throwing a page error is a failure.
- [ ] **Step 3: Commit** - `feat(gui): filter bar wired to the store`.

---

### Task 8: Live count in the status bar + filtered empty state

**Files:** Modify `gui/frontend/src/lib/explorer/StatusBar.svelte`, `gui/frontend/src/lib/explorer/rowCount.ts`, `gui/frontend/src/lib/explorer/Explorer.svelte`, and their `.test.ts`. Implements the honest counting affordance (recon GAP 1/6) and the filtered empty state (GAP 3).

**Interfaces (produces):** `StatusBar` gains props `counting: boolean`, `matchCount: number`, `matchExact: boolean`, `filterActive: boolean`; `rowCount.ts` gains a branch for the counting state.

**Wiring:**
- `Explorer.svelte` threads `counting={$explorer.counting}`, `matchCount={$explorer.matchCount}`, `matchExact={$explorer.matchExact}`, `filterActive={$explorer.filterActive}` into `StatusBar`.
- `StatusBar`: when `filterActive`, the row-count text reflects the filtered count - `counting` → `counting…` with a Cancel affordance (a small `<button on:click>` dispatching a `cancelCount` event the Explorer wires to `explorer.cancelCount()`); when a `matchCount >= 0` is known, show `{matchCount} of {unfilteredTotal}` style or just `{matchCount} rows` per `formatRowCount`, with the estimate/exact styling driven by `matchExact`. When not `filterActive`, unchanged from E2.
- **`rowCount.ts`:** extend `formatRowCount` (or add `formatFilteredCount`) so `counting` renders `counting…` and a known filtered count renders `N rows` (exact) / `~N rows` (inexact). Keep the existing unfiltered behavior byte-identical.
- **Filtered empty state (`Explorer.svelte:106`):** when `filterActive && total === 0 && (totalExact || version > 0)`, render "No rows match this filter" (with a "Clear filter" affordance) instead of "No rows in this file". The non-filter empty state stays for `!filterActive`.

- [ ] **Step 1: Write failing tests** - `rowCount.test.ts` cases (pure, discriminating): `formatRowCount` with `counting:true, filterActive:true` → contains "counting"; with `filterActive:true, matchCount:42, matchExact:true` → "42 rows" exact styling; with `matchExact:false` → "~42". Then `StatusBar.test.ts` (jsdom) + `Explorer.test.ts` wiring cases: the Cancel button in the counting state dispatches `cancelCount`; `filterActive` empty state renders "No rows match this filter" (mutation: drop the `filterActive` branch → it reads "No rows in this file" and the test fails).
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run - PASS + `npm run check`.**
- [ ] **Step 5: Commit** - `feat(gui): live filtered count and filtered empty state`.

---

### Task 9: Scroll-to-top on filter change + click-to-seed from the sidebar

**Files:** Modify `gui/frontend/src/lib/explorer/DataTable.svelte` (react to `resetToken` → scroll to row 0), `gui/frontend/src/lib/explorer/StructureMap.svelte` + `TreeNode.svelte` (a filter affordance per row), `gui/frontend/src/lib/explorer/Explorer.svelte` + `FilterBar.svelte` (route the seed). Implements recon GAP 9 and the locked click-to-seed decision.

**Interfaces (produces):** `DataTable` prop `resetToken: number` (bumping it sets `viewport.scrollTop = 0` and recomputes the window); `TreeNode`/`StructureMap` event `seedFilter` (detail `{ path: string; type: string }`); `FilterBar.seed(path, type)` (public method or a prop-driven trigger) that appends `newCondition` for that column and focuses its value input.

**Consumes:** Task 4's `resetToken`; Task 6 `ConditionRow` focus; Task 2 `newCondition`.

**Wiring:**
- `DataTable`: add `export let resetToken = 0;` and `$: resetToken, scrollToTop();` where `scrollToTop()` sets `viewportEl.scrollTop = 0` and calls the existing `recomputeRange()`. Guard against the initial run (a `prevResetToken` sentinel, like the existing `columns`-changed guard at `DataTable.svelte:141`) so mount doesn't force a scroll. Thread `resetToken={$explorer.resetToken}` from `Explorer.svelte`.
- `TreeNode`: add a small `<button class="seed" aria-label="Add filter for {node.path}">` (a funnel/filter glyph) shown on `isColumn` rows (a column-less node has nothing to filter), `dispatch("seedFilter", {path: node.path, type: node.field ? dominantKind(node.field) : "string"})`; `stopPropagation` so it does not also trigger the row's focus/scroll. `StructureMap` forwards it.
- `Explorer.svelte`: on `seedFilter`, open the filter bar by assigning its bindable `filterOpen = true` (which propagates up through `App.svelte`'s `bind:filterOpen` to Header's `aria-pressed` - this is why Task 7 made `filterOpen` a bindable prop, not a local `let`), and pass the seed to `FilterBar` (e.g. bind a `seed` prop `{path,type,nonce}` that `FilterBar` reacts to, appending a `newCondition` and focusing its value input via `tick()` then a query for the new row's input).
- The default op comes from `defaultOpForType(type)` (Task 1) - numeric → `gte`, string → `contains`, etc. - so clicking a numeric field's funnel yields `age ≥ [_]` with the value focused, exactly the locked wow.

- [ ] **Step 1: Build it**, then tests: `DataTable.test.ts` - bumping `resetToken` after a deep scroll sets `scrollTop` back to 0 (jsdom, stub `scrollTop`); mutation: drop the `resetToken` reactive → scroll stays deep, test fails. `StructureMap.test.ts` - clicking the seed button on a column row emits `seedFilter` with the path and does NOT emit `focus` (stopPropagation); a column-less row has no seed button. `Explorer.test.ts` - a `seedFilter` event opens the bar and results in a condition for that path (assert via the eventual `setFilter`/`QueryRows` filter).
- [ ] **Step 2: BUILD + RUN + SCREENSHOT (the wow).** `cd gui && wails build`, open `nested.ndjson`, click the funnel on `user.age` → the bar opens with `user.age ≥ [ ]`, value focused; type `20` → rows filter, count updates; the table is scrolled to the top. **Look at it.** If the funnel also scrolls the column (event leaked) or the value isn't focused, that's a failure.
- [ ] **Step 3: Commit** - `feat(gui): scroll-to-top on filter and click-to-seed from sidebar`.

---

### Task 10: Full-stack verification and the filter screenshot

**Files:** Modify `gui/README.md` (document the filter). No source changes unless verification finds a defect - if it does, fix it here and say so.

- [ ] **Step 1: Verify the whole stack.**
  - `cd gui/frontend && npm run check` - 0 errors; `npm run test` - green, and count the tests (state the number, it must exceed E2's 131).
  - `cd .. && go test ./... -count=1` - all 16 packages still green (E3 touched no Go, so this is a regression guard).
  - `grep -rn "dependencies" gui/frontend/package.json` - confirm no runtime `dependencies` block was added.
  - `cd gui && wails build` succeeds; regenerate bindings and confirm unchanged: `wails generate module && git diff --exit-code gui/frontend/wailsjs/` (E3 added no Go DTOs, so this MUST be empty - a non-empty diff means something touched the Go DTO surface, which is out of scope).
- [ ] **Step 2: The filter screenshots.** On `nested.ndjson`: a two-condition AND filter (`user.age ≥ 20` AND `meta notnull`) narrowing the rows, exact count in the status bar. On a generated large rescan-tier NDJSON: a filter with the `counting…` affordance mid-count, then the resolved exact number, then Cancel mid-count leaving the prior number. **Look at them.** If the filtered count ever shows the unfiltered total, or `counting…` never resolves, or Cancel wedges the UI, say so rather than declaring success.
- [ ] **Step 3: Commit** - `docs(gui): document the visual filter` (plus any `fix(gui):` commits verification produced, each separate).

---

## Self-Review

**Coverage (E3 from product spec §3.4 + §5):** operator vocabulary + type re-exports (T1) · draft model + valid-Filter coercion with per-op §5 rules (T2) · debounce (T3) · store threads a live filter with the GAP-2 generation guard and GAP-3 total reset (T4) · cancellable superseded-safe live count, skipped on memory tier (T5) · condition row editor (T6) · filter bar + header toggle + store wiring (T7) · honest counting affordance + filtered empty state (T8) · scroll-to-top on filter change + click-to-seed from the sidebar (T9) · full-stack verification + screenshots (T10).

**Explicitly NOT in this plan, with owners:** nested AND/OR `Groups` and per-group `Negate` (later; zero engine change needed - the UI stays flat) · the global search box (E6, per §5/§6) · transform / column select / rename / flatten and export (E4) · jq/SQL codegen for the current filter (E5) · the nested tree-of-values view (E6) · a determinate count progress bar (needs the E4-scoped `shape:progress` emitter; E3 is honestly indeterminate). No Go changes at all - the engine, bindings, and `models.ts` Filter AST are consumed as-is.

**Placeholder note:** none. Every task carries its own tests with a stated mutation; the only "VERIFY"-style item is T6's screenshot, which defers to T7 if no isolated preview path exists - a phase-safe sequencing note, not a gap.

**Type consistency:** `OpId`/`OpSpec`/`operatorsForType`/`defaultOpForType` (T1) are consumed by `newCondition`/`buildFilter` (T2), `ConditionRow` (T6), and the seed (T9). `DraftCondition`/`FilterDraft`/`buildFilter` (T2) flow into `FilterBar` (T7) and produce the engine `Filter` that `setFilter` (T4) threads into `QueryRows` and `startCount` (T5). `resetToken` is written by `setFilter` (T4) and consumed by `DataTable` (T9). `filterActive`/`counting`/`matchCount`/`matchExact` are set in the store (T4/T5) and rendered by `StatusBar`/`Explorer` (T8). `Filter`/`Condition`/`Value`/`CountRequest` are re-exported once in `types.ts` (T1) and imported everywhere else - never redefined.

**Correctness / concurrency checks (each has an explicit, mutation-proven test):** filter change supersedes an in-flight old-filter page so it can't land in the new slot (T4, mutation removes **both** `++gen` and `inflight.clear()` - either guard alone suffices, so the test only dies when both go) · the filtered total resets to `-1` so the stale unfiltered count is never shown as filtered (T4) · `CountMatches` is skipped on the memory tier where page 0 is already exact (T5, mutation drops the tier guard) · a slow count for filter A never overwrites filter B on the A→B path (T5, mutation removes the `countReqId` guard) AND never lands on a cleared-to-empty filter (T5, mutation drops the `genAtStart !== gen` guard - the two guards are load-bearing in different scenarios, each with its own test) · a half-typed/invalid regex or empty value is omitted from the built Filter, never sent (T2 + T7, mutation makes `buildFilter` include incomplete conditions) · a debounced filter armed against file A cannot fire after a switch to file B (T7, mutation removes FilterBar's `onDestroy` cancel) · a column change updates the draft's `type` so an `in`-list is not mis-tagged (T6) · the seed funnel does not also fire the column-scroll `focus` event (T9, stopPropagation) · zero Go changes, guarded by `git diff --exit-code wailsjs/` and the Go suite staying green (T10).
