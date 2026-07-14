package cmd

import (
	"github.com/hoijun-kim/shape/internal/pipeline"
	"github.com/hoijun-kim/shape/internal/profile"
)

// profileSource is the CLI's adapter over the shared pipeline.
func profileSource(src, format string, csvRaw bool, table string) (profile.ProfileResult, error) {
	return pipeline.Profile(pipeline.Options{Path: src, Format: format, CSVRaw: csvRaw, Table: table})
}
