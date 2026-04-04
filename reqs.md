# 2ndbrain Requirements Specification

## Overview

**Product**: 2ndbrain — A native macOS markdown editor built for joint AI-human use.

**System Name**: "the editor" (used consistently throughout all requirements)

### EARS Pattern Reference

| Pattern | Keyword(s) | Syntax | Code |
|---------|-----------|--------|------|
| Ubiquitous | *(none)* | The editor shall `<response>`. | UB |
| Event-Driven | When | When `<trigger>`, the editor shall `<response>`. | EV |
| State-Driven | While | While `<state>`, the editor shall `<response>`. | ST |
| Optional Feature | Where | Where `<feature>`, the editor shall `<response>`. | OF |
| Unwanted Behavior | If/Then | If `<condition>`, then the editor shall `<response>`. | UW |
| Complex | While+When | While `<state>`, when `<trigger>`, the editor shall `<response>`. | CX |

### Requirement ID Format

`<AREA>-<PATTERN>-<SEQ>` (e.g., `DOC-EV-001`)

---

## 1. Document Management

**DOC-UB-001**: The editor shall store all documents as plain UTF-8 encoded markdown files on the local filesystem.

**DOC-UB-002**: The editor shall support opening multiple documents simultaneously in separate tabs.

**DOC-UB-003**: The editor shall preserve the original line ending format (LF or CRLF) of each document on save.

**DOC-EV-001**: When the user selects "New Document", the editor shall create an untitled document with the vault's default frontmatter template applied.

**DOC-EV-002**: When the user opens a file via Finder, the editor shall load the document and display it in a new tab within 500 milliseconds for files under 1 MB.

**DOC-EV-003**: When Cmd+S is pressed, the editor shall write the current document to disk atomically using a temporary file and rename.

**DOC-EV-004**: When the user selects "Export as PDF", the editor shall render the markdown to a paginated PDF and present a save dialog.

**DOC-EV-005**: When a document tab is dragged outside the tab bar, the editor shall open that document in a new window.

**DOC-EV-006**: When the user selects "Duplicate Document", the editor shall create a copy with a unique filename and a new UUID in the frontmatter.

**DOC-EV-007**: When the user drags a markdown file into the editor window, the editor shall open that file in a new tab.

**DOC-ST-001**: While a document has unsaved changes, the editor shall display a modification indicator in the tab title and window title bar.

**DOC-ST-002**: While a document is loading, the editor shall display a progress indicator and disable editing controls.

**DOC-UW-001**: If the user attempts to close a document with unsaved changes, then the editor shall present a save confirmation dialog with Save, Discard, and Cancel options.

**DOC-UW-002**: If a file opened by the editor is deleted from disk by an external process, then the editor shall notify the user and offer to save a new copy.

**DOC-UW-003**: If a file cannot be opened due to insufficient permissions, then the editor shall display an error message identifying the permission issue and the file path.

---

## 2. Editor Core

**EDT-UB-001**: The editor shall support undo and redo operations with a minimum history depth of 1,000 actions per document.

**EDT-UB-002**: The editor shall render the cursor at a consistent 60 fps blink rate.

**EDT-EV-001**: When Cmd+Z is pressed, the editor shall undo the most recent edit operation.

**EDT-EV-002**: When Cmd+Shift+Z is pressed, the editor shall redo the most recently undone operation.

**EDT-EV-003**: When the user selects text and presses Cmd+B, the editor shall wrap the selection in bold markdown syntax (`**`).

**EDT-EV-004**: When the user selects text and presses Cmd+I, the editor shall wrap the selection in italic markdown syntax (`*`).

**EDT-EV-005**: When the user presses Tab at the start of a list item, the editor shall increase the list indentation level by one.

**EDT-EV-006**: When the user presses Shift+Tab at the start of a list item, the editor shall decrease the list indentation level by one.

**EDT-EV-007**: When the user presses Return inside a list, the editor shall create a new list item at the same indentation level.

**EDT-EV-008**: When the user double-clicks a word, the editor shall select that entire word including adjacent underscores in identifiers.

**EDT-EV-009**: When Cmd+A is pressed, the editor shall select all text in the active document body, excluding frontmatter.

