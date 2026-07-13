package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/hoijun-kim/shape/internal/profile"
	"github.com/hoijun-kim/shape/internal/readers/jsonreader"
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
			src := args[0]
			r, peek, closeFn, err := openSource(src)
			if err != nil {
				return err
			}
			defer closeFn()

			mode := jsonreader.DetectMode(src, format, peek)
			stream := jsonreader.New(r, mode)
			p := profile.NewProfiler()
			for {
				rec, err := stream.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return fmt.Errorf("read %s: %w", src, err)
				}
				p.AddRecord(rec)
			}
			p.AddSkipped(stream.Skipped())
			res := p.Result()
			res.Source = src

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

// openSource opens a file path or stdin ("-"), returning the reader, a peek of
// the first bytes (for format detection), and a close function.
func openSource(src string) (io.Reader, []byte, func(), error) {
	if src == "-" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(os.Stdin, buf)
		peek := buf[:n]
		combined := io.MultiReader(bytesReader(peek), os.Stdin)
		return combined, peek, func() {}, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, nil, nil, err
	}
	peek := make([]byte, 512)
	n, _ := f.Read(peek)
	peek = peek[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, nil, err
	}
	return f, peek, func() { f.Close() }, nil
}
