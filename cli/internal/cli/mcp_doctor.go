package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	mcppkg "github.com/apresai/2ndbrain/internal/mcp"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var mcpDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "End-to-end self-test of the MCP engine (proves it actually answers)",
	Long: `Exercises the MCP engine in-process — counts the registered tools and runs
real kb_info / kb_list / kb_search round-trips against this vault — so you get a
true "does it work?" answer, not just "is it configured?". Also reports AI
readiness, whether the server is wired into ~/.claude.json, the initialize
instructions string, and reliability signals (WAL size, stale server count).

Engine checks are hard (a failure exits 2); AI readiness and the
wiring/reliability signals are warnings, so doctor stays a usable offline/CI
gate. The GUI "Verify" button runs this alongside a real model test.`,
	RunE: runMCPDoctor,
}

func init() {
	mcpCmd.AddCommand(mcpDoctorCmd)
}

// MCPDoctorReport is the JSON payload of `2nb mcp doctor`. checks[] reuses the
// shared DoctorCheck shape so the Swift decoder is common with config doctor.
type MCPDoctorReport struct {
	OK                  bool          `json:"ok"`
	Configured          bool          `json:"configured"`
	Scope               string        `json:"scope,omitempty"`
	ConfigPath          string        `json:"config_path"`
	ToolCount           int           `json:"tool_count"`
	Tools               []string      `json:"tools,omitempty"`
	ToolsExercised      []string      `json:"tools_exercised"`
	InstructionsPresent bool          `json:"instructions_present"`
	WALBytes            int64         `json:"wal_bytes"`
	DBBytes             int64         `json:"db_bytes"`
	AliveServers        int           `json:"alive_servers"`
	StaleServers        int           `json:"stale_servers"`
	Checks              []DoctorCheck `json:"checks"`
}

func runMCPDoctor(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)
	initAIProviders(v)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	report := buildMCPDoctorReport(ctx, v)

	if format := getFormat(cmd); format != "" {
		if err := output.Write(os.Stdout, format, report); err != nil {
			return err
		}
		if !report.OK {
			return exitWithError(ExitValidation, "mcp doctor: engine self-test failed")
		}
		return nil
	}

	for _, c := range report.Checks {
		mark := "✓"
		if c.Warn {
			mark = "!"
		}
		if !c.OK {
			mark = "✗"
		}
		fmt.Printf("%s %s: %s\n", mark, c.Name, c.Detail)
		if c.Fix != "" && (!c.OK || c.Warn) {
			fmt.Printf("    %s\n", c.Fix)
		}
	}
	if report.OK {
		fmt.Println("\nMCP engine self-test passed.")
		return nil
	}
	return exitWithError(ExitValidation, "mcp doctor: engine self-test failed")
}