**EDT-EV-011**: When the user types `[[`, the editor shall display an autocomplete dropdown of note titles in the vault.

**EDT-EV-012**: When the user types three backticks followed by Return, the editor shall create a fenced code block and place the cursor on the language identifier line.

**EDT-ST-001**: While text is selected, the editor shall display the selection character count and word count in the status bar.

**EDT-ST-002**: While the document contains more than 10,000 words, the editor shall display a document length indicator in the status bar.

**EDT-CX-001**: While vim mode is enabled, when the user presses Escape, the editor shall transition from insert mode to normal mode.

**EDT-CX-002**: While autocomplete is active, when the user presses Return, the editor shall insert the selected completion and dismiss the dropdown.

---

## 3. Markdown Rendering

**RND-UB-001**: The editor shall support the CommonMark 0.31 specification for markdown parsing and rendering.

**RND-UB-002**: The editor shall support GitHub Flavored Markdown extensions including tables, task lists, strikethrough, and autolinks.

**RND-UB-003**: The editor shall apply syntax highlighting to fenced code blocks for a minimum of 50 programming languages.

**RND-UB-004**: The editor shall render inline code spans in a monospace font distinct from the body text.

**RND-EV-001**: When the user modifies markdown source text, the editor shall update the rendered preview within 200 milliseconds.

**RND-EV-002**: When the user clicks a heading in the document outline, the editor shall scroll both the source and preview panes to that heading.

**RND-EV-003**: When the user hovers over a wikilink in preview mode, the editor shall display a popup preview of the linked document's first 200 characters.

**RND-EV-004**: When the user clicks an external URL in preview mode, the editor shall open that URL in the default system browser.

**RND-OF-001**: Where the Mermaid rendering extension is enabled, the editor shall render Mermaid diagram code blocks as interactive SVG.

**RND-OF-002**: Where the LaTeX math extension is enabled, the editor shall render inline (`$...$`) and block (`$$...$$`) LaTeX expressions using MathJax.

**RND-UW-001**: If the editor encounters a malformed markdown construct during rendering, then the editor shall display the raw source text for that block rather than omitting it.

**RND-UW-002**: If a fenced code block specifies an unrecognized language identifier, then the editor shall render the block with plain monospace formatting and no syntax highlighting.

---

## 4. File System & Storage

**FST-UB-001**: The editor shall organize documents within a vault, defined as a designated root directory on the local filesystem.

**FST-UB-002**: The editor shall monitor all files in the active vault using macOS FSEvents for change detection.

**FST-UB-003**: The editor shall maintain a SQLite index file (`.2ndbrain/index.db`) in the vault root for structured metadata queries.

**FST-EV-001**: When an external process modifies a file in the vault, the editor shall detect the change within 1 second via FSEvents and offer to reload the document.

**FST-EV-002**: When the user renames a file through the editor, the editor shall update all wikilinks referencing that file across the vault.

**FST-EV-003**: When a new markdown file is added to the vault directory by an external process, the editor shall index it within 2 seconds.

**FST-EV-004**: When the user creates a new vault, the editor shall initialize the `.2ndbrain/` directory with default configuration, schema definitions, and an empty index database.

**FST-EV-005**: When the editor launches, the editor shall reopen the most recently opened vault automatically and restore the sidebar file list.

**FST-ST-001**: While autosave is enabled, the editor shall write unsaved changes to disk every 30 seconds.

**FST-ST-002**: While the vault index is rebuilding, the editor shall display a progress indicator and allow continued editing.

**FST-UW-001**: If available disk space falls below 50 MB during a save operation, then the editor shall warn the user and complete the current save before blocking further writes.

**FST-UW-002**: If the vault index database becomes corrupted, then the editor shall delete the index and trigger a full rebuild from the source markdown files.

**FST-UW-003**: If a file move operation fails due to a naming conflict, then the editor shall append a numeric suffix to the target filename and notify the user.

---

## 5. Search & Discovery

**SRC-UB-001**: The editor shall maintain a full-text BM25 search index over all markdown documents in the vault.

**SRC-UB-002**: The editor shall maintain a vector embedding index for semantic search over all markdown documents in the vault.

