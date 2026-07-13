package cmd

import (
	"github.com/hoijun-kim/shape/internal/render"
	"github.com/spf13/cobra"
)

func newProfileCmd() *cobra.Command {
	var asJSON bool
	var format string

	cmd := &cobra.Command{
		Use:   "profile <file|->",
		Short: "Profile the shape of a JSON or NDJSON input",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := profileSource(args[0], format)
			if err != nil {
				return err
			}
			if asJSON {
				return render.JSON(cmd.OutOrStdout(), res)
			}
			render.Table(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	cmd.Flags().StringVar(&format, "format", "auto", "input format: auto|json|ndjson")
	return cmd
}
