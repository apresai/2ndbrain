package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	gitpkg "github.com/apresai/2ndbrain/internal/git"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/spf13/cobra"
)

var gitCmd = &cobra.Command{
	Use:   "git",
	Short: "Read-only git integration for vaults that are git repositories",
	Long: `When a vault is kept in a git repo these commands surface recent activity,
uncommitted changes, and file diffs without the user having to leave 2ndbrain.

All git commands are READ-ONLY. To commit, push, or pull, use git directly.`,
}

var gitActivityCmd = &cobra.Command{
	Use:   "activity",
	Short: "Show recent commits that touched files in the vault",
	RunE:  runGitActivity,
}

var gitDiffCmd = &cobra.Command{
	Use:   "diff <path>",
	Short: "Show the unified diff of a file against HEAD",
	Args:  cobra.ExactArgs(1),
	RunE:  runGitDiff,
}

var gitStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show uncommitted and untracked files in the vault",
	RunE:  runGitStatus,
}

var gitActivitySince string

func init() {
	gitCmd.GroupID = "integr"
	gitActivityCmd.Flags().StringVar(&gitActivitySince, "since", "7d", "Duration to look back (e.g. 1d, 7d, 24h)")
	gitCmd.AddCommand(gitActivityCmd)
	gitCmd.AddCommand(gitDiffCmd)
	gitCmd.AddCommand(gitStatusCmd)
	rootCmd.AddCommand(gitCmd)
}

func runGitActivity(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	if !gitpkg.IsRepo(v.Root) {
		return printNotAGitRepo(cmd)
	}

	since, err := parseDuration(gitActivitySince)
	if err != nil {
		return fmt.Errorf("parse --since: %w", err)
	}

	changes, err := gitpkg.Activity(v.Root, since)
	if err != nil {
		return fmt.Errorf("git log: %w", err)
	}

	if getFormat(cmd) == output.FormatJSON {
		data, err := json.Marshal(changes)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if len(changes) == 0 {
		fmt.Printf("No commits in the last %s.\n", gitActivitySince)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HASH\tDATE\tAUTHOR\tFILES\tSUBJECT")
	for _, c := range changes {
		hash := c.Hash
		if len(hash) > 7 {
			hash = hash[:7]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			hash,
			c.Date.Local().Format("2006-01-02 15:04"),
			c.Author,
			len(c.Files),
			c.Subject,
		)
	}
	return w.Flush()
}

func runGitDiff(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	if !gitpkg.IsRepo(v.Root) {
		return printNotAGitRepo(cmd)
	}

	diff, err := gitpkg.DiffFile(v.Root, args[0])
	if err != nil {
		return fmt.Errorf("git diff: %w", err)
	}

	if getFormat(cmd) == output.FormatJSON {
		result := map[string]string{"path": args[0], "diff": diff}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if diff == "" {
		fmt.Printf("No changes to %s.\n", args[0])
		return nil
	}
	fmt.Print(diff)
	return nil
}

func runGitStatus(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	if !gitpkg.IsRepo(v.Root) {
		return printNotAGitRepo(cmd)
	}

	statuses, err := gitpkg.StatusFiles(v.Root)
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}

	if getFormat(cmd) == output.FormatJSON {
		data, err := json.Marshal(statuses)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if len(statuses) == 0 {
		fmt.Println("Working tree clean.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tPATH")
	for path, code := range statuses {
		fmt.Fprintf(w, "%s\t%s\n", code, path)
	}
	return w.Flush()
}

func printNotAGitRepo(cmd *cobra.Command) error {
	if getFormat(cmd) == output.FormatJSON {
		fmt.Println(`{"git_repo": false}`)
		return nil
	}
	fmt.Println("Vault is not a git repository. Run `git init` to enable git integration.")
	return nil
}

// parseDuration extends Go's time.ParseDuration with a "d" (days) unit since
// that's what users expect from --since flags.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
