package render

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/hoijunkim/shape/internal/profile"
)

// Table writes a human-readable profile table to w.
func Table(w io.Writer, res profile.ProfileResult) {
	if res.Source != "" {
		fmt.Fprintf(w, "source: %s\n", res.Source)
	}
	fmt.Fprintf(w, "records: %d", res.Records)
	if res.Skipped > 0 {
		fmt.Fprintf(w, "  skipped: %d", res.Skipped)
	}
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "FIELD\tPRESENCE\tTYPES\tNULL\tDISTINCT\tDRIFT")
	for _, f := range res.Fields {
		drift := ""
		if profile.IsTypeDrift(f) {
			drift = "!"
		}
		distinct := fmt.Sprintf("%d", f.DistinctCount)
		if !f.DistinctExact {
			distinct = "~" + distinct // approximate estimate (HyperLogLog)
		}
		fmt.Fprintf(tw, "%s\t%.0f%%\t%s\t%.0f%%\t%s\t%s\n",
			f.Path, f.PresenceRate*100, typesLabel(f), f.NullRate*100, distinct, drift)
	}
	tw.Flush()
}

func typesLabel(f profile.FieldProfile) string {
	best := ""
	var bestFrac float64
	for k, frac := range f.TypeDist {
		if frac > bestFrac || (frac == bestFrac && (best == "" || string(k) < best)) {
			bestFrac, best = frac, string(k)
		}
	}
	if len(f.TypeDist) > 1 {
		return best + ".."
	}
	return best
}
