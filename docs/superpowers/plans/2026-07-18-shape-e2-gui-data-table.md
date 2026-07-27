# E2: GUI Data Table + Structure-Map Sidebar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. Tasks 1-4 are Go and end with a failing-test-first cycle; Tasks 5-9 are frontend and end with a `wails build` + screenshot verification, not only a unit test.

**Goal:** Drag any data file into the shape GUI and browse its actual rows in a virtualized table, with the profiler demoted to a structure-map sidebar that focuses columns - the first "wow" screenshot of the data-explorer pivot.

**Architecture:** `internal/query`'s Engine (E1, merged) gains cancellation and a count entry point; `gui/app.go` becomes the Wails binding layer holding one `*query.Engine` plus the current handle; the Svelte frontend gains a hand-rolled two-axis virtualized table fed by paged `QueryRows` calls and a sidebar rendered from `OpenResult.Profile` (`ProfileDTO`). No new runtime frontend dependency, no new Go dependency, cgo-free, no DuckDB.

**Tech Stack:** Go stdlib + already-vendored `modernc.org/sqlite`, `github.com/parquet-go/parquet-go`, `github.com/wailsapp/wails/v2`. Frontend: Svelte 3.49 + Vite 3 + TypeScript 4.6, zero runtime dependencies; `vitest` added as a devDependency in Task 5 for the two pure-logic modules only.

**AUTHORITATIVE SPECS:** engine contract - `docs/superpowers/specs/2026-07-17-shape-engine-design.md` (§3 cells/columns, §4 backends/windows, §8 binding surface). Product intent - `docs/superpowers/specs/2026-07-17-shape-data-explorer-design.md` (§3.2 structure map, §3.3 table view, §5 phasing). Where a task names a spec section, transcribe that section's rules exactly; where a task shows code, that code is the deliverable.

**Decisions locked before this plan (do not relitigate):**
- The explorer is the **only** view after a file opens. `KpiRow.svelte`, `FieldGrid.svelte`, `FieldCard.svelte`, `FieldDetail.svelte`, `App.ProfileFile`, `App.DiffFiles` and the whole `internal/visual` path **stay in the repo, compiling and tested, but are no longer imported by `src/App.svelte`**. Delete nothing.
- Virtualization is **hand-rolled on both axes**. No table/virtual-list dependency is added.
- Sidebar click = **column focus only** (scroll-into-view + highlight). Filtering is E3 (spec §5).
- **No sorting.** There is no `Sort` field in `QueryRequest`, `CompiledPlan`, or `Backend`; row order is each reader's fixed order (engine spec §9). Header click means focus, not sort.

## Global Constraints

- **cgo-free.** `CGO_ENABLED=0 go build ./...` must pass. No DuckDB, no new module dependency in `go.mod`. (Spec §0.)
- **Determinism.** No Go map iteration may reach any DTO. Column order is **first-seen**, never alphabetized; `ProfileDTO.Fields` is alphabetized and must not be used to order table columns. (Spec §3/§9.)
- **Numbers render `Cell.Str`, never `Cell.Num`.** `Str` holds the exact source literal; `Num` is a lossy float64 kept for compare only. Rendering `Num` reintroduces the float loss E1 paid to avoid, twice (Go float64 then JS number). (Spec §3.)
- **`CellNull` and `CellMissing` render differently.** Both blank throws away a distinction the engine preserves. (Spec §3.)
- Constants, pinned: `MaxColumns=512` (`columns.go:477`), `previewCap=200` (`columns.go:154`), `DefaultMemBudgetBytes=512<<20` (`source.go:18`), `cancelCheckStride=4096` (`rescan.go:128`), new `maxMatchCacheEntries=16` (Task 1), new `ROW_H=28`, `PAGE_ROW_BUDGET=30000`, `PAGE_ROWS_MIN=40`, `PAGE_ROWS_MAX=200` (Task 5).
- **Commits: Conventional Commits, lowercase imperative subject, NO co-author trailer.** This overrides Claude Code's default trailer.
- **Do not touch** the existing CLI (`internal/cmd`), `internal/visual`, `internal/diff`, or the P3 Svelte dashboard components. `go test ./...` must stay green for all 16 packages at every commit.
- **Leave `engine.go:178`'s warning string byte-identical**, em dash included: `"large file - streaming mode (totals are estimates)"`. The spec quotes it verbatim and a test matches on it.
- Every Go task ends with `go test ./...`; every frontend task ends with `cd gui/frontend && npm run check` reporting 0 errors.

---

### Task 1: Filter-only cache key and one LRU match cache (fixes I2)

**Files:** Modify `internal/query/filter.go` (add `CompiledFilter.key` + `Key()`, set it in `CompileFilter`), `internal/query/transform.go` (rename `canonicalPlanKey` to `canonicalFilterKey`, retarget `CompiledPlan.FilterKey`), `internal/query/memstore.go` (delete `countCache`, replace `filterCache` with one LRU-capped `matchCache`). Modify tests `internal/query/memstore_test.go`, `internal/query/transform_test.go`. Implements E1 final-review item **I2**.

**Every remaining `mb.filterCache` / `mb.countCache` reference in `memstore_test.go` must be retargeted to `mb.matchCache` in this same commit or the package will not compile** - the affected test functions are `TestMemBackend_Query_ReusesCachedBitsetAcrossWindows`, `TestMemBackend_Count_ReusesBitsetForSameFilterPointer`, `TestMemBackend_Close`, `TestMemBackend_Query_CancelledContext_NotCached`, `TestMemBackend_Count_CancelledContext_NotCached`, and `TestMemBackend_Query_DifferentFilters_NoContamination`. Read a cached bitset as `mb.matchCache[key].Value.(*matchEntry).bs`, and key on `p.FilterKey()` / `cf.Key()` rather than on a `*CompiledFilter` pointer.

**Interfaces (produces):** `func (cf *CompiledFilter) Key() string`; `func canonicalFilterKey(f Filter) (string, error)`; `const maxMatchCacheEntries = 16`; `(*CompiledPlan) FilterKey() string` now returns the **filter-only** key.

**Why:** `countCache map[*CompiledFilter]*bitset` (`memstore.go:94`) is keyed on pointer identity. `CompileFilter` (`filter.go:111`) mints a fresh pointer per call, so Task 3's `CountMatches` - which compiles per request - would hit 0% forever. And neither cache is ever evicted (`memstore.go:237` clears them only on `Close`), so N distinct filters over a 512 MiB source retain N x (rows/8) bytes. Giving `CompiledFilter` a content key collapses both problems: the two caches become one, keyed by a value both call paths can compute, and the cap only has to be built once.

- [ ] **Step 1: Write failing tests** in `internal/query/memstore_test.go` and `internal/query/transform_test.go` covering:
  - `TestCompiledFilter_Key_SameLogicalFilterSameKey`: compile the same non-empty `Filter` twice into two distinct `*CompiledFilter` values; `a.Key() == b.Key()` and `a != b` (different pointers).
  - `TestCompiledFilter_Key_DifferentFilterDifferentKey`: two filters differing only in `Condition.Value.Num` produce different keys.
  - `TestCompiledFilter_Key_EmptyFilterStable`: `CompileFilter(Filter{}, nil).Key()` is non-empty and equal across two calls (the match-all filter is a legitimate, very common cache key).
  - `TestCompiledPlan_FilterKey_IgnoresTransform`: `CompilePlan(f, Transform{}, cm).FilterKey() == CompilePlan(f, Transform{Select: []ColumnSpec{{Path: "a"}}}, cm).FilterKey()` - **the key is filter-only now**, so Query and Count share one bitset.
  - `TestMemBackend_Count_ReusesBitsetAcrossSeparateCompiles` (**rewrite of the existing `TestMemBackend_Count_ReusesBitsetForSameFilterPointer` at `memstore_test.go:370-398`, which pins the defective pointer semantics**): compile the same logical filter twice into two pointers, `Count` with each, and assert `mb.matchCache` holds exactly **one** entry and both calls returned the same total.
  - `TestMemBackend_QueryAndCountShareOneCacheEntry`: `Query` with plan P then `Count` with a separately compiled filter of the same `Filter` leaves `len(mb.matchCache) == 1`.
  - `TestMemBackend_MatchCache_EvictsLeastRecentlyUsed`: run `maxMatchCacheEntries+3` distinct filters through `Count`; assert `len(mb.matchCache) == maxMatchCacheEntries`, that the **first** filter's key is gone, and that the most recent `maxMatchCacheEntries` keys are all present. Then re-`Count` with the evicted filter and assert the total is still correct (eviction is a cache miss, never a wrong answer).
  - `TestMemBackend_MatchCache_TouchOnHitPreventsEviction`: fill the cache, re-`Count` the oldest key (making it newest), add one more filter, assert the touched key survived and the second-oldest was evicted instead.
  - `TestMemBackend_MatchCache_CancelledComputeNotCached`: cancel mid-scan; assert `len(mb.matchCache) == 0` afterwards.
  - `TestMemBackend_Close_ClearsMatchCache`: after `Close()`, `len(mb.matchCache) == 0` (**rewrite of the `countCache` assertion at `memstore_test.go:478`**).
  - **Delete `TestCompiledPlan_FilterKey_DistinctForDifferentTransform` (`transform_test.go:357-374`).** It pins the (Filter,Transform) key this task deliberately removes and asserts the exact inverse of `TestCompiledPlan_FilterKey_IgnoresTransform`; both cannot pass. This is an intentional behavior change, not a test dropped to go green - `TestCompiledPlan_FilterKey_IgnoresTransform` replaces it, while `TestCompiledPlan_FilterKey_DistinctForDifferentFilter` (`:338`) and `_StableForIdenticalInput` (`:317`) stay as-is and must still pass.
- [ ] **Step 2: Run - FAIL** (`go test ./internal/query/ -run 'TestCompiledFilter_Key|TestCompiledPlan_FilterKey|TestMemBackend_(Count_Reuses|QueryAndCount|MatchCache|Close_Clears)' -v`).
- [ ] **Step 3: Implement.**

In `internal/query/transform.go`, replace `canonicalPlanKey` (currently at `:286`) with a filter-only sibling and retarget its two callers:

