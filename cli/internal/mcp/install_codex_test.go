package mcp

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Pure builders are always testable, no codex binary needed.
func TestCodexArgBuilders(t *testing.T) {
	got := codexAddArgs("/abs/2nb", "/v")
	want := []string{"mcp", "add", "2ndbrain", "--", "/abs/2nb", "mcp-server", "--vault", "/v"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codexAddArgs = %v, want %v", got, want)
	}
	if r := codexRemoveArgs(); !reflect.DeepEqual(r, []string{"mcp", "remove", "2ndbrain"}) {
		t.Errorf("codexRemoveArgs = %v", r)
	}
	fb := codexFallbackInstructions("/abs/2nb", "/v")
	for _, want := range []string{"codex mcp add 2ndbrain", "/abs/2nb", "--vault /v", "[mcp_servers.2ndbrain]"} {
		if !strings.Contains(fb, want) {
			t.Errorf("fallback instructions missing %q:\n%s", want, fb)
		}
	}
}

func stubCodexAbsCommand(t *testing.T) {
	t.Helper()
	orig := desktopLookPath
	desktopLookPath = func(string) (string, error) { return "/abs/2nb", nil }
	t.Cleanup(func() { desktopLookPath = orig })
}

// codex absent on PATH -> degrade with Instructions, no error, not configured.
func TestInstallCodex_Absent(t *testing.T) {
	stubCodexAbsCommand(t)
	origLook := codexLookPath
	codexLookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { codexLookPath = origLook })

	res, err := installCodex("", "/v", false)
	if err != nil {
		t.Fatalf("installCodex should degrade, not error: %v", err)
	}
	if res.Configured || res.Changed {
		t.Errorf("absent codex should not report configured/changed: %+v", res)
	}
	if !strings.Contains(res.Instructions, "codex mcp add 2ndbrain") {
		t.Errorf("expected fallback instructions, got: %q", res.Instructions)
	}
}

// codex present -> runs `codex mcp add` with the right argv.
func TestInstallCodex_Present(t *testing.T) {
	stubCodexAbsCommand(t)
	origLook, origRun, origList := codexLookPath, codexRun, codexList
	t.Cleanup(func() { codexLookPath, codexRun, codexList = origLook, origRun, origList })

	codexLookPath = func(string) (string, error) { return "/usr/bin/codex", nil }
	codexList = func() (string, error) { return "", nil } // not yet present
	var gotArgs []string
	codexRun = func(args ...string) error { gotArgs = args; return nil }

	res, err := installCodex("", "/v", false)
	if err != nil {
		t.Fatalf("installCodex: %v", err)
	}
	if !res.Changed || !res.Configured {
		t.Errorf("present codex install should be changed+configured: %+v", res)
	}
	if !reflect.DeepEqual(gotArgs, codexAddArgs("/abs/2nb", "/v")) {
		t.Errorf("codexRun got %v", gotArgs)
	}

	// Idempotent: codex mcp list already shows it -> no run.
	codexList = func() (string, error) { return "2ndbrain  /abs/2nb", nil }
	called := false
	codexRun = func(args ...string) error { called = true; return nil }
	res2, _ := installCodex("", "/v", false)
	if called {
		t.Error("idempotent install should not call codex mcp add when already listed")
	}
	if res2.Changed || !res2.Configured {
		t.Errorf("idempotent result: %+v", res2)
	}
}

// A codex mcp add failure is RETURNED as an error so a single-client install
// exits non-zero; InstallAll's runAll captures it into res.Error and continues.
func TestInstallCodex_AddFailureReturnsError(t *testing.T) {
	stubCodexAbsCommand(t)
	origLook, origRun, origList := codexLookPath, codexRun, codexList
	t.Cleanup(func() { codexLookPath, codexRun, codexList = origLook, origRun, origList })

	codexLookPath = func(string) (string, error) { return "/usr/bin/codex", nil }
	codexList = func() (string, error) { return "", nil }
	codexRun = func(args ...string) error { return errors.New("boom") }

	if _, err := installCodex("", "/v", false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected a returned error containing boom, got %v", err)
	}
}

// codexListHasServer matches the server name as a standalone field, never as a
// substring of a path (a vault path can itself contain "2ndbrain").
func TestCodexListHasServer(t *testing.T) {
	for _, out := range []string{"2ndbrain  stdio  ok", "- 2ndbrain", "2ndbrain: enabled", "* 2ndbrain (stdio)"} {
		if !codexListHasServer(out) {
			t.Errorf("should detect the server in %q", out)
		}
	}
	if codexListHasServer("other  /Users/chad/dev/2ndbrain/vault  stdio") {
		t.Error("must NOT match 2ndbrain as a path substring")
	}
	if codexListHasServer("") {
		t.Error("empty output should be false")
	}
}

func TestUninstallCodex(t *testing.T) {
	origLook, origRun := codexLookPath, codexRun
	t.Cleanup(func() { codexLookPath, codexRun = origLook, origRun })

	// Absent codex -> Instructions, no error, not changed.
	codexLookPath = func(string) (string, error) { return "", errors.New("no codex") }
	res, err := uninstallCodex(false)
	if err != nil {
		t.Fatalf("absent uninstall should not error: %v", err)
	}
	if res.Changed || res.Instructions == "" {
		t.Errorf("absent uninstall should carry Instructions, not change: %+v", res)
	}

	// Present codex -> runs `codex mcp remove`.
	codexLookPath = func(string) (string, error) { return "/usr/bin/codex", nil }
	var gotArgs []string
	codexRun = func(args ...string) error { gotArgs = args; return nil }
	res, err = uninstallCodex(false)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !res.Changed {
		t.Error("present uninstall should be changed")
	}
	if !reflect.DeepEqual(gotArgs, codexRemoveArgs()) {
		t.Errorf("codexRun got %v, want %v", gotArgs, codexRemoveArgs())
	}

	// Run failure -> error.
	codexRun = func(args ...string) error { return errors.New("rm fail") }
	if _, err := uninstallCodex(false); err == nil {
		t.Error("expected an error when codex mcp remove fails")
	}
}
