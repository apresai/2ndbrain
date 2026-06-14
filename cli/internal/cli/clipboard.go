package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

// flagCopy is the global --copy switch: when set, a command that renders through
// writeOut also copies its output to the system clipboard.
var flagCopy bool

// writeOut renders data in the given format to stdout, and (when --copy is set)
// also copies the rendered bytes to the system clipboard. It is the --copy-aware
// drop-in for `output.Write(os.Stdout, format, data)` in commands where copying
// the result is meaningful (read/print, meta, search, unresolved, files, daily).
func writeOut(_ *cobra.Command, format output.Format, data any) error {
	if !flagCopy {
		return output.Write(os.Stdout, format, data)
	}
	var buf bytes.Buffer
	if err := output.Write(io.MultiWriter(os.Stdout, &buf), format, data); err != nil {
		return err
	}
	return copyToClipboard(buf.String())
}

// copyToClipboard writes s to the system clipboard. macOS uses pbcopy; other
// platforms return a clear ExitValidation error, since the --copy flag is only
// honored where a clipboard tool is available (no silent no-op).
func copyToClipboard(s string) error {
	if err := clipboardSupported(runtime.GOOS); err != nil {
		return err
	}
	path, err := exec.LookPath("pbcopy")
	if err != nil {
		return exitWithError(ExitValidation, "--copy requires pbcopy, which was not found on PATH")
	}
	c := exec.Command(path)
	c.Stdin = strings.NewReader(s)
	if err := c.Run(); err != nil {
		return fmt.Errorf("copy to clipboard: %w", err)
	}
	return nil
}

// clipboardSupported returns a clear ExitValidation error when --copy is used on
// a platform with no clipboard integration (only macOS/pbcopy today), or nil
// when supported. Extracted so the unsupported-platform branch is unit-testable
// without depending on the host GOOS.
func clipboardSupported(goos string) error {
	if goos != "darwin" {
		return exitWithError(ExitValidation,
			fmt.Sprintf("--copy is not supported on %s (no clipboard integration available)", goos))
	}
	return nil
}
