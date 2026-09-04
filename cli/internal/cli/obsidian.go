package cli

import (
	"github.com/spf13/cobra"
)

// obsidianCmd groups the commands that exist purely to make a vault read
// correctly IN OBSIDIAN. They are opt-in, one-shot, and they touch surfaces the
// rest of 2nb deliberately leaves alone: note frontmatter across the whole
// vault, and (for register-types) one file inside .obsidian/.
//
// Deliberately NOT runnable on its own. A bare `2nb obsidian` prints its help
// rather than defaulting into a subcommand, because both subcommands here can
// write, and a parent default that writes is a footgun the other parent
// defaults (`2nb ai`, `2nb mcp`, all read-only status views) do not have.
var obsidianCmd = &cobra.Command{
	Use:   "obsidian",
	Short: "One-shot commands that make a vault read correctly in Obsidian",
	Long: `Obsidian-compatibility commands. Each previews by default and applies only
with --write.

  migrate-properties   Rewrite quoted date values into the plain form Obsidian
                       types as Date and time
  register-types       Declare 2nb's property types in .obsidian/types.json`,
}

func init() {
	obsidianCmd.GroupID = "quality"
	rootCmd.AddCommand(obsidianCmd)
}
