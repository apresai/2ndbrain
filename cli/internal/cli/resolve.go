package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

// flagResolveMode controls how a positional document target is resolved. It is
// set by the obsidian-syntax shim (preprocessArgs): path= -> "exact" (strict
// vault-relative filesystem path), file= -> "fuzzy" (always run the resolver),
// and a bare positional leaves it at the default "auto" (exact path if it
// exists on disk, else the fuzzy resolver). See docs/obsidian-cli-mapping.md.
var flagResolveMode string

const (
	resolveAuto  = "auto"
	resolveExact = "exact"
	resolveFuzzy = "fuzzy"
)

// resolveTargetArg turns a user-supplied document argument into absolute and
// vault-relative paths, honoring flagResolveMode:
//
//	exact  (path=) : v.AbsPath(expandPath(arg)) only, no fuzzy matching.
//	fuzzy  (file=) : store.ResolveTarget only (exact path -> shortest-unique
//	                 suffix/basename -> title -> alias), failing loudly on ambiguity.
//	auto   (bare)  : the exact path if it exists on disk, otherwise the fuzzy
//	                 resolver (the chosen low-regression fallback).
//
// An *store.AmbiguousTargetError is surfaced as an ExitValidation error listing
// the candidate paths, so a caller never reads or writes a guessed file.
//
// The resolution mode comes from the shim-set flagResolveMode (path= -> exact,
// file= -> fuzzy), defaulting to auto for a bare positional. A destructive
// command can opt out of the auto fallback with resolveTargetArgMode (see
// delete).
func resolveTargetArg(v *vault.Vault, arg string) (absPath, relPath string, err error) {
	mode := flagResolveMode
	if mode == "" {
		mode = resolveAuto
	}
	return resolveTargetArgMode(v, arg, mode)
}

// resolveTargetArgMode is resolveTargetArg with an explicit mode. delete uses it
// to keep a BARE positional strict-exact (its pre-compat behavior): a bare path
// that no longer exists must error, never silently fuzzy-resolve to a different
// note that then gets deleted (especially under --force, which skips the prompt).
// An explicit file=/path= from the shim still overrides, so fuzzy delete-by-title
// stays available opt-in (and still fails loudly on ambiguity + still prompts).
func resolveTargetArgMode(v *vault.Vault, arg, mode string) (absPath, relPath string, err error) {
	if mode == "" {
		mode = resolveAuto
	}

	exact := func() (string, string) {
		abs := v.AbsPath(expandPath(arg))
		return abs, v.RelPath(abs)
	}

	switch mode {
	case resolveExact:
		abs, rel := exact()
		return abs, rel, nil
	case resolveFuzzy:
		abs, rel, fErr := resolveFuzzyTarget(v, arg)
		if errors.Is(fErr, store.ErrTargetNotFound) {
			return "", "", exitWithError(ExitNotFound, fmt.Sprintf(
				"no document matches %q\n\nRun `2nb list` to see available documents", arg))
		}
		return abs, rel, fErr
	default: // auto
		abs, rel := exact()
		if _, statErr := os.Stat(abs); statErr == nil {
			return abs, rel, nil
		}
		// Exact path missed, so fall back to the fuzzy resolver.
		fAbs, fRel, fErr := resolveFuzzyTarget(v, arg)
		if fErr != nil {
			// On a plain not-found, return the exact path so the caller's own
			// open/parse produces its established "file not found" message; on
			// ambiguity, surface the candidates instead.
			if errors.Is(fErr, store.ErrTargetNotFound) {
				return abs, rel, nil
			}
			return "", "", fErr
		}
		return fAbs, fRel, nil
	}
}

// resolveFuzzyTarget runs the store resolver and maps its errors to CLI-friendly
// exit errors. It returns store.ErrTargetNotFound unwrapped (via errors.Is) so
// the auto-mode caller can distinguish "nothing matched" from ambiguity.
func resolveFuzzyTarget(v *vault.Vault, arg string) (string, string, error) {
	rel, err := v.DB.ResolveTarget(arg)
	if err != nil {
		var amb *store.AmbiguousTargetError
		if errors.As(err, &amb) {
			return "", "", exitWithError(ExitValidation, fmt.Sprintf(
				"ambiguous target %q matches %d documents:\n  %s\n\nUse a path-qualified target (path=…) to disambiguate.",
				amb.Name, len(amb.Candidates), strings.Join(amb.Candidates, "\n  ")))
		}
		// ErrTargetNotFound (and any other error) is returned unwrapped so the
		// caller can decide: auto mode falls back to the exact path, fuzzy mode
		// converts it to a friendly ExitNotFound message.
		return "", "", err
	}
	abs := v.AbsPath(rel)
	return abs, rel, nil
}
