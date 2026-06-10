package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Interactive multi-turn Q&A with your knowledge base",
	Long: `A terminal REPL over the same multi-turn RAG pipeline as ` + "`2nb ask --history`" + `:
each question is condensed against the conversation so far, answered from
retrieved documents, and cited with sources. The conversation lives in this
process only; nothing is persisted.

Interactive only (no --json). For scripted multi-turn use, call
` + "`2nb ask --history <path|->`" + ` instead.`,
	Example: `  2nb chat
  > what is the deploy rotation?
  > who owns it?
  > exit`,
	Args: cobra.NoArgs,
	RunE: runChat,
}

func init() {
	chatCmd.GroupID = "ai"
	rootCmd.AddCommand(chatCmd)
}

func runChat(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	generator, err := ai.DefaultRegistry.Generator(cfg.Provider)
	if err != nil {
		return fmt.Errorf("no generation provider: %w\nRun `2nb ai status` to check provider configuration", err)
	}
	if !generator.Available(ctx) {
		return fmt.Errorf("generation provider %q not available", cfg.Provider)
	}

	fmt.Fprintf(os.Stderr, "2ndbrain chat: vault %q. Answers cite source notes. Type 'exit' or Ctrl-D to quit.\n", v.Config.Name)
	return chatLoop(ctx, v, generator, os.Stdin, os.Stdout)
}

// chatLoop is the REPL body, separated from runChat so a test can drive it
// with a scripted reader against a real provider. Turns are appended to the
// in-process history only after a successful answer, so a failed ask never
// poisons the conversation. The history slice is re-trimmed each turn to
// bound memory in long sessions (the prompt-side cap lives in ai.TrimHistory).
func chatLoop(ctx context.Context, v *vault.Vault, generator ai.GenerationProvider, in io.Reader, out io.Writer) error {
	var history []ai.ChatTurn
	scanner := bufio.NewScanner(in)
	// Default Scanner tokens cap at 64KB; a long pasted question would end
	// the whole session with ErrTooLong. Allow up to 1MB per line instead.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for {
		fmt.Fprint(os.Stderr, "\n> ")
		if !scanner.Scan() {
			fmt.Fprintln(os.Stderr)
			break // EOF (Ctrl-D) or read error: end the session.
		}
		question := strings.TrimSpace(scanner.Text())
		if question == "" {
			continue
		}
		if question == "exit" || question == "quit" {
			break
		}

		resp, err := askOnce(ctx, v, generator, question, history)
		if err != nil {
			// Render the error and keep the session alive; the failed turn
			// is not recorded, so the next question starts clean.
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}

		fmt.Fprintln(out, resp.Answer)
		if len(resp.Sources) > 0 {
			fmt.Fprintf(os.Stderr, "\nSources: %s\n", strings.Join(resp.Sources, ", "))
		}

		history = append(history,
			ai.ChatTurn{Role: "user", Content: question},
			ai.ChatTurn{Role: "assistant", Content: resp.Answer},
		)
		history = ai.TrimHistory(history)
	}

	slog.Info("chat session ended", "turns", len(history))
	return scanner.Err()
}
