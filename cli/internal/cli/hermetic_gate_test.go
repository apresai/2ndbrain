package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Makefile's hermetic `test` target and this package's
// neutralizeAWSCredentials both claim to mean "no AWS credentials reachable".
// Two lists that must agree, with nothing checking that they do, is how they
// drifted: the Makefile covered five names while the helper already covered
// nine, and it redirected NEITHER of the two variables that name a FILE. Those
// two are the ones a redirected HOME cannot touch, so on any machine exporting
// AWS_SHARED_CREDENTIALS_FILE (an SSO wrapper, direnv) the "credential-free"
// suite reached live AWS, ran a strictly larger set of tests than CI, and the
// release gate then passed or failed on what happened to be in the environment.
//
// Enumerating beats inspecting: the Makefile is read here, so adding a source to
// the helper without adding it to the gate fails this test rather than waiting
// for someone to notice.
func TestHermeticGateCoversEveryCredentialSourceTheHelperDoes(t *testing.T) {
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("reading cli/Makefile: %v", err)
	}
	hermetic := hermeticEnvBlock(t, string(makefile))

	helper, err := os.ReadFile("models_verify_test.go")
	if err != nil {
		t.Fatalf("reading models_verify_test.go: %v", err)
	}
	names := credentialNamesIn(t, string(helper))
	if len(names) < 9 {
		t.Fatalf("found only %d credential names in neutralizeAWSCredentials (%v); the parser has drifted from the helper, so this test would pass vacuously", len(names), names)
	}

	for _, name := range names {
		// The gate may either unset it or point it somewhere harmless. Both are
		// "the SDK cannot read a real credential from here"; which one is right
		// depends on whether the variable holds a value or a path.
		if !strings.Contains(hermetic, "-u "+name) && !strings.Contains(hermetic, name+"=") {
			t.Errorf("cli/Makefile HERMETIC_ENV neither unsets nor overrides %s, but neutralizeAWSCredentials scrubs it: the gate is not as sealed as the helper", name)
		}
	}

	// The two that a redirected HOME provably cannot cover, called out by name
	// because they are the ones that were actually missing and the failure was
	// silent.
	for _, mustOverride := range []string{"AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE"} {
		if !strings.Contains(hermetic, mustOverride+"=") {
			t.Errorf("HERMETIC_ENV must POINT %s at a path that does not exist, not merely unset it: it names a file, so an ambient value survives a redirected HOME", mustOverride)
		}
	}
	// Without this the SDK falls through to the EC2 metadata service, which is
	// unreachable off EC2 and burns its full timeout on EVERY probe.
	if !strings.Contains(hermetic, "AWS_EC2_METADATA_DISABLED=true") {
		t.Error("HERMETIC_ENV must set AWS_EC2_METADATA_DISABLED=true, or a credential-free run stalls on IMDS instead of failing fast")
	}
}

// hermeticEnvBlock returns the HERMETIC_ENV assignment, continuation lines
// included. It fails rather than returning empty, so a renamed variable is a
// visible failure instead of a test that silently checks nothing.
func hermeticEnvBlock(t *testing.T, makefile string) string {
	t.Helper()
	lines := strings.Split(makefile, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "HERMETIC_ENV") {
			continue
		}
		var b strings.Builder
		for ; i < len(lines); i++ {
			b.WriteString(lines[i])
			b.WriteString(" ")
			if !strings.HasSuffix(strings.TrimSpace(lines[i]), "\\") {
				break
			}
		}
		return b.String()
	}
	t.Fatal("no HERMETIC_ENV assignment in cli/Makefile; if the hermetic gate was renamed, update this test rather than deleting it")
	return ""
}

// credentialNamesIn pulls the AWS_* / 2NB_* names neutralizeAWSCredentials
// touches, from its string list and its individual t.Setenv calls alike.
func credentialNamesIn(t *testing.T, src string) []string {
	t.Helper()
	start := strings.Index(src, "func neutralizeAWSCredentials(")
	if start < 0 {
		t.Fatal("neutralizeAWSCredentials not found; update this test rather than deleting it")
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatal("could not find the end of neutralizeAWSCredentials")
	}
	body := src[start : start+end]

	seen := map[string]bool{}
	var out []string
	for _, m := range regexp.MustCompile(`"(AWS_[A-Z0-9_]+)"`).FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
