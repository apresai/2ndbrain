package cli

import "testing"

// TestRootCmdSilencesUsageOnError guards the SilenceUsage/SilenceErrors contract
// the macOS app depends on. When a command fails at runtime (a RunE error),
// cobra would otherwise print the error followed by the full usage/flag dump;
// the app's index sheet scrapes the last stderr line, so that dump made it show
// a stray flag description (e.g. "--yaml … Output as YAML") instead of the real
// error. A future refactor that drops SilenceUsage — or sets SilenceErrors and
// hides the message entirely — would silently reintroduce that bug, so assert
// both here.
func TestRootCmdSilencesUsageOnError(t *testing.T) {
	if !rootCmd.SilenceUsage {
		t.Error("rootCmd.SilenceUsage must be true so a runtime error doesn't print the usage dump that masks the real error message")
	}
	if rootCmd.SilenceErrors {
		t.Error("rootCmd.SilenceErrors must be false so the actual error message still reaches stderr (the GUI surfaces it)")
	}
}
