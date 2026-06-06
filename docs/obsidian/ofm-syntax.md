---
id: 05b56ca4-6cc2-4a3f-ad34-fe9424d5ba0b
title: "Obsidian Flavored Markdown Syntax Spec"
type: note
status: complete
---

# Obsidian Flavored Markdown Syntax Spec

This document details 2ndbrain's support levels and indexing behaviors for various Obsidian Flavored Markdown (OFM) syntax elements.

## Wikilinks

### Standard Wikilinks
* **Syntax:** `[[target-note]]`
* **Support Level:** Full
* **Indexing Implication:** Exposes an edge in the link graph from the source document to `target-note`.

### Aliased Wikilinks
* **Syntax:** `[[target-note|Display Text]]`
* **Support Level:** Full
* **Indexing Implication:** Exposes an edge to `target-note`. The display text is recorded in the link metadata but omitted from path resolution.

### Headings and Block Anchors
* **Syntax:** `[[target-note#Heading Section]]` or `[[target-note#^block-id]]`
* **Support Level:** Full (parsing and recording)
* **Indexing Implication:** Exposes an edge in the link graph. The heading anchor is recorded in `links.heading` and a block reference is stored in `links.block_id`; block-id definitions (`^block-id`) found in document content are stored in `chunks.block_id`.
* **Known limitation:** Link resolution operates at document granularity — it matches the target document by path, basename, title, or alias but does **not** yet verify that the referenced heading or block actually exists within that document. Anchor verification is backlog.

---

## Markdown Links

* **Syntax:** `[Display Text](relative/path/to/note.md)`
* **Support Level:** Full
* **Indexing Implication:** Exposes an edge in the link graph. Path resolution normalizes the relative path to resolve against vault basenames.

---

## Embedded Transclusions

* **Syntax:** `![[target-note]]` or `![[target-note#Heading Section]]`
* **Support Level:** Parsed and flagged — the link is recorded with an `embed` flag and resolves like any wikilink.
* **Indexing Implication:** Recorded as a document dependency, distinguished from a normal link by the embed flag. Expanding the transcluded target's text inline as extra RAG context is **not** yet implemented (RAG retrieves via hybrid search over indexed chunks); that expansion is backlog.

---

## Tag Formats

### Frontmatter Tags
* **Syntax:**
  ```yaml
  tags:
    - engineering
    - database
  ```
* **Support Level:** Full
* **Indexing Implication:** Tags are recorded in the tags table for search filtering.

### Inline Tags
* **Syntax:** `#engineering` (a tag is a `#` followed by letters, digits, `-`, or `_`)
* **Support Level:** Full for flat tags.
* **Indexing Implication:** Extracted from the document body (skipping code blocks and heading lines) and recorded in the tags table for search filtering. Nested tags like `#engineering/database` currently capture only the first segment (`engineering`); the `/sub-tag` portion is not indexed.

---

## Frontmatter Properties

* **Syntax:** Standard YAML block at the beginning of a document.
* **Support Level:** Full
* **Indexing Implication:** Parsed and stored as JSON in the metadata columns. Standard fields (`type`, `status`) map to relational columns for filtering.

---

## Callout Blocks

* **Syntax:**
  ```markdown
  > [!NOTE]
  > Callout text content.
  ```
* **Support Level:** Full (Read-only)
* **Indexing Implication:** The callout syntax block is parsed as a chunk. The text is processed by embedding models for semantic search queries.

---

## Comments

* **Syntax:** `%% Comment Text %%`
* **Support Level:** Ignored
* **Indexing Implication:** Stripped during document parsing and vector embedding generation to prevent comments from polluting search results.
