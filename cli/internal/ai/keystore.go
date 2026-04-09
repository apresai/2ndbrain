package ai

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const keychainService = "dev.apresai.2ndbrain"

// envVarName returns the environment variable name for a provider's API key.
func envVarName(provider string) string {
	switch provider {
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "bedrock":
		return "" // uses AWS SDK credential chain, not an API key
	default:
		return strings.ToUpper(provider) + "_API_KEY"
	}
}

// GetAPIKey retrieves the API key for a provider.
// Checks environment variables first, then macOS Keychain.
func GetAPIKey(provider string) (string, error) {
	if provider == "bedrock" || provider == "ollama" {
		return "", nil // these don't use API keys
	}

	envName := envVarName(provider)
	if envName != "" {
		if key := os.Getenv(envName); key != "" {
			return key, nil
		}
	}

	if runtime.GOOS == "darwin" {
		if key, err := keychainGet(provider); err == nil && key != "" {
			return key, nil
		}
	}

	return "", fmt.Errorf("no API key for %s: set %s or run `2nb config set-key %s`",
		provider, envName, provider)
}

// HasAPIKey checks whether an API key is available for a provider
// without exposing the key value.
func HasAPIKey(provider string) bool {
	if provider == "bedrock" || provider == "ollama" {
		return true // these don't use API keys
	}
	key, err := GetAPIKey(provider)
	return err == nil && key != ""
}

// SetAPIKey stores an API key in the macOS Keychain.
func SetAPIKey(provider, key string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("keychain storage only available on macOS; set %s environment variable instead", envVarName(provider))
	}

	// Delete existing entry first (security won't update, only add)
	_ = keychainDelete(provider)
	return keychainSet(provider, key)
}

// DeleteAPIKey removes an API key from the macOS Keychain.
func DeleteAPIKey(provider string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("keychain storage only available on macOS")
	}
	return keychainDelete(provider)
}

func keychainGet(account string) (string, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", keychainService,
		"-a", account,
		"-w")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func keychainSet(account, password string) error {
	cmd := exec.Command("security", "add-generic-password",
		"-s", keychainService,
		"-a", account,
		"-w", password,
		"-U") // update if exists
	return cmd.Run()
}

func keychainDelete(account string) error {
	cmd := exec.Command("security", "delete-generic-password",
		"-s", keychainService,
		"-a", account)
	return cmd.Run()
}
