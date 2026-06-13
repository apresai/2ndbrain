package main

import (
	"os"

	"github.com/apresai/2ndbrain/internal/cli"
)

func main() {
	// Honor the error's exit code (ExitValidation=2, ExitNotFound=1,
	// ExitStaleRef=3) instead of flattening every failure to 1, so scripts
	// and CI can distinguish "bad input" from "not found". cli.Execute already
	// printed the error to stderr.
	os.Exit(cli.ExitCode(cli.Execute()))
}
