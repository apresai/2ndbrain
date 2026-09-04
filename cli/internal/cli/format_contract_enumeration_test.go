package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The format contract, enforced by ENUMERATION rather than by inspection.
//
// --format, --json, --csv and --yaml are PERSISTENT flags on the root command,
// so every command in this binary advertises them. Cycle 1 converted the
// commands someone looked at and missed six; cycle 5 found eight more the same
// way, by hand. A table of commands someone remembered to add cannot stop that
// recurring, so this test walks the cobra tree instead: every runnable command
// must appear in EXACTLY ONE of the two maps below, and a new command fails the
// suite until it is classified.
//
// The contract each exercised command must satisfy under an explicit
// `--format json`:
//
//   - STDOUT parses as JSON, in full. Not "starts with a brace": the whole
//     buffer, so a command that prints a human line and then a record fails.
//     That is why stdout and stderr are captured SEPARATELY here and why
//     jsonPortion() is deliberately not used.
//   - or the command REFUSES and names the format (a body-less report asked for
//     raw/md, a JSON event stream asked for csv).
//
// Silently ignoring the flag, and writing nothing at all while exiting 0, are
// both failures. Those were the two shapes the 0.22.3 matrix found.

// formatContractArgv is every command this test EXERCISES, keyed by cobra
// command path, with the argv that makes it do its job in the fixture vault.
// The `--vault` flag and `--format json` are added by the runner.
var formatContractArgv = map[string][]string{
	"2nb aliases":                     {"aliases"},
	"2nb append":                      {"append", "append-target.md", "--text", "appended"},
	"2nb backlinks":                   {"backlinks", "doc.md"},
	"2nb config":                      {"config"},
	"2nb config bedrock":              {"config", "bedrock"},
	"2nb config get":                  {"config", "get", "ai.provider"},
	"2nb config set":                  {"config", "set", "ai.bm25_weight", "1.0"},
	"2nb config show":                 {"config", "show"},
	"2nb create":                      {"create", "--title", "Fixture Created", "--type", "note"},
	"2nb daily":                       {"daily"},
	"2nb daily append":                {"daily", "append", "--text", "x"},
	"2nb daily path":                  {"daily", "path"},
	"2nb daily prepend":               {"daily", "prepend", "--text", "y"},
	"2nb daily read":                  {"daily", "read"},
	"2nb deadends":                    {"deadends"},
	"2nb delete":                      {"delete", "spare.md", "--force"},
	"2nb export-context":              {"export-context"},
	"2nb folders":                     {"folders"},
	"2nb graph":                       {"graph", "doc.md"},
	"2nb index":                       {"index"},
	"2nb instructions":                {"instructions"},
	"2nb instructions configured":     {"instructions", "configured"},
	"2nb instructions install":        {"instructions", "install", "--dry-run"},
	"2nb instructions uninstall":      {"instructions", "uninstall", "--client", "claude-code"},
	"2nb links":                       {"links", "doc.md"},
	"2nb lint":                        {"lint"},
	"2nb list":                        {"list"},
	"2nb mcp":                         {"mcp"},
	"2nb mcp configured":              {"mcp", "configured"},
	"2nb mcp install":                 {"mcp", "install", "--client", "claude-code", "--dry-run"},
	"2nb mcp reap":                    {"mcp", "reap"},
	"2nb mcp status":                  {"mcp", "status"},
	"2nb mcp uninstall":               {"mcp", "uninstall", "--client", "claude-code", "--dry-run"},
	"2nb mcp-setup":                   {"mcp-setup"},
	"2nb meta":                        {"meta", "doc.md"},
	"2nb metrics":                     {"metrics"},
	"2nb metrics clear":               {"metrics", "clear"},
	"2nb metrics show":                {"metrics", "show"},
	"2nb migrate":                     {"migrate"},
	"2nb models":                      {"models"},
	"2nb models add":                  {"models", "add", "fixture.model", "--provider", "bedrock", "--type", "generation"},
	"2nb models bench compare":        {"models", "bench", "compare"},
	"2nb models bench fav":            {"models", "bench", "fav", "fixture.model", "--provider", "bedrock"},
	"2nb models bench favs":           {"models", "bench", "favs"},
	"2nb models bench history":        {"models", "bench", "history"},
	"2nb models bench unfav":          {"models", "bench", "unfav", "fixture.model", "--provider", "bedrock"},
	"2nb models cost-preview":         {"models", "cost-preview", "us.anthropic.claude-haiku-4-5-20251001-v1:0"},
	"2nb models disable":              {"models", "disable", "fixture.model", "--provider", "bedrock"},
	"2nb models enable":               {"models", "enable", "fixture.model", "--provider", "bedrock"},
	"2nb models enable-state":         {"models", "enable-state", "fixture.model", "--provider", "bedrock", "--state", "default"},
	"2nb models list":                 {"models", "list"},
	"2nb models policy":               {"models", "policy"},
	"2nb models policy clear":         {"models", "policy", "clear", "--provider", "bedrock"},
	"2nb models policy set":           {"models", "policy", "set", "--provider", "bedrock", "--enable-only", "amazon"},
	"2nb models policy show":          {"models", "policy", "show"},
	"2nb models remove":               {"models", "remove", "fixture.model", "--provider", "bedrock"},
	"2nb move":                        {"move", "movable.md", "moved.md", "--dry-run"},
	"2nb obsidian migrate-properties": {"obsidian", "migrate-properties"},
	"2nb obsidian register-types":     {"obsidian", "register-types"},
	"2nb orphans":                     {"orphans"},
	"2nb outline":                     {"outline", "doc.md"},
	"2nb plugin":                      {"plugin"},
	"2nb plugin status":               {"plugin", "status"},
	"2nb prepend":                     {"prepend", "prepend-target.md", "--text", "prepended"},
	"2nb read":                        {"read", "doc.md"},
	"2nb related":                     {"related", "doc.md"},
	"2nb relink":                      {"relink", "doc.md", "--from", "nope", "--to", "doc.md"},
	"2nb rename":                      {"rename", "movable.md", "renamed", "--dry-run"},
	"2nb repair-links":                {"repair-links", "doc.md"},
	"2nb replace":                     {"replace", "replace-target.md", "--text", "replaced"},
	"2nb search":                      {"search", "intro"},
	"2nb setup":                       {"setup", "--dry-run"},
	"2nb skills":                      {"skills"},
	"2nb skills doctor":               {"skills", "doctor"},
	"2nb skills install":              {"skills", "install", "codex"},
	"2nb skills list":                 {"skills", "list"},
	"2nb skills show":                 {"skills", "show", "codex"},
	"2nb skills uninstall":            {"skills", "uninstall", "codex"},
	"2nb stale":                       {"stale"},
	"2nb tag add":                     {"tag", "add", "tag-target.md", "fixture"},
	"2nb tag remove":                  {"tag", "remove", "tag-target.md", "x"},
	"2nb tags":                        {"tags"},
	"2nb tags list":                   {"tags", "list"},
	"2nb tags rename":                 {"tags", "rename", "x", "y", "--dry-run"},
	"2nb task":                        {"task", "task-target.md", "1"},
	"2nb tasks":                       {"tasks"},
	"2nb unlink":                      {"unlink", "doc.md", "--target", "nope"},
	"2nb unresolved":                  {"unresolved"},
	"2nb vault":                       {"vault"},
	"2nb vault checkpoint":            {"vault", "checkpoint"},
	"2nb vault list":                  {"vault", "list"},
	"2nb vault show":                  {"vault", "show"},
	"2nb vault status":                {"vault", "status"},
	"2nb wordcount":                   {"wordcount", "doc.md"},
	// These need a path this fixture creates at run time, so their argv is
	// completed in exerciseArgv rather than written here.
	"2nb export-obsidian": nil,
	"2nb import-obsidian": nil,
	"2nb vault create":    nil,
	"2nb vault set":       nil,
	// git needs a real repository; the fixture builds one best-effort and these
	// four skip individually when git is unavailable, so the ENUMERATION above
	// still holds on a machine without it.
	"2nb git":          {"git"},
	"2nb git activity": {"git", "activity"},
	"2nb git diff":     {"git", "diff", "doc.md"},
	"2nb git status":   {"git", "status"},
	"2nb git show":     nil,
}

