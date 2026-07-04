package cli

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
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

var configGetEffective bool

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a configuration value",
	Long: `Get a configuration value.

With --effective on ai.similarity_threshold, print the RESOLVED threshold and
its source (vault config > user calibration > model recommendation > default)
instead of the raw stored value, which is often 0/unset because the real value
falls through to the active embedding model's recommendation.`,
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
	configGetCmd.Flags().BoolVar(&configGetEffective, "effective", false,
		"Print the resolved value + source instead of the raw stored value (ai.similarity_threshold only)")
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
	VaultRoot string `json:"vault_root" yaml:"vault_root"`
	VaultDir  string `json:"vault_dir" yaml:"vault_dir"`
	VaultName string `json:"vault_name" yaml:"vault_name"`
	Config    any    `json:"config" yaml:"config"`
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

	// --effective resolves the value through its full resolution chain rather
	// than reading the raw stored key. Only ai.similarity_threshold has a
	// resolution chain today (raw is often 0/unset, with the real value coming
	// from the model recommendation), so --effective is meaningful only there.
	if configGetEffective {
		if key != "ai.similarity_threshold" {
			return exitWithError(ExitValidation, fmt.Sprintf("--effective is only supported for ai.similarity_threshold (got %q)", key))
		}
		threshold, src := v.Config.AI.ResolveSimilarityThresholdFull(v.Root)
		if flagPorcelain {
			fmt.Println(strconv.FormatFloat(threshold, 'g', -1, 64))
		} else {
			fmt.Printf("%g (%s)\n", threshold, src)
		}
		return nil
	}

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
	setupFileLogging(v)

	key, value := args[0], args[1]
	if err := setConfigValue(&v.Config.AI, key, value); err != nil {
		return err
	}

	// Changing the embedding model must also resync ai.dimensions. The
	// dimension is threaded into every embedder constructor (and, for Bedrock,
	// sent as the requested output width), so a stale dimension produces a
	// DIMENSION BREAK that silently disables semantic search. `ai setup`
	// already keeps the pair in sync; this is the path the GUI "Set Active"
	// and a bare `config set ai.embedding_model` take, which previously left
	// the dimension stale.
	if key == "ai.embedding_model" {
		resyncEmbeddingDimensions(v, value, os.Stderr)
	}

	if err := v.Config.Save(v.DotDir); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	slog.Info("config set", "key", key, "value", value)

	// Surface (loudly, but non-blocking) any internal inconsistency the write
	// introduced — e.g. an active model that belongs to a different provider
	// than ai.provider, which would silently break search/generation. We warn
	// rather than refuse so a legitimate step-by-step reconfigure (set the
	// provider, then each model) isn't blocked midway.
	for _, issue := range v.Config.AI.Validate(v.Root) {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", issue)
		slog.Warn("ai config inconsistency", "key", key, "issue", issue)
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Set %s = %s\n", key, value)
	}
	return nil
}

