package ai

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupBedrockHome(t *testing.T) string {
	t.Helper()
	home := setupHome(t)
	t.Setenv(bedrockBearerTokenEnv, "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	return home
}

func TestBedrockFileRoundTrip(t *testing.T) {
	setupBedrockHome(t)

	if err := WriteBedrockFile(BedrockFile{Region: "us-west-2", Token: "ABSK-test"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadBedrockFile()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Region != "us-west-2" || got.Token != "ABSK-test" {
		t.Fatalf("got %+v", got)
	}
	fi, err := os.Stat(BedrockFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %04o, want 0600", perm)
	}
}

func TestBedrockFileMissingIsEmpty(t *testing.T) {
	setupBedrockHome(t)
	got, err := ReadBedrockFile()
	if err != nil {
		t.Fatalf("missing file should be empty, not error: %v", err)
	}
	if got.Region != "" || got.Token != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestBedrockFileRefusesWorldReadable(t *testing.T) {
	setupBedrockHome(t)
	path := BedrockFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"token":"ABSK-leaked"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBedrockFile(); err == nil {
		t.Fatal("expected refuse on 0644")
	} else if !strings.Contains(err.Error(), "not private") {
		t.Fatalf("error = %v, want not-private", err)
	}
	if tok := readBedrockFileToken(); tok != "" {
		t.Fatalf("refused file must not yield a token, got %q", tok)
	}
}

func TestBedrockFileXDGConfigHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	if err := WriteBedrockFile(BedrockFile{Token: "ABSK-xdg"}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(xdg, "2nb", bedrockFileName)
	if BedrockFilePath() != want {
		t.Fatalf("path = %s, want %s", BedrockFilePath(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected file at %s: %v", want, err)
	}
}

func TestUpdateBedrockFileMerge(t *testing.T) {
	setupBedrockHome(t)
	if err := WriteBedrockFile(BedrockFile{Region: "eu-west-1", Token: "ABSK-old"}); err != nil {
		t.Fatal(err)
	}
	tok := "ABSK-new"
	if err := UpdateBedrockFile("", &tok, false); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBedrockFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.Region != "eu-west-1" || got.Token != "ABSK-new" {
		t.Fatalf("merge token should keep region: %+v", got)
	}
	if err := UpdateBedrockFile("ap-southeast-2", nil, false); err != nil {
		t.Fatal(err)
	}
	got, err = ReadBedrockFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.Region != "ap-southeast-2" || got.Token != "ABSK-new" {
		t.Fatalf("merge region should keep token: %+v", got)
	}
	empty := ""
	if err := UpdateBedrockFile("", &empty, false); err != nil {
		t.Fatal(err)
	}
	got, err = ReadBedrockFile()
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "" || got.Region != "ap-southeast-2" {
		t.Fatalf("clear token should keep region: %+v", got)
	}
}

func TestResolveBedrockConfigFileRegionWins(t *testing.T) {
	setupBedrockHome(t)
	vault := BedrockConfig{Region: "us-east-1", Profile: "default"}
	got := ResolveBedrockConfig(vault)
	if got.Region != "us-east-1" {
		t.Fatalf("no file: region = %q", got.Region)
	}
	if err := WriteBedrockFile(BedrockFile{Region: "us-west-2"}); err != nil {
		t.Fatal(err)
	}
	got = ResolveBedrockConfig(vault)
	if got.Region != "us-west-2" {
		t.Fatalf("file region should win, got %q", got.Region)
	}
	if got.Profile != "default" {
		t.Fatalf("profile should pass through, got %q", got.Profile)
	}
}

func TestResolveBedrockConfigEmptyFallsBack(t *testing.T) {
	setupBedrockHome(t)
	got := ResolveBedrockConfig(BedrockConfig{})
	if got.Region != "us-east-1" {
		t.Fatalf("empty vault+file should default us-east-1, got %q", got.Region)
	}
}

func TestResolveBedrockTokenPrecedence(t *testing.T) {
	setupBedrockHome(t)

	tok, src := ResolveBedrockToken()
	if tok != "" || src != BedrockTokenNone {
		t.Fatalf("empty: token=%q source=%s", tok, src)
	}

	if err := WriteBedrockFile(BedrockFile{Token: "ABSK-file"}); err != nil {
		t.Fatal(err)
	}
	tok, src = ResolveBedrockToken()
	if tok != "ABSK-file" || src != BedrockTokenFile {
		t.Fatalf("file: token=%q source=%s", tok, src)
	}

	t.Setenv(bedrockBearerTokenEnv, "ABSK-env")
	tok, src = ResolveBedrockToken()
	if tok != "ABSK-env" || src != BedrockTokenEnv {
		t.Fatalf("env: token=%q source=%s", tok, src)
	}
}

func TestCheckBedrockCredentialsFileToken(t *testing.T) {
	setupBedrockHome(t)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-such-creds"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-such-config"))

	if CheckBedrockCredentials(context.Background(), BedrockConfig{Region: "us-east-1"}) {
		t.Fatal("no token and no SigV4 should be false")
	}
	if err := WriteBedrockFile(BedrockFile{Token: "ABSK-file-only"}); err != nil {
		t.Fatal(err)
	}
	if !CheckBedrockCredentials(context.Background(), BedrockConfig{Region: "us-east-1"}) {
		t.Fatal("file token should count as credentials without SigV4")
	}
	if os.Getenv(bedrockBearerTokenEnv) != "ABSK-file-only" {
		t.Fatalf("hydrate should export env, got %q", os.Getenv(bedrockBearerTokenEnv))
	}
}
