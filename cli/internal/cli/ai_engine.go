package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/llama"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var aiEngineCmd = &cobra.Command{
	Use:   "engine",
	Short: "Manage the bundled local inference engine (llama.cpp)",
	Long: `Manage the always-on local inference engine that powers the "llama-local"
provider (Gemma 4 generation, EmbeddingGemma embeddings, and cross-encoder
reranking). The engine runs as a launchd background agent, decoupled from any
GUI: it starts at login and stays resident until stopped. The Go CLI never links
llama.cpp — it talks to the engine over localhost HTTP.`,
	// Bare `2nb ai engine` shows status, matching the other parent commands.
	RunE: runAIEngineStatus,
}

var (
	engineServeGen           string
	engineServeEmbed         string
	engineServeRerank        string
	engineServeEngine        string
	engineInstallCmdOverride string
)

func init() {
	aiCmd.AddCommand(aiEngineCmd)
	aiEngineCmd.AddCommand(
		aiEngineServeCmd,
		aiEngineInstallCmd,
		aiEngineUninstallCmd,
		aiEngineStartCmd,
		aiEngineStopCmd,
		aiEngineStatusCmd,
		aiEnginePullCmd,
	)
	aiEngineServeCmd.Flags().StringVar(&engineServeGen, "gen-model", "", "generation model id to serve")
	aiEngineServeCmd.Flags().StringVar(&engineServeEmbed, "embed-model", "", "embedding model id to serve")
	aiEngineServeCmd.Flags().StringVar(&engineServeRerank, "rerank-model", "", "rerank model id to serve")
	aiEngineServeCmd.Flags().StringVar(&engineServeEngine, "engine-path", "", "override the llama-server binary path")
	aiEngineInstallCmd.Flags().StringVar(&engineInstallCmdOverride, "command", "", "path to the 2nb binary the agent should run (default: this binary)")
}

var aiEngineStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show engine binary, launchd agent, and per-role health",
	RunE:  runAIEngineStatus,
}

var aiEngineServeCmd = &cobra.Command{
	Use:    "serve",
	Short:  "Run the engine supervisor in the foreground (launchd runs this)",
	Long:   "Starts and supervises the llama-server role processes until interrupted. Normally run by the launchd agent, not by hand.",
	RunE:   runAIEngineServe,
	Hidden: true,
}

var aiEngineInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install and start the always-on engine (launchd agent, macOS)",
	RunE:  runAIEngineInstall,
}

var aiEngineUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop and remove the launchd engine agent",
	RunE:  func(cmd *cobra.Command, _ []string) error { return llama.UninstallAgent() },
}

var aiEngineStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start (bootstrap) the launchd engine agent",
	RunE:  func(cmd *cobra.Command, _ []string) error { return llama.Bootstrap() },
}

var aiEngineStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop (bootout) the launchd engine agent and free its RAM",
	RunE:  func(cmd *cobra.Command, _ []string) error { return llama.Bootout() },
}

var aiEnginePullCmd = &cobra.Command{
	Use:   "pull <model-id>...",
	Short: "Download and sha256-verify local model weights into the cache",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runAIEnginePull,
}

func runAIEngineServe(cmd *cobra.Command, _ []string) error {
	gen, embed, rerank, enginePath := engineServeGen, engineServeEmbed, engineServeRerank, engineServeEngine

	// No models on the command line (a hand-run serve): fall back to the open
	// vault's config. Under launchd the models are baked into the plist args by
	// `install`, so this path isn't taken at login.
	if gen == "" && embed == "" && rerank == "" {
		if v, err := openVault(); err == nil {
			defer v.Close()
			cfg := v.Config.AI
			gen, embed = cfg.GenerationModel, cfg.EmbeddingModel
			if cfg.Rerank.Enabled {
				rerank = cfg.Rerank.Model
				if rerank == "" {
					rerank = ai.DefaultLlamaRerankModel
				}
			}
			if enginePath == "" {
				enginePath = cfg.Llama.EnginePath
			}
		}
	}

	specs, warnings := engineSpecsFor(gen, embed, rerank)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if len(specs) == 0 {
		return fmt.Errorf("no local models available to serve; download them first, e.g. `2nb ai engine pull embeddinggemma-300m gemma4-e4b`")
	}

	mgr, err := llama.NewManager(enginePath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(os.Stderr, "2nb engine: serving %d role(s); Ctrl-C to stop\n", len(specs))
	return mgr.Serve(ctx, specs)
}

// engineSpecsFor resolves configured model ids to RoleSpecs, skipping (with a
// warning) any model that isn't in the manifest or hasn't been downloaded.
func engineSpecsFor(gen, embed, rerank string) ([]llama.RoleSpec, []string) {
	var specs []llama.RoleSpec
	var warnings []string
	add := func(role llama.Role, id string) {
		if id == "" {
			return
		}
		art, ok := llama.ArtifactFor(id)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("%s: %q is not a known local model (skipped)", role, id))
			return
		}
		path, err := llama.ModelPath(id, art.File)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", role, err))
			return
		}
		if _, err := os.Stat(path); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s model %q not downloaded — run: 2nb ai engine pull %s", role, id, id))
			return
		}
		specs = append(specs, llama.RoleSpec{Role: role, ModelPath: path})
	}
	add(llama.RoleGen, gen)
	add(llama.RoleEmbed, embed)
	add(llama.RoleRerank, rerank)
	return specs, warnings
}

