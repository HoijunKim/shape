package profile

import "sort"

// DefaultExactCap is the number of distinct values a field tracks exactly before
// promoting to bounded sketches. It stays above 2.5*2^hllPrecision (10240) so the
// HLL only estimates cardinalities in its unbiased regime, and far above the
// top-K cap of 10 so small enums are never promoted.
const DefaultExactCap = 16384

// ProfileResult is the profile of a whole input.
type ProfileResult struct {
	Records int
	Skipped int
	Source  string
	Fields  []FieldProfile
}

// Profiler accumulates records into a ProfileResult.
type Profiler struct {
	accs    map[string]*fieldAccumulator
	order   []string
	records int
	skipped int
	cap     int
}

// NewProfiler returns a Profiler using the default distinct cap.
func NewProfiler() *Profiler {
	return &Profiler{accs: map[string]*fieldAccumulator{}, cap: DefaultExactCap}
}

// AddSkipped records malformed inputs skipped by a reader.
func (p *Profiler) AddSkipped(n int) { p.skipped += n }

// AddRecord flattens one record and updates presence and value stats.
func (p *Profiler) AddRecord(record any) {
	p.records++
	seen := map[string]bool{}
	Flatten(record, func(o Observation) {
		a := p.acc(o.Path)
		a.AddValue(o)
		if !seen[o.Path] {
			seen[o.Path] = true
			a.MarkPresent()
		}
	})
}

func (p *Profiler) acc(path string) *fieldAccumulator {
	a, ok := p.accs[path]
	if !ok {
		a = newFieldAccumulator(path, p.cap)
		p.accs[path] = a
		p.order = append(p.order, path)
	}
	return a
}

// Result assembles the sorted ProfileResult.
func (p *Profiler) Result() ProfileResult {
	paths := append([]string(nil), p.order...)
	sort.Strings(paths)
	fields := make([]FieldProfile, 0, len(paths))
	for _, path := range paths {
		fields = append(fields, p.accs[path].Result(p.records))
	}
	return ProfileResult{Records: p.records, Skipped: p.skipped, Fields: fields}
}

// IsTypeDrift reports whether a field shows more than one non-null value type.
// Int and float collapse to a single "number" type so 1 and 2.5 do not drift.
func IsTypeDrift(fp FieldProfile) bool {
	types := map[string]bool{}
	for k, frac := range fp.TypeDist {
		if frac <= 0 || k == KindNull {
			continue
		}
		switch k {
		case KindInt, KindFloat:
			types["number"] = true
		default:
			types[string(k)] = true
		}
	}
	return len(types) > 1
}
