package main

import (
	"fmt"
	"os"

	"github.com/akyrey/projector/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func main() {
	root := cli.NewRootCmd(version)

	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
