package ai

import (
	"strings"
	"testing"
)

// ReadinessRemediation deliberately diverges from RemediationFor on
// access_denied, and now that the two are neighbours in one package this
// assertion is the only thing stopping a future "dedupe". A comment would
// drift; this will not.
//
// Why they must differ: the readiness probe is a control-plane
// ListFoundationModels call, so a denial means the IAM principal cannot call
// that API. RemediationFor answers for `models test`, which invokes a model,
// and so reads access_denied as model entitlement and points at the Bedrock
// console's "Model access" page. That page cannot grant an API permission, so
// following it on the readiness path is a dead end.
func TestReadinessRemediation_DivergesOnAccessDenied(t *testing.T) {
	readiness := ReadinessRemediation(TestErrAccessDenied, "bedrock")
	invocation := RemediationFor(TestErrAccessDenied, "bedrock", "")

	if readiness == invocation {
		t.Fatal("ReadinessRemediation must not reuse the model-entitlement text for access_denied")
	}
	if !strings.Contains(readiness, "bedrock:ListFoundationModels") {
		t.Errorf("readiness remediation should name the API permission: %q", readiness)
	}

	// Every other code means the same thing on both paths and passes through.
	for _, code := range []TestErrorCode{
		TestErrBadCredentials, TestErrTimeout, TestErrProviderUnreachable,
		TestErrThrottled, TestErrNotFound, TestErrIncompatible, TestErrInvalidRequest,
	} {
		if got, want := ReadinessRemediation(code, "bedrock"), RemediationFor(code, "bedrock", ""); got != want {
			t.Errorf("%s should pass through unchanged:\n got  %q\n want %q", code, got, want)
		}
	}

	// RemediationFor has no text for an unclassifiable failure, and a message
	// ending at a bare code helps nobody.
	if got := ReadinessRemediation(TestErrUnknown, "bedrock"); got == "" || !strings.Contains(got, "2nb doctor") {
		t.Errorf("an unknown cause still needs a next step: %q", got)
	}
}

// The empty-code path is what ollama, openrouter, and llama-local hit today,
// none of which have credentials to check. Blaming credentials there would be a
// new false claim on surfaces that never made one.
func TestNotReadyMessage_UnclassifiedNeverBlamesCredentials(t *testing.T) {
	msg := NotReadyMessage("generation", "ollama", "")
	if strings.Contains(msg, "credentials") {
		t.Errorf("an unclassified failure must not blame credentials: %q", msg)
	}
	if !strings.Contains(msg, "did not report a cause") {
		t.Errorf("expected the honest no-cause wording: %q", msg)
	}

	// A classified failure still names its cause and carries its code.
	classified := NotReadyMessage("embedding", "bedrock", TestErrTimeout)
	if !strings.Contains(classified, string(TestErrTimeout)) {
		t.Errorf("expected the code in the message: %q", classified)
	}
	if !strings.HasPrefix(classified, "embedding provider") {
		t.Errorf("expected the role named first: %q", classified)
	}
}
