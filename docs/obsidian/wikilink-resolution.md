---
id: 441f0ed9-d7bc-404d-a9b5-c8844c34f61f
title: "Wikilink Resolution Algorithm"
type: note
status: complete
---

# Wikilink Resolution Algorithm

This document defines the resolution algorithm utilized by 2ndbrain to resolve wikilinks, matching Obsidian's path resolution strategy.

## Resolution Sequence

When the parser encounters a link `[[Target]]`, it attempts to resolve the destination file using the following priority order. Each rule after the first only succeeds when it matches **exactly one** document; a name that matches several documents falls through to the next rule, and if none yields a single match the link is recorded but left **unresolved** (`target_id` is `NULL`).

1. **Exact Path Match:** `Target` (or `Target.md`) equals a full vault-relative file path (e.g., `folder/subfolder/note.md`). Paths are unique, so this always resolves to one document.
2. **Shortest Unique Name Match:** `Target` equals a document's basename or any `/`-delimited suffix of its path, with or without the `.md` extension — but only when that name maps to a single document. This covers `[[note]]`, `[[work/project]]`, and `[[beta/architecture]]`. If two files share the name (e.g., `work/project.md` and `home/project.md`), `[[project]]` is ambiguous and is left unresolved; `[[work/project]]` disambiguates by suffix.
3. **Title Match:** Exactly one document whose frontmatter `title` equals `Target`.
4. **Alias Match:** Exactly one document listing `Target` in its frontmatter `aliases` array.

---

## Worked Collision Examples

Consider a vault with the following structure:

```
vault-root/
├── engineering/
│   └── architecture.md
├── marketing/
│   └── architecture.md
├── projects/
│   └── beta/
│       └── architecture.md
└── main.md
```

### Case 1: Simple Ambiguity
* **Link in `main.md`:** `[[architecture]]`
* **Resolution Behavior:** Because three files match the basename `architecture.md`, the link is ambiguous. The resolver does **not** guess — the link is left unresolved (`target_id` is `NULL`). Use a disambiguating suffix (Case 2/3) or an alias to resolve it. `2nb lint` surfaces unresolved links so they can be found and fixed.

### Case 2: Shortest Path Disambiguation
* **Link in `main.md`:** `[[marketing/architecture]]`
* **Resolution Behavior:** The resolver matches against the relative path fragment `marketing/architecture`. It resolves successfully to `marketing/architecture.md`, avoiding the collision with other folders.

### Case 3: Nested Disambiguation
* **Link in `main.md`:** `[[beta/architecture]]`
* **Resolution Behavior:** The resolver checks for relative paths ending in `beta/architecture.md`. It matches and resolves to `projects/beta/architecture.md`.

---

## Anchor Resolution

If a link contains an anchor (e.g., `[[Target#Section Title]]` or `[[Target#^block-id]]`), the resolver matches the document using the sequence above and records the anchor alongside the resolved edge:

* **Heading Anchor (`#Section Title`):** The heading text is stored in `links.heading`.
* **Block Anchor (`#^block-id`):** The block identifier is stored in `links.block_id` (the leading `^` is stripped).

> [!NOTE]
> Resolution currently operates at document granularity. The anchor is parsed and recorded, but the resolver does **not** yet verify that the referenced heading or block actually exists within the target document. Anchor-level verification is backlog.