```go
// canonicalFilterKey renders f as canonical JSON and returns the hex-encoded
// SHA-256 digest. Filter contains only structs/slices/scalars (no maps), so
// encoding/json's field-order-following marshal is already deterministic --
// no map-iteration dependence enters the key, and identical logical input
// (down to slice element order) always yields the same bytes.
//
// The key is FILTER-ONLY on purpose: it keys match bitsets, and only the
// Filter determines which records match. Keying it on (Filter, Transform)
// -- as the pre-E2 canonicalPlanKey did -- split one logical bitset across
// as many cache entries as there were transforms over it, and gave Count
// (which never has a Transform) no way to share Query's entry at all.
func canonicalFilterKey(f Filter) (string, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
```

`CompilePlan` (`transform.go:263`) drops its own key computation and takes the key off the compiled filter:

```go
func CompilePlan(f Filter, t Transform, cm *ColumnModel) (*CompiledPlan, error) {
	cf, err := CompileFilter(f, cm)
	if err != nil {
		return nil, fmt.Errorf("query: compile plan: filter: %w", err)
	}
	ct, err := CompileTransform(t, cm)
	if err != nil {
		return nil, fmt.Errorf("query: compile plan: transform: %w", err)
	}
	return &CompiledPlan{Filter: cf, Transform: ct, Columns: cm, filterKey: cf.Key()}, nil
}
```

Update `CompiledPlan.FilterKey`'s doc comment (`transform.go:245-250`) to say the key is filter-only and is exactly `p.Filter.Key()`.

In `internal/query/filter.go`, give `CompiledFilter` content identity:

```go
type CompiledFilter struct {
	pred func(rec any) bool
	key  string // canonical Filter hash; see Key
}

// Key returns a canonical, stable cache key for the Filter this predicate was
// compiled from: two CompiledFilters compiled from the same logical Filter
// always share a key, and any difference in the Filter produces a different
// one. A nil *CompiledFilter returns "".
func (cf *CompiledFilter) Key() string {
	if cf == nil {
		return ""
	}
	return cf.key
}

func CompileFilter(f Filter, cm *ColumnModel) (*CompiledFilter, error) {
	key, err := canonicalFilterKey(f)
	if err != nil {
		return nil, fmt.Errorf("query: compile filter: key: %w", err)
	}
	if isEmptyFilter(f) {
		return &CompiledFilter{key: key}, nil // nil pred: match-all
	}
	pred, err := compileGroup(f, cm)
	if err != nil {
		return nil, err
	}
	return &CompiledFilter{pred: pred, key: key}, nil
}
```

In `internal/query/memstore.go`, replace the two cache fields (`:81` and `:94`) with one LRU-capped cache. Use a doubly-linked recency list from `container/list` so touch and evict are both O(1):

```go
// maxMatchCacheEntries caps how many distinct match bitsets one memBackend
// retains. Each bitset is len(records)/8 bytes, so with the 512 MiB ingest
// budget an entry is at most a few MiB and the cache is bounded by a small
// constant multiple of that regardless of how many filters a session tries.
// Eviction is least-recently-used: an evicted filter simply recomputes on its
// next use (a latency cost, never a wrong answer).
const maxMatchCacheEntries = 16

type memBackend struct {
	records []any
	cm      *ColumnModel
	prof    profile.ProfileResult

	mu sync.Mutex
	// matchCache holds one match bitset per CompiledFilter.Key(). Query and
	// Count share it: both key on the filter alone, so scrolling a window and
	// counting the same filter never scan the records twice.
	matchCache map[string]*list.Element // key -> element holding *matchEntry
	matchLRU   *list.List               // front = most recently used
}

type matchEntry struct {
	key string
	bs  *bitset
}
```

`newMemBackend` initializes `matchCache: make(map[string]*list.Element)` and `matchLRU: list.New()`. `Close` (`:234`) resets both.

Replace `matchBitsetForPlan` (`:256`) and `matchBitsetForFilter` (`:285`) with one function; keep the existing off-lock double-checked-locking structure verbatim, since the I-T5 re-review already validated it:

```go
// matchBitsetFor returns the cached match bitset for cf, computing and storing
// it under cf.Key() on a miss. The compute happens OUTSIDE m.mu (double-checked
// locking) so a long or cancelled scan never holds the lock; a cancelled or
// errored compute is never stored, so it can never be cached.
func (m *memBackend) matchBitsetFor(ctx context.Context, cf *CompiledFilter) (*bitset, error) {
	key := cf.Key()

	m.mu.Lock()
	if el, ok := m.matchCache[key]; ok {
		m.matchLRU.MoveToFront(el)
		bs := el.Value.(*matchEntry).bs
		m.mu.Unlock()
		return bs, nil
	}
	m.mu.Unlock()

	bs, err := m.computeMatchBitset(ctx, cf)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if el, ok := m.matchCache[key]; ok {
		m.matchLRU.MoveToFront(el)
		bs = el.Value.(*matchEntry).bs
	} else {
		m.matchCache[key] = m.matchLRU.PushFront(&matchEntry{key: key, bs: bs})
		for m.matchLRU.Len() > maxMatchCacheEntries {
			oldest := m.matchLRU.Back()
			m.matchLRU.Remove(oldest)
			delete(m.matchCache, oldest.Value.(*matchEntry).key)
		}
	}
	m.mu.Unlock()
	return bs, nil
}
```

`Query` (`:138`) calls `m.matchBitsetFor(ctx, p.Filter)`; `Count` (`:191`) calls `m.matchBitsetFor(ctx, f)`. No other backend has a cache, so nothing else changes. `Backend.Count`'s signature is untouched - the key rides on the `*CompiledFilter` it already receives.

- [ ] **Step 4: Run - PASS + full `go test ./...`.**
- [ ] **Step 5: Commit** - `fix(query): filter-only cache key and LRU match cache`.

---

### Task 2: Thread ctx through open and query, add the cancel registry (fixes I3)

**Files:** Modify `internal/query/engine.go` (ctx params on `OpenSource`/`QueryRows`, `OpenRequest.RequestID`, in-flight registry, `Cancel`), `internal/query/source.go` (`openBackend`/`openIngestBackend` take ctx; ingest loop checks it), `internal/query/sqlbackend.go` (`newSQLBackend` takes ctx; `RowCount` takes ctx), `internal/query/parquetbackend.go` (`newParquetBackend` takes ctx; `RowCount` takes ctx), `internal/query/backend.go` (`RowCount(ctx)`), `internal/query/memstore.go` + `internal/query/rescan.go` (`RowCount(ctx)`). Modify tests across `internal/query/*_test.go` for the signature changes. Implements E1 final-review item **I3** and spec §8's `Cancel(requestID)`.

**Interfaces (produces):**
`func (e *Engine) OpenSource(ctx context.Context, req OpenRequest) (OpenResult, error)`;
`func (e *Engine) QueryRows(ctx context.Context, req QueryRequest) (RowSet, error)`;
`func (e *Engine) Cancel(requestID string) error`;
`OpenRequest.RequestID string \`json:"requestId,omitempty"\``;
`Backend.RowCount(ctx context.Context) (n int64, exact bool)`.

**Consumes:** nothing from Task 1 (independent), but lands on top of it.

**Why:** `Engine.QueryRows` hands backends `context.Background()` (`engine.go:206`), so every cancellation check E1 built - stride checks at `memstore.go:215`, `rescan.go:150`, ctx-bound `sql.QueryContext`, parquet scans - is inert from outside the package. `newSQLBackend` (`sqlbackend.go:103`) and `newParquetBackend` (`parquetbackend.go:108`) run a full-file profiling pass on `context.Background()`, and the JSON/CSV ingest loop (`source.go:122-140`) has no ctx at all. `QueryRequest.RequestID` (`engine.go:62`) is declared and read nowhere.

**NOTE (documented spec deviation):** spec §8's `OpenRequest` has no `RequestID`. E2 adds one so that opening a large sqlite/parquet file - a full-file scan - is cancellable by the same `Cancel(requestID)` path as a query. Everything else in §8 is honored as written.

