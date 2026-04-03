# Document Templates

2ndbrain ships with four built-in document types, each with a template and schema.

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
