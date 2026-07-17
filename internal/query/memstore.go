package query

import (
	"context"
	"fmt"
	"math/bits"
	"sync"
	"time"

	"github.com/hoijun-kim/shape/internal/profile"
)

// var _ Backend ensures memBackend satisfies the Backend interface at
// compile time, so any accidental signature drift fails the build here
// rather than surfacing later at a call site.
var _ Backend = (*memBackend)(nil)

// bitset is a compact, fixed-size bitset (one bit per record index): the
// memBackend match cache keyed by CompiledPlan.FilterKey() (spec §4).
type bitset struct {
	words []uint64
	n     int // number of addressable bits (== len(records) at construction)
}

// newBitset returns a bitset sized to hold n bits, all initially clear.
func newBitset(n int) *bitset {
	if n < 0 {
		n = 0
	}
	return &bitset{words: make([]uint64, (n+63)/64), n: n}
}

// Set marks bit i (i must be in [0,n); Set does not bounds-check, matching
// Get/Count's simplicity -- computeMatchBitset, the only caller, always
// iterates i over exactly [0,n)).
func (b *bitset) Set(i int) {
	b.words[i/64] |= 1 << uint(i%64)
}

// Get reports whether bit i is set. An out-of-range i (negative or >= n)
// reports false rather than panicking.
func (b *bitset) Get(i int) bool {
	if i < 0 || i >= b.n {
		return false
	}
	return b.words[i/64]&(1<<uint(i%64)) != 0
}

// Count returns the number of set bits.
func (b *bitset) Count() int64 {
	var c int64
	for _, w := range b.words {
		c += int64(bits.OnesCount64(w))
	}
	return c
}

// memBackend is the Tier-1 in-memory Backend (spec §4): every record
// decoded at open lives in records, so Query/Count/Export never re-read or
// re-decode a source. Match results for a given CompiledFilter are cached as
// a bitset so re-windowing (scrolling) an unchanged filter never re-runs the
// (possibly expensive: regex, nested paths) compiled predicate against every
// record again -- only the cheap O(1) bit tests needed to walk to the
// requested window.
type memBackend struct {
	records []any // decoded records, in source/reader order; never mutated after construction
	cm      *ColumnModel
	prof    profile.ProfileResult

	mu sync.Mutex

	// filterCache holds one match bitset per CompiledPlan.FilterKey(), the
	// cache Query uses. FilterKey() hashes (Filter, Transform) together
	// (Task 4), so in the rare case a caller re-plans with an unchanged
	// Filter but a different Transform, this cache computes a fresh
	// (logically identical) bitset rather than reusing the prior one --
	// FilterKey is the cache key spec §4 and the task brief name
	// explicitly ("keyed by p.FilterKey()"), and only Filter determines
	// membership, so this is a correctness-neutral, at-most-a-constant-
	// factor inefficiency versus a filter-only key, not a bug.
	filterCache map[string]*bitset

	// countCache serves Count, which is handed a bare *CompiledFilter (no
	// CompiledPlan, hence no FilterKey()) by the Backend interface. It is
	// keyed by the *CompiledFilter's own pointer identity: using a pointer
	// as a Go map key keeps the pointed-to CompiledFilter reachable for as
	// long as the cache entry exists, so a live entry can never later
	// alias a different CompiledFilter that happens to reuse a freed
	// address -- the cache is safe for the backend's whole lifetime with
	// no extra bookkeeping. Two different *CompiledFilter values compiled
	// from the same logical Filter simply miss each other's cache entry
	// (each gets its own, correctly computed); that's a missed-reuse
	// opportunity, never a correctness issue.
	countCache map[*CompiledFilter]*bitset
}

// newMemBackend wraps already-decoded records (as produced by OpenSource's
// ingest pass) into a memBackend, given the ColumnModel and ProfileResult
// computed over the same records.
func newMemBackend(records []any, cm *ColumnModel, prof profile.ProfileResult) *memBackend {
	return &memBackend{
		records:     records,
		cm:          cm,
		prof:        prof,
		filterCache: make(map[string]*bitset),
		countCache:  make(map[*CompiledFilter]*bitset),
	}
}

// Columns returns the base ColumnModel this backend was built with.
func (m *memBackend) Columns() *ColumnModel { return m.cm }

// Profile returns the sidebar structure map computed at open.
func (m *memBackend) Profile() profile.ProfileResult { return m.prof }

// RowCount returns the exact record count: every record is already in RAM.
func (m *memBackend) RowCount() (n int64, exact bool) {
	return int64(len(m.records)), true
}

// Query applies p's compiled filter over m.records (using the cached match
// bitset for p.FilterKey(), computing it once per distinct key) and
// materializes only the window [w.Offset, w.Offset+w.Limit) of matching
// records via p.Transform.Project. Total is the bitset's exact match count
// (always computed: for an in-RAM store this is a cheap O(words) popcount
// over an already-built bitset, so there is no latency reason to skip it
// even when wantTotal is false -- see the doc comment on Backend.Query for
// why other, non-RAM tiers make wantTotal meaningful).
func (m *memBackend) Query(ctx context.Context, p *CompiledPlan, w Window, wantTotal bool) (RowSet, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return RowSet{}, err
	}
	if p == nil {
		return RowSet{}, fmt.Errorf("query: memBackend.Query: nil CompiledPlan")
	}

	bs, err := m.matchBitsetForPlan(ctx, p)
	if err != nil {
		return RowSet{}, err
	}

	limit := w.Limit
	if limit < 0 {
		limit = 0
	}
	offset := w.Offset
	if offset < 0 {
		offset = 0
	}

	rows := make([]Row, 0, limit)
	var scanned int64
	var skipped int64
	for i, rec := range m.records {
		if !bs.Get(i) {
			continue
		}
		scanned = int64(i + 1)
		if skipped < offset {
			skipped++
			continue
		}
		if len(rows) >= limit {
			break
		}
		rows = append(rows, p.Transform.Project(rec, int64(i)))
	}

	rs := RowSet{
		Columns:   p.Transform.Columns(),
		Rows:      rows,
		Offset:    w.Offset,
		Scanned:   scanned,
		Truncated: len(rows) < limit,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	_ = wantTotal // see doc comment: memBackend always computes the exact total
	rs.Total = bs.Count()
	rs.TotalExact = true
	return rs, nil
}