// anyLocalModelConfigured reports whether any of the given model ids is a known
// local (manifest) model — used to keep `install` from wiring up an agent that
// would have nothing to serve.
func anyLocalModelConfigured(ids ...string) bool {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := llama.ArtifactFor(id); ok {
			return true
		}
	}
	return false
}

func runAIEngineInstall(cmd *cobra.Command, _ []string) error {
	exe := engineInstallCmdOverride
	if exe == "" {
		e, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve this binary: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(e); err == nil {
			e = resolved
		}
		exe = e
	}

	// Resolve the vault's model choices to bake into the plist so serve is
	// deterministic at login without needing an open vault.
	var extra []string
	var gen, embed, rerank string
	if v, err := openVault(); err == nil {
		defer v.Close()
		cfg := v.Config.AI
		gen, embed = cfg.GenerationModel, cfg.EmbeddingModel
		if cfg.Rerank.Enabled {
			rerank = cfg.Rerank.Model
			if rerank == "" {
				rerank = ai.DefaultLlamaRerankModel
			}
		}
		if cfg.Llama.EnginePath != "" {
			extra = append(extra, "--engine-path", cfg.Llama.EnginePath)
		}
	}

	// Refuse to install an agent with nothing local to serve: on a non-local
	// vault serve would find no manifest models, exit non-zero, and launchd's
	// KeepAlive would restart-loop it. (Not-yet-downloaded local models are OK —
	// that's the install-then-`pull` flow.)
	if !anyLocalModelConfigured(gen, embed, rerank) {
		return fmt.Errorf("no local models configured for this vault; set ai.provider=llama-local and a local ai.embedding_model/ai.generation_model (e.g. embeddinggemma-300m / gemma4-e4b) before installing the engine agent")
	}
	if gen != "" {
		extra = append(extra, "--gen-model", gen)
	}
	if embed != "" {
		extra = append(extra, "--embed-model", embed)
	}
	if rerank != "" {
		extra = append(extra, "--rerank-model", rerank)
	}

	if err := llama.InstallAgent(exe, extra); err != nil {
		return err
	}
	fmt.Println("Installed and started the 2nb engine launchd agent.")
	fmt.Println("Check it with: 2nb ai engine status")
	return nil
}

func runAIEngineStatus(cmd *cobra.Command, _ []string) error {
	st := llama.Status(context.Background())
	if format := getFormat(cmd); format != "" {
		return output.Write(os.Stdout, format, st)
	}
	if st.EnginePath == "" {
		fmt.Println("Engine binary: NOT FOUND (bundle it, run `2nb ai engine pull`, or install llama-server on PATH)")
	} else {
		fmt.Println("Engine binary:", st.EnginePath)
	}
	fmt.Printf("launchd agent loaded: %v\n", st.AgentLoaded)
	if len(st.Roles) == 0 {
		fmt.Println("No engine roles running.")
		return nil
	}
	for _, r := range st.Roles {
		health := "unhealthy"
		if r.Healthy {
			health = "healthy"
		}
		fmt.Printf("  %-7s pid=%d port=%d %-9s model=%s\n", r.Role, r.PID, r.Port, health, r.Model)
	}
	return nil
}

func runAIEnginePull(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	var failed bool
	for _, id := range args {
		fmt.Printf("Pulling %s...\n", id)
		path, err := llama.EnsureModelProgress(ctx, id, enginePullProgress(id))
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", id, err)
			failed = true
			continue
		}
		fmt.Printf("  %s → %s\n", id, path)
	}
	if failed {
		return fmt.Errorf("one or more models could not be pulled")
	}
	return nil
}

// enginePullProgress renders a single \r-updated download progress line to
// stderr. Returns nil (no callback → no output) when stderr isn't a TTY, so
// piped/CI logs stay clean.
func enginePullProgress(id string) llama.ProgressFunc {
	if !stderrIsTTY() {
		return nil
	}
	var finished bool // latch so the terminating newline prints exactly once
	return func(done, total int64) {
		if finished {
			return
		}
		if total > 0 {
			pct := float64(done) / float64(total) * 100
			fmt.Fprintf(os.Stderr, "\r  %-22s %5.1f%%  (%.0f / %.0f MB)   ", id, pct, float64(done)/1e6, float64(total)/1e6)
			if done >= total {
				fmt.Fprintln(os.Stderr)
				finished = true
			}
		} else {
			fmt.Fprintf(os.Stderr, "\r  %-22s %.0f MB   ", id, float64(done)/1e6)
		}
	}
}