- [ ] **Step 1: Write failing tests** in `internal/query/engine_test.go` covering:
  - `TestEngine_QueryRows_HonorsCancelledContext`: pass an already-cancelled ctx; expect `context.Canceled`.
  - `TestEngine_Cancel_CancelsInFlightQuery`: start `QueryRows` on a large mem source in a goroutine with `RequestID: "r1"`, and from the main goroutine spin until the engine reports `r1` in flight (`e.inFlightCount() == 1`, a test-only helper), then `e.Cancel("r1")`; expect the query to return `context.Canceled`. **This is the mid-flight case; a pre-cancelled ctx short-circuits at the top-level guard and never exercises the stride check** (the gap flagged in the I-T5 and I-T7 re-reviews).
  - `TestEngine_Cancel_UnknownRequestID`: returns an error mentioning the id, does not panic.
  - `TestEngine_Cancel_SupersedesDuplicateRequestID`: two concurrent `QueryRows` with the same `RequestID` - the first is cancelled when the second registers, the second completes normally.
  - `TestEngine_QueryRows_EmptyRequestIDStillRuns`: `RequestID: ""` is not registered, is not cancellable, and returns rows.
  - `TestEngine_Cancel_ReleasesRegistryOnCompletion`: after a normal `QueryRows`, `e.inFlightCount() == 0`, and `Cancel` on that id now errors (no leak).
  - `TestEngine_OpenSource_HonorsCancelledContext` for all four tiers (ndjson/csv fixture -> ingest, sqlite fixture, parquet fixture): an already-cancelled ctx makes `OpenSource` return `context.Canceled` rather than a populated `OpenResult`.
  - `TestOpenIngestBackend_CancelsMidIngest`: call `openIngestBackend` **directly** with a fake `readers.RecordStream` (the interface is 2 methods - `Next() (any, error)` and `Skipped() int`, `internal/readers/readers.go:17`) whose `Next` returns a small `map[string]any` every call, counts its calls, and invokes the test's `cancel()` exactly once on call 4096 - immediately before the loop's `n%cancelCheckStride == 0` check at n=4096 - then keeps returning records so the loop can never reach EOF. Pass `DefaultMemBudgetBytes` so the over-budget `break` cannot end the loop first, and any `path` string (it is only used for `fileSizeOf` and error text). Assert `errors.Is(err, context.Canceled)`. **Do not** write this as an NDJSON fixture plus a `time.Sleep` goroutine cancel: `openIngestBackend` exposes no progress signal and the only check points are n=0 and n=4096, so a sleep-aimed cancel lands either before n=0 (silently duplicating `TestEngine_OpenSource_HonorsCancelledContext` and never reaching the stride check) or after EOF (a false failure under the `-count=2` no-flakes gate, especially on Windows' coarse timer). `TestSQLBackend_Query_WantTotal_CountCancelledMidFlight` (`internal/query/sqlbackend_test.go:437`) documents this same objection for the sqlite path - follow that precedent.
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement.**

`internal/query/engine.go` - registry on the Engine, alongside the existing backend map:

```go
type Engine struct {
	mu       sync.Mutex
	backends map[string]Backend
	next     uint64

	// inflight maps a caller-supplied RequestID to the CancelFunc of the ctx
	// that request is running under, so Cancel(requestID) can interrupt a
	// long scan from another goroutine (the GUI's stale-scroll and
	// "stop counting" paths, spec §8). Entries are removed when the request
	// returns; an empty RequestID is never registered. gen distinguishes two
	// requests that reused the same id, so a finishing older request cannot
	// unregister the newer one that superseded it.
	inflight map[string]inflightEntry
	gen      uint64
}

type inflightEntry struct {
	cancel context.CancelFunc
	gen    uint64
}

func NewEngine() *Engine {
	return &Engine{
		backends: make(map[string]Backend),
		inflight: make(map[string]inflightEntry),
	}
}

// begin derives a cancellable ctx for requestID and registers it. The returned
// release function cancels the derived ctx and unregisters it, and is safe to
// call exactly once via defer. An empty requestID is not registered (the
// request is simply uncancellable) but still gets its own derived ctx, so the
// release path is uniform. Registering a requestID that is already in flight
// cancels the older one: a caller reusing an id means "supersede", which is
// exactly what a fast scroll wants.
func (e *Engine) begin(ctx context.Context, requestID string) (context.Context, func()) {
	cctx, cancel := context.WithCancel(ctx)
	if requestID == "" {
		return cctx, cancel
	}
	e.mu.Lock()
	if prev, ok := e.inflight[requestID]; ok {
		prev.cancel()
	}
	e.gen++
	myGen := e.gen
	e.inflight[requestID] = inflightEntry{cancel: cancel, gen: myGen}
	e.mu.Unlock()

	return cctx, func() {
		e.mu.Lock()
		// Only unregister if this request still owns the id: a newer request
		// may have superseded it, and that entry must survive.
		if cur, ok := e.inflight[requestID]; ok && cur.gen == myGen {
			delete(e.inflight, requestID)
		}
		e.mu.Unlock()
		cancel()
	}
}

// Cancel interrupts the in-flight request registered under requestID (spec
// §8). It returns an error if no such request is running -- which is a normal
// race (the request may have just finished), so callers may ignore it.
func (e *Engine) Cancel(requestID string) error {
	e.mu.Lock()
	entry, ok := e.inflight[requestID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("query: Cancel: no in-flight request %q", requestID)
	}
	entry.cancel()
	return nil
}

// inFlightCount reports how many requests are registered. Test-only.
func (e *Engine) inFlightCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.inflight)
}
```

**Implementation note on `begin`'s release:** the generation token is load-bearing and easy to get wrong. Do **not** try to identify the owning request by comparing `context.CancelFunc` values - funcs are not comparable in Go, and comparing the addresses of two local copies (`&cur == &cancel`) is always false, which would let a finishing older request unregister the newer one that superseded it. `TestEngine_Cancel_SupersedesDuplicateRequestID` covers exactly this case.

`OpenSource` and `QueryRows` gain ctx and use the registry:

```go
func (e *Engine) OpenSource(ctx context.Context, req OpenRequest) (OpenResult, error) {
	if req.Path == "" || req.Path == "-" {
		return OpenResult{}, fmt.Errorf("query: OpenSource: a real file path is required (stdin/empty rejected, spec §2)")
	}
	ctx, release := e.begin(ctx, req.RequestID)
	defer release()

	backend, format, tier, err := openBackend(ctx, req)
	if err != nil {
		return OpenResult{}, err
	}

	n, exact := backend.RowCount(ctx)
	// ... unchanged from here: warnings, register, OpenResult{...}
}

func (e *Engine) QueryRows(ctx context.Context, req QueryRequest) (RowSet, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return RowSet{}, err
	}
	plan, err := CompilePlan(req.Filter, req.Transform, backend.Columns())
	if err != nil {
		return RowSet{}, fmt.Errorf("query: QueryRows: %w", err)
	}
	ctx, release := e.begin(ctx, req.RequestID)
	defer release()
	return backend.Query(ctx, plan, Window{Offset: req.Offset, Limit: req.Limit}, req.WantTotal)
}
```

`internal/query/source.go` - thread ctx down and make the ingest loop cancellable:

```go
func openBackend(ctx context.Context, req OpenRequest) (backend Backend, format readers.Format, tier string, err error) {
	// ... unchanged until the switch
	case readers.FormatJSON, readers.FormatCSV:
		// ...
		backend, tier, err = openIngestBackend(ctx, req.Path, format, req.Format, req.CSVRaw, stream, budgetBytesOf(req))
		return backend, format, tier, err
	case readers.FormatSQLite:
		sb, serr := newSQLBackend(ctx, req.Path, req.Table)
		// ...
	case readers.FormatParquet:
		pb, perr := newParquetBackend(ctx, req.Path)
		// ...
}
```

and inside `openIngestBackend`'s loop (currently `source.go:122-140`), immediately after `rec, nerr := stream.Next()`'s error handling, add the same stride pattern the other backends use:

```go
	for n := 0; ; n++ {
		if n%cancelCheckStride == 0 {
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
		}
		rec, nerr := stream.Next()
		// ... unchanged
	}
```

`sqlbackend.go:103` becomes `sb.scan(ctx, ...)`; `parquetbackend.go:108` becomes `pb.scan(ctx, 0, ...)`; both constructors take `ctx context.Context` as their first parameter.

`internal/query/backend.go:63` - `RowCount` gains ctx so `sqlBackend.RowCount` (`sqlbackend.go:361`) can stop calling `context.Background()`:

```go
	// RowCount reports the source's row count. exact is false when the number
	// is an estimate (the rescan tier). A cancelled ctx returns (0, false).
	RowCount(ctx context.Context) (n int64, exact bool)
```

`memBackend.RowCount` and `parquetBackend.RowCount` accept and ignore ctx apart from a leading `ctx.Err()` check; `rescanBackend.RowCount` likewise; `sqlBackend.RowCount` calls `s.rowCountSQL(ctx)` and returns `(0, false)` on error. Delete the now-stale workaround comment at `sqlbackend.go:416-421`.

After this task, `grep -n 'context.Background()' internal/query/*.go` must return **only** matches inside `_test.go` files.

- [ ] **Step 4: Run - PASS + full `go test ./...` + `CGO_ENABLED=0 go build ./...`.**
- [ ] **Step 5: Commit** - `feat(query): thread ctx through open/query and add cancel registry`.

---

### Task 3: CountMatches, wide-data DTO fields, and the CellKind enum export

**Files:** Modify `internal/query/engine.go` (`CountRequest`, `CountResult`, `Engine.CountMatches`, wide-data fields on `OpenResult`), `internal/query/backend.go` (wide-data fields on `RowSet`), `internal/query/columns.go` (`AllCellKindValues`). Modify tests `internal/query/engine_test.go`, `internal/query/columns_test.go`. Implements spec §8 (`CountMatches`) and spec §3's wide-data bound.

**Interfaces (produces):**
`type CountRequest struct { RequestID string \`json:"requestId,omitempty"\`; Handle string \`json:"handle"\`; Filter Filter \`json:"filter"\` }`;
`type CountResult struct { Total int64 \`json:"total"\`; Exact bool \`json:"exact"\`; ElapsedMs int64 \`json:"elapsedMs"\` }`;
`func (e *Engine) CountMatches(ctx context.Context, req CountRequest) (CountResult, error)`;
`OpenResult.ColumnsTruncated bool \`json:"columnsTruncated"\``, `OpenResult.TotalPaths int \`json:"totalPaths"\``;
`RowSet.ColumnsTruncated bool \`json:"columnsTruncated"\``, `RowSet.TotalPaths int \`json:"totalPaths"\``;
`var AllCellKindValues = []struct{ Value CellKind; TSName string }{...}`.

**Consumes:** Task 1's `CompiledFilter.Key()` (so `CountMatches` hits the same cache `QueryRows` fills); Task 2's `Engine.begin`/`Cancel` (so counting is cancellable).

**Why three things in one task:** all three are small additions to the same DTO boundary, they are what Task 4's binding layer needs to exist, and a reviewer would accept or reject them together. `ColumnModel.Truncated`/`TotalPaths` (`columns.go:457`) are computed by E1 but **flattened away** at `engine.go:186` (`backend.Columns().Columns`), so spec §3's mandated "showing 512 of N columns" affordance has no field on any DTO today. `CellKind` generates as a bare TS `string` unless the enum is registered with Wails, so the cell renderer gets no exhaustiveness checking.

- [ ] **Step 1: Write failing tests** covering:
  - `TestEngine_CountMatches_ExactOnMemoryTier`: open an NDJSON fixture, count with a filter matching a known subset; `Total` correct, `Exact == true`, `ElapsedMs >= 0`.
  - `TestEngine_CountMatches_MatchAllEqualsRowCount`: empty `Filter{}` returns the same total as `OpenResult.RowEstimate` on the memory tier.
  - `TestEngine_CountMatches_UnknownHandle`: returns an error naming the handle.
  - `TestEngine_CountMatches_Cancellable`: mid-flight `Cancel(requestID)` returns `context.Canceled` (same spin-until-in-flight pattern as Task 2).
  - `TestEngine_CountMatches_SharesQueryBitset`: `QueryRows` then `CountMatches` with the same logical filter on a memory-tier handle leaves exactly one entry in the backend's `matchCache` (reach it via a package-internal test helper, as Task 1's tests do).
  - `TestEngine_CountMatches_RescanTierExactFlag`: on a source forced to the rescan tier (`BudgetMB: 1` over a fixture larger than that), `Exact` is reported per `Backend.Count`'s own contract, not hardcoded.
  - `TestEngine_OpenSource_ReportsColumnTruncation`: build a fixture whose record has `MaxColumns+10` distinct keys; assert `OpenResult.ColumnsTruncated == true`, `OpenResult.TotalPaths == MaxColumns+10`, `len(OpenResult.Columns) == MaxColumns`. Also assert a narrow fixture gives `ColumnsTruncated == false` and `TotalPaths == len(Columns)`.
  - `TestEngine_QueryRows_ReportsColumnTruncation`: the same wide fixture through `QueryRows` sets the same two fields on `RowSet`.
  - `TestAllCellKindValues_CoversEveryKind`: assert `AllCellKindValues` has exactly 8 entries and that its `Value` set equals `{CellMissing, CellNull, CellBool, CellInt, CellFloat, CellString, CellObject, CellArray}` - **a compile-time-adjacent guard so a future ninth kind cannot silently skip the TS union**.
  - `TestOpenResult_JSONShape`: `json.Marshal` an `OpenResult` and assert the raw JSON contains `"columnsTruncated"` and `"totalPaths"` (tag conformance - the frontend reads these names).