// buildMCPDoctorReport runs the checks. Engine checks are hard (OK=false trips
// the roll-up + exit 2); readiness/wiring/reliability checks are warn-only
// (OK=true, Warn set) so the engine answering offline stays "green".
func buildMCPDoctorReport(ctx context.Context, v *vault.Vault) MCPDoctorReport {
	r := MCPDoctorReport{ToolsExercised: []string{}}

	eng := mcppkg.NewEngine(v)
	r.ToolCount = eng.ToolCount()
	r.Tools = eng.ToolNames()
	r.InstructionsPresent = mcppkg.ServerInstructions != ""

	add := func(c DoctorCheck) { r.Checks = append(r.Checks, c) }

	// HARD 1: tools registered.
	add(DoctorCheck{
		Name:   "mcp tools registered",
		OK:     r.ToolCount >= 20,
		Detail: fmt.Sprintf("%d tools registered", r.ToolCount),
		Fix:    "rebuild 2nb (CGO_ENABLED=1 -tags fts5); the MCP tool registry is empty",
	})

	// HARD 2-4: read-only tool round-trips.
	r.ToolsExercised = append(r.ToolsExercised, "kb_info")
	add(engineCheck(ctx, eng, "kb_info round-trip", "kb_info", nil,
		"kb_info failed — the vault DB is unreadable; run `2nb index` and check the vault path"))

	r.ToolsExercised = append(r.ToolsExercised, "kb_list")
	add(engineCheck(ctx, eng, "kb_list round-trip", "kb_list", map[string]any{"limit": 1},
		"kb_list failed — the document index is unreadable"))

	r.ToolsExercised = append(r.ToolsExercised, "kb_search")
	add(engineSearchCheck(ctx, eng, v))

	// WARN: AI readiness (a lightweight probe; the GUI runs a real model test).
	embedder, eerr := ai.DefaultRegistry.Embedder(v.Config.AI.Provider)
	aiReady := eerr == nil && embedder != nil && embedder.Available(ctx)
	add(DoctorCheck{
		Name:   "ai readiness",
		OK:     true,
		Warn:   !aiReady,
		Detail: aiReadinessDetail(v.Config.AI.Provider, aiReady),
		Fix:    "run `2nb ai status` (search falls back to BM25 meanwhile)",
	})

	// WARN: configured in ~/.claude.json.
	cs := mcppkg.Configured(v)
	r.Configured = cs.Configured
	r.Scope = cs.Scope
	r.ConfigPath = cs.ConfigPath
	add(DoctorCheck{
		Name:   "mcp configured (~/.claude.json)",
		OK:     true,
		Warn:   !cs.Configured,
		Detail: configuredDetail(cs),
		Fix:    "run `2nb mcp install` (or click Configure automatically in the app)",
	})

	// WARN: instructions string present.
	add(DoctorCheck{
		Name:   "initialize instructions",
		OK:     true,
		Warn:   !r.InstructionsPresent,
		Detail: instructionsDetail(r.InstructionsPresent),
		Fix:    "upgrade 2nb; the server announces no instructions string",
	})

	// WARN: reliability — WAL health.
	r.WALBytes = fileBytes(v.DB.WALPath())
	r.DBBytes = fileBytes(v.DB.Path())
	walBloated := r.WALBytes > r.DBBytes && r.WALBytes > 1<<20
	add(DoctorCheck{
		Name:   "index WAL health",
		OK:     true,
		Warn:   walBloated,
		Detail: fmt.Sprintf("WAL %s, DB %s", humanBytes(r.WALBytes), humanBytes(r.DBBytes)),
		Fix:    "run `2nb vault checkpoint` to collapse the WAL",
	})

	// WARN: reliability — stale/orphan servers.
	alive, _ := mcppkg.ListStatuses(v)
	r.AliveServers = len(alive)
	stale, _, _ := mcppkg.StaleServers(v, 6*time.Hour, time.Now(), os.Getpid())
	r.StaleServers = len(stale)
	add(DoctorCheck{
		Name:   "mcp server processes",
		OK:     true,
		Warn:   r.StaleServers > 0,
		Detail: fmt.Sprintf("%d running, %d stale (>6h idle)", r.AliveServers, r.StaleServers),
		Fix:    "run `2nb mcp reap` to terminate stale servers",
	})

	r.OK = true
	for _, c := range r.Checks {
		if !c.OK {
			r.OK = false
		}
	}
	return r
}

// engineCheck invokes a read-only tool and turns the result into a hard check.
func engineCheck(ctx context.Context, eng *mcppkg.Engine, name, tool string, args map[string]any, failFix string) DoctorCheck {
	text, isErr, err := eng.Call(ctx, tool, args)
	if err != nil || isErr {
		detail := tool + " errored"
		if err != nil {
			detail = trimDetail(err.Error())
		} else if text != "" {
			detail = trimDetail(text)
		}
		return DoctorCheck{Name: name, OK: false, Detail: detail, Fix: failFix}
	}
	return DoctorCheck{Name: name, OK: true, Detail: tool + " answered"}
}

