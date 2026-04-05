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
	"prd": `# {{.Title}}

## Problem Statement

What problem are we solving? Who has this problem? Why does it matter?

## Target Users

Who are the primary, secondary, and tertiary users?

## Goals

| # | Goal | Rationale |
|---|------|-----------|
| 1 | | |
| 2 | | |

## Non-Goals

- What are we explicitly not doing?

## User Stories

- **As a** [user type], **I want** [action] **so that** [benefit]

## Functional Requirements

### P0 — MVP

| ID | Requirement |
|----|-------------|
| FR-1 | |

### P1 — Enhancements

| ID | Requirement |
|----|-------------|
| FR-10 | |

## Non-Functional Requirements

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-1 | | |

## Success Metrics

| # | Metric | How to verify |
|---|--------|---------------|
| 1 | | |

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| | | | |
`,
	"prfaq": `# {{.Title}}

## Press Release

**FOR IMMEDIATE RELEASE**

### [Headline: one sentence describing the product and its key benefit]

*[Subheadline: expand on the value proposition]*

**[City, State]** — Today, [company] announced [product], a [brief description]. [Product] enables [target user] to [key benefit] by [how it works at a high level].

[Problem paragraph: describe the pain point this solves]

[Quote from a leader or stakeholder about why this matters]

**How it works:**

1. [Step 1]
2. [Step 2]
3. [Step 3]

[Call to action: where to get it, how to start]

---

## Frequently Asked Questions

### External FAQ (Customer Questions)

**Q: Who is this for?**
A:

**Q: How does it work?**
A:

**Q: How much does it cost?**
A:

**Q: How is this different from [alternative]?**
A:

### Internal FAQ (Engineering / Business Questions)

**Q: Why now?**
A:

**Q: What's the technical approach?**
A:

**Q: What are the main risks?**
A:

**Q: What does success look like?**
A:
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
