---
id: afc61e21-b30a-4a44-a3db-b0d3d0ce3100
title: "JSON Canvas and YAML Bases Spec"
type: note
status: complete
---

# JSON Canvas and YAML Bases Spec

This document describes the structure and indexing integration contract for Obsidian JSON Canvas (`.canvas`) and YAML Bases (`.base`) documents.

## JSON Canvas

Obsidian Canvas allows users to create infinite visual boards combining markdown files, text cards, URLs, and group nodes.

### Support Contract: Read-Only Indexing
2ndbrain provides read-only indexing for Canvas files. Under no circumstances will the CLI or MCP server modify or write to a `.canvas` file.

### Structure Example
A `.canvas` file is a JSON document containing arrays of `nodes` and `edges`:

```json
{
  "nodes": [
    {
      "id": "node-1",
      "type": "text",
      "text": "Core authentication strategy using JWT tokens",
      "x": -100,
      "y": -50,
      "width": 250,
      "height": 100
    },
    {
      "id": "node-2",
      "type": "file",
      "file": "engineering/auth-model.md",
      "x": 200,
      "y": -50,
      "width": 250,
      "height": 150
    }
  ],
  "edges": [
    {
      "id": "edge-1",
      "fromNode": "node-1",
      "toNode": "node-2"
    }
  ]
}
```

### Indexing Implication
The CLI flattens a canvas into a synthetic markdown body (a `# Canvas Nodes` section followed by a `# Canvas Edges` section) and indexes that body. The two parts behave differently:

* **Nodes:** Text cards (type `text`) are parsed, chunked, and embedded for vector search. File-reference cards (type `file`) emit a `[[wikilink]]` to the referenced document, which then resolves through the normal link graph like any other wikilink. (`file` is a *node* type, not an edge type.)
* **Edges:** Connections between nodes are rendered as descriptive prose in the synthetic body (`## Edge <id>` with `From:` / `To:` lines naming the connected nodes) so the relationships are searchable. Edges are **not** stored as rows in the `links` table and do **not** create graph edges; only the `[[wikilinks]]` emitted by file-type nodes appear in the link graph.

---

## YAML Bases

Bases (`.base`) are structured YAML configuration profiles used for metadata definition and vault automation templates.

### Support Contract: Read-Only Indexing
2ndbrain indexes bases as structured metadata documents, allowing AI tools to retrieve definitions semantically.

### Structure Example
A `.base` file is a YAML document outlining configurations, properties, or schemas:

```yaml
base:
  name: Service Configuration
  version: 1.0.0
  environment: production
  settings:
    timeout_ms: 5000
    retry_attempts: 3
```

### Indexing Implication
* The YAML content is parsed and indexed as key-value property chunks.
* Embedding models process the structured keys and values, allowing queries like "what is the production timeout setting?" to return the correct configuration values.