// formatContractUnexercised names every runnable command this test does NOT
// run, with the reason. The gap is deliberate and visible: a command may only
// be here for a reason that makes running it in a unit test wrong, never
// because it fails the contract.
var formatContractUnexercised = map[string]string{
	"2nb":      "the bare root command prints help, not a payload",
	"2nb help": "cobra's generated help command prints usage text, not a payload",

	// PAID: reaches a generation or embedding model for real. The repo's
	// no-mock policy means these would spend money on every `make test`.
	// `search` and `index` ARE exercised: they embed too, and paying for an
	// embedding on an indexed fixture is the established norm here; what is
	// excluded is the generation calls and the multi-model probe batteries.
	"2nb ask":              "PAID: one generation call per run",
	"2nb chat":             "PAID and BLOCKS: an interactive REPL over the ask pipeline",
	"2nb doctor":           "PAID: probes the active models for real (only `doctor --versions` is free)",
	"2nb eval":             "PAID: a scored retrieval run over a generated QA set",
	"2nb eval answers":     "PAID: an LLM jury grades every answer",
	"2nb eval tune":        "PAID: sweeps retrieval settings across real calls",
	"2nb polish":           "PAID: one generation call per note",
	"2nb models calibrate": "PAID: embeds a sample of the vault",
	"2nb models test":      "PAID: a real smoke probe against one model",
	"2nb models verify":    "PAID: a batch of access probes, cost-gated at $0.50 by default",
	"2nb models wizard":    "PAID and INTERACTIVE: an end-to-end setup wizard",
	"2nb ai embed":         "PAID: one embedding call",
	"2nb ai embed-probe":   "PAID: embeds repeatedly to find a concurrency ceiling",
	"2nb ai setup":         "PAID and INTERACTIVE: a multi-provider wizard that probes each one",
	"2nb mcp doctor":       "PAID: embeds a probe query to prove the semantic channel works",
	"2nb suggest-links":    "PAID: embeds the note; its format contract is covered by TestContract_ReportCommandsHonorEveryFormat, which gates on the embedding capability",
	"2nb suggest-target":   "PAID: embeds the target; covered by the same test",

	// NETWORK: reaches a vendor or GitHub, so the answer depends on the runner.
	"2nb ai":              "NETWORK: `ai status` probes every configured provider's reachability",
	"2nb ai status":       "NETWORK: probes provider reachability",
	"2nb ai local":        "NETWORK: probes a local provider server",
	"2nb models discover": "NETWORK: walks both Bedrock planes across three regions",
	"2nb update":          "NETWORK: checks the GitHub releases API for CLI, app and plugin",
	"2nb plugin install":  "NETWORK: downloads the plugin bundle from a GitHub release",

	// BLOCKS or takes over the process.
	"2nb mcp-server":      "BLOCKS: a stdio server that runs until its client exits",
	"2nb ai engine":       "the parent's default is `ai engine status`, which reports on a local llama.cpp install this test does not provision",
	"2nb ai engine serve": "BLOCKS: runs the llama.cpp server in the foreground",

	// Changes state outside the vault, or needs an engine/host install.
	"2nb completion install":  "writes a shell rc file on the host",
	"2nb ai engine install":   "installs a launch agent on the host",
	"2nb ai engine uninstall": "removes a launch agent from the host",
	"2nb ai engine start":     "starts a background host process",
	"2nb ai engine stop":      "stops a background host process",
	"2nb ai engine status":    "reports on a local llama.cpp install this test does not provision",
	"2nb ai engine pull":      "NETWORK: downloads model weights; it is a JSON event stream and TestContract_JSONEventStreamsRefuseOtherFormats covers its refusals",
	"2nb ai engine rm":        "a JSON event stream; TestContract_JSONEventStreamsRefuseOtherFormats covers its refusals",
	"2nb models bench":        "PAID and a JSON event stream; TestContract_JSONEventStreamsRefuseOtherFormats covers its refusals",
	"2nb config set-key":      "takes a provider API key; a test must not write credentials",
	"2nb config doctor":       "NETWORK: diagnoses AI config by reaching the provider",
	"2nb init":                "a deprecated alias for `vault create` that refuses on an initialized vault, so it has no success path in a fixture that has one",

	// A CONTRACT DECISION, not a gap: cobra generates these scripts and their
	// only consumer is `source <(2nb completion zsh)`. There is no record shape
	// for a shell completion, and `completion install` reads the script
	// internally rather than through a format.
	"2nb completion bash":       "cobra generates a shell script; `source` is its only consumer and there is no record shape for one",
	"2nb completion zsh":        "cobra generates a shell script; `source` is its only consumer and there is no record shape for one",
	"2nb completion fish":       "cobra generates a shell script; `source` is its only consumer and there is no record shape for one",
	"2nb completion powershell": "cobra generates a shell script; `source` is its only consumer and there is no record shape for one",
}