- [ ] **Step 2: Run - FAIL.**
- [ ] **Step 3: Implement.**

`internal/query/engine.go`:

```go
// CountRequest is the CountMatches request DTO (spec §8).
type CountRequest struct {
	RequestID string `json:"requestId,omitempty"`
	Handle    string `json:"handle"`
	Filter    Filter `json:"filter"`
}

// CountResult is the CountMatches response DTO (spec §8). Exact is false when
// the backend can only supply a lower bound or an estimate.
type CountResult struct {
	Total     int64 `json:"total"`
	Exact     bool  `json:"exact"`
	ElapsedMs int64 `json:"elapsedMs"`
}

// CountMatches returns how many records match req.Filter (spec §8): the
// cancellable, cached exact count behind the UI's "counting..." affordance,
// used when a tier can only report a lower bound or estimate from Query.
// On the memory tier it shares Query's match bitset (both key on
// CompiledFilter.Key()), so counting a filter already scrolled is free.
func (e *Engine) CountMatches(ctx context.Context, req CountRequest) (CountResult, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return CountResult{}, err
	}
	cf, err := CompileFilter(req.Filter, backend.Columns())
	if err != nil {
		return CountResult{}, fmt.Errorf("query: CountMatches: %w", err)
	}
	ctx, release := e.begin(ctx, req.RequestID)
	defer release()

	start := time.Now()
	total, exact, err := backend.Count(ctx, cf)
	if err != nil {
		return CountResult{}, err
	}
	return CountResult{Total: total, Exact: exact, ElapsedMs: time.Since(start).Milliseconds()}, nil
}
```

Add to `OpenResult` (after `Warnings`) and to `RowSet` in `backend.go` (after `ElapsedMs`) the identical pair, with this doc comment on both:

```go
	// ColumnsTruncated and TotalPaths surface spec §3's wide-data bound: the
	// column set is capped at MaxColumns (keeping highest-presence first, then
	// first-seen), so a source with more distinct paths than that reports
	// ColumnsTruncated=true and TotalPaths = the uncapped count. The UI shows
	// "showing 512 of N columns". Note this is NOT RowSet.Truncated, which
	// means "fewer rows than Limit: EOF reached".
	ColumnsTruncated bool `json:"columnsTruncated"`
	TotalPaths       int  `json:"totalPaths"`
```

Populate both at the Engine layer, the single place that already flattens `*ColumnModel` to `[]Column` - no backend changes:

```go
	// in OpenSource, replacing `Columns: backend.Columns().Columns,`
	cm := backend.Columns()
	// ...
	return OpenResult{
		// ...
		Columns:          cm.Columns,
		ColumnsTruncated: cm.Truncated,
		TotalPaths:       cm.TotalPaths,
		// ...
	}, nil

	// in QueryRows, on the RowSet returned by backend.Query
	rs, err := backend.Query(ctx, plan, Window{Offset: req.Offset, Limit: req.Limit}, req.WantTotal)
	if err != nil {
		return RowSet{}, err
	}
	if cm := backend.Columns(); cm != nil {
		rs.ColumnsTruncated = cm.Truncated
		rs.TotalPaths = cm.TotalPaths
	}
	return rs, nil
```

`internal/query/columns.go`, next to the `CellKind` constants (`:142-149`):

```go
// AllCellKindValues enumerates every CellKind for Wails' EnumBind option, so
// the generated TypeScript gets a real enum type instead of a bare `string`
// and the cell renderer's switch can be checked for exhaustiveness. Adding a
// CellKind without adding it here is caught by TestAllCellKindValues_CoversEveryKind.
var AllCellKindValues = []struct {
	Value  CellKind
	TSName string
}{
	{CellMissing, "MISSING"},
	{CellNull, "NULL"},
	{CellBool, "BOOL"},
	{CellInt, "INT"},
	{CellFloat, "FLOAT"},
	{CellString, "STRING"},
	{CellObject, "OBJECT"},
	{CellArray, "ARRAY"},
}
```

- [ ] **Step 4: Run - PASS + full `go test ./...`.**
- [ ] **Step 5: Commit** - `feat(query): CountMatches, wide-data DTO fields, CellKind enum export`.

---

### Task 4: Wails binding layer - App owns the Engine

**Files:** Modify `gui/app.go` (engine field, handle lifecycle, five new bindings), `gui/main.go` (`EnumBind`), `gui/app_test.go` (new tests), `.gitignore` (un-ignore `gui/frontend/wailsjs/`). Create `gui/testdata/` **only if** an existing fixture under `internal/cmd/testdata/` does not suffice - prefer reusing `../internal/cmd/testdata/sample.ndjson` as the existing tests do. Implements spec §8's `App` surface.

**Interfaces (produces, all Wails-bound):**
`func (a *App) OpenSource(req query.OpenRequest) (query.OpenResult, error)`;
`func (a *App) QueryRows(req query.QueryRequest) (query.RowSet, error)`;
`func (a *App) CountMatches(req query.CountRequest) (query.CountResult, error)`;
`func (a *App) Cancel(requestID string) error`;
`func (a *App) CloseSource(handle string) error`.

**Consumes:** Tasks 2 and 3's Engine methods.

**Constraints specific to this task:**
- **Every new method must work with a nil `a.ctx`.** `gui/app_test.go` constructs `NewApp()` and never calls `startup`, so `a.ctx` is nil (`gui/app.go:23`). Route all ctx use through one helper; never pass `a.ctx` to anything unguarded.
- **No Wails runtime calls** (`wr.EventsEmit` and friends) in these methods - they panic on a nil ctx and would make the whole surface untestable. E2 ships no progress events; the UI's "counting..." affordance is a spinner plus a Cancel button, and streaming progress is deferred to E3 where filtered counts get slow enough to need it.
- Opening a second source **must** `CloseSource` the first, or the memory tier's up-to-512 MiB store and the sqlite connection leak for the process lifetime.

- [ ] **Step 1: Write failing tests** in `gui/app_test.go` covering:
  - `TestAppOpenSourceAndQueryRows`: `NewApp()` (nil ctx), `OpenSource({Path: "../internal/cmd/testdata/sample.ndjson"})` returns a non-empty `Handle`, `Tier == "memory"`, non-empty `Columns`, non-empty `Profile.Fields`; then `QueryRows({Handle, Limit: 10, WantTotal: true})` returns `len(Rows) > 0`, `Rows[0].Index == 0`, `len(Rows[0].Cells) == len(rs.Columns)`.
  - `TestAppOpenSourceClosesPrevious`: open the same fixture twice; assert the two handles differ and that `QueryRows` with the **first** handle now errors (it was closed).
  - `TestAppQueryRowsUnknownHandle`: errors, does not panic.
  - `TestAppCountMatches`: match-all count equals the fixture's record count, `Exact == true`.
  - `TestAppCancelUnknownRequest`: returns an error, does not panic (nil ctx path).
  - `TestAppCloseSourceThenQuery`: after `CloseSource(h)`, `QueryRows` on `h` errors; a second `CloseSource(h)` errors rather than panicking.
  - `TestAppRowSetMarshals`: `json.Marshal` the `RowSet` from a real query and assert no error and that the output contains `"columnsTruncated"` and `"cells"` - the Wails bridge marshals exactly this.
- [ ] **Step 2: Run - FAIL** (`cd gui && go test ./... -run 'TestApp(OpenSource|QueryRows|CountMatches|Cancel|CloseSource|RowSet)' -v`).
- [ ] **Step 3: Implement.**

`gui/app.go`:

```go
// App is the Wails-bound application. Every exported method becomes a callable
// TypeScript binding. App owns exactly one query.Engine and, at most one open
// source at a time: opening a new one closes the previous handle so a memory-
// tier store (up to 512 MiB) or a sqlite connection is never leaked.
type App struct {
	ctx context.Context
	eng *query.Engine

	mu     sync.Mutex
	handle string // current open source handle; "" when none
}

func NewApp() *App { return &App{eng: query.NewEngine()} }

// reqCtx returns the context requests run under. a.ctx is nil until Wails
// calls startup, and the Go tests never call it, so fall back to Background
// rather than passing a nil ctx into the engine.
func (a *App) reqCtx() context.Context {
	if a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}

// OpenSource opens a data file for exploration and returns its structure map,
// column set, and tier (spec §8). Any previously open source is closed first.
func (a *App) OpenSource(req query.OpenRequest) (query.OpenResult, error) {
	res, err := a.eng.OpenSource(a.reqCtx(), req)
	if err != nil {
		return query.OpenResult{}, err
	}
	a.mu.Lock()
	prev := a.handle
	a.handle = res.Handle
	a.mu.Unlock()
	if prev != "" && prev != res.Handle {
		_ = a.eng.CloseSource(prev) // best effort: a stale handle is not the caller's problem
	}
	return res, nil
}

// QueryRows returns one window of rows for an open handle (spec §8).
func (a *App) QueryRows(req query.QueryRequest) (query.RowSet, error) {
	return a.eng.QueryRows(a.reqCtx(), req)
}

// CountMatches returns the exact match count for a filter (spec §8).
func (a *App) CountMatches(req query.CountRequest) (query.CountResult, error) {
	return a.eng.CountMatches(a.reqCtx(), req)
}

// Cancel interrupts an in-flight request by id (spec §8). An unknown id is a
// normal race (the request may have just finished) and returns an error the
// caller may ignore.
func (a *App) Cancel(requestID string) error { return a.eng.Cancel(requestID) }

// CloseSource releases a handle's backend.
func (a *App) CloseSource(handle string) error {
	a.mu.Lock()
	if a.handle == handle {
		a.handle = ""
	}
	a.mu.Unlock()
	return a.eng.CloseSource(handle)
}
```

Add `"sync"` and `"github.com/hoijun-kim/shape/internal/query"` to the imports. Leave `ProfileFile`, `DiffFiles`, `SchemaJSON`, `OpenFileDialog`, `SaveText` exactly as they are.

`gui/main.go` - register the enum so `CellKind` generates as a TS union:

```go
	if err := wails.Run(&options.App{
		// ... unchanged
		Bind:     []any{app},
		EnumBind: []any{query.AllCellKindValues},
	}); err != nil {
```

