package vault

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type SchemaSet struct {
	Types map[string]DocTypeSchema `yaml:"types" json:"types"`
}

type DocTypeSchema struct {
	Name        string                   `yaml:"name" json:"name"`
	Description string                   `yaml:"description" json:"description"`
	Fields      map[string]FieldDef      `yaml:"fields" json:"fields"`
	Required    []string                 `yaml:"required" json:"required"`
	Status      *StatusMachine           `yaml:"status,omitempty" json:"status,omitempty"`
}

type FieldDef struct {
	Type    string   `yaml:"type" json:"type"`       // text, number, date, datetime, boolean, list, tags
	Enum    []string `yaml:"enum,omitempty" json:"enum,omitempty"`
	Default any      `yaml:"default,omitempty" json:"default,omitempty"`
}

type StatusMachine struct {
	Initial     string              `yaml:"initial" json:"initial"`
	Transitions map[string][]string `yaml:"transitions" json:"transitions"` // state -> allowed next states
}

func DefaultSchemas() *SchemaSet {
	return &SchemaSet{
		Types: map[string]DocTypeSchema{
			"adr": {
				Name:        "Architecture Decision Record",
				Description: "Records an architecture decision with context and consequences",
				Fields: map[string]FieldDef{
					"status":        {Type: "text", Enum: []string{"proposed", "accepted", "deprecated", "superseded"}},
					"deciders":      {Type: "list"},
					"superseded-by": {Type: "text"},
				},
				Required: []string{"title", "status"},
				Status: &StatusMachine{
					Initial: "proposed",
					Transitions: map[string][]string{
						"proposed":   {"accepted", "deprecated"},
						"accepted":   {"deprecated", "superseded"},
						"deprecated": {},
						"superseded": {},
					},
				},
			},
			"runbook": {
				Name:        "Runbook",
				Description: "Step-by-step operational procedure",
				Fields: map[string]FieldDef{
					"status":   {Type: "text", Enum: []string{"draft", "active", "archived"}},
					"service":  {Type: "text"},
					"severity": {Type: "text", Enum: []string{"low", "medium", "high", "critical"}},
				},
				Required: []string{"title", "status"},
			},
			"note": {
				Name:        "Note",
				Description: "General knowledge note",
				Fields: map[string]FieldDef{
					"status": {Type: "text", Enum: []string{"draft", "complete"}},
				},
				Required: []string{"title"},
			},
			"postmortem": {
				Name:        "Postmortem",
				Description: "Incident postmortem analysis",
				Fields: map[string]FieldDef{
					"status":       {Type: "text", Enum: []string{"draft", "reviewed", "published"}},
					"incident-date": {Type: "date"},
					"severity":     {Type: "text", Enum: []string{"low", "medium", "high", "critical"}},
					"services":     {Type: "list"},
				},
				Required: []string{"title", "status", "incident-date"},
			},
		},
	}
}

func LoadSchemas(dotDir string) (*SchemaSet, error) {
	path := filepath.Join(dotDir, "schemas.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultSchemas(), nil
		}
		return nil, fmt.Errorf("read schemas: %w", err)
	}

	var schemas SchemaSet
	if err := yaml.Unmarshal(data, &schemas); err != nil {
		return nil, fmt.Errorf("parse schemas: %w", err)
	}
	return &schemas, nil
}

func (s *SchemaSet) Save(dotDir string) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal schemas: %w", err)
	}
	return os.WriteFile(filepath.Join(dotDir, "schemas.yaml"), data, 0o644)
}

func (s *SchemaSet) ValidateField(docType, field string, value any) error {
	schema, ok := s.Types[docType]
	if !ok {
		return nil // no schema for this type, allow anything
	}

	fieldDef, ok := schema.Fields[field]
	if !ok {
		return nil // field not in schema, allow it
	}

	if len(fieldDef.Enum) > 0 {
		strVal, ok := value.(string)
		if !ok {
			return fmt.Errorf("field %q expects a string value", field)
		}
		for _, allowed := range fieldDef.Enum {
			if strVal == allowed {
				return nil
			}
		}
		return fmt.Errorf("field %q value %q not in allowed values: %v", field, strVal, fieldDef.Enum)
	}

	return nil
}

func (s *SchemaSet) ValidateStatusTransition(docType, from, to string) error {
	schema, ok := s.Types[docType]
	if !ok || schema.Status == nil {
		return nil
	}

	allowed, ok := schema.Status.Transitions[from]
	if !ok {
		return fmt.Errorf("unknown status %q for type %q", from, docType)
	}

	for _, a := range allowed {
		if a == to {
			return nil
		}
	}

	return fmt.Errorf("invalid status transition %q -> %q for type %q (allowed: %v)", from, to, docType, allowed)
}
