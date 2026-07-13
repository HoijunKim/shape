package main

import (
	"fmt"
	"os"

	"github.com/hoijun-kim/shape/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "shape:", err)
		os.Exit(1)
	}
}