**VERIFY:** confirm the installed Wails version (v2.12.0) accepts `EnumBind` on `options.App` - `go build ./...` inside `gui/` is the check. If it does not compile, drop the `EnumBind` line and instead hand-write the union in `gui/frontend/src/lib/explorer/types.ts` (Task 5) as `export type CellKind = "missing" | "null" | "bool" | "int" | "float" | "string" | "object" | "array";`, and note in the task report which path was taken - Task 6's renderer must import the union from one place either way.

`.gitignore` - delete line 12 (`gui/frontend/wailsjs/`) and put the generated bindings under version control, so `npm run check` is a reproducible gate and a DTO change that breaks the frontend fails in review rather than on someone's machine:

```
 gui/frontend/dist/*
 !gui/frontend/dist/.gitkeep
 gui/frontend/node_modules/
-gui/frontend/wailsjs/
 gui/frontend/package.json.md5
```

- [ ] **Step 4: Run - PASS + full `go test ./...`.** Then regenerate and stage the bindings:
  - `cd gui && wails generate module`
  - Confirm `gui/frontend/wailsjs/go/main/App.d.ts` now declares `OpenSource`, `QueryRows`, `CountMatches`, `Cancel`, `CloseSource`, and that `gui/frontend/wailsjs/go/models.ts` contains a `query` namespace with `Cell`, `Row`, `RowSet`, `Column`, `OpenResult`, `ProfileDTO`, `CountResult`.
  - `cd gui/frontend && npm run check` - expect 0 errors.
- [ ] **Step 5: Commit** `gui/app.go`, `gui/main.go`, `gui/app_test.go`, `.gitignore`, and the now-tracked `gui/frontend/wailsjs/` - `feat(gui): bind the query engine to the Wails app`.

---

### Task 5: Frontend explorer state - store, paging math, tree math

**Files:** Create `gui/frontend/src/lib/explorer/types.ts`, `gui/frontend/src/lib/explorer/paging.ts`, `gui/frontend/src/lib/explorer/tree.ts`, `gui/frontend/src/lib/explorer/store.ts`, `gui/frontend/src/lib/explorer/paging.test.ts`, `gui/frontend/src/lib/explorer/tree.test.ts`. Modify `gui/frontend/package.json` (add `vitest` devDependency + `test` script). Implements the client half of spec §4's windowing contract.

**Interfaces (produces):**
`pageRowsFor(columnCount: number): number`;
`pageIndexOf(row: number, pageRows: number): number`;
`pagesForRange(first: number, last: number, pageRows: number): number[]`;
`class PageCache { get(i): RowSet|undefined; set(i, rs): void; has(i): boolean; clear(): void; size: number }`;
`buildTree(fields: FieldDTO[]): TreeNode[]` where `TreeNode = { name: string; path: string; children: TreeNode[]; field: FieldDTO | null }`;
`explorer` store with `{ status, error, source, pages, columns, total, totalExact, columnsTruncated, totalPaths, focusPath }` plus actions `open(path)`, `ensurePages(first, last)`, `focus(path)`, `close()`.

**Consumes:** Task 4's generated bindings. From `src/lib/explorer/` the binding paths are **three** levels up - `../../../wailsjs/go/main/App`, `../../../wailsjs/go/models` - matching the existing `src/lib/charts/*.svelte`; `wailsjs/` lives at `gui/frontend/`, not under `src/`, and `tsconfig.json` defines no path alias.

**Why vitest:** `paging.ts` and `tree.ts` are the only non-visual logic in E2, and both fail silently when wrong (a wrong page index shows the wrong rows; a wrong tree hides fields). Two test files, a devDependency, no runtime dependency added. Everything else in E2 is verified visually, per the P3 convention.

**Paging model (spec §4):** `QueryRequest.Offset` is an offset into **matching** rows and is `int64`; with the empty filter E2 sends, matching rows are all rows. Page size is column-count dependent because the whole `RowSet` crosses the webview bridge as JSON - measured, a 512-column x 500-row page is ~14 MB, while 20 columns x 500 rows is ~576 KB. `PAGE_ROW_BUDGET / columnCount`, clamped to `[40, 200]`, keeps a page under roughly 1.5 MB at any width.

- [ ] **Step 1: Write failing tests.**

`gui/frontend/src/lib/explorer/paging.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { pageRowsFor, pageIndexOf, pagesForRange, PageCache } from "./paging";

describe("pageRowsFor", () => {
  it("clamps to the max for narrow tables", () => {
    expect(pageRowsFor(1)).toBe(200);
    expect(pageRowsFor(20)).toBe(200);
  });
  it("shrinks as columns grow", () => {
    expect(pageRowsFor(512)).toBe(Math.floor(30000 / 512));
  });
  it("clamps to the min for very wide tables", () => {
    expect(pageRowsFor(5000)).toBe(40);
  });
  it("never returns 0 for a degenerate column count", () => {
    expect(pageRowsFor(0)).toBe(200);
  });
});

describe("pageIndexOf", () => {
  it("maps rows to their page", () => {
    expect(pageIndexOf(0, 100)).toBe(0);
    expect(pageIndexOf(99, 100)).toBe(0);
    expect(pageIndexOf(100, 100)).toBe(1);
  });
});

describe("pagesForRange", () => {
  it("covers a range spanning three pages", () => {
    expect(pagesForRange(95, 205, 100)).toEqual([0, 1, 2]);
  });
  it("returns one page for a range inside one page", () => {
    expect(pagesForRange(10, 20, 100)).toEqual([0]);
  });
});

describe("PageCache", () => {
  it("evicts least-recently-used beyond its cap", () => {
    const c = new PageCache(2);
    c.set(0, {} as any);
    c.set(1, {} as any);
    c.get(0);              // touch 0 so 1 is now the oldest
    c.set(2, {} as any);
    expect(c.has(0)).toBe(true);
    expect(c.has(1)).toBe(false);
    expect(c.has(2)).toBe(true);
    expect(c.size).toBe(2);
  });
});
```

`gui/frontend/src/lib/explorer/tree.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { buildTree } from "./tree";

const f = (path: string) => ({ path, types: [], presence: 1, nullRate: 0, distinct: 0, distinctExact: true, drift: false } as any);

describe("buildTree", () => {
  it("nests dotted paths under synthetic parents", () => {
    const t = buildTree([f("user.name"), f("user.age"), f("id")]);
    expect(t.map((n) => n.name)).toEqual(["user", "id"]);
    expect(t[0].field).toBe(null);            // synthetic interior node
    expect(t[0].children.map((n) => n.name)).toEqual(["name", "age"]);
    expect(t[1].children).toEqual([]);
  });
  it("keeps a real field that also has children", () => {
    const t = buildTree([f("a"), f("a.b")]);
    expect(t[0].field).not.toBe(null);        // 'a' is a drifting path: field AND parent
    expect(t[0].children.map((n) => n.name)).toEqual(["b"]);
  });
  it("treats array-element segments as one segment", () => {
    const t = buildTree([f("items[].sku")]);
    expect(t[0].name).toBe("items[]");
    expect(t[0].children[0].name).toBe("sku");
  });
  it("preserves input order, which is the profiler's alphabetized order", () => {
    const t = buildTree([f("b"), f("a")]);
    expect(t.map((n) => n.name)).toEqual(["b", "a"]);
  });
});
```

- [ ] **Step 2: Run - FAIL.** `cd gui/frontend && npm i -D vitest && npm run test` - expect module-not-found for `./paging` and `./tree`. Add to `package.json` scripts: `"test": "vitest run"`.
- [ ] **Step 3: Implement.**

`types.ts` re-exports the generated DTOs under short local names. Its `CellKind` export depends on Task 4's VERIFY outcome - **use exactly one of blocks (A) or (B) below and record which in the task report**:

```ts
import type { query } from "../../../wailsjs/go/models";

export type Cell = query.Cell;
export type Row = query.Row;
export type RowSet = query.RowSet;
export type Column = query.Column;
export type OpenResult = query.OpenResult;
export type ProfileDTO = query.ProfileDTO;
export type FieldDTO = query.FieldDTO;
export type CountResult = query.CountResult;
```

```ts
// (A) EnumBind kept (expected: wails v2.12.0 declares options.App.EnumBind).
// models.ts emits `export enum CellKind { MISSING = "missing", ... }` inside
// `namespace query`, and Cell.kind is typed as that enum. Re-export it as BOTH
// a value and a type so the renderer can compare against members. Do NOT also
// declare a string-literal union here -- comparing the enum against a bare
// "missing" is TS2367 and npm run check will reject it.
import { query } from "../../../wailsjs/go/models"; // value import, not `import type`
export const CellKind = query.CellKind;
export type CellKind = query.CellKind;
export const CELL_KINDS = Object.values(CellKind) as CellKind[];
```

```ts
// (B) EnumBind dropped. Cell.kind generates as a bare `string`; own the union here.
export const CELL_KINDS = ["missing", "null", "bool", "int", "float", "string", "object", "array"] as const;
export type CellKind = (typeof CELL_KINDS)[number];
```

`paging.ts`:

```ts
import type { RowSet } from "./types";

export const PAGE_ROW_BUDGET = 30000; // rows*columns per fetch; ~1.5 MB of JSON
export const PAGE_ROWS_MIN = 40;
export const PAGE_ROWS_MAX = 200;

/** Rows per fetched page. Wide tables fetch shorter pages: the whole RowSet
 *  crosses the webview bridge as JSON, and 512 columns x 500 rows is ~14 MB. */
export function pageRowsFor(columnCount: number): number {
  const cols = Math.max(1, columnCount | 0);
  const n = Math.floor(PAGE_ROW_BUDGET / cols);
  return Math.min(PAGE_ROWS_MAX, Math.max(PAGE_ROWS_MIN, n));
}

export function pageIndexOf(row: number, pageRows: number): number {
  return Math.floor(row / pageRows);
}

export function pagesForRange(first: number, last: number, pageRows: number): number[] {
  const a = pageIndexOf(Math.max(0, first), pageRows);
  const b = pageIndexOf(Math.max(0, last), pageRows);
  const out: number[] = [];
  for (let i = a; i <= b; i++) out.push(i);
  return out;
}

/** LRU cache of fetched pages, so scrolling back is instant and memory stays
 *  bounded regardless of how far the user scrolls. */
export class PageCache {
  private map = new Map<number, RowSet>();
  constructor(private cap = 8) {}
  get size(): number { return this.map.size; }
  has(i: number): boolean { return this.map.has(i); }
  get(i: number): RowSet | undefined {
    const v = this.map.get(i);
    if (v !== undefined) { this.map.delete(i); this.map.set(i, v); } // touch
    return v;
  }
  set(i: number, rs: RowSet): void {
    if (this.map.has(i)) this.map.delete(i);
    this.map.set(i, rs);
    while (this.map.size > this.cap) this.map.delete(this.map.keys().next().value as number);
  }
  clear(): void { this.map.clear(); }
}
```