// runnableCommandPaths returns the CommandPath of every runnable command in the
// tree, which is exactly the set that can be handed a --format.
func runnableCommandPaths() []string {
	// Cobra adds its generated `help` command lazily, on the first Execute, so
	// the tree this walks depends on whether another test ran first. Adding it
	// up front makes the enumeration the same in both orders.
	rootCmd.InitDefaultHelpCmd()

	var paths []string
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() {
			paths = append(paths, c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
	return paths
}

// TestContract_EveryCommandIsClassifiedForFormat is the half that cannot be
// satisfied by remembering: a command added tomorrow lands in neither map and
// fails here, and an entry whose command was renamed or removed fails too.
func TestContract_EveryCommandIsClassifiedForFormat(t *testing.T) {
	runnable := map[string]bool{}
	for _, p := range runnableCommandPaths() {
		runnable[p] = true
		_, exercised := formatContractArgv[p]
		_, skipped := formatContractUnexercised[p]
		switch {
		case exercised && skipped:
			t.Errorf("%q is both exercised and unexercised; it must be in exactly one map", p)
		case !exercised && !skipped:
			t.Errorf("%q is a runnable command with no format-contract classification.\n"+
				"Add it to formatContractArgv with an argv that runs it in the fixture vault,\n"+
				"or to formatContractUnexercised with the reason it cannot be run in a test.", p)
		}
	}
	for p := range formatContractArgv {
		if !runnable[p] {
			t.Errorf("formatContractArgv names %q, which is not a runnable command any more", p)
		}
	}
	for p := range formatContractUnexercised {
		if !runnable[p] {
			t.Errorf("formatContractUnexercised names %q, which is not a runnable command any more", p)
		}
	}
}

// newFormatContractVault builds the fixture every exercised command runs
// against: a vault with a linked note carrying a checkbox and a broken link, a
// spare note to delete, a movable note, an index, and (best effort) a git
// repository. It returns the vault root and the HEAD sha, which is empty when
// git is unavailable.
func newFormatContractVault(t *testing.T) (root, headSHA string) {
	t.Helper()
	_, root = newContractVault(t)

	// Every command that MUTATES a note body gets its own target. The subtests
	// run in Go map order, which is randomized per run, so a shared note made
	// the suite depend on which mutation happened to land first: `task doc.md 3`
	// failed whenever `replace` had already shortened the body.
	notes := map[string]string{
		"doc.md":            "---\ntitle: Doc\ntype: note\nstatus: draft\ntags: [x]\n---\nintro text [[nope]]\n\n- [ ] a task\n\n## Setup\n\nsetup body\n",
		"spare.md":          "---\ntitle: Spare\ntype: note\nstatus: draft\n---\nspare body\n",
		"movable.md":        "---\ntitle: Movable\ntype: note\nstatus: draft\n---\nmovable body\n",
		"append-target.md":  "---\ntitle: Append Target\ntype: note\nstatus: draft\n---\nbody\n",
		"prepend-target.md": "---\ntitle: Prepend Target\ntype: note\nstatus: draft\n---\nbody\n",
		"replace-target.md": "---\ntitle: Replace Target\ntype: note\nstatus: draft\n---\nbody\n",
		"tag-target.md":     "---\ntitle: Tag Target\ntype: note\nstatus: draft\ntags: [x]\n---\nbody\n",
		"task-target.md":    "---\ntitle: Task Target\ntype: note\nstatus: draft\n---\n- [ ] a task\n",
	}
	for name, body := range notes {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}

	// Seed the user catalog row the `models remove` case removes. Subtests run
	// in Go map order, which is randomized per run, so `models remove` cannot
	// depend on `models add` having gone first. `models add` MERGES, so the two
	// orders both work with the row seeded here.
	if out, err := runCLIArgs(t, root, "models", "add", "fixture.model",
		"--provider", "bedrock", "--type", "generation"); err != nil {
		t.Fatalf("seed the catalog row: %v\n%s", err, out)
	}

	git := func(args ...string) (string, bool) {
		c := exec.Command("git", args...)
		c.Dir = root
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := c.CombinedOutput()
		return strings.TrimSpace(string(out)), err == nil
	}
	if _, ok := git("init", "-q"); !ok {
		return root, ""
	}
	if _, ok := git("add", "doc.md"); !ok {
		return root, ""
	}
	if _, ok := git("commit", "-q", "-m", "add doc"); !ok {
		return root, ""
	}
	sha, ok := git("rev-parse", "HEAD")
	if !ok {
		return root, ""
	}
	return root, sha
}

// exerciseArgv completes the argv entries the table left nil, which are the
// ones needing a path or a sha only the fixture knows. It also returns the
// vault to run AGAINST, which is the shared fixture for all but one command.
// Returning false skips the case (git absent), which keeps the classification
// test authoritative without making this one depend on the runner's tools.
func exerciseArgv(t *testing.T, path, root, headSHA string) (argv []string, vaultRoot string, ok bool) {
	t.Helper()
	if argv := formatContractArgv[path]; argv != nil {
		return argv, root, true
	}
	switch path {
	case "2nb export-obsidian":
		return []string{"export-obsidian", filepath.Join(t.TempDir(), "exported")}, root, true
	case "2nb import-obsidian":
		// Its OWN vault, not the shared fixture. `import-obsidian` copies the
		// source notes into the TARGET vault and then walks the whole target,
		// stamping id/type/status/created/modified into every note that lacks
		// them. Pointed at the fixture it rewrites the other cases' notes with
		// fresh uuids, which orphans their index rows: the next single-file
		// reindex of one of them mints a row whose path already belongs to
		// another id and fails on the UNIQUE constraint. That side effect is
		// the command's own behavior, not this test's to work around, so the
		// test simply does not aim it at a vault other cases share.
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "plain.md"), []byte("# Plain\n\ntext\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		target := t.TempDir()
		if _, err := runCLIArgs(t, target, "vault", "create", target); err != nil {
			t.Fatalf("create the import target vault: %v", err)
		}
		return []string{"import-obsidian", src}, target, true
	case "2nb vault create":
		return []string{"vault", "create", filepath.Join(t.TempDir(), "fresh")}, root, true
	case "2nb vault set":
		return []string{"vault", "set", root}, root, true
	case "2nb git show":
		if headSHA == "" {
			return nil, "", false
		}
		return []string{"git", "show", headSHA}, root, true
	}
	t.Fatalf("no argv for %q", path)
	return nil, "", false
}

func TestContract_EveryExercisedCommandHonorsAnExplicitFormat(t *testing.T) {
	root, headSHA := newFormatContractVault(t)

	paths := make([]string, 0, len(formatContractArgv))
	for p := range formatContractArgv {
		paths = append(paths, p)
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			argv, vaultRoot, ok := exerciseArgv(t, path, root, headSHA)
			if !ok {
				t.Skipf("%s needs a git repository, which is unavailable here", path)
			}
			if strings.HasPrefix(path, "2nb git ") || path == "2nb git" {
				if headSHA == "" {
					t.Skip("git is unavailable here")
				}
			}

			stdout, stderr, err := runCLISplit(t, vaultRoot, append(append([]string{}, argv...), "--format", "json")...)

			// A refusal is a correct answer, as long as it NAMES the format
			// rather than failing for some unrelated reason.
			if err != nil && len(bytes.TrimSpace(stdout)) == 0 {
				if !strings.Contains(err.Error(), "--format") && !strings.Contains(err.Error(), "format json") {
					t.Fatalf("failed for a reason unrelated to the format (fix the argv in formatContractArgv): %v\nstderr:\n%s", err, stderr)
				}
				return
			}

			// Otherwise stdout must be the structured document, in full. A
			// non-zero exit is fine here: `skills doctor` exits non-zero
			// exactly when it has a verdict worth showing, and still emits it.
			if len(bytes.TrimSpace(stdout)) == 0 {
				t.Fatalf("--format json wrote NOTHING to stdout (the flag was silently ignored)\nstderr:\n%s", stderr)
			}
			var payload any
			if jerr := json.Unmarshal(stdout, &payload); jerr != nil {
				t.Fatalf("--format json did not produce a JSON document on stdout: %v\nstdout:\n%s\nstderr:\n%s",
					jerr, truncateForFailure(stdout), stderr)
			}
		})
	}
}

