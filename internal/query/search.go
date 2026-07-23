package query

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

// compileSearch returns a predicate that reports whether a record has any
// scalar leaf whose text contains query (case-insensitive, Unicode via
// strings.ToLower -- consistent with the filter's OpContains, NOT ASCII-only).
// An empty query returns nil (match-all): a blank search box costs no predicate
// and changes no result (decision 6). The query is lowercased once here; each
// leaf is lowercased as it is tested.
func compileSearch(query string) func(rec any) bool {
	if query == "" {
		return nil
	}
	lowered := strings.ToLower(query)
	return func(rec any) bool {
		return matchesSearch(rec, lowered)
	}
}

// matchesSearch reports whether any scalar LEAF of v, rendered as text and
// lowercased, contains lowered. It recurses map[string]any/[]any containers;
// KEYS are never tested (they are structure, not data -- searching them would
// surface "name" for a query of "nam", decision 5). The first match short-
// circuits. The value model is readers.ToProfileValue's:
// nil/bool/string/json.Number, plus float64 (a reader that emits a raw double,
// e.g. SQLite REAL / Parquet DOUBLE) and nested containers.
//
// Numbers match on their source text -- json.Number's exact literal, so no
// float precision is lost (decision 5) -- and float64 leaves match on the same
// shortest representation the table shows (toCell's FormatFloat, or the non-
// finite sentinel), so search matches what the user sees. null is never a
// match (there is nothing to contain).
func matchesSearch(v any, lowered string) bool {
	switch t := v.(type) {
	case map[string]any:
		for _, cv := range t {
			if matchesSearch(cv, lowered) {
				return true
			}
		}
		return false
	case []any:
		for _, cv := range t {
			if matchesSearch(cv, lowered) {
				return true
			}
		}
		return false
	case string:
		return strings.Contains(strings.ToLower(t), lowered)
	case json.Number:
		return strings.Contains(strings.ToLower(t.String()), lowered)
	case bool:
		if t {
			return strings.Contains("true", lowered)
		}
		return strings.Contains("false", lowered)
	case float64:
		if _, sentinel := sanitizeFloat(t); sentinel != "" {
			return strings.Contains(strings.ToLower(sentinel), lowered)
		}
		return strings.Contains(strings.ToLower(strconv.FormatFloat(t, 'g', -1, 64)), lowered)
	default: // nil and anything unexpected
		return false
	}
}

// searchKeySep namespaces the search text inside the canonical cache key so a
// filter whose JSON happens to equal a search string can never collide with
// it, and the byte is one the base hex key never contains.
const searchKeySep = "\x00search\x00"

// CompileFilterWithSearch compiles f and folds a global search into it: the
// returned predicate is (filter AND search) -- a record matches iff the visual
// filter matches AND some scalar leaf contains search (case-insensitive). The
// search is folded into the canonical key (so two searches never share a
// cached bitset/count -- decision 7) and -- critically -- the returned src is
// NULLED whenever search != "".
//
// The src=nil is load-bearing. mem/rescan/parquet apply cf.pred and honor the
// folded search for free, but sqlBackend's pushdown gate reads cf.src (the
// filter AST), NOT cf.pred (sqlbackend.go's pushdownFor). A pushable filter
// plus a search would otherwise run queryPushed/pushedCountSQL/exportPushed,
// none of which call Match, silently DROPPING the search on the whole SQLite
// tier. A nil src makes pushdownFor return exact=false, so sqlBackend falls
// back to its Go-scan Query/Count/Export, all of which apply cf.pred. Because
// the composed pred is non-nil, isMatchAllFilter is also false, so the
// unfiltered fast path is correctly avoided too.
//
// When search == "" the result is byte-identical to CompileFilter (src = &f
// preserved, key unchanged), so pure-filter pushdown is unaffected (decision 6).
func CompileFilterWithSearch(f Filter, search string, cm *ColumnModel) (*CompiledFilter, error) {
	cf, err := CompileFilter(f, cm)
	if err != nil {
		return nil, err
	}
	if search == "" {
		return cf, nil
	}
	sp := compileSearch(search) // non-nil: search != ""
	filterPred := cf.pred       // may be nil (an empty/match-all filter)
	cf.pred = func(rec any) bool {
		if filterPred != nil && !filterPred(rec) {
			return false
		}
		return sp(rec)
	}
	// Fold the search into the key: distinct searches over the same filter must
	// not share a cache entry. The base key already hashes the filter JSON.
	sum := sha256.Sum256([]byte(cf.key + searchKeySep + search))
	cf.key = hex.EncodeToString(sum[:])
	// The predicate now enforces a search the filter AST does not describe:
	// sqlBackend must not push. See the doc comment.
	cf.src = nil
	return cf, nil
}
