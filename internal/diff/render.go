package diff

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// RenderText writes a human-readable diff summary to w.
func RenderText(w io.Writer, d DiffResult) {
	fmt.Fprintf(w, "diff %s -> %s\n", srcOr(d.Old), srcOr(d.New))
	fmt.Fprintf(w, "  %d paths compared - %d added, %d removed, %d changed (%d breaking)\n",
		d.Compared, d.Added, d.Removed, d.Changed, d.Breaking)
	for _, c := range d.Caveats {
		fmt.Fprintf(w, "  ! %s\n", c)
	}
	if len(d.Changes) == 0 {
		fmt.Fprintln(w, "  no changes")
		return
	}
	sorted := append([]Change(nil), d.Changes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Breaking != sorted[j].Breaking {
			return sorted[i].Breaking
		}
		return sorted[i].Path < sorted[j].Path
	})
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, c := range sorted {
		marker := "ok"
		if c.Breaking {
			marker = "BREAK"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", marker, c.Kind, c.Path, messages(c.Details))
	}
	tw.Flush()
}

func messages(ds []Detail) string {
	ms := make([]string, 0, len(ds))
	for _, d := range ds {
		ms = append(ms, d.Message)
	}
	return strings.Join(ms, "; ")
}

func srcOr(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