`tree.ts` splits the profiler's dotted paths into a nav tree. **Known limitation to note in the code:** `profile.Flatten` cannot distinguish a literal dot inside a key from a nesting separator (carried forward from E1 Task 1's WATCH item); the tree inherits that ambiguity and it is not this task's to fix.

```ts
import type { FieldDTO } from "./types";

export interface TreeNode {
  name: string;         // this segment, e.g. "user" or "items[]"
  path: string;         // full dotted path from the root, matches Column.path
  children: TreeNode[];
  field: FieldDTO | null; // null for a synthetic interior node with no profile of its own
}

/** Splits dotted paths into a tree. Order is input order (the profiler's
 *  alphabetized field order), NOT the table's first-seen column order -- the
 *  two legitimately differ and are matched by path, never by index. */
export function buildTree(fields: FieldDTO[]): TreeNode[] {
  const roots: TreeNode[] = [];
  const byPath = new Map<string, TreeNode>();

  for (const f of fields) {
    const segs = f.path.split(".");
    let prefix = "";
    let siblings = roots;
    let node: TreeNode | undefined;
    for (const seg of segs) {
      prefix = prefix === "" ? seg : prefix + "." + seg;
      node = byPath.get(prefix);
      if (node === undefined) {
        node = { name: seg, path: prefix, children: [], field: null };
        byPath.set(prefix, node);
        siblings.push(node);
      }
      siblings = node.children;
    }
    if (node) node.field = f;
  }
  return roots;
}
```

`store.ts` - one Svelte 3 writable store plus actions. It owns the request lifecycle the frontend has never had: a monotonic request id per fetch, a stale-response guard, and `Cancel` for superseded requests.

```ts
import { writable, get } from "svelte/store";
import { OpenSource, QueryRows, CloseSource, Cancel } from "../../../wailsjs/go/main/App";
import type { Column, FieldDTO, OpenResult, RowSet } from "./types";
import { PageCache, pageRowsFor, pagesForRange } from "./paging";

export type Status = "idle" | "opening" | "ready" | "error";

export interface ExplorerState {
  status: Status;
  error: string;
  path: string;
  handle: string;
  tier: string;
  format: string;
  warnings: string[];
  fields: FieldDTO[];
  columns: Column[];
  columnsTruncated: boolean;
  totalPaths: number;
  total: number;        // -1 = unknown
  totalExact: boolean;
  sampled: boolean;
  skipped: number;      // ProfileDTO.skipped: malformed records the reader dropped (T8's zero-columns state)
  focusPath: string;    // sidebar-selected column path, "" = none
  fetching: boolean;
  version: number;      // bumps whenever a page lands, so views re-read the cache
}

const empty: ExplorerState = {
  status: "idle", error: "", path: "", handle: "", tier: "", format: "",
  warnings: [], fields: [], columns: [], columnsTruncated: false, totalPaths: 0,
  total: -1, totalExact: false, sampled: false, skipped: 0, focusPath: "", fetching: false, version: 0,
};

function createExplorer() {
  const { subscribe, set, update } = writable<ExplorerState>({ ...empty });
  let cache = new PageCache(8);
  let inflight = new Map<number, string>(); // page index -> requestId
  let seq = 0;
  let gen = 0; // bumps on open()/close(); a fetch from an older gen must not touch state

  async function open(path: string): Promise<void> {
    const prev = get({ subscribe });
    if (prev.handle) { void CloseSource(prev.handle).catch(() => {}); }
    gen++;
    cache = new PageCache(8);
    inflight = new Map();
    set({ ...empty, status: "opening", path });
    try {
      const res: OpenResult = await OpenSource({ path, format: "", table: "", csvRaw: false, budgetMB: 0, requestId: "" } as any);
      update((s) => ({
        ...s, status: "ready", handle: res.handle, tier: res.tier, format: res.format,
        warnings: res.warnings ?? [], fields: res.profile?.fields ?? [], columns: res.columns ?? [],
        columnsTruncated: res.columnsTruncated, totalPaths: res.totalPaths,
        total: res.rowEstimate, totalExact: res.rowExact, sampled: res.sampled,
        skipped: res.profile?.skipped ?? 0,
        focusPath: (res.columns && res.columns.length > 0) ? res.columns[0].path : "",
      }));
      await ensurePages(0, 0);
    } catch (e) {
      update((s) => ({ ...s, status: "error", error: String(e) }));
    }
  }

  /** Fetches every page covering rows [first, last] that is not already cached
   *  or in flight. Pages already in flight are left alone; pages no longer
   *  needed are cancelled, so a fast scroll does not queue dead work. */
  async function ensurePages(first: number, last: number): Promise<void> {
    const s = get({ subscribe });
    if (s.status !== "ready" || !s.handle) return;
    const myGen = gen;
    const pageRows = pageRowsFor(s.columns.length);
    const wanted = pagesForRange(first, last, pageRows);

    for (const [page, reqId] of inflight) {
      if (!wanted.includes(page)) { void Cancel(reqId).catch(() => {}); inflight.delete(page); }
    }

    const todo = wanted.filter((p) => !cache.has(p) && !inflight.has(p));
    if (todo.length === 0) return;
    update((st) => ({ ...st, fetching: true }));

    await Promise.all(todo.map(async (page) => {
      const reqId = `q${++seq}`;
      inflight.set(page, reqId);
      try {
        const rs: RowSet = await QueryRows({
          requestId: reqId, handle: s.handle, filter: {} as any, transform: {} as any,
          offset: page * pageRows, limit: pageRows, wantTotal: false,
        } as any);
        if (myGen !== gen || inflight.get(page) !== reqId) return; // superseded or stale file
        cache.set(page, rs);

        // EOF reconciliation. On the rescan tier `total` starts as
        // fileSize/avgBytes (spec §4) -- an estimate that misses in BOTH
        // directions and is systematically LOW, because avgBytes is the decoded
        // in-memory size per record while fileSize is on-disk bytes
        // (source.go:146). The backend never corrects it. A landed page is
        // ground truth for its own range, so use it: a short page (rs.truncated)
        // pins the real end; a full page at the current tail proves at least one
        // more page exists. Without this the scrollbar addresses rows that do
        // not exist (permanent skeletons, indistinguishable from a hung fetch)
        // or hides rows that do. On the exact tiers it is a no-op: pageEnd
        // already equals total on the last page.
        const pageEnd = page * pageRows + rs.rows.length;
        update((st) => {
          let total = rs.total >= 0 ? rs.total : st.total;
          let totalExact = rs.total >= 0 ? rs.totalExact : st.totalExact;
          if (rs.truncated) {
            total = pageEnd;
            // An entirely empty page past EOF bounds the end from above but does
            // not prove exactness; the next fetch converges. A short non-empty
            // page (or an empty page 0) is exact.
            totalExact = rs.rows.length > 0 || page === 0;
          } else if (!totalExact && pageEnd >= total) {
            total = pageEnd + pageRows; // full page at the tail: more to come
          }
          return {
            ...st, total, totalExact,
            columnsTruncated: rs.columnsTruncated, totalPaths: rs.totalPaths,
            version: st.version + 1,
          };
        });
      } catch (e) {
        // A fetch belonging to a file the user has already navigated away from
        // must never write to the new file's state. CloseSource does NOT cancel
        // in-flight queries (engine.go: the handle is simply deleted), so such a
        // fetch fails with "unknown handle" or a backend-closed error, never
        // "context canceled" -- the sentinel below would miss it.
        if (myGen !== gen || inflight.get(page) !== reqId) return;
        if (String(e).includes("context canceled")) return; // expected on supersede
        update((st) => ({ ...st, status: "error", error: String(e) }));
      } finally {
        if (inflight.get(page) === reqId) inflight.delete(page);
        if (inflight.size === 0) update((st) => ({ ...st, fetching: false }));
      }
    }));
  }

  function rowAt(index: number): { row: RowSet["rows"][number] | null } {
    const s = get({ subscribe });
    const pageRows = pageRowsFor(s.columns.length);
    const rs = cache.get(Math.floor(index / pageRows));
    if (!rs) return { row: null };
    return { row: rs.rows[index - Math.floor(index / pageRows) * pageRows] ?? null };
  }

  function focus(path: string): void { update((s) => ({ ...s, focusPath: path })); }

  async function close(): Promise<void> {
    const s = get({ subscribe });
    if (s.handle) { await CloseSource(s.handle).catch(() => {}); }
    gen++;
    cache.clear(); inflight.clear();
    set({ ...empty });
  }

  return { subscribe, open, ensurePages, rowAt, focus, close };
}

export const explorer = createExplorer();
```

**`wantTotal` is always `false` on the page path.** E2 sends the empty filter, so `OpenResult.RowEstimate` already carries the best total every tier can offer: exact on memory/sqlite/parquet (all three `RowCount`s return `exact == true`), an estimate on rescan (`rescan.go:94-96` returns `(rowEstimate, false)`). Passing `wantTotal: true` would gain nothing and, on the rescan tier, would cost everything - `totalExact` there is false permanently, so every page fetch would disable the early-stop at `rescan.go:229` and rescan the whole file to EOF only to return `rs.Total = r.rowEstimate`, the number the store already holds, still flagged inexact. With `wantTotal: false`, `RowSet.Total` comes back `-1` and the `rs.total >= 0 ? ... : st.total` guard leaves the open-time estimate in place, so `StatusBar` keeps rendering the honest `~N rows` until EOF reconciliation makes it exact. Never use `totalExact` as a re-request trigger. Exact filtered counts are `CountMatches`' job (Task 3), wired to UI in E3.

**`total` is authoritative only on the exact tiers.** On `rescan` it is a seed that each landed page corrects via the EOF reconciliation above; Task 6's row addressing and Task 8's row-count label both read the reconciled value.

- [ ] **Step 4: Run - PASS.** `cd gui/frontend && npm run test` (all paging + tree tests green) and `npm run check` (0 errors).
- [ ] **Step 5: Commit** - `feat(gui): explorer store, paging and tree math`.

---

### Task 6: The virtualized data table

