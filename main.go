package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/hoijunkim/shape/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "shape:", err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps an error to a process exit code. Read, open, and usage errors
// exit 2. Exit code 1 is reserved for a future breaking-drift contract
// (shape diff --fail-on breaking). An error may override its code by
// implementing ExitCode() int.
func exitCode(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return 2
}