**SRC-UB-003**: The editor shall support hybrid search combining BM25 keyword matching and vector semantic similarity using Reciprocal Rank Fusion.

**SRC-UB-004**: The editor shall support structured search filters on frontmatter fields including type, status, tags, and date ranges.

**SRC-EV-001**: When the user presses Cmd+Shift+F, the editor shall open the vault-wide search panel.

**SRC-EV-002**: When the user types a search query, the editor shall display ranked results within 300 milliseconds for vaults containing up to 10,000 documents.

**SRC-EV-003**: When the user clicks a search result, the editor shall open the document and scroll to the matching section.

**SRC-EV-004**: When the user presses Cmd+P, the editor shall open a quick-open dialog for fuzzy filename search across the vault.

**SRC-EV-005**: When the user enters a search query prefixed with `tag:`, the editor shall filter results to documents containing the specified tag.

**SRC-EV-006**: When the user enters a search query prefixed with `type:`, the editor shall filter results to documents matching the specified document type.

**SRC-EV-007**: When the user enters a search query prefixed with `status:`, the editor shall filter results to documents matching the specified status value.

**SRC-EV-008**: When the user selects "Find Similar", the editor shall return the 10 most semantically similar documents to the current document using vector cosine similarity.

**SRC-ST-001**: While search results are displayed, the editor shall highlight all matching terms in the document preview pane.

**SRC-ST-002**: While the search panel is open, the editor shall update results incrementally as the user types.

**SRC-ST-003**: While a structured filter is active, the editor shall display the active filter criteria as removable badges in the search panel.

**SRC-UW-001**: If a search query returns no results, then the editor shall suggest semantically related queries based on the vault's content index.

**SRC-UW-002**: If the vector index has not been built yet, then the editor shall fall back to BM25-only search and display a notice that semantic search is initializing.

**SRC-UW-003**: If the embedding model fails to load, then the editor shall continue operating with keyword-only search and log the failure.

**SRC-CX-001**: While a tag filter is active, when the user enters a text query, the editor shall combine the tag filter with hybrid text search on the filtered subset.

---

## 6. AI Integration

**AI-UB-001**: The editor shall support fully local vector embeddings using GGUF-format models via Ollama without requiring external API calls or cloud credentials. Cloud providers (AWS Bedrock, OpenRouter) shall be available as optional alternatives.

**AI-UB-002**: The editor shall chunk documents at heading boundaries for embedding, preserving the full frontmatter as metadata on each chunk.

**AI-UB-003**: The editor shall assign a stable UUID to each document at creation time, stored in the `id` frontmatter field, that persists across file renames.

**AI-UB-004**: The editor shall expose all vault operations through a built-in MCP server using stdio transport.

**AI-UB-005**: The editor shall support the MCP Resources specification, exposing each document as a resource with `audience`, `priority`, and `lastModified` annotations.

**AI-EV-001**: When a document is saved, the editor shall re-embed only the chunks whose content hash has changed since the last embedding.

**AI-EV-002**: When a new document is created, the editor shall generate a UUID and insert it into the frontmatter `id` field.

**AI-EV-003**: When the user selects "Export Context Bundle", the editor shall generate a CLAUDE.md-compatible file containing the most relevant documents for the current working directory.

**AI-EV-004**: When the user presses Cmd+Shift+M, the editor shall open the MCP server status panel showing connected clients and recent tool invocations.

**AI-EV-005**: When an MCP client invokes the `kb_search` tool, the editor shall execute a hybrid search and return results as JSON with chunk content, metadata, and relevance scores.

**AI-EV-006**: When an MCP client invokes the `kb_read` tool, the editor shall return the full document or a specific chunk identified by heading path or chunk ID.

**AI-EV-007**: When an MCP client invokes the `kb_related` tool, the editor shall traverse the link graph to the specified depth and return connected documents as JSON.

**AI-EV-008**: When an MCP client invokes the `kb_create` tool, the editor shall create a new document from the specified template type with auto-generated UUID and frontmatter.

**AI-EV-009**: When an MCP client invokes the `kb_update_meta` tool, the editor shall update the specified frontmatter fields without modifying the document body.