// engineSearchCheck runs kb_search and inspects the PAYLOAD, not just the error.
//
// retrieve.Retrieve degrades to BM25 whenever the vector channel is unusable
// (expired credentials, dimension break, provider unreachable) and reports that
// only through the per-result search_mode field. A check that looked at the
// error alone therefore passed with a straight face on a vault whose semantic
// search was completely dead — the exact false confidence this command exists
// to prevent. So: a vault that HAS embeddings and still answers in BM25 is a
// hard failure, not a green tick.
func engineSearchCheck(ctx context.Context, eng *mcppkg.Engine, v *vault.Vault) DoctorCheck {
	const name = "kb_search round-trip"
	const failFix = "kb_search failed — the search index (FTS5/embeddings) is unusable"

	text, isErr, err := eng.Call(ctx, "kb_search", map[string]any{"query": "knowledge", "limit": 1})
	if err != nil || isErr {
		detail := "kb_search errored"
		if err != nil {
			detail = trimDetail(err.Error())
		} else if text != "" {
			detail = trimDetail(text)
		}
		return DoctorCheck{Name: name, OK: false, Detail: detail, Fix: failFix}
	}

	mode, observable := firstSearchMode(text)
	embedded, cerr := v.DB.EmbeddingCount()
	if cerr != nil {
		// Can't tell whether BM25 is legitimate; don't manufacture a failure
		// out of a DB read error the portability checks will report properly.
		embedded = 0
	}
	return classifySearchMode(name, mode, observable, embedded)
}

// classifySearchMode is the verdict half of engineSearchCheck, split out so the
// BM25-fallback rule is unit-testable without a live provider (the no-mock
// policy rules out faking a dead vector channel through the real engine).
func classifySearchMode(name, mode string, observable bool, embedded int) DoctorCheck {
	switch {
	case !observable:
		// No rows came back, so the payload carries no mode to judge. The engine
		// answered, which is what this check is nominally about.
		return DoctorCheck{
			Name:   name,
			OK:     true,
			Detail: "kb_search answered (no results for the probe query; search mode not observable)",
		}
	case mode == string(search.ModeHybrid):
		return DoctorCheck{Name: name, OK: true, Detail: "kb_search answered via hybrid search (vector channel live)"}
	case embedded == 0:
		// BM25 is the correct and only possible answer for a vault with no
		// vectors. Worth saying, not worth failing.
		return DoctorCheck{
			Name:   name,
			OK:     true,
			Warn:   true,
			Detail: fmt.Sprintf("kb_search answered in %s mode — this vault has no embeddings yet", mode),
			Fix:    "run `2nb index` to embed the vault and enable semantic search",
		}
	default:
		return DoctorCheck{
			Name:   name,
			OK:     false,
			Detail: fmt.Sprintf("kb_search silently fell back to %s despite %d embedded documents — the vector channel is down", mode, embedded),
			Fix:    "run `2nb ai status` to see why (expired credentials, dimension break, or an unreachable provider)",
		}
	}
}

// firstSearchMode pulls search_mode off the first kb_search result. Reports
// false when the payload has no rows to read it from.
func firstSearchMode(payload string) (string, bool) {
	var rows []struct {
		SearchMode string `json:"search_mode"`
	}
	if err := json.Unmarshal([]byte(payload), &rows); err != nil || len(rows) == 0 {
		return "", false
	}
	return rows[0].SearchMode, rows[0].SearchMode != ""
}

func aiReadinessDetail(provider string, ready bool) string {
	if ready {
		return fmt.Sprintf("provider %q embedder reachable", provider)
	}
	return fmt.Sprintf("provider %q not ready (search uses BM25)", provider)
}

func configuredDetail(cs mcppkg.ConfiguredStatus) string {
	if cs.Configured {
		if cs.Scope != "" {
			return fmt.Sprintf("configured (%s scope) in ~/.claude.json", cs.Scope)
		}
		return "configured in ~/.claude.json"
	}
	return "not configured in ~/.claude.json"
}

func instructionsDetail(present bool) string {
	if present {
		return "server announces an instructions string at initialize"
	}
	return "no instructions string (server may look absent in clients)"
}

func fileBytes(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

func trimDetail(s string) string {
	const max = 160
	// Drop trailing newlines first, for one-line display.
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return "(no detail)"
	}
	// Truncate by runes, not bytes: a byte slice would split a multi-byte rune
	// and emit invalid UTF-8 into the JSON payload.
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
