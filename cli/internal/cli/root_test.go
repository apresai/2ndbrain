package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestExitCode guards the main() exit-code contract: an *ExitError carries its
// own code (so scripts can tell ExitValidation=2 from ExitNotFound=1), a plain
// error is a generic failure (1), a wrapped *ExitError is still unwrapped, and
// nil is success (0). Before this, main() flattened everything to 1.
func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, ExitOK},
		{"validation error", exitWithError(ExitValidation, "bad input"), ExitValidation},
		{"not-found error", exitWithError(ExitNotFound, "missing"), ExitNotFound},
		{"stale-ref error", exitWithError(ExitStaleRef, "stale"), ExitStaleRef},
		{"wrapped exit error is unwrapped", fmt.Errorf("context: %w", exitWithError(ExitValidation, "x")), ExitValidation},
		{"plain error is generic failure", errors.New("boom"), ExitNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

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

// TestNextStepHint locks the four-way next-action switch, in particular the
// embeddableCount branch: a vault that is fully embedded except for empty notes
// (embedded == embeddable < docCount) must NOT keep nudging "2nb index", since
// those notes can never be embedded.
func TestNextStepHint(t *testing.T) {
	tests := []struct {
		name                                   string
		docCount, embeddedCount, embeddableDoc int
		provider                               string
		wantLabel, wantHintContains            string
	}{
		{"empty_vault", 0, 0, 0, "bedrock", "Next", "create"},
		{"no_provider", 3, 0, 3, "", "Next", "ai setup"},
		{"needs_index", 5, 3, 5, "bedrock", "Next", "2nb index"},
		// 3 content docs all embedded + 2 empty notes: docCount 5 > embedded 3,
		// but embedded == embeddable, so no index nudge — this is the fix.
		{"fully_embedded_with_empties", 5, 3, 3, "bedrock", "Try", "search"},
		{"ready", 5, 5, 5, "bedrock", "Try", "search"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, hint := nextStepHint(tt.docCount, tt.embeddedCount, tt.embeddableDoc, tt.provider)
			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
			if !strings.Contains(hint, tt.wantHintContains) {
				t.Errorf("hint = %q, want it to contain %q", hint, tt.wantHintContains)
			}
		})
	}
}