// resyncEmbeddingDimensions updates v.Config.AI.Dimensions to match the
// embedding model `embedID` resolves to, and (when existing embeddings would
// be invalidated) prints a re-embed hint to `warn`. Factored out of
// runConfigSet so the wizard's --set-active path takes the EXACT same dimension
// sync the `config set ai.embedding_model` path takes. A stale dimension is a
// silent DIMENSION BREAK that disables semantic search.
func resyncEmbeddingDimensions(v *vault.Vault, embedID string, warn io.Writer) {
	dims := ai.EmbeddingDimensionsFor(v.Root, v.Config.AI.Provider, embedID)
	if dims <= 0 || dims == v.Config.AI.Dimensions {
		return
	}
	if v.DB != nil {
		if embCount, _ := v.DB.EmbeddingCount(); embCount > 0 {
			fmt.Fprintf(warn,
				"Note: embedding dimension changes from %d to %d; run `2nb index --force-reembed` to re-embed %d existing document(s).\n",
				v.Config.AI.Dimensions, dims, embCount)
		}
	}
	v.Config.AI.Dimensions = dims
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
	"ai.similarity_threshold",
	"ai.bm25_weight",
	"ai.vector_weight",
	"ai.rag_context_budget",
	"ai.rag_note_budget",
	"ai.embed_concurrency",
	"ai.rerank.enabled",
	"ai.rerank.model",
	"ai.rerank.candidate_docs",
	"ai.bedrock.profile",
	"ai.bedrock.region",
	"ai.bedrock.disabled",
	"ai.openrouter.api_key_env",
	"ai.openrouter.disabled",
	"ai.ollama.endpoint",
	"ai.ollama.disabled",
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
	case "ai.similarity_threshold":
		return strconv.FormatFloat(cfg.SimilarityThreshold, 'g', -1, 64), nil
	case "ai.bm25_weight":
		return strconv.FormatFloat(cfg.BM25Weight, 'g', -1, 64), nil
	case "ai.vector_weight":
		return strconv.FormatFloat(cfg.VectorWeight, 'g', -1, 64), nil
	case "ai.rag_context_budget":
		return strconv.Itoa(cfg.RAGContextBudgetRunes), nil
	case "ai.rag_note_budget":
		return strconv.Itoa(cfg.RAGNoteBudgetRunes), nil
	case "ai.embed_concurrency":
		return strconv.Itoa(cfg.EmbedConcurrency), nil
	case "ai.rerank.enabled":
		return strconv.FormatBool(cfg.Rerank.Enabled), nil
	case "ai.rerank.model":
		return cfg.Rerank.Model, nil
	case "ai.rerank.candidate_docs":
		return strconv.Itoa(cfg.Rerank.CandidateDocs), nil
	case "ai.bedrock.profile":
		return cfg.Bedrock.Profile, nil
	case "ai.bedrock.region":
		return cfg.Bedrock.Region, nil
	case "ai.bedrock.disabled":
		return strconv.FormatBool(cfg.Bedrock.Disabled), nil
	case "ai.openrouter.api_key_env":
		return cfg.OpenRouter.APIKeyEnv, nil
	case "ai.openrouter.disabled":
		return strconv.FormatBool(cfg.OpenRouter.Disabled), nil
	case "ai.ollama.endpoint":
		return cfg.Ollama.Endpoint, nil
	case "ai.ollama.disabled":
		return strconv.FormatBool(cfg.Ollama.Disabled), nil
	default:
		return "", unknownConfigKeyError(key)
	}
}

func containsInt(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func intsCSV(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ", ")
}

