package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/apresai/2ndbrain/internal/document"
)

type SchemaSet struct {
	Types map[string]DocTypeSchema `yaml:"types" json:"types"`
}

type DocTypeSchema struct {
	Name        string              `yaml:"name" json:"name"`
	Description string              `yaml:"description" json:"description"`
	Fields      map[string]FieldDef `yaml:"fields" json:"fields"`
	Required    []string            `yaml:"required" json:"required"`
	Status      *StatusMachine      `yaml:"status,omitempty" json:"status,omitempty"`
}

type FieldDef struct {
	Type    string   `yaml:"type" json:"type"` // text, number, date, datetime, boolean, list, tags
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
			"prd": {
				Name:        "Product Requirements Document",
				Description: "Product requirements with problem statement, user stories, and functional specs",
				Fields: map[string]FieldDef{
					"status":   {Type: "text", Enum: []string{"draft", "review", "approved", "shipped", "archived"}},
					"owner":    {Type: "text"},
					"priority": {Type: "text", Enum: []string{"p0", "p1", "p2", "p3"}},
				},
				Required: []string{"title", "status"},
				Status: &StatusMachine{
					Initial: "draft",
					Transitions: map[string][]string{
						"draft":    {"review"},
						"review":   {"draft", "approved"},
						"approved": {"shipped", "draft"},
						"shipped":  {"archived"},
						"archived": {},
					},
				},
			},
			"prfaq": {
				Name:        "Press Release / FAQ",
				Description: "Amazon-style PR/FAQ for working backwards from the customer experience",
				Fields: map[string]FieldDef{
					"status": {Type: "text", Enum: []string{"draft", "review", "final"}},
					"owner":  {Type: "text"},
				},
				Required: []string{"title", "status"},
				Status: &StatusMachine{
					Initial: "draft",
					Transitions: map[string][]string{
						"draft":  {"review"},
						"review": {"draft", "final"},
						"final":  {},
					},
				},
			},
			"postmortem": {
				Name:        "Postmortem",
				Description: "Incident postmortem analysis",
				Fields: map[string]FieldDef{
					"status":        {Type: "text", Enum: []string{"draft", "reviewed", "published"}},
					"incident-date": {Type: "date"},
					"severity":      {Type: "text", Enum: []string{"low", "medium", "high", "critical"}},
					"services":      {Type: "list"},
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

// IsListField reports whether a frontmatter field holds a YAML list rather than
// a scalar. True for the universal array fields "tags" and "aliases" (every doc
// type treats these as lists, even when not declared in its schema), and for any
// field the type schema marks as "list" or "tags". Callers use this to coerce a
// CLI "key=value" set into an array instead of writing a stray scalar.
func (s *SchemaSet) IsListField(docType, field string) bool {
	if field == "tags" || field == "aliases" {
		return true
	}
	if schema, ok := s.Types[docType]; ok {
		if def, ok := schema.Fields[field]; ok {
			return def.Type == "list" || def.Type == "tags"
		}
	}
	return false
}

// IsDateField reports whether a frontmatter field holds a DATE rather than a
// scalar of some other kind. True for the universal "created" and "modified"
// (every doc type carries those, declared or not, and NewDocument writes them),
// and for any field the type schema marks "date" or "datetime".
//
// This is the first consumer of FieldDef.Type beyond "list" and "tags", which
// IsListField reads. A schema that declares a date field has, until now, had
// that declaration ignored entirely.
func (s *SchemaSet) IsDateField(docType, field string) bool {
	return s.dateFieldKind(docType, field) != notADateField
}

// dateFieldKind classifies a field ONCE, so the "which type strings are dates"
// rule and the `created`/`modified` special case each exist in one place.
// IsDateField and IsCalendarDateField are both views of it: written as separate
// walks they were two copies that had to agree, and a third universal date
// field would have had to be added to both.
type dateFieldKind int

const (
	notADateField dateFieldKind = iota
	// calendarDateField is a day on a calendar. Obsidian types it Date, and its
	// written precision is preserved.
	calendarDateField
	// instantDateField is a moment in time. Obsidian types it Date and time,
	// and it is normalized to one RFC3339 spelling.
	instantDateField
)

func (s *SchemaSet) dateFieldKind(docType, field string) dateFieldKind {
	// created and modified are universal and are always instants: 2nb writes
	// them itself as a full timestamp, and `stale` and `list --sort modified`
	// compare them as moments. A schema cannot redeclare them a calendar date.
	if field == "created" || field == "modified" {
		return instantDateField
	}
	if schema, ok := s.Types[docType]; ok {
		if def, ok := schema.Fields[field]; ok {
			switch def.Type {
			case "date":
				return calendarDateField
			case "datetime":
				return instantDateField
			}
		}
	}
	return notADateField
}

// CoerceDate reports the value a DATE field should be STORED as,
// and false when the field is not a date or the value is not date-shaped text.
//
// It exists because the two write surfaces would otherwise diverge. `meta --set`
// passes the raw CLI string and `kb_update_meta` passes the raw JSON value, so
// once a note's `created` node holds an unquoted date, writing a string over it
// makes the surgical writer requote it and the note is back to Obsidian's Text
// type. Both callers route through here so the CLI and the MCP server cannot
// disagree about what a date is.
//
// A value it refuses is stored exactly as the caller supplied it, which is what
// every value did before: coercion is additive, and `--set created=tomorrow`
// still writes that text rather than failing.
//
// It returns a document.PlainDate rather than a time.Time so a CALENDAR DATE
// can keep the precision the caller typed. `--set incident-date=2026-07-14` on
// a field the schema declares `date` used to write `2026-07-14T00:00:00Z`,
// inventing a time of day nobody supplied and flipping Obsidian's type for that
// property from Date to Date and time.
//
// A calendar date is the only field that keeps its spelling. `created` and
// `modified` are INSTANTS 2nb owns and writes itself, and a schema `datetime`
// says the same, so those still normalize to one second-precision RFC3339 form
// however the caller spelled them: `TestContract_MetaSetKeepsADateUnquoted`
// pins that three spellings of one moment come back as one instant, which is
// what makes the file and the index column agree.
func (s *SchemaSet) CoerceDate(docType, field string, value any) (document.PlainDate, bool) {
	if !s.IsDateField(docType, field) {
		return "", false
	}
	str, ok := value.(string)
	if !ok {
		return "", false
	}
	if s.dateFieldKind(docType, field) == calendarDateField {
		return document.ParseFrontmatterDateText(str)
	}
	t, ok := document.ParseFrontmatterDate(str)
	if !ok {
		return "", false
	}
	return document.PlainDate(t.Format(time.RFC3339)), true
}

// IsCalendarDateField reports whether a field means a DATE ON A CALENDAR rather
// than an instant in time. Obsidian types the two differently (Date versus Date
// and time), so a value written `2026-07-14` must not come back carrying a time
// of day it never had.
//
// `created` and `modified` are never calendar dates however a schema declares
// them: 2nb writes them itself, always as a full instant, and `stale` and
// `list --sort modified` compare them as instants.
func (s *SchemaSet) IsCalendarDateField(docType, field string) bool {
	return s.dateFieldKind(docType, field) == calendarDateField
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
		// `from` is not a node in the machine — the document's current status is
		// invalid/corrupt (e.g. a hand-edited value the schema never allowed, the
		// exact case `2nb lint` flags). A transition graph can't bind a document
		// that isn't on the graph, so permit moving to any VALID status as a
		// repair rather than trapping the doc in its broken state forever. `to`
		// is still checked against the status enum so the repair can't substitute
		// one invalid value for another.
		if statusField, has := schema.Fields["status"]; has {
			for _, e := range statusField.Enum {
				if e == to {
					return nil
				}
			}
		}
		return fmt.Errorf("unknown status %q for type %q", from, docType)
	}

	for _, a := range allowed {
		if a == to {
			return nil
		}
	}

	return fmt.Errorf("invalid status transition %q -> %q for type %q (allowed: %v)", from, to, docType, allowed)
}
