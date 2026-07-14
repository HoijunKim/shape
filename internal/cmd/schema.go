package cmd

import (
	"encoding/json"
	"os"

	"github.com/hoijun-kim/shape/internal/schema"
	"github.com/spf13/cobra"
)

func newSchemaCmd() *cobra.Command {
	var out string
	var format string
	var csvRaw bool

	cmd := &cobra.Command{
		Use:   "schema <file|->",
		Short: "Infer a JSON Schema (Draft 2020-12) from a JSON or NDJSON input",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := profileSource(args[0], format, csvRaw)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(schema.Reconstruct(res), "", "  ")
			if err != nil {
				return err
			}
			b = append(b, '\n')
			if out != "" {
				return os.WriteFile(out, b, 0o644)
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "write the schema to a file instead of stdout")
	cmd.Flags().StringVar(&format, "format", "auto", "input format: auto|json|ndjson|csv")
	cmd.Flags().BoolVar(&csvRaw, "csv-raw", false, "read CSV cells as raw strings (no type inference)")
	return cmd
}
