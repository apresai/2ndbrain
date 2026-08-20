package ai

import "testing"

// TestResolveReasoningEffort covers the config -> GenOpts contract: a valid
// depth resolves (normalized), and anything else resolves to "" so the request
// omits the reasoning field and the model's own default applies. Invalid input
// must degrade rather than error: config.yaml is hand-editable and a typo there
// cannot be allowed to break every `ask`.
func TestResolveReasoningEffort(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unset", "", ""},
		{"none", "none", "none"},
		{"low", "low", "low"},
		{"medium", "medium", "medium"},
		{"high", "high", "high"},
		{"uppercase", "HIGH", "high"},
		{"mixed case", "Medium", "medium"},
		{"padded", "  low  ", "low"},
		{"whitespace only", "   ", ""},
		{"unknown depth degrades to default", "maximum", ""},
		{"typo degrades to default", "hgih", ""},
		{"numeric degrades to default", "2", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := AIConfig{ReasoningEffort: tt.in}
			if got := cfg.ResolveReasoningEffort(); got != tt.want {
				t.Errorf("AIConfig{ReasoningEffort: %q}.ResolveReasoningEffort() = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsValidReasoningEffort(t *testing.T) {
	for _, e := range ReasoningEfforts {
		if !IsValidReasoningEffort(e) {
			t.Errorf("IsValidReasoningEffort(%q) = false, want true for a member of ReasoningEfforts", e)
		}
	}
	// Empty is not a member: callers treat it separately as "unset".
	for _, bad := range []string{"", "  ", "maximum", "off", "true", "0"} {
		if IsValidReasoningEffort(bad) {
			t.Errorf("IsValidReasoningEffort(%q) = true, want false", bad)
		}
	}
}

// TestRAGGenOpts_ReasoningEffort asserts the RAG answer pipeline picks up the
// configured reasoning effort while leaving the measured token/temperature
// settings alone. Pure options assembly, no provider call.
func TestRAGGenOpts_ReasoningEffort(t *testing.T) {
	const system = "sys"

	base := ragGenOpts(system, nil)
	if base.ReasoningEffort != "" {
		t.Errorf("no options: ReasoningEffort = %q, want empty (model default)", base.ReasoningEffort)
	}
	if base.MaxTokens != 1024 || base.Temperature == nil || *base.Temperature != 0.1 || base.SystemPrompt != system {
		t.Fatalf("unexpected base opts: %+v", base)
	}

	// An unset config resolves to "", which must be a no-op rather than
	// clobbering the field with an empty string the provider would reject.
	unset := ragGenOpts(system, []GenOption{WithReasoningEffort(AIConfig{}.ResolveReasoningEffort())})
	if unset.ReasoningEffort != "" {
		t.Errorf("unset config: ReasoningEffort = %q, want empty", unset.ReasoningEffort)
	}

	for _, effort := range ReasoningEfforts {
		t.Run(effort, func(t *testing.T) {
			cfg := AIConfig{ReasoningEffort: effort}
			got := ragGenOpts(system, []GenOption{WithReasoningEffort(cfg.ResolveReasoningEffort())})
			if got.ReasoningEffort != effort {
				t.Errorf("ReasoningEffort = %q, want %q", got.ReasoningEffort, effort)
			}
			if got.MaxTokens != 1024 || got.Temperature == nil || *got.Temperature != 0.1 {
				t.Errorf("reasoning effort must not disturb the tuned opts: %+v", got)
			}
			if got.SystemPrompt != system {
				t.Errorf("SystemPrompt = %q, want %q", got.SystemPrompt, system)
			}
		})
	}
}

func TestApplyGenOptions_NilSafe(t *testing.T) {
	got := applyGenOptions(GenOpts{MaxTokens: 7}, []GenOption{nil, WithReasoningEffort("high"), nil})
	if got.MaxTokens != 7 {
		t.Errorf("MaxTokens = %d, want 7", got.MaxTokens)
	}
	if got.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want %q", got.ReasoningEffort, "high")
	}
}
