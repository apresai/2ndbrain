package main

import (
	"os"

	"github.com/apresai/2ndbrain/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
