package cli

import (
	"runtime"
	"strings"
	"testing"
)

func TestClipboardSupported(t *testing.T) {
	if err := clipboardSupported("darwin"); err != nil {
		t.Errorf("darwin should be supported, got %v", err)
	}
	for _, goos := range []string{"linux", "windows", "freebsd"} {
		err := clipboardSupported(goos)
		if err == nil {
			t.Errorf("%s should be unsupported", goos)
			continue
		}
		if ExitCode(err) != ExitValidation {
			t.Errorf("%s: want ExitValidation, got exit %d", goos, ExitCode(err))
		}
		if !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%s: error should explain lack of support, got %q", goos, err.Error())
		}
	}
}

func TestCopyToClipboard_MissingPbcopy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("pbcopy-missing branch is darwin-only; non-darwin is covered by TestClipboardSupported")
	}
	// With an empty PATH, exec.LookPath("pbcopy") fails -> the pbcopy-not-found
	// branch returns a clear ExitValidation error.
	t.Setenv("PATH", "")
	err := copyToClipboard("anything")
	if ExitCode(err) != ExitValidation {
		t.Fatalf("want ExitValidation, got exit %d (err=%v)", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "pbcopy") {
		t.Errorf("error should mention pbcopy, got %q", err.Error())
	}
}
