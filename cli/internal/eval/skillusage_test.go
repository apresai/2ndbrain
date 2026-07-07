package eval

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/skills"
)

// usageTask is one skill-selection case: a natural task prompt, the command
// or tool families a well-taught agent should reach for, and the ones the
// skill explicitly warns away from.
type usageTask struct {
	name      string
	prompt    string
	allowed   []string // regexes; any match = correct selection
	forbidden []string // regexes; any match = definite miss
}

var usageTasks = []usageTask{
	{
		name:      "semantic recall uses search, not grep",
		prompt:    "Find my notes about JWT token rotation.",
		allowed:   []string{`2nb search`, `kb_search`},
		forbidden: []string{`\bgrep\b`, `\brg\b`, `\bfind\b .*-name`},
	},
	{
		name:      "question answering uses ask",
		prompt:    "What did we decide about the reservation data model? Answer from my notes.",
		allowed:   []string{`2nb ask`, `kb_ask`},
		forbidden: []string{`\bgrep\b`},
	},
	{
		name:      "capture searches before creating",
		prompt:    "Save a new note titled Deploy Checklist about our release steps. What do you run first?",
		allowed:   []string{`2nb search`, `kb_search`},
		forbidden: []string{`^echo .*>`, `\btouch\b`},
	},
	{
		name:      "renaming a note uses move or rename, never mv",
		prompt:    "Rename the note runbook-old.md to deploy-runbook.md so links keep working.",
		allowed:   []string{`2nb (move|rename)`},
		forbidden: []string{`\bmv\b`, `os.Rename`},
	},
	{
		name:      "frontmatter edits use meta, never sed",
		prompt:    "Set status to accepted in the frontmatter of adr-12.md.",
		allowed:   []string{`2nb meta`, `kb_update_meta`},
		forbidden: []string{`\bsed\b`, `\bawk\b`},
	},
	{
		name:      "broken links vault-wide use unresolved",
		prompt:    "List every broken wikilink in the vault.",
		allowed:   []string{`2nb unresolved`, `link:unresolved`, `2nb lint`},
		forbidden: []string{`\bgrep\b`},
	},
	{
		name:      "inbound links use backlinks",
		prompt:    "Which notes link to concepts/auth.md?",
		allowed:   []string{`2nb backlinks`, `kb_backlinks`},
		forbidden: []string{`\bgrep\b`},
	},
	{
		name:      "search output parsing uses the envelope",
		prompt:    "You ran 2nb search --json. Which JSON field holds the result list?",
		allowed:   []string{`\.?results\b`, `"results"`},
		forbidden: []string{`"documents"`, `"hits"`},
	},
	{
		name:      "new note creation uses create, not raw files",
		prompt:    "Create a brand-new ADR note titled Vector Store Choice.",
		allowed:   []string{`2nb create`, `kb_create`},
		forbidden: []string{`^echo .*>`, `\btouch\b`, `cat >`},
	},
	{
		name:      "body appends use append",
		prompt:    "Add a paragraph to the end of meeting-notes.md.",
		allowed:   []string{`2nb append`, `kb_append`},
		forbidden: []string{`>>`, `\bsed\b`},
	},
	{
		name:      "degraded search is detected via mode",
		prompt:    "2nb search --json returned mode keyword instead of hybrid. What does that mean?",
		allowed:   []string{`(vector|semantic|embedding)`, `keyword`},
		forbidden: []string{},
	},
	{
		name:      "daily capture uses daily append",
		prompt:    "Add a line to today's daily note.",
		allowed:   []string{`2nb daily append`, `daily:append`},
		forbidden: []string{`^echo .*>`},
	},
}

// TestSkillUsageSelection feeds the canonical SKILL.md to a real generation
// model as its system prompt and scores whether it reaches for the commands
// the skill teaches. Opt-in twice over (real tokens, model-dependent): skips
// unless AWS credentials resolve AND 2NB_SKILL_EVAL=1. Run via
// `make test-skill-eval`. Asserts a loose floor so model drift doesn't flake
// the suite; the per-task log is the real signal.
func TestSkillUsageSelection(t *testing.T) {
	if os.Getenv("2NB_SKILL_EVAL") != "1" {
		t.Skip("set 2NB_SKILL_EVAL=1 to run the skill-usage eval (spends real tokens)")
	}
	ctx := context.Background()
	cfg := ai.BedrockConfig{Profile: "default", Region: "us-east-1"}
	if !ai.CheckBedrockCredentials(ctx, cfg) {
		t.Skip("AWS credentials not configured")
	}
	gen, err := ai.NewBedrockGenerator(ctx, cfg, "us.anthropic.claude-haiku-4-5-20251001-v1:0")
	if err != nil {
		t.Skipf("generator: %v", err)
	}

	system := skills.CanonicalContent() +
		"\n\nYou are an agent working in a vault governed by the skill above. " +
		"For each task, reply with ONLY the exact command line or MCP tool call you would run first. One line. No prose."

	correct := 0
	for _, task := range usageTasks {
		tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		reply, gerr := gen.Generate(tctx, task.prompt, ai.GenOpts{MaxTokens: 120, SystemPrompt: system})
		cancel()
		if gerr != nil {
			t.Logf("MISS  %-45s generate error: %v", task.name, gerr)
			continue
		}
		ok := false
		for _, pat := range task.allowed {
			if regexp.MustCompile(pat).MatchString(reply) {
				ok = true
				break
			}
		}
		for _, pat := range task.forbidden {
			if regexp.MustCompile(pat).MatchString(reply) {
				ok = false
				break
			}
		}
		if ok {
			correct++
			t.Logf("OK    %-45s %q", task.name, firstLine(reply))
		} else {
			t.Logf("MISS  %-45s %q", task.name, firstLine(reply))
		}
	}

	accuracy := float64(correct) / float64(len(usageTasks))
	t.Logf("skill-usage selection accuracy: %.0f%% (%d/%d)", 100*accuracy, correct, len(usageTasks))
	if accuracy < 0.7 {
		t.Errorf("selection accuracy %.0f%% below the 70%% floor — the skill may have stopped teaching effectively", 100*accuracy)
	}
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
