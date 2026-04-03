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