**AI-EV-010**: When an MCP client invokes the `kb_structure` tool, the editor shall return the document's heading hierarchy as a JSON tree with chunk IDs.

**AI-EV-011**: When the user selects "Suggest Links", the editor shall analyze the current document and suggest wikilinks to semantically related documents in the vault.

**AI-EV-012**: When an MCP client invokes the `kb_ask` tool with a question, the editor shall retrieve relevant documents via hybrid search, pass them as context to the configured generation provider, and return the answer with source document paths.

**AI-ST-001**: While the MCP server is running, the editor shall display a connected indicator in the status bar showing the number of active MCP clients.

**AI-ST-002**: While the embedding index is being built, the editor shall display progress as a percentage of documents processed in the status bar.

**AI-ST-003**: While a document is open, the editor shall display the document's chunk count and estimated token count in the status bar.

**AI-OF-001**: Where a local LLM is configured via Ollama, the editor shall offer AI-powered autocomplete suggestions as the user types.

**AI-OF-002**: Where a local LLM is configured, the editor shall provide a "Q&A over vault" command that answers questions using RAG retrieval from the vault.

**AI-UW-001**: If the configured embedding model is not found on disk, then the editor shall prompt the user to download it and disable semantic search until the model is available.

**AI-UW-002**: If an MCP tool invocation fails, then the editor shall return a structured JSON error response with an error code and human-readable message.

**AI-UW-003**: If document chunking produces a chunk exceeding 2,048 tokens, then the editor shall split the chunk at the next paragraph boundary and log a warning.

**AI-CX-001**: While the MCP server is running, when a vault file is modified by an external process, the editor shall re-index the file and push a resource update notification to subscribed MCP clients.

---

## 7. CLI Interface

**CLI-UB-001**: The editor shall provide a command-line interface binary (`2nb`) that operates independently of the GUI application.

**CLI-UB-002**: The editor shall support `--json`, `--csv`, and `--yaml` output format flags on all CLI commands that return data.

**CLI-UB-003**: The editor shall return exit code 0 for success, 1 for not found, 2 for validation error, and 3 for stale reference on all CLI commands.

**CLI-EV-001**: When the user runs `2nb search <query>`, the editor shall execute a hybrid search and return ranked results to stdout.

**CLI-EV-002**: When the user runs `2nb search <query> --type <type>`, the editor shall filter search results to documents matching the specified type.

**CLI-EV-003**: When the user runs `2nb read <path>`, the editor shall output the full document content to stdout.

**CLI-EV-004**: When the user runs `2nb read <path> --chunk <heading-path>`, the editor shall output only the specified section's content.

**CLI-EV-005**: When the user runs `2nb meta <path>`, the editor shall output the document's frontmatter as structured data.

**CLI-EV-006**: When the user runs `2nb meta <path> --set key=value`, the editor shall update the specified frontmatter field without modifying the document body.

**CLI-EV-007**: When the user runs `2nb create --type <type> --title <title>`, the editor shall create a new document from the specified template with auto-generated UUID and frontmatter.

**CLI-EV-008**: When the user runs `2nb related <path> --depth <n>`, the editor shall traverse the link graph to depth n and output connected documents.

**CLI-EV-009**: When the user runs `2nb lint <glob>`, the editor shall validate frontmatter schemas and report broken wikilinks for all matched files.

**CLI-EV-010**: When the user runs `2nb stale --since <days>`, the editor shall list documents not modified within the specified number of days.

**CLI-EV-011**: When the user runs `2nb export-context --types <types>`, the editor shall generate a CLAUDE.md-compatible bundle of matching documents to stdout.

**CLI-EV-012**: When the user runs `2nb mcp-server`, the editor shall start an MCP server on stdio transport for integration with AI coding tools.

**CLI-EV-014**: When the user runs `2nb graph <path>`, the editor shall output the link graph for the specified document as a JSON adjacency list.

**CLI-ST-001**: While the `--porcelain` flag is set, the editor shall suppress all color codes, progress indicators, and decorative output.

**CLI-UW-001**: If a CLI command references a vault path that does not exist, then the editor shall print an error message to stderr and exit with code 1.

**CLI-UW-002**: If `2nb meta --set` receives a value that violates the frontmatter schema, then the editor shall print a validation error to stderr and exit with code 2.

