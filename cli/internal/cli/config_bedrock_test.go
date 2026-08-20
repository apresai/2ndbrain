package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func isolateBedrockFile(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("2NB_BEDROCK_SKIP_KEYCHAIN", "1")
}

func TestConfigBedrockShowEmpty(t *testing.T) {
	isolateBedrockFile(t)
	root := t.TempDir()
	out, err := runCLIArgs(t, root, "config", "bedrock", "--json")
	if err != nil {
		t.Fatalf("config bedrock --json: %v\n%s", err, out)
	}
	var st bedrockMachineStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	// A developer Keychain token is still visible (the login keychain is not
	// sandboxed). The file itself is empty; env is cleared.
	if st.TokenSource == string(ai.BedrockTokenFile) || st.TokenSource == string(ai.BedrockTokenEnv) {
		t.Fatalf("empty file + empty env must not report file/env: %+v", st)
	}
	if st.Path == "" || !strings.Contains(st.Path, "bedrock.json") {
		t.Fatalf("path = %q", st.Path)
	}
	if strings.Contains(string(out), "ABSK") {
		t.Fatal("status leaked a token")
	}
}

func TestConfigBedrockSetAndClear(t *testing.T) {
	isolateBedrockFile(t)
	root := t.TempDir()

	out, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--region", "us-west-2", "--token", "ABSK-cli")
	if err != nil {
		t.Fatalf("set: %v\n%s", err, out)
	}
	got, err := ai.ReadBedrockFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.Region != "us-west-2" || got.Token != "ABSK-cli" {
		t.Fatalf("file = %+v", got)
	}

	out, err = runCLIArgs(t, root, "config", "bedrock", "--json")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	var st bedrockMachineStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if !st.TokenSet || st.TokenSource != string(ai.BedrockTokenFile) || st.Region != "us-west-2" {
		t.Fatalf("after set: %+v", st)
	}
	if strings.Contains(string(out), "ABSK-cli") {
		t.Fatal("json leaked the token")
	}

	out, err = runCLIArgs(t, root, "config", "bedrock", "--clear-token")
	if err != nil {
		t.Fatalf("clear: %v\n%s", err, out)
	}
	got, err = ai.ReadBedrockFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "" || got.Region != "us-west-2" {
		t.Fatalf("clear should keep region: %+v", got)
	}
}

func TestConfigBedrockSetTokenStdin(t *testing.T) {
	isolateBedrockFile(t)
	root := t.TempDir()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("ABSK-stdin\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })

	out, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--region", "eu-west-1", "--token-stdin")
	if err != nil {
		t.Fatalf("set stdin: %v\n%s", err, out)
	}
	got, err := ai.ReadBedrockFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "ABSK-stdin" || got.Region != "eu-west-1" {
		t.Fatalf("file = %+v", got)
	}
}

func TestConfigBedrockEnvWinsSource(t *testing.T) {
	isolateBedrockFile(t)
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "ABSK-from-env")
	root := t.TempDir()
	if _, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--token", "ABSK-file"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIArgs(t, root, "config", "bedrock", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st bedrockMachineStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if st.TokenSource != string(ai.BedrockTokenEnv) {
		t.Fatalf("env should win source, got %+v", st)
	}
	if strings.Contains(string(out), "ABSK-from-env") || strings.Contains(string(out), "ABSK-file") {
		t.Fatal("json leaked a token")
	}
}

func TestConfigBedrockRegionsSetShowClear(t *testing.T) {
	isolateBedrockFile(t)
	root := t.TempDir()

	out, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--regions", "us-west-2, us-east-2")
	if err != nil {
		t.Fatalf("set regions: %v\n%s", err, out)
	}
	out, err = runCLIArgs(t, root, "config", "bedrock", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st bedrockMachineStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Regions) != 2 || st.Regions[0] != "us-west-2" || st.Regions[1] != "us-east-2" {
		t.Fatalf("regions = %+v", st.Regions)
	}

	// A non-bare label is refused with a validation exit, file untouched.
	if _, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--regions", "us-west-2,http://evil"); err == nil {
		t.Fatal("expected refusal of a non-bare region label")
	}
	got, _ := ai.ReadBedrockFile()
	if len(got.Regions) != 2 {
		t.Fatalf("refused set must not modify regions: %+v", got.Regions)
	}

	if _, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--clear-regions"); err != nil {
		t.Fatalf("clear regions: %v", err)
	}
	got, _ = ai.ReadBedrockFile()
	if len(got.Regions) != 0 {
		t.Fatalf("clear left regions: %+v", got.Regions)
	}
}

func TestConfigBedrockTokenUpdatedAtInStatus(t *testing.T) {
	isolateBedrockFile(t)
	root := t.TempDir()

	if _, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--token", "ABSK-token-updated-check"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIArgs(t, root, "config", "bedrock", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st bedrockMachineStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if st.TokenUpdatedAt == "" {
		t.Fatalf("token write should surface token_updated_at: %+v", st)
	}
}

func TestConfigBedrockEnvOverridesStoredWarning(t *testing.T) {
	isolateBedrockFile(t)
	root := t.TempDir()

	if _, err := runCLIArgs(t, root, "config", "bedrock", "--set", "--token", "ABSK-stored-key-value-alpha"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "ABSK-environ-key-value-bravo")

	out, err := runCLIArgs(t, root, "config", "bedrock", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st bedrockMachineStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if !st.EnvOverridesStored {
		t.Fatalf("expected env_overrides_stored: %+v", st)
	}
	if st.StoredTokenSuffix != "lpha" || st.TokenSuffix != "ravo" {
		t.Fatalf("suffixes should identify both keys: %+v", st)
	}

	// Same key in both places: no divergence flag.
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "ABSK-stored-key-value-alpha")
	out, err = runCLIArgs(t, root, "config", "bedrock", "--json")
	if err != nil {
		t.Fatal(err)
	}
	st = bedrockMachineStatus{}
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if st.EnvOverridesStored {
		t.Fatalf("identical env and stored keys must not diverge: %+v", st)
	}
}

func TestConfigBedrockShowReportsUnprivateFile(t *testing.T) {
	isolateBedrockFile(t)
	path := ai.BedrockFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"region":"us-east-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIArgs(t, t.TempDir(), "config", "bedrock", "--json")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	var st bedrockMachineStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if st.Error == "" || !strings.Contains(st.Error, "not private") {
		t.Fatalf("expected not-private error, got %+v", st)
	}
}
