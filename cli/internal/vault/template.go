package vault

var templates = map[string]string{
	"adr": `# {{.Title}}

## Status

{{.Status}}

## Context

What is the issue that we're seeing that is motivating this decision or change?

## Decision

What is the change that we're proposing and/or doing?

## Consequences

What becomes easier or more difficult to do because of this change?
`,
	"runbook": `# {{.Title}}

## Overview

Brief description of what this runbook addresses.

## Prerequisites

- [ ] Access to relevant systems
- [ ] Required permissions

## Steps

1. First step
2. Second step
3. Third step

## Verification

How to verify the procedure was successful.

## Rollback

Steps to undo if something goes wrong.
`,
	"note": `# {{.Title}}

`,
	"postmortem": `# {{.Title}}

## Summary

Brief summary of the incident.

## Timeline

| Time | Event |
|------|-------|
| | Incident detected |
| | Investigation started |
| | Root cause identified |
| | Fix deployed |
| | Incident resolved |

## Root Cause

What caused the incident?

## Impact

Who/what was affected and for how long?

## Action Items

- [ ] Action item 1
- [ ] Action item 2

## Lessons Learned

What did we learn from this incident?
`,
}

func GetTemplate(docType string) string {
	if tmpl, ok := templates[docType]; ok {
		return tmpl
	}
	return templates["note"]
}