**CLI-UW-003**: If the vault index does not exist when a search command is invoked, then the editor shall build the index before executing the search and print a notice to stderr.

**CLI-EV-015**: When the user runs `2nb ask <question>`, the editor shall search the vault using hybrid search, pass the top results as context to the generation provider, and return an answer with source document paths.

**CLI-EV-016**: When the user runs `2nb models list`, the editor shall display all available AI models from configured providers with pricing, dimensions, and context length.

**CLI-EV-017**: When the user runs `2nb ai status`, the editor shall display the current provider, model names, readiness, document count, and embedding count.

**CLI-EV-018**: When the user runs `2nb config set <key> <value>`, the editor shall update the vault configuration file and persist the change.

**CLI-EV-019**: When the user runs `2nb config set-key <provider>`, the editor shall prompt for an API key and store it securely in the macOS Keychain.

**CLI-UB-004**: All CLI commands shall resolve the active vault from `--vault` flag, `2NB_VAULT` environment variable, `~/.2ndbrain-active-vault` file, or current directory — in that priority order.

**CLI-UB-005**: All CLI commands shall default to human-readable output and only produce JSON when `--json` is explicitly passed.

**CLI-UW-004**: If the user attempts to create a document with a title containing invalid filename characters or starting with a dash, then the editor shall reject the title with a descriptive error.

**CLI-UB-006**: All CLI commands accepting file paths shall resolve `~` to the user's home directory, support relative paths from the current directory, and accept absolute paths — following standard POSIX path conventions.

---

## 8. Knowledge Graph & Linking

**LNK-UB-001**: The editor shall support wikilink syntax (`[[target]]`) for linking between documents in the vault.

**LNK-UB-002**: The editor shall support heading-anchored wikilinks (`[[target#heading]]`) for linking to specific sections.

**LNK-UB-003**: The editor shall support aliased wikilinks (`[[target|display text]]`) for custom link display text.

**LNK-UB-004**: The editor shall maintain a bidirectional link index mapping forward links and backlinks for every document in the vault.

**LNK-UB-005**: The editor shall resolve wikilinks by UUID first, falling back to filename match if no UUID match is found.

**LNK-EV-001**: When the user opens a document, the editor shall display a backlinks panel listing all documents that link to the current document.

**LNK-EV-002**: When the user Cmd+Clicks a wikilink, the editor shall navigate to the linked document in a new tab.

**LNK-EV-003**: When the user creates a wikilink to a non-existent document, the editor shall mark the link as unresolved and offer to create the target document.

**LNK-EV-004**: When the user selects "Show Graph View", the editor shall render an interactive force-directed graph of all linked documents in the vault.

**LNK-EV-005**: When the user selects "Show Local Graph", the editor shall render a graph showing only documents within 2 hops of the current document.

**LNK-ST-001**: While the graph view is open, the editor shall update the graph in real time as links are added or removed.

**LNK-ST-002**: While the backlinks panel is visible, the editor shall display the linking context (surrounding paragraph) for each backlink.

**LNK-UW-001**: If a wikilink target document is renamed outside the editor, then the editor shall detect the broken link on next index scan and surface it in the lint report.

**LNK-UW-002**: If the link graph contains a circular reference chain, then the editor shall detect and display it without entering an infinite traversal loop.

**LNK-CX-001**: While the graph view is open, when the user clicks a node, the editor shall center the graph on that node and highlight its direct connections.

---

## 9. Frontmatter & Metadata

**META-UB-001**: The editor shall parse YAML frontmatter delimited by `---` at the start of every markdown document.

**META-UB-002**: The editor shall support typed frontmatter properties: text, number, date, datetime, boolean, list, and tags.

**META-UB-003**: The editor shall enforce frontmatter schemas defined per document type in the vault configuration file `.2ndbrain/schemas.yaml`.

**META-UB-004**: The editor shall auto-populate the `created` and `modified` datetime fields on document creation and save.

**META-EV-001**: When the user creates a document from a template, the editor shall populate all required frontmatter fields defined by the template's schema.

**META-EV-002**: When the user modifies a frontmatter field through the properties panel, the editor shall validate the value against the schema before saving.

