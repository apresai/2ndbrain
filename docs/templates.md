# Document Templates

2ndbrain ships with six built-in document types, each with a template and schema.

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
## Functional Requirements (P0 / P1)
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
