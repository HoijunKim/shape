package cmd

import "github.com/spf13/cobra"

// Version is the CLI version string, overridable at build time via -ldflags.
var Version = "0.1.0-dev"

// NewRootCmd builds the root `shape` command.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "shape",
		Short:         "See the real shape of your structured data files",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("shape version {{.Version}}\n")
	return root
}

// Execute runs the root command against os.Args.
func Execute() error {
	return NewRootCmd().Execute()
}