// Count returns the exact number of records matching f, using (and caching)
// the same bitset machinery as Query, keyed by f's own pointer identity
// (see countCache's doc comment).
func (m *memBackend) Count(ctx context.Context, f *CompiledFilter) (total int64, exact bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	bs, err := m.matchBitsetForFilter(ctx, f)
	if err != nil {
		return 0, false, err
	}
	return bs.Count(), true, nil
}

// Export streams every record matching p.Filter, projected via
// p.Transform, through enc, in source order. Unlike Query it does not
// materialize a bitset or a Row slice up front: Export's whole job is a
// single linear pass, so there is nothing to cache or window, and streaming
// keeps memory bounded independent of how many records match (spec §4/§8:
// export is never capped).
func (m *memBackend) Export(ctx context.Context, p *CompiledPlan, enc RowEncoder) (rows int64, err error) {
	if p == nil {
		return 0, fmt.Errorf("query: memBackend.Export: nil CompiledPlan")
	}
	if enc == nil {
		return 0, fmt.Errorf("query: memBackend.Export: nil RowEncoder")
	}

	const cancelCheckStride = 1024
	var n int64
	for i, rec := range m.records {
		if i%cancelCheckStride == 0 {
			if err := ctx.Err(); err != nil {
				return n, err
			}
		}
		if !p.Filter.Match(rec) {
			continue
		}
		if err := enc.Encode(p.Transform.Project(rec, int64(i))); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Close drops the cached bitsets. memBackend holds no other closable
// resource (records are already fully decoded in RAM, no open file handle),
// so this never errors.
func (m *memBackend) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.filterCache = make(map[string]*bitset)
	m.countCache = make(map[*CompiledFilter]*bitset)
	return nil
}

// matchBitsetForPlan returns the cached match bitset for p.Filter, computing
// and storing it under p.FilterKey() on a cache miss.
//
// The compute itself happens OUTSIDE m.mu (double-checked locking): (1) lock,
// check the cache, unlock on a hit; (2) on a miss, unlock and run the
// (possibly expensive, ctx-checked) scan with no lock held, so unrelated
// concurrent Query/Count calls are never blocked behind it and a
// long/cancelled scan never holds the lock; (3) re-lock and re-check --
// another goroutine may have finished computing the same key first, in which
// case its bitset is used (last-write-in-the-lock-is-irrelevant: both
// computations are deterministic and produce an identical result over the
// same immutable m.records, so either winning is correct) -- otherwise this
// goroutine's result is stored. A cancelled/errored compute is never locked
// back in, so it can never be cached.
func (m *memBackend) matchBitsetForPlan(ctx context.Context, p *CompiledPlan) (*bitset, error) {
	key := p.FilterKey()

	m.mu.Lock()
	if bs, ok := m.filterCache[key]; ok {
		m.mu.Unlock()
		return bs, nil
	}
	m.mu.Unlock()

	bs, err := m.computeMatchBitset(ctx, p.Filter)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.filterCache[key]; ok {
		bs = existing
	} else {
		m.filterCache[key] = bs
	}
	m.mu.Unlock()
	return bs, nil
}

// matchBitsetForFilter returns the cached match bitset for the standalone
// filter f (Count's call path -- see countCache's doc comment), computing
// and storing it under f's pointer identity on a cache miss. Uses the same
// off-lock double-checked pattern as matchBitsetForPlan; see its doc comment.
func (m *memBackend) matchBitsetForFilter(ctx context.Context, f *CompiledFilter) (*bitset, error) {
	m.mu.Lock()
	if bs, ok := m.countCache[f]; ok {
		m.mu.Unlock()
		return bs, nil
	}
	m.mu.Unlock()

	bs, err := m.computeMatchBitset(ctx, f)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.countCache[f]; ok {
		bs = existing
	} else {
		m.countCache[f] = bs
	}
	m.mu.Unlock()
	return bs, nil
}

// computeMatchBitset scans every record in m.records exactly once and
// returns a bitset with bit i set iff f matches records[i]. f.Match handles
// a nil *CompiledFilter (or a nil/empty-Filter-compiled predicate) as
// match-all, so an empty filter naturally yields an all-set bitset here with
// no special-casing.
//
// ctx is checked every cancelCheckStride records (mirroring Export's
// discipline): a cancelled/expired ctx aborts the scan and returns the error
// with a nil bitset, so a caller changing filters in the GUI can cancel a
// large cold scan (up to the full 512 MiB record budget, potentially
// regex-driven) instead of blocking until it completes.
func (m *memBackend) computeMatchBitset(ctx context.Context, f *CompiledFilter) (*bitset, error) {
	const cancelCheckStride = 4096
	bs := newBitset(len(m.records))
	for i, rec := range m.records {
		if i%cancelCheckStride == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if f.Match(rec) {
			bs.Set(i)
		}
	}
	return bs, nil
}
