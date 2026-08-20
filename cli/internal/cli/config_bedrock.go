package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var (
	bedrockSet          bool
	bedrockClearToken   bool
	bedrockRegion       string
	bedrockToken        string
	bedrockTokenStdin   bool
	bedrockRegions      string
	bedrockClearRegions bool
)

// bedrockMachineStatus is the redacted view of machine-local Bedrock creds.
// The token itself is never included.
type bedrockMachineStatus struct {
	Path     string `json:"path"`
	Region   string `json:"region,omitempty"`
	TokenSet bool   `json:"token_set"`
	// TokenSuffix is the last 4 characters of the resolved token, so a UI can
	// render a masked value the user can actually recognize ("••••••••9f2a")
	// and tell two keys apart. Standard BYOK practice: enough to identify,
	// useless to an attacker. Empty when no token is set.
	TokenSuffix string `json:"token_suffix,omitempty"`
	TokenSource string `json:"token_source"`
	// Regions are the additional included regions for model verification and
	// discovery (the primary region is always included).
	Regions []string `json:"regions,omitempty"`
	// TokenUpdatedAt is when the STORED token last changed (RFC3339), so a UI
	// can flag model-access verdicts that predate the current key.
	TokenUpdatedAt string `json:"token_updated_at,omitempty"`
	// EnvOverridesStored flags the split-brain state: AWS_BEARER_TOKEN_BEDROCK
	// in this environment overrides a DIFFERENT saved key, so this shell keeps
	// using the env token while the app uses the stored one.
	EnvOverridesStored bool `json:"env_overrides_stored,omitempty"`
	// StoredTokenSuffix identifies the stored key when it diverges from the
	// env token (TokenSuffix shows the RESOLVED, i.e. env, token then).
	StoredTokenSuffix string `json:"stored_token_suffix,omitempty"`
	Error             string `json:"error,omitempty"`
}

// tokenSuffix delegates to the single suffix-redaction rule in internal/ai.
func tokenSuffix(token string) string {
	return ai.TokenSuffix(token)
}

var configBedrockCmd = &cobra.Command{
	Use:   "bedrock",
	Short: "Show or set machine-local Bedrock credentials",
	Long: `Read or write ~/.config/2nb/bedrock.json (XDG-aware).

This file is machine-local and is never written into a vault. The bearer
token is never printed. AWS_BEARER_TOKEN_BEDROCK wins over the file; the
macOS Keychain is a legacy fallback.

Does not require an open vault.`,
	Example: `  2nb config bedrock
  2nb config bedrock --json
  2nb config bedrock --set --region us-east-1 --token-stdin
  2nb config bedrock --clear-token`,
	RunE: runConfigBedrock,
}

func init() {
	configBedrockCmd.Flags().BoolVar(&bedrockSet, "set", false, "Write region and/or token to the machine file")
	configBedrockCmd.Flags().BoolVar(&bedrockClearToken, "clear-token", false, "Remove the stored token; keep region")
	configBedrockCmd.Flags().StringVar(&bedrockRegion, "region", "", "AWS region to store (with --set)")
	configBedrockCmd.Flags().StringVar(&bedrockToken, "token", "", "Bedrock API key (prefer --token-stdin so it does not appear in ps)")
	configBedrockCmd.Flags().BoolVar(&bedrockTokenStdin, "token-stdin", false, "Read the Bedrock API key from stdin")
	configBedrockCmd.Flags().StringVar(&bedrockRegions, "regions", "", "Comma-separated additional regions to include when verifying model access and discovering listings (with --set)")
	configBedrockCmd.Flags().BoolVar(&bedrockClearRegions, "clear-regions", false, "Remove the additional included regions (with --set)")
	configBedrockCmd.MarkFlagsMutuallyExclusive("set", "clear-token")
	configBedrockCmd.MarkFlagsMutuallyExclusive("token", "token-stdin")
	configBedrockCmd.MarkFlagsMutuallyExclusive("regions", "clear-regions")
	configCmd.AddCommand(configBedrockCmd)
}

