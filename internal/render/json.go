package render

import (
	"encoding/json"
	"io"

	"github.com/hoijun-kim/shape/internal/profile"
)

// JSON writes the profile result as stable indented JSON.
func JSON(w io.Writer, res profile.ProfileResult) error {
	type field struct {
		Path          string             `json:"path"`
		Presence      float64            `json:"presence"`
		Types         map[string]float64 `json:"types"`
		NullRate      float64            `json:"null_rate"`
		Min           *float64           `json:"min,omitempty"`
		Max           *float64           `json:"max,omitempty"`
		DistinctCount int                `json:"distinct_count"`
		DistinctExact bool               `json:"distinct_exact"`
		Drift         bool               `json:"drift"`
		Top           []map[string]any   `json:"top_values,omitempty"`
	}
	out := struct {
		Records int     `json:"records"`
		Skipped int     `json:"skipped"`
		Fields  []field `json:"fields"`
	}{Records: res.Records, Skipped: res.Skipped}

	for _, f := range res.Fields {
		types := map[string]float64{}
		for k, v := range f.TypeDist {
			types[string(k)] = v
		}
		top := make([]map[string]any, 0, len(f.TopValues))
		for _, v := range f.TopValues {
			top = append(top, map[string]any{"value": v.Value, "count": v.Count})
		}
		out.Fields = append(out.Fields, field{
			Path: f.Path, Presence: f.PresenceRate, Types: types,
			NullRate: f.NullRate, Min: f.Min, Max: f.Max,
			DistinctCount: f.DistinctCount, DistinctExact: f.DistinctExact,
			Drift: profile.IsTypeDrift(f), Top: top,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