**Files:** Create `gui/frontend/src/lib/explorer/DataTable.svelte`, `gui/frontend/src/lib/explorer/CellView.svelte`, `gui/frontend/src/lib/explorer/widths.ts`. Implements spec §3 (cell rendering) and §3.3 of the product spec (virtualized rows x columns, huge-file scroll).

**Interfaces (produces):** `DataTable` props `{ columns: Column[]; total: number; focusPath: string }`, event `focus` with `detail: { path: string }`; `CellView` props `{ cell: Cell; align: "left" | "right" }`; `columnWidths(columns: Column[]): number[]` and `prefixSums(widths: number[]): number[]`.

**Consumes:** Task 5's `explorer` store (`ensurePages`, `rowAt`, `version`), `types.ts`, `paging.ts`.

**Virtualization model (both axes, hand-rolled):**
- Fixed row height `ROW_H = 28`. A spacer `div` of height `total * ROW_H` inside a scroll container gives a native scrollbar; only the visible rows are rendered, absolutely positioned at `top: i * ROW_H`.
- Visible rows: `first = floor(scrollTop / ROW_H) - OVERSCAN_ROWS`, `last = ceil((scrollTop + clientHeight) / ROW_H) + OVERSCAN_ROWS`, both clamped to `[0, total-1]`. `OVERSCAN_ROWS = 8`.
- Column widths come from `columnWidths()` once per column set; a prefix-sum array gives total width and lets a binary search find the first/last visible column from `scrollLeft`. `OVERSCAN_COLS = 3`. At `MaxColumns=512` this is what keeps the DOM node count flat.
- On every scroll (rAF-throttled) call `explorer.ensurePages(first, last)`. Rows whose page has not landed render as a skeleton row (a dim bar per cell), never as blank space, so scrolling never looks broken.
- The header row is `position: sticky; top: 0`; the row-index gutter is `position: sticky; left: 0`. The gutter shows `Row.Index` - the **absolute record ordinal**, not the window position (spec §3).

**Cell rendering rules (spec §3, all eight kinds - do not collapse any two):**

| Kind | Render | Align |
|---|---|---|
| `string` | the text, single line, `text-overflow: ellipsis`, `title` = full value. **`previewCap=200` bounds containers only - a long string arrives in full and the UI must clip it.** | left |
| `int`, `float` | **`cell.str`** (exact source literal), monospace. Never `cell.num`. | right |
| `bool` | `true` / `false`, monospace, `--kind-bool` tint | left |
| `null` | the literal text `null`, italic, `--text-muted` | left |
| `missing` | empty cell with a faint diagonal-hatch background, `title="missing"` | - |
| `object` | `cell.str` preview in monospace + a `{n}` count badge from `cell.count`, plus a trailing `...` when `cell.hasMore` | left |
| `array` | same as object but the badge reads `[n]` | left |

**Every `Cell` field except `kind` is `omitempty` (`columns.go:157-164`), so the generated `query.Cell` types them optional and `""`, `false`, `0` arrive as `undefined` - these are the common cases, not edge cases.** Read them as `cell.str ?? ""`, `cell.count ?? 0`, `cell.bool === true`, `cell.hasMore === true`. Interpolating a raw optional field renders the literal text `undefined` (Svelte 3 compiles `{x}` to `x + ""`).

**Color tokens:** `src/app.css` defines `--kind-number/string/bool/array/object/null` and `--text-muted` - there is **no** `--kind-int`, `--kind-float`, or `--muted`. `CellKind` carries raw `int`/`float` (the fold to `number` lives in `internal/visual/geometry.go:familyOf`, which E2 does not use), so the frontend must fold them. Define, local to `CellView.svelte`:

```ts
const KIND_TOKEN: Record<string, string> = {
  int: "number", float: "number", bool: "bool", string: "string",
  object: "object", array: "array", null: "null",
};
```

and resolve color as ``KIND_TOKEN[cell.kind] ? `var(--kind-${KIND_TOKEN[cell.kind]})` : "var(--text-muted)"``, mirroring the guard in `charts/TypeMixBar.svelte:12`.

- [ ] **Step 1: Build it.**

`widths.ts`:

```ts
import type { Column } from "./types";

export const MIN_W = 80;
export const MAX_W = 320;

/** Width heuristic: header text sets the floor, the column's type sets the
 *  natural width (numbers and bools are narrow, containers and strings wide). */
export function columnWidths(columns: Column[]): number[] {
  return columns.map((c) => {
    const header = 16 + c.name.length * 7.2;
    const natural =
      c.container ? 260 :
      c.type === "bool" ? 90 :
      c.type === "int" || c.type === "float" ? 120 :
      c.type === "mixed" ? 200 : 180;
    return Math.round(Math.min(MAX_W, Math.max(MIN_W, header, natural)));
  });
}

/** prefixSums(widths)[i] is the x offset of column i; the last element is the
 *  total width. Used for horizontal virtualization by binary search. */
export function prefixSums(widths: number[]): number[] {
  const out = new Array<number>(widths.length + 1);
  out[0] = 0;
  for (let i = 0; i < widths.length; i++) out[i + 1] = out[i] + widths[i];
  return out;
}
```

`CellView.svelte` renders exactly the table above, one `{#if}` chain on `cell.kind`. Under Task 5's branch (A) the chain must compare **enum members** - `{#if cell.kind === CellKind.MISSING}` ... `CellKind.ARRAY}`, member names being Task 3's `TSName` values - not string literals; under branch (B) it compares the string literals directly. `npm run check` catches the wrong form (TS2367). `DataTable.svelte` owns the scroll container, the sticky header (each header cell is a `<button>` dispatching `focus` with its `path`, and carries `.focused` when `column.path === focusPath`), the sticky index gutter, the rAF-throttled scroll handler, and a `focusPath` reactive block that scrolls the focused column into view via `scrollLeft = prefix[i] - 24` when focus changes from outside.

- [ ] **Step 2: BUILD + RUN + SCREENSHOT (the star check).** `cd gui && wails build`, run the binary, drop `internal/cmd/testdata/sample.ndjson` on it, then a wide/deep fixture. **Capture screenshots and look at them.** Check: rows render with real values; numbers right-aligned, monospace, and in the `--kind-number` blue rather than muted gray; `null` visibly different from `missing`; container cells show a preview plus a count badge - `sample.ndjson` row 3 is `"tags":[]`, whose badge must read `[0]`, not `[undefined]`, and a `false` bool and an empty string must render `false` and blank, never `undefined`; the header stays put while scrolling down and the gutter stays put while scrolling right; scrolling 10k rows stays smooth and shows skeleton rows briefly rather than blank space. On a rescan-tier fixture, drag the scrollbar to the very bottom: the last row must be a real row, not a skeleton, and the status count must have settled to an exact `N`. A blank frame, a misaligned header, cells that shift horizontally as rows load, permanent skeleton rows at the tail, or a literal `undefined` anywhere is a failure - iterate before committing.
- [ ] **Step 3: Commit** - `feat(gui): virtualized data table with typed cell rendering`.

---

### Task 7: The structure-map sidebar

**Files:** Create `gui/frontend/src/lib/explorer/StructureMap.svelte`, `gui/frontend/src/lib/explorer/TreeNode.svelte`, `gui/frontend/src/lib/explorer/KindChip.svelte`, and `gui/testdata/nested.ndjson` - **the repo has no nested fixture** (`internal/cmd/testdata/` is entirely flat scalars and flat arrays, and `profile.Flatten` emits array elements as a single `path[]` segment with no dot, so arrays alone produce no tree depth). Make it ~20 records with `user.name`, `user.address.city`, `items[].sku`, `items[].qty`, and a `meta` path that is a scalar in some records and an object in others so the drift badge has something to show. Task 9's star screenshot needs this file too. Implements product spec §3.2 ("a field tree with type, presence/null, distinct, distribution, and drift/health flags... the profiler, demoted to a navigation sidebar").

**Interfaces (produces):** `StructureMap` props `{ fields: FieldDTO[]; focusPath: string; columnPaths: Set<string> }`, event `focus` with `detail: { path: string }`; `TreeNode` props `{ node: TreeNode; depth: number; focusPath: string; columnPaths: Set<string> }`, same event bubbled; `KindChip` props `{ kind: string }`.

**Consumes:** Task 5's `buildTree` and `types.ts`.

**Rules:**
- The sidebar renders `OpenResult.Profile.fields` (`FieldDTO`), **not** `visual.FieldCard`. `Backend.Profile()`'s own doc comment names this exact use ("the sidebar structure map computed when the source was opened"), and `OpenResult` already carries the DTO - no second profiling pass, no new binding.
- Each row shows: indentation by depth, an expand/collapse caret for nodes with children, the segment name, a `KindChip` for the dominant type (from the highest-share entry in `field.types`, or the literal `mixed` when `field.drift`), a compact presence/null bar, and `distinct` when known. A synthetic interior node (`field === null`) shows only the caret and name.
- A field that is **not** in the table's column set (array-element paths, pure interior objects, and paths cut by `MaxColumns`) renders dimmed and is not clickable for focus - it has no column to focus. `columnPaths` is that set, built once by the parent from `columns.map(c => c.path)`.
- **Click = focus only.** Dispatch `focus`; the parent sets `focusPath`, which Task 6's `DataTable` scrolls into view and highlights. **No filtering** - that is E3's condition builder (spec §5), and a one-off filter path here would create a model to unwind.
- Focus is bidirectional: a header click in `DataTable` sets the same `focusPath`, which the sidebar reflects with a `.focused` ring, and the sidebar auto-expands ancestors of the focused path.
- `KindChip` maps its `kind` prop through the same `KIND_TOKEN` table Task 6 defines before building `var(--kind-*)`: `FieldDTO.Types[].Kind` carries raw `profile.JSONKind` values (`int`/`float`) which have no token of their own, and unmapped kinds - including the literal `mixed` - fall back to `--text-muted`, matching the `KNOWN_KINDS` guard in `FieldCard.svelte:14-16`.
- Reuse `charts/Meter.svelte` for presence/null and `charts/Badge.svelte` for the drift flag by passing the plain props they already take (`presenceRate`, `nullRate`, `presenceText`, `nullText`, `nullStatus` / `severity`, `icon`, `label`) - map `FieldDTO` to those props in `TreeNode.svelte`. Do **not** import `internal/visual` shapes or add a Go adapter.
- Keyboard: each clickable row is `role="button" tabindex="0"` handling Enter and Space, matching `FieldCard.svelte:37-42`.