**META-EV-003**: When the user runs a schema validation, the editor shall report all documents with missing required fields or type mismatches.

**META-EV-004**: When the user changes a document's `status` field, the editor shall validate the transition against the status state machine defined in the schema (e.g., `proposed` -> `accepted` -> `superseded`).

**META-ST-001**: While the properties panel is open, the editor shall display all frontmatter fields with type-appropriate input controls (date picker for dates, dropdown for enums, checkbox for booleans).

**META-ST-002**: While a document type schema requires a `superseded-by` field, the editor shall display a warning if the field is empty when status is set to `superseded`.

**META-UW-001**: If the frontmatter YAML is malformed, then the editor shall display a parse error banner above the document and preserve the raw YAML text without modification.

**META-UW-002**: If a frontmatter field value does not match its schema-defined type, then the editor shall highlight the field in the properties panel and display the expected type.

---

## 10. User Interface

**UI-UB-001**: The editor shall provide a sidebar displaying the vault file tree, document outline, and backlinks panels.

**UI-UB-002**: The editor shall support light and dark color themes that follow the macOS system appearance setting.

**UI-UB-003**: The editor shall provide a command palette accessible via Cmd+Shift+P listing all available commands.

**UI-EV-001**: When the user presses Cmd+Shift+P, the editor shall open the command palette with fuzzy search.

**UI-EV-002**: When the user presses Cmd+\\, the editor shall toggle the sidebar visibility.

**UI-EV-003**: When the user selects "Split View", the editor shall display the source editor and rendered preview side by side.

**UI-EV-004**: When the user presses Cmd+Shift+E, the editor shall switch to focus mode, hiding all UI elements except the document content.

**UI-EV-005**: When the user presses Cmd+,, the editor shall open the preferences window.

**UI-EV-006**: When the user drags the split view divider, the editor shall resize both panes proportionally.

**UI-EV-007**: When the user right-clicks in the editor, the editor shall display a context menu with formatting options, link insertion, and AI actions.

**UI-ST-001**: While focus mode is active, the editor shall hide the sidebar, tab bar, status bar, and toolbar.

**UI-ST-002**: While the document outline is visible in the sidebar, the editor shall highlight the heading nearest to the current cursor position.

**UI-ST-003**: While a split view is active, the editor shall synchronize scroll positions between the source and preview panes.

**UI-UW-001**: If the editor window is resized below 600 pixels wide, then the editor shall collapse the sidebar automatically and switch to single-pane layout.

**UI-CX-001**: While focus mode is active, when the user moves the mouse to the top edge of the window, the editor shall reveal the toolbar temporarily.

---

## 11. macOS Platform Integration

**MAC-UB-001**: The editor shall index all vault documents in the macOS Spotlight search index using CoreSpotlight, including title, tags, document type, and the first 500 characters of body text.

**MAC-UB-002**: The editor shall provide a Quick Look extension that renders markdown files as formatted HTML when the user presses Space in Finder.

**MAC-UB-003**: The editor shall be sandboxed and notarized for distribution via the Mac App Store and direct download.

**MAC-EV-001**: When a vault document is created or modified, the editor shall update its CoreSpotlight index entry within 5 seconds.

**MAC-EV-002**: When the user activates the menu bar icon and types a query, the editor shall search the vault and display results in a dropdown.

**MAC-EV-003**: When the user presses the global hotkey (default: Cmd+Shift+N), the editor shall open a quick capture window for rapid note entry regardless of which application is focused.

**MAC-EV-004**: When the user copies text in the editor, the editor shall place both the plain text and the rendered HTML on the system clipboard for rich paste in other applications.

**MAC-OF-001**: Where Touch ID is available on the device, the editor shall offer Touch ID as an authentication method for unlocking encrypted vaults.

**MAC-OF-002**: Where Handoff is enabled, the editor shall advertise the current document for continuation on other Apple devices.

**MAC-UW-001**: If the Spotlight index becomes stale due to a background indexing failure, then the editor shall schedule a full re-index on next application launch.

---

## 12. Performance

**PERF-UB-001**: The editor shall launch and display the last-opened document within 2 seconds on Apple Silicon hardware.