// truncateForFailure keeps a failure message readable when the offending output
// is a 13KB catalog or a 16KB completion script.
func truncateForFailure(b []byte) string {
	const max = 600
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + fmt.Sprintf("\n... (%d bytes total)", len(b))
}

// runCLISplit invokes rootCmd with argv and returns STDOUT and STDERR
// separately, which runCLIArgs deliberately does not: it merges them, and every
// caller then reaches for jsonPortion() to skip past whatever prose came first.
// That is exactly the defect this contract exists to catch, so the two streams
// are kept apart here and the assertion is made against stdout alone.
//
// Both pipes are drained by their own goroutine. A single reader deadlocks past
// the ~64KB pipe buffer, and `models list` alone is 13KB with `completion bash`
// at 16KB, which is close enough to make that intermittent rather than
// impossible. The restores run through t.Cleanup so a t.Fatalf inside a subtest
// cannot leave the rest of the binary writing into a closed pipe.
func runCLISplit(t *testing.T, vaultRoot string, argv ...string) (stdout, stderr []byte, err error) {
	t.Helper()
	resetCLIFlags(t)

	outR, outW, perr := os.Pipe()
	if perr != nil {
		t.Fatalf("os.Pipe: %v", perr)
	}
	errR, errW, perr := os.Pipe()
	if perr != nil {
		t.Fatalf("os.Pipe: %v", perr)
	}

	origOut, origErr := os.Stdout, os.Stderr
	origCmdOut, origCmdErr := rootCmd.OutOrStdout(), rootCmd.ErrOrStderr()
	restore := func() {
		os.Stdout, os.Stderr = origOut, origErr
		rootCmd.SetOut(origCmdOut)
		rootCmd.SetErr(origCmdErr)
	}
	t.Cleanup(restore)

	os.Stdout, os.Stderr = outW, errW
	rootCmd.SetOut(outW)
	rootCmd.SetErr(errW)
	rootCmd.SetArgs(append([]string{"--vault", vaultRoot}, argv...))

	drain := func(r *os.File) chan []byte {
		ch := make(chan []byte, 1)
		go func() {
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)
			ch <- buf.Bytes()
		}()
		return ch
	}
	outCh, errCh := drain(outR), drain(errR)

	execErr := rootCmd.Execute()

	outW.Close()
	errW.Close()
	restore()
	return <-outCh, <-errCh, execErr
}
