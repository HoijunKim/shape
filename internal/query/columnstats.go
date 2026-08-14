package query

import (
	"context"

	"github.com/hoijunkim/shape/internal/visual"
)

// ColumnStatsRequest asks for the rich profile of ONE source field.
type ColumnStatsRequest struct {
	Handle string `json:"handle"`
	Path   string `json:"path"`
}

// ColumnStatsResult carries the visual FieldCard for that field. Found is false
// when no field with the requested path is in the source's profile (e.g. a
// projected/renamed output column, which is not a source field).
type ColumnStatsResult struct {
	Card  visual.FieldCard `json:"card"`
	Found bool             `json:"found"`
}

// ColumnStats returns the visual FieldCard for one source field, built from the
// profile the backend already retains from the open-time scan (no rescan). It
// mirrors GetCell: a lazy, per-item lookup that owns no state. ctx is accepted
// for binding-signature symmetry; the lookup is in-memory and does not block.
func (e *Engine) ColumnStats(ctx context.Context, req ColumnStatsRequest) (ColumnStatsResult, error) {
	backend, err := e.lookup(req.Handle)
	if err != nil {
		return ColumnStatsResult{}, err
	}
	// FromProfile is pure geometry over already-computed stats. Only the
	// per-field Fields are consumed here, so Options can be zero (its
	// Name/Format feed the whole-model Summary/KPIs, which E8 ignores).
	model := visual.FromProfile(backend.Profile(), visual.Options{})
	for _, c := range model.Fields {
		if c.Path == req.Path {
			return ColumnStatsResult{Card: c, Found: true}, nil
		}
	}
	return ColumnStatsResult{Found: false}, nil
}