**PERF-UB-002**: The editor shall consume less than 200 MB of RAM with a single vault of up to 5,000 documents open.

**PERF-UB-003**: The editor shall render markdown preview updates within 200 milliseconds of a keystroke for documents up to 50,000 words.

**PERF-EV-001**: When the user opens a document, the editor shall display the rendered content within 500 milliseconds for files up to 1 MB.

**PERF-EV-002**: When the user initiates a vault-wide search, the editor shall return the first page of results within 300 milliseconds for vaults containing up to 10,000 documents.

**PERF-EV-003**: When the vault index is triggered to rebuild, the editor shall complete a full rebuild of a 5,000-document vault within 60 seconds.

**PERF-ST-001**: While the editor has more than 20 tabs open, the editor shall lazy-load document content and release memory for non-visible tabs.

**PERF-ST-002**: While the graph view is rendering more than 1,000 nodes, the editor shall use level-of-detail rendering, showing only labels for nodes within the viewport.

**PERF-UW-001**: If a single document exceeds 100 MB, then the editor shall open it in a read-only streaming mode and display a large file warning.

**PERF-UW-002**: If the embedding index build exceeds available memory, then the editor shall process documents in batches of 100 and write intermediate results to disk.

---

## 13. Security & Privacy

**SEC-UB-001**: The editor shall store all vault configuration and index data within the vault directory, never transmitting data to external servers unless explicitly configured by the user.

**SEC-UB-002**: The editor shall never include frontmatter fields named `secret`, `password`, `token`, or `key` in search index entries or MCP responses.

**SEC-EV-001**: When the user enables vault encryption, the editor shall encrypt all documents at rest using AES-256 with a user-provided passphrase.

**SEC-EV-002**: When the user locks the vault, the editor shall clear all decrypted content from memory and require authentication to resume.

**SEC-EV-003**: When the MCP server receives a tool invocation, the editor shall validate that the requesting client is authorized before executing the operation.

**SEC-ST-001**: While the vault is locked, the editor shall reject all read and write operations and display only the unlock prompt.

**SEC-UW-001**: If the editor detects a file matching `.env`, `credentials.*`, or `*secret*` patterns in the vault, then the editor shall exclude that file from search indexing and MCP resource exposure.

**SEC-UW-002**: If an MCP client attempts to access a file outside the vault directory via path traversal, then the editor shall reject the request and log a security warning.

---

## 14. Error Handling & Recovery

**ERR-UB-001**: The editor shall maintain a crash recovery journal in `.2ndbrain/recovery/` containing unsaved document states.

**ERR-UB-002**: The editor shall log all errors to `.2ndbrain/logs/` with timestamps, error codes, and stack traces.

**ERR-EV-001**: When the editor launches after an abnormal termination, the editor shall present a recovery dialog listing all documents with unsaved changes from the previous session.

**ERR-EV-002**: When the user selects "Recover" for a document, the editor shall restore the document to its last journaled state.

**ERR-EV-003**: When a plugin fails to load, the editor shall disable that plugin, log the error, and continue launching without it.

**ERR-ST-001**: While a background operation (indexing, embedding, sync) encounters repeated failures, the editor shall pause the operation and display a notification with retry and diagnostic options.

**ERR-UW-001**: If a document save fails due to a filesystem error, then the editor shall retain the unsaved content in the recovery journal and display the specific filesystem error to the user.

**ERR-UW-002**: If the editor detects conflicting edits to the same document from the editor and an external process simultaneously, then the editor shall present a diff-based merge conflict resolution dialog.

**ERR-UW-003**: If the YAML frontmatter write-back corrupts the document structure, then the editor shall restore the document from the pre-write backup in the recovery journal.

**ERR-UW-004**: If the undo history is exhausted, then the editor shall disable the undo control and display "Nothing to undo" in the status bar.

---

## 15. Obsidian Vault Conversion

**OBS-EV-001**: When the user selects "Import Obsidian Vault", the editor shall scan the selected directory for `.obsidian/` and all `.md` files, generate UUIDs for documents missing an `id` frontmatter field, set `type: note` for documents without a `type` field, normalize inline `#tag` syntax to frontmatter `tags` array, initialize a `.2ndbrain/` directory, and build the full search index.

