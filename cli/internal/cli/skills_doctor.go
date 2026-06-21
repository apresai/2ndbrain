package cli

import (
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/skills"
	"github.com/spf13/cobra"
)

var skillsDoctorCmd = &cobra.Command{
	Use:   "doctor [slug]",
	Short: "Verify a Claude Code (or other agent) skill is installed and its 2nb resolves",
	Long: `Checks that an agent's skill is genuinely set up: the SKILL.md is installed
and non-empty with frontmatter, and the 2nb binary it shells to resolves on PATH
(the way the agent's shell finds it) and runs. slug defaults to "claude-code".

This verifies "installed + dependencies resolve", not "the agent invoked it" —
final proof requires the agent loading the file in a session. The 2nb-on-PATH
check is the common real failure (a cask upgrade bumps the app but the terminal
2nb is stale or missing).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSkillsDoctor,
}

func init() {
	skillsCmd.AddCommand(skillsDoctorCmd)
}

// SkillDoctorReport flattens skills.Verification (slug/name/paths/installed flags
// + verification fields) and adds the shared doctor check list + roll-up.
type SkillDoctorReport struct {
	skills.Verification
	OK     bool          `json:"ok"`
	Checks []DoctorCheck `json:"checks"`
}

func runSkillsDoctor(cmd *cobra.Command, args []string) error {
	slug := "claude-code"
	if len(args) == 1 {
		slug = args[0]
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home directory: %w", err)
	}
	var projectDir string
	if v, err := openVault(); err == nil {
		projectDir = v.Root
		v.Close()
	}

	ver := skills.Doctor(slug, projectDir, homeDir)
	report := SkillDoctorReport{Verification: ver}

	// HARD: the skill must be installed.
	report.Checks = append(report.Checks, DoctorCheck{
		Name:   "skill installed",
		OK:     ver.Installed,
		Detail: skillInstalledDetail(ver),
		Fix:    fmt.Sprintf("run `2nb skills install %s --user`", slug),
	})
	// HARD (only meaningful when installed): the file must be valid.
	if ver.Installed {
		report.Checks = append(report.Checks, DoctorCheck{
			Name:   "skill file valid",
			OK:     ver.FileNonEmpty && ver.Parses,
			Detail: skillFileDetail(ver),
			Fix:    fmt.Sprintf("re-run `2nb skills install %s --user --force` (the SKILL.md is empty or malformed)", slug),
		})
	}
	// WARN: 2nb resolves on PATH and runs.
	report.Checks = append(report.Checks, DoctorCheck{
		Name:   "2nb resolves on PATH",
		OK:     true,
		Warn:   !(ver.BinaryOnPath && ver.BinaryOK),
		Detail: skillBinaryDetail(ver),
		Fix:    "install/upgrade the CLI so `2nb` is on your shell PATH (brew install apresai/tap/twonb)",
	})

	report.OK = true
	for _, c := range report.Checks {
		if !c.OK {
			report.OK = false
		}
	}

	if format := getFormat(cmd); format != "" {
		if err := output.Write(os.Stdout, format, report); err != nil {
			return err
		}
		if !report.OK {
			return exitWithError(ExitValidation, "skills doctor found problems")
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
		fmt.Println("\nSkill installed and its 2nb resolves.")
		return nil
	}
	return exitWithError(ExitValidation, "skills doctor found problems")
}

func skillInstalledDetail(v skills.Verification) string {
	switch {
	case v.UserInstalled:
		return "installed (user scope)"
	case v.ProjectInstalled:
		return "installed (project scope)"
	default:
		return "not installed"
	}
}

func skillFileDetail(v skills.Verification) string {
	if v.FileNonEmpty && v.Parses {
		return "SKILL.md present, non-empty, has frontmatter"
	}
	if !v.FileNonEmpty {
		return "SKILL.md is empty"
	}
	return "SKILL.md has no frontmatter block"
}

func skillBinaryDetail(v skills.Verification) string {
	if v.BinaryOnPath && v.BinaryOK {
		return fmt.Sprintf("2nb on PATH: %s", v.BinaryVersion)
	}
	if !v.BinaryOnPath {
		detail := "2nb is NOT on your shell PATH"
		if v.SelfPath != "" {
			detail += fmt.Sprintf(" (this binary: %s)", v.SelfPath)
		}
		return detail
	}
	return "2nb is on PATH but `2nb --version` did not run"
}
