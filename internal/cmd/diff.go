package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/hoijun-kim/shape/internal/diff"
	"github.com/spf13/cobra"
)

// failErr signals the --fail-on gate tripped; exit code 1 (reserved for a
// failing diff), routed through main.go's ExitCode() hook.
type failErr struct{ msg string }

func (e failErr) Error() string { return e.msg }
func (e failErr) ExitCode() int { return 1 }

func newDiffCmd() *cobra.Command {
	var failOn, format string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "diff <old> <new>",
		Short: "Diff two snapshots and flag changes that break consumers of the old data",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch failOn {
			case "breaking", "any", "none":
			default:
				return fmt.Errorf("invalid --fail-on %q (want breaking|any|none)", failOn)
			}

			a, err := profileSource(args[0], format)
			if err != nil {
				return err
			}
			b, err := profileSource(args[1], format)
			if err != nil {
				return err
			}
			d := diff.Diff(a, b)

			if asJSON {
				out, err := json.MarshalIndent(d, "", "  ")
				if err != nil {
					return err
				}
				out = append(out, '\n')
				if _, err := cmd.OutOrStdout().Write(out); err != nil {
					return err
				}
			} else {
				diff.RenderText(cmd.OutOrStdout(), d)
			}

			switch failOn {
			case "any":
				if len(d.Changes) > 0 {
					return failErr{fmt.Sprintf("%d change(s)", len(d.Changes))}
				}
			case "breaking":
				if d.Breaking > 0 {
					return failErr{fmt.Sprintf("%d breaking change(s)", d.Breaking)}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&failOn, "fail-on", "breaking", "exit 1 on: breaking|any|none")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	cmd.Flags().StringVar(&format, "format", "auto", "input format: auto|json|ndjson")
	return cmd
}