**OBS-EV-002**: When the user selects "Export to Obsidian", the editor shall copy all vault markdown files to the selected directory, create an `.obsidian/` directory with default configuration, convert UUID-based wikilink references to filename-based wikilinks, and optionally strip 2ndbrain-specific frontmatter fields (`id`, `type`).

**OBS-EV-003**: When importing an Obsidian vault, the editor shall preserve all existing frontmatter fields not defined in the 2ndbrain schema as custom metadata.

**OBS-EV-004**: When importing an Obsidian vault, the editor shall map Obsidian `aliases` frontmatter to wikilink alias resolution during index building.

**OBS-EV-005**: When importing an Obsidian vault containing `.canvas` files, the editor shall preserve them as-is without modification.

**OBS-EV-006**: When the user runs `2nb import-obsidian <path>`, the CLI shall perform the same import operation as the GUI, outputting progress to stderr and a summary to stdout.

**OBS-EV-007**: When the user runs `2nb export-obsidian <path>`, the CLI shall perform the same export operation as the GUI with an optional `--strip-ids` flag to remove 2ndbrain-specific frontmatter fields.

**OBS-ST-001**: While importing an Obsidian vault, the editor shall display a progress indicator showing the number of documents processed out of total.

**OBS-UW-001**: If an Obsidian vault contains documents with conflicting filenames in different subdirectories, then the editor shall resolve wikilinks using the shortest unique path and log any ambiguous references.

**OBS-UW-002**: If an Obsidian vault uses Obsidian-specific syntax (block references `^block-id`, embedded transclusions `![[note]]`), then the editor shall preserve the raw syntax and log unsupported features.

**OBS-UW-003**: If an imported document's frontmatter contains YAML parsing errors, then the editor shall skip that document, log the error, and continue processing remaining files.

---

## 16. Testing & Quality

**TST-UB-001**: All GUI features marked as "Manual" test type in the test plan shall be verified via Claude computer-use automation against the running macOS app.

**TST-UB-002**: The `.app` bundle shall be ad-hoc codesigned on every build so that computer-use can control it for automated GUI testing.

**TST-EV-001**: When a GUI feature is implemented or modified, the developer shall run the corresponding computer-use test to verify the feature works end-to-end.

**TST-UW-001**: If a computer-use test fails, then the test output shall include a screenshot of the failure state for debugging.

---

## Summary

| Area | UB | EV | ST | UW | OF | CX | Total |
|------|----|----|----|----|----|----|-------|
| Document Management | 3 | 7 | 2 | 3 | 0 | 0 | **15** |
| Editor Core | 2 | 9 | 2 | 0 | 0 | 2 | **15** |
| Markdown Rendering | 4 | 4 | 0 | 2 | 2 | 0 | **12** |
| File System & Storage | 3 | 5 | 2 | 3 | 0 | 0 | **13** |
| Search & Discovery | 4 | 8 | 3 | 3 | 0 | 1 | **19** |
| AI Integration | 5 | 11 | 3 | 3 | 2 | 1 | **25** |
| CLI Interface | 3 | 13 | 1 | 3 | 0 | 0 | **20** |
| Knowledge Graph & Linking | 5 | 5 | 2 | 2 | 0 | 1 | **15** |
| Frontmatter & Metadata | 4 | 4 | 2 | 2 | 0 | 0 | **12** |
| User Interface | 3 | 7 | 3 | 1 | 0 | 1 | **15** |
| macOS Platform Integration | 3 | 4 | 0 | 1 | 2 | 0 | **10** |
| Performance | 3 | 3 | 2 | 2 | 0 | 0 | **10** |
| Security & Privacy | 2 | 3 | 1 | 2 | 0 | 0 | **8** |
| Error Handling & Recovery | 2 | 3 | 1 | 4 | 0 | 0 | **10** |
| Obsidian Vault Conversion | 0 | 7 | 1 | 3 | 0 | 0 | **11** |
| Testing & Quality | 2 | 1 | 0 | 1 | 0 | 0 | **4** |
| **Total** | **48** | **96** | **25** | **35** | **6** | **6** | **216** |
