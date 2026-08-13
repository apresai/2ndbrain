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
	bedrockSet        bool
	bedrockClearToken bool
	bedrockRegion     string
	bedrockToken      string
	bedrockTokenStdin bool
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
	Error       string `json:"error,omitempty"`
}

// tokenSuffix returns the last 4 characters of a token, or "" when the token is
// too short to reveal a suffix without leaking a meaningful fraction of it.
func tokenSuffix(token string) string {
	const revealed = 4
	// Require a comfortable margin over the revealed length: showing the tail of
	// an 6-character secret is a leak, not a hint.
	r := []rune(token)
	if len(r) < revealed*3 {
		return ""
	}
	return string(r[len(r)-revealed:])
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
	configBedrockCmd.MarkFlagsMutuallyExclusive("set", "clear-token")
	configBedrockCmd.MarkFlagsMutuallyExclusive("token", "token-stdin")
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
		if bedrockRegion == "" && token == nil {
			return exitWithError(ExitValidation, "--set needs --region and/or a token (--token or --token-stdin)")
		}
		if err := ai.UpdateBedrockFile(bedrockRegion, token, false); err != nil {
			return err
		}
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "Wrote %s\n", ai.BedrockFilePath())
		}
		return writeBedrockStatus(cmd)
	}
	return writeBedrockStatus(cmd)
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
	if st.TokenSet {
		if st.TokenSuffix != "" {
			fmt.Printf("Token:  set (%s, ends %s)\n", st.TokenSource, st.TokenSuffix)
		} else {
			fmt.Printf("Token:  set (%s)\n", st.TokenSource)
		}
	} else {
		fmt.Printf("Token:  not set\n")
	}
	return nil
}

func currentBedrockStatus() bedrockMachineStatus {
	doc, err := ai.ReadBedrockFile()
	tok, src := ai.ResolveBedrockToken()
	st := bedrockMachineStatus{
		Path:        ai.BedrockFilePath(),
		Region:      doc.Region,
		TokenSet:    src != ai.BedrockTokenNone,
		TokenSuffix: tokenSuffix(tok),
		TokenSource: string(src),
	}
	if err != nil {
		st.Error = err.Error()
	}
	return st
}