# Document Templates

2ndbrain ships with six built-in document types, each with a template and schema.

## Property types and Obsidian

Every note `2nb create` writes carries `id`, `title`, `type`, `status`, `tags`,
`created` and `modified`. Obsidian infers each property's TYPE from how its
value is written, so the spelling is part of the template contract:

- `created` and `modified` are written as a PLAIN, second-precision RFC3339
  value (`created: 2026-09-04T12:34:56Z`), which Obsidian types as **Date and
  time**. They are held in the frontmatter map as a `time.Time`, not a string,
  because yaml.v3 quotes any Go string that would re-resolve to a timestamp and
  Obsidian reads a quoted ISO value as **Text**: no date picker, no date
  sorting, no date-based query. Notes written before 0.23.0 carry the quoted
  form; `2nb obsidian migrate-properties` repairs them.
- `2nb meta --set` and the MCP `kb_update_meta` coerce a date-shaped value for
  those fields (and for any field a schema declares `date` or `datetime`) to the
  same plain form, so an ordinary edit cannot revert a note to Text.
- `2nb obsidian register-types` declares the types in `.obsidian/types.json`, so
  the right editor appears even for a note where the property is empty. `status`
  is declared `text`: Obsidian has no enum type, and its list editor would write
  a YAML sequence back, which reads as no status at all.

## Obsidian template files

A template's frontmatter is deliberately not valid YAML (`date: {{date}}` is a
flow mapping used as a mapping key), so it is scaffolding, not a note. 2nb
recognizes one in two ways: it is inside a template FOLDER (Obsidian's own
`templates.json` / Templater setting, or a top-level `templates/` when the
Templates core plugin is enabled and that folder exists), or its frontmatter
carries a `{{placeholder}}`. Neither is indexed, neither is a link-resolution
candidate, and neither is migrated.

If you keep a template of your own, prefer leaving the date placeholder OUT of
the frontmatter and putting `{{date}}` in the BODY, where it is ordinary
markdown:

```markdown
---
title:
tags: [daily]
---

# {{date}}
```

Quoting the placeholder (`date: "{{date}}"`) makes the YAML valid but produces a
QUOTED value after substitution, which is exactly the Text-not-date shape this
release exists to fix.

**Required frontmatter per type:** `adr`, `runbook`, `prd`, and `prfaq` require `title` + `status`; `note` requires only `title`; `postmortem` requires `title` + `status` + `incident-date`.

## ADR (Architecture Decision Record)

**Schema fields:**
- `status`: proposed, accepted, deprecated, superseded
- `deciders`: list of people
- `superseded-by`: path to superseding ADR

**Status state machine:**
```
proposed -> accepted | deprecated
accepted -> deprecated | superseded
deprecated -> (terminal)
superseded -> (terminal)
```

**Template:**
```markdown
# {Title}

## Status

proposed

## Context

What is the issue that we're seeing that is motivating this decision or change?

## Decision

What is the change that we're proposing and/or doing?

## Consequences

What becomes easier or more difficult to do because of this change?
```

## Runbook

**Schema fields:**
- `status`: draft, active, archived
- `service`: service name
- `severity`: low, medium, high, critical

**Template:**
```markdown
# {Title}

## Overview
## Prerequisites
## Steps
## Verification
## Rollback
```

## Postmortem

**Schema fields:**
- `status`: draft, reviewed, published
- `incident-date`: date (required)
- `severity`: low, medium, high, critical
- `services`: list

**Template:**
```markdown
# {Title}

## Summary
## Timeline
## Root Cause
## Impact
## Action Items
## Lessons Learned
```

## PRD (Product Requirements Document)

**Schema fields:**
- `status`: draft, review, approved, shipped, archived
- `owner`: text
- `priority`: p0, p1, p2, p3

**Status state machine:**
```
draft -> review
review -> draft | approved
approved -> shipped | draft
shipped -> archived
archived -> (terminal)
```

**Template:**
```markdown
# {Title}

## Problem Statement
## Target Users
## Goals
## Non-Goals
## User Stories
## Functional Requirements
### P0 — MVP
### P1 — Enhancements
## Non-Functional Requirements
## Success Metrics
## Risks
```

## PR/FAQ (Press Release / FAQ)

**Schema fields:**
- `status`: draft, review, final
- `owner`: text

**Status state machine:**
```
draft -> review
review -> draft | final
final -> (terminal)
```

**Template:**
```markdown
# {Title}

## Press Release
  - Headline, subheadline, body, how it works, call to action
## Frequently Asked Questions
  ### External FAQ (Customer Questions)
  ### Internal FAQ (Engineering / Business Questions)
```

## Note

**Schema fields:**
- `status`: draft, complete

Simple freeform template with just the title heading.

## Custom Types

Add new types by editing `.2ndbrain/schemas.yaml`:

```yaml
types:
  service-doc:
    name: Service Documentation
    description: Internal service catalog entry
    fields:
      status:
        type: text
        enum: [draft, active, deprecated]
      owner:
        type: text
      repo:
        type: text
    required: [title, status, owner]
```

Templates are currently built into the CLI binary. Custom types use the `note` template by default.
