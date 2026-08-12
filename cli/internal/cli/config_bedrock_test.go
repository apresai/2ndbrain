package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func isolateBedrockFile(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	path := ai.BedrockFilePath()
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
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
