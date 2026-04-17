package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage vault configuration",
	// Default action when invoked without a subcommand: show config.
	RunE: runConfigShow,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show full vault configuration",
	RunE:  runConfigShow,
}

var configGetCmd = &cobra.Command{
	Use:               "get <key>",
	Short:             "Get a configuration value",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeConfigKeys,
	RunE:              runConfigGet,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Args:  cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Only complete the first positional (the key). The value half
		// is free-form and depends on the key chosen.
		if len(args) == 0 {
			return completeConfigKeys(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: runConfigSet,
}

var configSetKeyCmd = &cobra.Command{
	Use:               "set-key <provider>",
	Short:             "Store an API key securely in macOS Keychain",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeProviders,
	RunE:              runConfigSetKey,
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configSetKeyCmd)
	configCmd.GroupID = "config"
	rootCmd.AddCommand(configCmd)
}

// configDisplay wraps Config with vault location metadata so `config show`
// answers "which vault am I editing?" at the top instead of requiring the
// user to cross-reference with `2nb vault`.
type configDisplay struct {
	VaultRoot string   `json:"vault_root" yaml:"vault_root"`
	VaultDir  string   `json:"vault_dir" yaml:"vault_dir"`
	VaultName string   `json:"vault_name" yaml:"vault_name"`
	Config    any      `json:"config" yaml:"config"`
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	display := configDisplay{
		VaultRoot: v.Root,
		VaultDir:  v.DotDir,
		VaultName: v.Config.Name,
		Config:    v.Config,
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, display)
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	key := args[0]
	value, err := getConfigValue(v.Config.AI, key)
	if err != nil {
		return err
	}

	fmt.Println(value)
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()

	key, value := args[0], args[1]
	if err := setConfigValue(&v.Config.AI, key, value); err != nil {
		return err
	}

	if err := v.Config.Save(v.DotDir); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Set %s = %s\n", key, value)
	}
	return nil
}

func runConfigSetKey(cmd *cobra.Command, args []string) error {
	provider := args[0]

	fmt.Fprintf(os.Stderr, "Enter API key for %s: ", provider)
	reader := bufio.NewReader(os.Stdin)
	key, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read key: %w", err)
	}
	key = strings.TrimSpace(key)

	if key == "" {
		return fmt.Errorf("empty key")
	}

	if err := ai.SetAPIKey(provider, key); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Stored %s API key in macOS Keychain\n", provider)
	return nil
}

// settableConfigKeys is the source-of-truth list of keys accepted by
// `config get` / `config set`. Drives both the switch statements below,
// shell completion (completeConfigKeys), and the "unknown key" error
// message. Order is presentation order.
var settableConfigKeys = []string{
	"ai.provider",
	"ai.embedding_model",
	"ai.generation_model",
	"ai.dimensions",
	"ai.bedrock.profile",
	"ai.bedrock.region",
	"ai.openrouter.api_key_env",
	"ai.ollama.endpoint",
}

func unknownConfigKeyError(key string) error {
	return fmt.Errorf("unknown config key: %q\n\nValid keys:\n  %s", key, strings.Join(settableConfigKeys, "\n  "))
}

func getConfigValue(cfg ai.AIConfig, key string) (string, error) {
	switch key {
	case "ai.provider":
		return cfg.Provider, nil
	case "ai.embedding_model":
		return cfg.EmbeddingModel, nil
	case "ai.generation_model":
		return cfg.GenerationModel, nil
	case "ai.dimensions":
		return fmt.Sprintf("%d", cfg.Dimensions), nil
	case "ai.bedrock.profile":
		return cfg.Bedrock.Profile, nil
	case "ai.bedrock.region":
		return cfg.Bedrock.Region, nil
	case "ai.openrouter.api_key_env":
		return cfg.OpenRouter.APIKeyEnv, nil
	case "ai.ollama.endpoint":
		return cfg.Ollama.Endpoint, nil
	default:
		return "", unknownConfigKeyError(key)
	}
}

func setConfigValue(cfg *ai.AIConfig, key, value string) error {
	switch key {
	case "ai.provider":
		cfg.Provider = value
	case "ai.embedding_model":
		cfg.EmbeddingModel = value
	case "ai.generation_model":
		cfg.GenerationModel = value
	case "ai.dimensions":
		var d int
		if _, err := fmt.Sscanf(value, "%d", &d); err != nil {
			return fmt.Errorf("dimensions must be a number")
		}
		cfg.Dimensions = d
	case "ai.bedrock.profile":
		cfg.Bedrock.Profile = value
	case "ai.bedrock.region":
		cfg.Bedrock.Region = value
	case "ai.openrouter.api_key_env":
		cfg.OpenRouter.APIKeyEnv = value
	case "ai.ollama.endpoint":
		cfg.Ollama.Endpoint = value
	default:
		return unknownConfigKeyError(key)
	}
	return nil
}
