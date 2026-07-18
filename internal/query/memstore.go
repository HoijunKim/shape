package query

import (
	"container/list"
	"context"
	"fmt"
	"math/bits"
	"sync"
	"time"

	"github.com/hoijun-kim/shape/internal/profile"
)

// maxMatchCacheEntries caps how many distinct match bitsets one memBackend
// retains. Each bitset is len(records)/8 bytes, so with the 512 MiB ingest
// budget an entry is at most a few MiB and the cache is bounded by a small
// constant multiple of that regardless of how many filters a session tries.
// Eviction is least-recently-used: an evicted filter simply recomputes on its
// next use (a latency cost, never a wrong answer).
const maxMatchCacheEntries = 16

// var _ Backend ensures memBackend satisfies the Backend interface at
// compile time, so any accidental signature drift fails the build here
// rather than surfacing later at a call site.
var _ Backend = (*memBackend)(nil)

// bitset is a compact, fixed-size bitset (one bit per record index): the
// memBackend match cache keyed by CompiledFilter.Key() (spec §4).
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
	// matchCache holds one match bitset per CompiledFilter.Key(). Query and
	// Count share it: both key on the filter alone, so scrolling a window and
	// counting the same filter never scan the records twice. Capped at
	// maxMatchCacheEntries, evicting least-recently-used.
	matchCache map[string]*list.Element // key -> element holding *matchEntry
	matchLRU   *list.List               // front = most recently used
}

// matchEntry is the value stored in memBackend.matchLRU: a cached bitset
// together with the key it was cached under, so evicting the LRU tail can
// delete its map entry without a reverse lookup.
type matchEntry struct {
	key string
	bs  *bitset
}

// newMemBackend wraps already-decoded records (as produced by OpenSource's
// ingest pass) into a memBackend, given the ColumnModel and ProfileResult
// computed over the same records.
func newMemBackend(records []any, cm *ColumnModel, prof profile.ProfileResult) *memBackend {
	return &memBackend{
		records:    records,
		cm:         cm,
		prof:       prof,
		matchCache: make(map[string]*list.Element),
		matchLRU:   list.New(),
	}
}

// Columns returns the base ColumnModel this backend was built with.
func (m *memBackend) Columns() *ColumnModel { return m.cm }

// Profile returns the sidebar structure map computed at open.
func (m *memBackend) Profile() profile.ProfileResult { return m.prof }

// RowCount returns the exact record count: every record is already in RAM. A
// cancelled ctx returns (0, false).
func (m *memBackend) RowCount(ctx context.Context) (n int64, exact bool) {
	if ctx.Err() != nil {
		return 0, false
	}
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

	bs, err := m.matchBitsetFor(ctx, p.Filter)
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
// the same matchCache as Query, keyed by f.Key() -- the content hash both
// Query and Count key on, so the two share one bitset per logical filter.
func (m *memBackend) Count(ctx context.Context, f *CompiledFilter) (total int64, exact bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	bs, err := m.matchBitsetFor(ctx, f)
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
	m.matchCache = make(map[string]*list.Element)
	m.matchLRU = list.New()
	return nil
}

// matchBitsetFor returns the cached match bitset for cf, computing and
// storing it under cf.Key() on a miss.
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
// goroutine's result is stored and the LRU is trimmed to maxMatchCacheEntries.
// A cancelled/errored compute is never locked back in, so it can never be
// cached.
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

// computeMatchBitset scans every record in m.records exactly once and
// returns a bitset with bit i set iff f matches records[i]. f.Match handles
// a nil *CompiledFilter (or a nil/empty-Filter-compiled predicate) as
// match-all, so an empty filter naturally yields an all-set bitset here with
// no special-casing.
//
// ctx is checked every cancelCheckStride records -- the SAME package-level
// constant rescan.go declares (not a locally shadowed copy: CQ-6 review fix,
// since a function-local const of the same name agreeing with the shared one
// only by coincidence would let a future change to one silently diverge from
// the other, breaking tests that assert against the package-level constant
// with a misleading message): a cancelled/expired ctx aborts the scan and
// returns the error with a nil bitset, so a caller changing filters in the
// GUI can cancel a large cold scan (up to the full 512 MiB record budget,
// potentially regex-driven) instead of blocking until it completes.
func (m *memBackend) computeMatchBitset(ctx context.Context, f *CompiledFilter) (*bitset, error) {
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
