package vault

import (
	"testing"
)

func TestDefaultSchemas_ContainsExpectedTypes(t *testing.T) {
	schemas := DefaultSchemas()
	for _, typ := range []string{"adr", "runbook", "note", "postmortem"} {
		if _, ok := schemas.Types[typ]; !ok {
			t.Errorf("missing schema for type %q", typ)
		}
	}
}

func TestValidateField_EnumPass(t *testing.T) {
	schemas := DefaultSchemas()
	if err := schemas.ValidateField("adr", "status", "proposed"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateField_EnumFail(t *testing.T) {
	schemas := DefaultSchemas()
	err := schemas.ValidateField("adr", "status", "invalid")
	if err == nil {
		t.Error("expected error for invalid enum value")
	}
}

func TestValidateField_UnknownType(t *testing.T) {
	schemas := DefaultSchemas()
	if err := schemas.ValidateField("unknown", "field", "value"); err != nil {
		t.Errorf("unknown type should pass: %v", err)
	}
}

func TestValidateStatusTransition_Valid(t *testing.T) {
	schemas := DefaultSchemas()
	if err := schemas.ValidateStatusTransition("adr", "proposed", "accepted"); err != nil {
		t.Errorf("proposed -> accepted should be valid: %v", err)
	}
}

func TestValidateStatusTransition_Invalid(t *testing.T) {
	schemas := DefaultSchemas()
	err := schemas.ValidateStatusTransition("adr", "proposed", "superseded")
	if err == nil {
		t.Error("proposed -> superseded should be invalid")
	}
}

func TestValidateStatusTransition_TerminalState(t *testing.T) {
	schemas := DefaultSchemas()
	err := schemas.ValidateStatusTransition("adr", "deprecated", "accepted")
	if err == nil {
		t.Error("deprecated is terminal, should not allow transition")
	}
}

// A document whose current status is invalid/corrupt (not a node in the
// machine — the case `2nb lint` flags) can be REPAIRED to any valid enum
// status. Without this, the GUI "Set value…" / `meta --set status=` flow could
// never fix an invalid status on a machine-backed type.
func TestValidateStatusTransition_RepairFromInvalidStatus(t *testing.T) {
	schemas := DefaultSchemas()
	// from "pending" (not a real adr status) -> "accepted" (valid enum): allowed.
	if err := schemas.ValidateStatusTransition("adr", "pending", "accepted"); err != nil {
		t.Errorf("repair from invalid status should be allowed: %v", err)
	}
	// Repairing to ANOTHER invalid value is still rejected (enum still enforced).
	if err := schemas.ValidateStatusTransition("adr", "pending", "banana"); err == nil {
		t.Error("repair to a non-enum status should still be rejected")
	}
}

func TestSaveAndLoadSchemas(t *testing.T) {
	dir := t.TempDir()
	original := DefaultSchemas()
	if err := original.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadSchemas(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(loaded.Types) != len(original.Types) {
		t.Errorf("type count = %d, want %d", len(loaded.Types), len(original.Types))
	}
}