func runConfigBedrock(cmd *cobra.Command, args []string) error {
	if bedrockClearToken {
		if err := ai.ClearBedrockStoredToken(); err != nil {
			return err
		}
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "Cleared Bedrock token in %s\n", ai.BedrockFilePath())
		}
		return writeBedrockStatus(cmd)
	}
	if bedrockSet {
		token, err := bedrockTokenFromFlags()
		if err != nil {
			return err
		}
		if bedrockRegion == "" && token == nil && bedrockRegions == "" && !bedrockClearRegions {
			return exitWithError(ExitValidation, "--set needs --region, --regions/--clear-regions, and/or a token (--token or --token-stdin)")
		}
		if bedrockRegion != "" || token != nil {
			if err := ai.UpdateBedrockFile(bedrockRegion, token, false); err != nil {
				return err
			}
		}
		if bedrockRegions != "" || bedrockClearRegions {
			if err := ai.UpdateBedrockRegions(splitCommaList(bedrockRegions), bedrockClearRegions); err != nil {
				return exitWithError(ExitValidation, err.Error())
			}
		}
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "Wrote %s\n", ai.BedrockFilePath())
		}
		return writeBedrockStatus(cmd)
	}
	return writeBedrockStatus(cmd)
}

// splitCommaList splits a comma-separated flag value, trimming entries and
// dropping empties.
func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func bedrockTokenFromFlags() (*string, error) {
	if bedrockTokenStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read token from stdin: %w", err)
		}
		tok := strings.TrimSpace(string(data))
		if tok == "" {
			return nil, exitWithError(ExitValidation, "empty token on stdin")
		}
		return &tok, nil
	}
	if bedrockToken != "" {
		tok := strings.TrimSpace(bedrockToken)
		return &tok, nil
	}
	return nil, nil
}

func writeBedrockStatus(cmd *cobra.Command) error {
	st := currentBedrockStatus()
	format := getFormat(cmd)
	if format == output.FormatJSON || format == output.FormatYAML || flagPorcelain {
		return output.Write(os.Stdout, format, st)
	}
	fmt.Printf("Path:   %s\n", st.Path)
	if st.Region != "" {
		fmt.Printf("Region: %s\n", st.Region)
	} else {
		fmt.Printf("Region: (not set in file; vault ai.bedrock.region is used)\n")
	}
	if len(st.Regions) > 0 {
		fmt.Printf("Also included regions: %s (verification and discovery)\n", strings.Join(st.Regions, ", "))
	}
	if st.TokenSet {
		if st.TokenSuffix != "" {
			fmt.Printf("Token:  set (%s, ends %s)\n", st.TokenSource, st.TokenSuffix)
		} else {
			fmt.Printf("Token:  set (%s)\n", st.TokenSource)
		}
	} else {
		fmt.Printf("Token:  not set\n")
	}
	if st.EnvOverridesStored {
		fmt.Printf("WARNING: AWS_BEARER_TOKEN_BEDROCK in this environment (ends %s) overrides the saved key (ends %s). This shell and anything it launches (MCP servers, agents) keep using the env token until you unset it or restart the process; the app uses the saved key.\n",
			suffixOrUnknown(st.TokenSuffix), suffixOrUnknown(st.StoredTokenSuffix))
	}
	return nil
}

func suffixOrUnknown(s string) string {
	if s == "" {
		return "????"
	}
	return s
}

func currentBedrockStatus() bedrockMachineStatus {
	doc, err := ai.ReadBedrockFile()
	tok, src := ai.ResolveBedrockToken()
	st := bedrockMachineStatus{
		Path:           ai.BedrockFilePath(),
		Region:         doc.Region,
		TokenSet:       src != ai.BedrockTokenNone,
		TokenSuffix:    tokenSuffix(tok),
		TokenSource:    string(src),
		Regions:        doc.Regions,
		TokenUpdatedAt: doc.TokenUpdatedAt,
	}
	if div := ai.BedrockTokenDivergence(); div.Diverges {
		st.EnvOverridesStored = true
		st.StoredTokenSuffix = div.StoredSuffix
	}
	if err != nil {
		st.Error = err.Error()
	}
	return st
}