func setConfigValue(cfg *ai.AIConfig, key, value string) error {
	switch key {
	case "ai.provider":
		if !ai.IsKnownProvider(value) {
			return fmt.Errorf("unknown provider %q; valid providers: %s", value, strings.Join(ai.KnownProviders, ", "))
		}
		cfg.Provider = value
		// Activating a provider clears its disabled flag. An active-but-disabled
		// provider is contradictory: the CLI still runs it, but the GUI hides
		// every one of its models, so the two surfaces disagree about the same
		// vault. Mirrors `ai setup`, which enables the chosen provider.
		cfg.SetProviderDisabled(value, false)
	case "ai.embedding_model":
		cfg.EmbeddingModel = value
	case "ai.generation_model":
		cfg.GenerationModel = value
	case "ai.dimensions":
		var d int
		if _, err := fmt.Sscanf(value, "%d", &d); err != nil {
			return fmt.Errorf("dimensions must be a number")
		}
		if d <= 0 {
			return fmt.Errorf("dimensions must be a positive integer")
		}
		// Matryoshka models emit only specific output widths; an unsupported
		// value is rejected by the provider at embed time (a silent search
		// break), so refuse it here against the model's declared set. Models
		// that declare no set (SupportedDimensionsFor == nil) are unconstrained.
		if supported := ai.SupportedDimensionsFor("", cfg.Provider, cfg.EmbeddingModel); len(supported) > 0 && !containsInt(supported, d) {
			return fmt.Errorf("dimension %d not supported by %s (Matryoshka widths: %s)", d, cfg.EmbeddingModel, intsCSV(supported))
		}
		cfg.Dimensions = d
	case "ai.similarity_threshold":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("similarity_threshold must be a float between 0 and 1 (got %q)", value)
		}
		if f < 0 || f > 1 {
			return fmt.Errorf("similarity_threshold must be between 0 and 1 (got %g)", f)
		}
		cfg.SimilarityThreshold = f
	case "ai.bm25_weight":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("bm25_weight must be a finite non-negative number (got %q); 0 resolves to the default 1.0", value)
		}
		cfg.BM25Weight = f
	case "ai.vector_weight":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("vector_weight must be a finite non-negative number (got %q); 0 resolves to the default 1.0", value)
		}
		cfg.VectorWeight = f
	case "ai.rag_context_budget":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 400000 {
			return fmt.Errorf("rag_context_budget must be a non-negative rune count <= 400000 (got %q); 0 resolves to the default %d", value, ai.DefaultRAGContextBudgetRunes)
		}
		cfg.RAGContextBudgetRunes = n
	case "ai.rag_note_budget":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 400000 {
			return fmt.Errorf("rag_note_budget must be a non-negative rune count <= 400000 (got %q); 0 resolves to the default %d", value, ai.DefaultRAGNoteBudgetRunes)
		}
		cfg.RAGNoteBudgetRunes = n
	case "ai.embed_concurrency":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 64 {
			return fmt.Errorf("embed_concurrency must be an integer between 1 and 64 (got %q); 0/unset resolves to the per-provider default", value)
		}
		cfg.EmbedConcurrency = n
	case "ai.rerank.enabled":
		b, err := parseConfigBool(key, value)
		if err != nil {
			return err
		}
		cfg.Rerank.Enabled = b
	case "ai.rerank.model":
		cfg.Rerank.Model = value
	case "ai.rerank.candidate_docs":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 100 {
			return fmt.Errorf("rerank.candidate_docs must be an integer between 0 and 100 (got %q); 0/unset resolves to the default %d, and Bedrock Rerank caps one query at 100 docs", value, ai.DefaultRerankCandidateDocs)
		}
		cfg.Rerank.CandidateDocs = n
	case "ai.bedrock.profile":
		cfg.Bedrock.Profile = value
	case "ai.bedrock.region":
		cfg.Bedrock.Region = value
	case "ai.bedrock.disabled":
		b, err := parseConfigBool(key, value)
		if err != nil {
			return err
		}
		cfg.Bedrock.Disabled = b
	case "ai.openrouter.api_key_env":
		cfg.OpenRouter.APIKeyEnv = value
	case "ai.openrouter.disabled":
		b, err := parseConfigBool(key, value)
		if err != nil {
			return err
		}
		cfg.OpenRouter.Disabled = b
	case "ai.ollama.endpoint":
		cfg.Ollama.Endpoint = value
	case "ai.ollama.disabled":
		b, err := parseConfigBool(key, value)
		if err != nil {
			return err
		}
		cfg.Ollama.Disabled = b
	default:
		return unknownConfigKeyError(key)
	}
	return nil
}

// parseConfigBool centralizes the accepted spellings of a boolean config
// value so `config set ai.bedrock.disabled true` / `1` / `yes` all work
// uniformly. GUI callers always pass literal "true"/"false" from
// String(Bool), but terminal users reach for `yes`/`no` / `on`/`off`.
func parseConfigBool(key, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y", "on":
		return true, nil
	case "false", "0", "no", "n", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s must be a boolean (got %q); accepted: true/false, yes/no, on/off, 1/0", key, value)
}