- [ ] **Step 1: Build it.**
- [ ] **Step 2: BUILD + RUN + SCREENSHOT.** `cd gui && wails build`, open `gui/testdata/nested.ndjson`. **Look at the screenshots.** Check: nested paths nest visually; carets expand/collapse; clicking a field scrolls that column into view in the table and both the sidebar row and the column header show as focused; clicking a table header highlights the matching sidebar row and expands its ancestors; non-column fields are dimmed and inert; presence bars and drift badges render in both themes; numeric chips show the `--kind-number` blue, not muted gray. A flat list, a dead click, or a sidebar that scrolls the wrong column is a failure.
- [ ] **Step 3: Commit** - `feat(gui): structure-map sidebar with column focus`.

---

### Task 8: Explorer shell - layout, status bar, and the states

**Files:** Create `gui/frontend/src/lib/explorer/Explorer.svelte`, `gui/frontend/src/lib/explorer/StatusBar.svelte`. Modify `gui/frontend/src/App.svelte` (route to `Explorer`, keep the file-drop and open-dialog paths, stop importing the P3 dashboard components), `gui/frontend/src/lib/Header.svelte` (show source/tier instead of profile counts). Implements spec §4's honesty requirements and the product spec's core-loop first two hops.

**Interfaces (produces):** `Explorer` props `{}` (reads the `explorer` store directly); `StatusBar` props `{ tier: string; total: number; totalExact: boolean; sampled: boolean; rowsLoaded: boolean; columnCount: number; columnsTruncated: boolean; totalPaths: number; warnings: string[]; fetching: boolean }`.

**Consumes:** Tasks 5-7.

**Layout:** `Explorer` is a two-pane row inside the existing `.body`: `StructureMap` in a left pane (`flex: 0 0 300px`, collapses under 900px like the current `.detail-pane` does at `App.svelte:188-198`), `DataTable` filling the rest, `StatusBar` pinned along the bottom.

**States - every one must be handled, none may render as a blank frame:**

| State | Trigger | Render |
|---|---|---|
| idle | no file opened | the existing `FileDrop.svelte`, unchanged |
| opening | `status === "opening"` | a skeleton table (header bar + dim rows), not the current full-body "Profiling..." text that blanks the screen |
| ready, rows pending | page not yet cached | skeleton rows inside the real table (Task 6) |
| ready, zero rows | `total === 0` | centered "No rows in this file" plus the column count |
| ready, zero columns | `columns.length === 0` | centered "No columns detected" plus `$explorer.skipped` when > 0 |
| error | `status === "error"` | the existing `role="alert"` bar with the message and a Retry button that re-runs `open(path)` |
| large file | `sampled === true` | the `warnings` strings rendered verbatim in the status bar - including `"large file - streaming mode (totals are estimates)"`, which must not be reworded |
| estimated total, EOF reached | `rs.truncated` on the last fetched page (Task 5 reconciles it) | the count switches from `~N` to an exact `N` and the scrollbar shortens to the real end; no skeleton rows remain below the last real row |
| wide file | `columnsTruncated` | `showing 512 of {totalPaths} columns` in the status bar |
| counting | `fetching === true` | a subtle "loading..." pip in the status bar; never a blanking overlay |

**Row-count display (spec §4's three honest states):** `totalExact` -> `1,234 rows`; estimate (`sampled`) -> `~1,234 rows`; unknown (`total === -1`) -> `counting...`. Never present an estimate as exact.

**`App.svelte` changes:** keep `OnFileDrop`/`OnFileDropOff`, `OpenFileDialog`, and the theme toggle exactly as they are; replace the `load()` body so it calls `explorer.open(path)` instead of `ProfileFile`; render `<Explorer />` where the dashboard was. **Remove the imports of `KpiRow`, `FieldGrid`, `FieldDetail` - do not delete the files.** The multi-file-drop seam at `App.svelte:65-71` stays as-is (still opens `paths[0]`); the DiffView it anticipates remains unbuilt and out of scope.

- [ ] **Step 1: Build it.**
- [ ] **Step 2: BUILD + RUN + SCREENSHOT every state.** `cd gui && wails build`. Exercise: launch with no file (drop zone); open `sample.ndjson` (ready); open a >512 MiB or `budgetMB`-forced rescan source (streaming-mode warning + `~N rows`), then scroll it to roughly row 5,000 and confirm each page lands at interactive latency - a multi-second stall per page means `wantTotal` is being sent and the backend is re-scanning to EOF - and drag to the very bottom to confirm the count settles to an exact `N` with no skeleton tail; open a wide fixture (`showing 512 of N columns`); open a nonexistent path via a stale drop (error bar + Retry); open a CSV with a header but no data rows (zero rows). **Look at each screenshot.** Then re-open a second file and confirm the first handle was closed (no memory growth across ten opens in Task Manager / `ps`). A blank frame in any state, a stale row set after switching files, or an estimate shown without its `~` is a failure.
- [ ] **Step 3: Commit** - `feat(gui): explorer shell, status bar and state handling`.

---

### Task 9: Full-stack verification and the star screenshot

**Files:** Modify `gui/README.md` (document the explorer view and the `npm run test` script). No source changes unless verification finds a defect - if it does, fix it here and say so in the report.

**Consumes:** everything.

- [ ] **Step 1: Verify the whole stack.**
  - `CGO_ENABLED=0 go build ./...` - passes.
  - `go test ./... -count=2` - all 16 packages green, no flakes across both runs.
  - `grep -n 'context.Background()' internal/query/*.go` - matches only in `_test.go` files.
  - `cd gui/frontend && npm run check` - 0 errors, 0 warnings; `npm run test` - green.
  - `cd gui && wails build` - succeeds; `gui/frontend/wailsjs/` is tracked and matches a fresh `wails generate module` (regenerate, then `git diff --exit-code gui/frontend/wailsjs/`).
  - Confirm the CLI is untouched: `go run ./cmd/shape profile internal/cmd/testdata/sample.ndjson` still prints the profile.
- [ ] **Step 2: The star screenshot.** Open `gui/testdata/nested.ndjson` (or a larger nested file), scroll deep, focus a nested field from the sidebar, and capture the single frame that would go in the README: structure map on the left, real rows filling the view, honest row count in the status bar. **Look at it.** If it does not read as "I dropped a messy file and I am browsing my data", say so in the report rather than declaring success - that judgment is the deliverable of this task.
- [ ] **Step 3: Commit** - `docs(gui): document the explorer view` (plus any `fix(gui):`/`fix(query):` commits the verification produced, each separate).

---

## Self-Review

**Coverage (E2 from product spec §5 + engine spec §3/§4/§8):** filter-only cache key + LRU (T1, closes I2) · ctx threading + cancel registry + `QueryRequest.RequestID` finally read (T2, closes I3) · `CountMatches` + wide-data DTO fields + `CellKind` enum export (T3) · Wails binding layer with handle lifecycle (T4) · explorer store, paging math, tree math (T5) · two-axis virtualized table with all eight `CellKind`s (T6) · structure-map sidebar with bidirectional column focus (T7) · shell, status bar, and every empty/loading/error/sampled/wide state (T8) · full-stack verification and the star screenshot (T9).

**Explicitly NOT in this plan, with owners:** the filter condition builder and global search (E3) · `Transform` column select/rename/flatten and export in any form, including `ExportQuery` and `RowEncoder` implementations (E4) · jq/SQL `Codegen` and SQL-WHERE pushdown (E5) · the nested tree view, `GetCell`, and launch polish (E6). Also deferred with reasons: **streaming `shape:progress` events** (E2 ships a spinner plus Cancel; progress plumbing needs engine callbacks and only pays off once filtered counts get slow, i.e. E3) · **sorting** (no backend exists; row order is reader order per spec §9) · **column resize/pin/reorder** (reorder collides with E4's `Transform.Select` model) · **`Backend.RowCount` returning an error** (it returns `(0,false)` on a cancelled ctx; a richer signature is not worth the churn) · the **multi-file-drop DiffView seam** at `App.svelte:65-71`, untouched.

**Placeholder note:** Task 4's `EnumBind` carries an explicit VERIFY with a stated fallback (a hand-written union in `types.ts`), because `EnumBind` availability in the installed Wails v2.12.0 was not confirmed by a build during planning. Each outcome has a matching, explicitly-written `types.ts` block in Task 5 and a matching comparison form in Task 6's `CellView`, so exactly one definition of `CellKind` reaches the renderer either way. Note the EnumBind path generates a TS `enum`, not a string-literal union, so `cell.kind` must be compared against `CellKind.*` members there.

**Type consistency:** `CompiledFilter.Key()` (T1) is what `CompiledPlan.FilterKey()` (T1) returns and what `memBackend.matchBitsetFor` (T1) and `CountMatches` (T3) key on. `Engine.begin`/`Engine.Cancel` (T2) are used by `QueryRows` (T2) and `CountMatches` (T3) and exposed as `App.Cancel` (T4). `ColumnsTruncated`/`TotalPaths` are the same two field names on `OpenResult` and `RowSet` (T3), read by `store.ts` (T5) and displayed by `StatusBar` (T8). `FieldDTO`/`ProfileDTO` flow `OpenResult.Profile` (T3) -> `types.ts` (T5) -> `buildTree` (T5) -> `StructureMap` (T7); `Column.path` is the only key joining sidebar to table - never an index, because `ProfileDTO.Fields` is alphabetized while columns are first-seen. `pageRowsFor` (T5) is called by both `store.ts` and `DataTable.svelte` (T6) so the fetch window and the render window can never disagree. `KIND_TOKEN` is defined in `CellView.svelte` (T6) and reused by `KindChip` (T7); `skipped` is stored by T5 and read by T8's zero-columns state; `gui/testdata/nested.ndjson` is created by T7 and reused by T9's star screenshot.

**Determinism / memory / cancellation checks:** the match cache is bounded at `maxMatchCacheEntries=16` and evicted LRU, with an explicit eviction and a touch test (T1) · a cancelled compute is never cached (T1) · cancellation is tested **mid-flight**, not only pre-cancelled, closing the test-adequacy gap the I-T5 and I-T7 re-reviews left open (T2, T3) · no `context.Background()` survives outside tests, checked by grep in T9 · every DTO field ordering stays map-iteration-free (`typeShares` already sorts; nothing new introduces a map) · opening a second source closes the first, tested (T4) and re-checked for real memory growth over ten opens (T8) · page memory is bounded by an 8-page LRU regardless of scroll distance (T5) · DOM node count is bounded by row and column overscan regardless of source size (T6).
