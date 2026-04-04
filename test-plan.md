# 2ndbrain Test Plan

## Overview

This test plan maps every requirement in reqs.md to specific test cases with type classification and implementation status tracking. It covers all 211 requirements across 15 sections.

### Test Types
- **Unit**: Automated Go or Swift unit test
- **Integration**: End-to-end CLI test or multi-component test
- **Manual**: GUI interaction test requiring human verification
- **Inspection**: Code review or static analysis

### Status Legend
- **Impl**: Implementation status (Not Started | Partial | Complete)
- **Test**: Test status (Not Written | Written | Passing | Failing)

---

## 1. Document Management

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| DOC-UB-001 | Store as plain UTF-8 markdown on local filesystem | Inspection | Verify all file writes use UTF-8 encoding and write to local paths | Complete | Not Written |
| DOC-UB-002 | Support opening multiple documents in separate tabs | Manual | Open 3+ documents, verify each appears in its own tab | Complete | Not Written |
| DOC-UB-003 | Preserve original line ending format on save | Unit | Create files with LF and CRLF, save via editor, verify line endings preserved | Partial | Not Written |
| DOC-EV-001 | New Document creates untitled doc with default frontmatter | Integration | Trigger New Document, verify frontmatter template applied with UUID and timestamps | Complete | Not Written |
| DOC-EV-002 | Open file via Finder within 500ms for files under 1MB | Manual | Open a 500KB markdown file from Finder, measure load time under 500ms | Complete | Not Written |
| DOC-EV-003 | Cmd+S writes atomically via temp file and rename | Inspection | Verify save implementation uses write-to-temp then atomic rename pattern | Partial | Not Written |
| DOC-EV-004 | Export as PDF renders markdown to paginated PDF | Manual | Select Export as PDF, verify rendered output includes headings, code blocks, tables | Not Started | Not Written |
| DOC-EV-005 | Dragging tab outside tab bar opens document in new window | Manual | Drag a tab outside the bar, verify new window opens with that document | Not Started | Not Written |
| DOC-EV-006 | Duplicate Document creates copy with new UUID and filename | Manual | Duplicate a document, verify new file has unique name and fresh UUID in frontmatter | Not Started | Not Written |
| DOC-EV-007 | Drag markdown file into editor opens in new tab | Manual | Drag a .md file from Finder into the editor window, verify it opens in a new tab | Not Started | Not Written |
| DOC-ST-001 | Unsaved changes show modification indicator in tab and title bar | Manual | Edit a document, verify dot/indicator appears in tab title before saving | Complete | Not Written |
| DOC-ST-002 | Loading document shows progress indicator and disables editing | Manual | Open a large document, verify progress indicator appears and editor is disabled until loaded | Partial | Not Written |
| DOC-UW-001 | Closing unsaved document shows Save/Discard/Cancel dialog | Manual | Edit a document, close the tab, verify three-option confirmation dialog appears | Partial | Not Written |
| DOC-UW-002 | Externally deleted file notifies user and offers to save copy | Integration | Open a file, delete it externally, verify editor detects deletion and offers save dialog | Partial | Not Written |
| DOC-UW-003 | Permission error shows error with file path | Integration | Attempt to open a file with no read permission, verify error message includes the path | Partial | Not Written |

---

## 2. Editor Core

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| EDT-UB-001 | Undo/redo with minimum 1000 action history depth | Unit | Perform 1000+ edits, verify all can be undone and re-done sequentially | Complete | Not Written |
| EDT-UB-002 | Cursor blink at consistent 60fps rate | Manual | Observe cursor blink in editor, verify smooth consistent animation | Complete | Not Written |
| EDT-EV-001 | Cmd+Z undoes most recent edit operation | Manual | Type text, press Cmd+Z, verify last edit is reversed | Complete | Not Written |
| EDT-EV-002 | Cmd+Shift+Z redoes most recently undone operation | Manual | Undo an edit, press Cmd+Shift+Z, verify edit is restored | Complete | Not Written |
| EDT-EV-003 | Cmd+B wraps selection in bold syntax | Manual | Select text, press Cmd+B, verify `**text**` wrapping applied | Complete | Not Written |
| EDT-EV-004 | Cmd+I wraps selection in italic syntax | Manual | Select text, press Cmd+I, verify `*text*` wrapping applied | Complete | Not Written |
| EDT-EV-005 | Tab at list item start increases indent level | Manual | Place cursor at start of list item, press Tab, verify indent increases by one level | Complete | Not Written |
| EDT-EV-006 | Shift+Tab at list item start decreases indent level | Manual | Place cursor at indented list item, press Shift+Tab, verify indent decreases | Complete | Not Written |
| EDT-EV-007 | Return inside list creates new list item at same indent | Manual | Press Return in a list, verify new bullet appears at same indentation level | Complete | Not Written |
| EDT-EV-008 | Double-click selects word including adjacent underscores | Manual | Double-click on `my_variable`, verify entire identifier is selected | Partial | Not Written |
| EDT-EV-009 | Cmd+A selects all body text excluding frontmatter | Manual | Press Cmd+A in a document with frontmatter, verify only body text is selected | Partial | Not Written |
| EDT-EV-011 | Typing `[[` shows autocomplete dropdown of note titles | Manual | Type `[[` in the editor, verify dropdown with vault note titles appears | Complete | Not Written |
| EDT-EV-012 | Triple backtick + Return creates fenced code block | Manual | Type three backticks and press Return, verify code block created with cursor on language line | Partial | Not Written |
| EDT-ST-001 | Selection shows character count and word count in status bar | Manual | Select text, verify status bar shows selection character and word count | Complete | Not Written |
| EDT-ST-002 | Document over 10k words shows length indicator in status bar | Manual | Open a 10k+ word document, verify length indicator in status bar | Complete | Not Written |
| EDT-CX-001 | Vim mode: Escape transitions from insert to normal mode | Manual | Enable vim mode, enter insert mode, press Escape, verify normal mode activated | Not Started | Not Written |
| EDT-CX-002 | Return in autocomplete inserts selection and dismisses dropdown | Manual | Open autocomplete, press Return, verify completion inserted and dropdown closed | Complete | Not Written |

---

## 3. Markdown Rendering

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| RND-UB-001 | Support CommonMark 0.31 specification | Unit | Render CommonMark spec examples, verify output matches expected HTML | Complete | Not Written |
| RND-UB-002 | Support GFM tables, task lists, strikethrough, autolinks | Unit | Render GFM constructs, verify tables, checkboxes, strikethrough, and autolinks render | Complete | Not Written |
| RND-UB-003 | Syntax highlighting for 50+ programming languages | Manual | Create code blocks for 50 languages, verify syntax coloring applied to each | Partial | Not Written |
| RND-UB-004 | Inline code spans render in monospace font | Manual | Verify `code spans` render in a monospace font distinct from body text | Complete | Not Written |
| RND-EV-001 | Preview updates within 200ms of source text change | Manual | Edit markdown source, verify preview updates within 200ms | Complete | Not Written |
| RND-EV-002 | Clicking outline heading scrolls source and preview to heading | Manual | Click a heading in the document outline, verify both panes scroll to it | Partial | Not Written |
| RND-EV-003 | Hovering wikilink in preview shows popup preview of target | Manual | Hover over a wikilink in preview, verify popup shows first 200 chars of linked doc | Partial | Not Written |
| RND-EV-004 | Clicking external URL in preview opens default browser | Manual | Click an external URL in preview mode, verify system browser opens the URL | Complete | Not Written |
| RND-OF-001 | Mermaid extension renders code blocks as interactive SVG | Manual | Enable Mermaid, create a diagram code block, verify SVG rendering with interactivity | Not Started | Not Written |
| RND-OF-002 | LaTeX extension renders inline and block math via MathJax | Manual | Enable LaTeX, write `$x^2$` and `$$\sum$$`, verify rendered math output | Not Started | Not Written |
| RND-UW-001 | Malformed markdown shows raw source instead of omitting | Unit | Feed malformed markdown to renderer, verify raw text displayed not blank | Complete | Not Written |
| RND-UW-002 | Unrecognized language in code block renders as plain monospace | Manual | Specify `nonsense-lang` in a code block, verify plain monospace rendering with no highlighting | Complete | Not Written |

---

## 4. File System & Storage

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| FST-UB-001 | Documents organized within a vault root directory | Inspection | Verify all document operations scope to the vault root directory | Complete | Not Written |
| FST-UB-002 | Monitor vault files via macOS FSEvents for change detection | Unit | Verify FSEventsWatcher registers and fires callbacks for file changes | Complete | Not Written |
| FST-UB-003 | Maintain SQLite index in `.2ndbrain/index.db` | Unit | Init vault, verify `index.db` created in `.2ndbrain/` with correct schema | Complete | Not Written |
| FST-EV-001 | Detect external file modification within 1 second via FSEvents | Integration | Modify a file externally, verify editor detects change within 1 second and offers reload | Complete | Not Written |
| FST-EV-002 | Renaming file updates all wikilinks referencing that file | Integration | Rename a file through editor, verify all wikilinks across vault are updated | Partial | Not Written |
| FST-EV-003 | New markdown file added externally is indexed within 2 seconds | Integration | Add a .md file to vault directory, verify it appears in index within 2 seconds | Complete | Not Written |
| FST-EV-004 | Creating new vault initializes `.2ndbrain/` with config, schemas, empty DB | Integration | Run vault creation, verify `.2ndbrain/` dir has config.yaml, schemas.yaml, and index.db | Complete | Not Written |
| FST-ST-001 | Autosave writes unsaved changes every 30 seconds | Manual | Edit a document, wait 30 seconds without saving, verify file is written to disk | Not Started | Not Written |
| FST-ST-002 | Index rebuild shows progress indicator and allows editing | Manual | Trigger index rebuild, verify progress bar visible and editor remains responsive | Complete | Not Written |
| FST-UW-001 | Low disk space warning when below 50MB during save | Integration | Simulate low disk space, attempt save, verify warning displayed and save completes | Not Started | Not Written |
| FST-UW-002 | Corrupted index triggers full rebuild from markdown files | Integration | Corrupt the index.db, reopen vault, verify full rebuild is triggered automatically | Partial | Not Written |
| FST-UW-003 | File move naming conflict appends numeric suffix | Integration | Attempt to move file to a conflicting name, verify numeric suffix appended and user notified | Not Started | Not Written |

---

## 5. Search & Discovery

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| SRC-UB-001 | Maintain full-text BM25 search index over all documents | Unit | Index 10 documents, run BM25 query, verify ranked results returned | Complete | Not Written |
| SRC-UB-002 | Maintain vector embedding index for semantic search | Unit | Index documents with embeddings, query by vector similarity, verify results | Not Started | Not Written |
| SRC-UB-003 | Support hybrid search via Reciprocal Rank Fusion | Unit | Run hybrid search combining BM25 and vector, verify RRF-fused ranking | Not Started | Not Written |
| SRC-UB-004 | Support structured filters on type, status, tags, date ranges | Unit | Search with type/status/tag/date filters, verify filtered results match criteria | Complete | Not Written |
| SRC-EV-001 | Cmd+Shift+F opens vault-wide search panel | Manual | Press Cmd+Shift+F, verify search panel opens with focus on search input | Complete | Not Written |
| SRC-EV-002 | Search results within 300ms for vaults up to 10k documents | Integration | Build index with 10k docs, run search, verify results returned within 300ms | Complete | Not Written |
| SRC-EV-003 | Clicking search result opens document scrolled to match | Manual | Click a search result, verify document opens and scrolls to matching section | Partial | Not Written |
| SRC-EV-004 | Cmd+P opens quick-open dialog for fuzzy filename search | Manual | Press Cmd+P, verify quick-open dialog opens with fuzzy search | Complete | Not Written |
| SRC-EV-005 | `tag:` prefix filters results by specified tag | Unit | Search with `tag:architecture`, verify only tagged documents returned | Complete | Not Written |
| SRC-EV-006 | `type:` prefix filters results by document type | Unit | Search with `type:adr`, verify only ADR documents returned | Complete | Not Written |
| SRC-EV-007 | `status:` prefix filters results by status value | Unit | Search with `status:accepted`, verify only accepted-status documents returned | Complete | Not Written |
| SRC-EV-008 | Find Similar returns 10 most semantically similar documents | Integration | Select Find Similar on a document, verify 10 results ranked by cosine similarity | Not Started | Not Written |
| SRC-ST-001 | Search results highlight matching terms in preview pane | Manual | Run a search, verify matching terms are highlighted in the document preview | Partial | Not Written |
| SRC-ST-002 | Search updates results incrementally as user types | Manual | Type a search query character by character, verify results update incrementally | Complete | Not Written |
| SRC-ST-003 | Active filter shown as removable badge in search panel | Manual | Apply a tag filter, verify badge displayed in search panel and is removable | Partial | Not Written |
| SRC-UW-001 | No results suggests semantically related queries | Integration | Search for a term with no matches, verify suggested alternative queries appear | Not Started | Not Written |
| SRC-UW-002 | Missing vector index falls back to BM25 with notice | Integration | Search without vector index built, verify BM25 results returned with notice | Not Started | Not Written |
| SRC-UW-003 | Embedding model load failure falls back to keyword search | Integration | Simulate model load failure, verify keyword-only search operates and failure logged | Not Started | Not Written |
| SRC-CX-001 | Tag filter + text query combines hybrid search on subset | Unit | Set tag filter, enter text query, verify combined filtered hybrid search results | Partial | Not Written |

---

## 6. AI Integration

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| AI-UB-001 | Generate vector embeddings locally using GGUF models | Integration | Load GGUF model, generate embeddings for a document, verify vector output | Not Started | Not Written |
| AI-UB-002 | Chunk documents at heading boundaries with frontmatter metadata | Unit | Chunk a multi-heading document, verify chunks split at headings with full frontmatter | Complete | Not Written |
| AI-UB-003 | Assign stable UUID at creation stored in `id` frontmatter field | Unit | Create document, rename file, verify UUID in frontmatter persists unchanged | Complete | Not Written |
| AI-UB-004 | Expose vault operations through built-in MCP server via stdio | Integration | Start MCP server, send tool invocation over stdin, verify JSON response on stdout | Complete | Not Written |
| AI-UB-005 | Support MCP Resources with audience, priority, lastModified | Integration | List MCP resources, verify documents exposed with proper annotations | Partial | Not Written |
| AI-EV-001 | On save, re-embed only chunks with changed content hash | Integration | Save document with one changed section, verify only that chunk re-embedded | Not Started | Not Written |
| AI-EV-002 | New document gets auto-generated UUID in frontmatter `id` | Unit | Create new document via CLI and GUI, verify UUID populated in frontmatter | Complete | Not Written |
| AI-EV-003 | Export Context Bundle generates CLAUDE.md-compatible file | Integration | Run export-context, verify output is valid CLAUDE.md format with relevant docs | Complete | Not Written |
| AI-EV-004 | Cmd+Shift+M opens MCP server status panel | Manual | Press Cmd+Shift+M, verify status panel shows connected clients and recent invocations | Not Started | Not Written |
| AI-EV-005 | `kb_search` returns hybrid search JSON with scores | Integration | Invoke kb_search via MCP, verify JSON response with chunk content, metadata, scores | Complete | Not Written |
| AI-EV-006 | `kb_read` returns full document or specific chunk | Integration | Invoke kb_read with and without chunk ID, verify correct content returned | Complete | Not Written |
| AI-EV-007 | `kb_related` traverses link graph to specified depth | Integration | Invoke kb_related with depth=2, verify connected documents returned as JSON | Complete | Not Written |
| AI-EV-008 | `kb_create` creates document from template with UUID | Integration | Invoke kb_create with type, verify new file with template frontmatter and UUID | Complete | Not Written |
| AI-EV-009 | `kb_update_meta` updates frontmatter without modifying body | Integration | Invoke kb_update_meta, verify frontmatter field changed and body text identical | Complete | Not Written |
| AI-EV-010 | `kb_structure` returns heading hierarchy as JSON tree | Integration | Invoke kb_structure, verify JSON tree with heading levels and chunk IDs | Complete | Not Written |
| AI-EV-011 | Suggest Links analyzes document and suggests wikilinks | Integration | Run Suggest Links on a document, verify suggested links to semantically related docs | Not Started | Not Written |
| AI-ST-001 | MCP status indicator in status bar with client count | Manual | Start MCP server, connect a client, verify status bar shows connected count | Not Started | Not Written |
| AI-ST-002 | Embedding build progress shown as percentage in status bar | Manual | Trigger embedding build, verify percentage progress displayed in status bar | Not Started | Not Written |
| AI-ST-003 | Open document shows chunk count and token estimate in status bar | Manual | Open a document, verify chunk count and estimated token count in status bar | Not Started | Not Written |
| AI-OF-001 | Ollama LLM provides autocomplete suggestions while typing | Manual | Configure Ollama, type text, verify AI autocomplete suggestions appear | Not Started | Not Written |
| AI-OF-002 | Local LLM provides Q&A over vault via RAG retrieval | Integration | Configure LLM, ask a question, verify RAG-based answer from vault content | Not Started | Not Written |
| AI-UW-001 | Missing embedding model prompts download, disables semantic search | Integration | Remove model file, launch editor, verify download prompt and semantic search disabled | Not Started | Not Written |
| AI-UW-002 | Failed MCP tool returns structured JSON error with code | Integration | Invoke MCP tool with invalid params, verify JSON error response with error code and message | Complete | Not Written |
| AI-UW-003 | Chunk exceeding 2048 tokens splits at paragraph boundary | Unit | Create document with a heading section exceeding 2048 tokens, verify split at paragraph | Partial | Not Written |
| AI-CX-001 | External file modification triggers MCP resource update notification | Integration | Modify file while MCP server running, verify resource update pushed to subscribed clients | Partial | Not Written |

---

## 7. CLI Interface

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| CLI-UB-001 | Provide `2nb` CLI binary independent of GUI | Integration | Run `2nb --help` without GUI running, verify CLI operates standalone | Complete | Not Written |
| CLI-UB-002 | Support `--json`, `--csv`, `--yaml` output format flags | Integration | Run data commands with each flag, verify correct output format for each | Complete | Not Written |
| CLI-UB-003 | Exit codes: 0 success, 1 not found, 2 validation, 3 stale | Integration | Trigger each exit condition, verify correct exit code returned | Complete | Not Written |
| CLI-EV-001 | `2nb search <query>` returns ranked hybrid search results | Integration | Run search command with a query, verify ranked results on stdout | Complete | Not Written |
| CLI-EV-002 | `2nb search --type <type>` filters by document type | Integration | Run search with --type adr, verify only ADR documents in results | Complete | Not Written |
| CLI-EV-003 | `2nb read <path>` outputs full document content | Integration | Run read on a document, verify full content output to stdout | Complete | Not Written |
| CLI-EV-004 | `2nb read --chunk <heading-path>` outputs specific section | Integration | Run read with --chunk flag, verify only the specified section is output | Complete | Not Written |
| CLI-EV-005 | `2nb meta <path>` outputs frontmatter as structured data | Integration | Run meta on a document, verify structured frontmatter output | Complete | Not Written |
| CLI-EV-006 | `2nb meta --set key=value` updates frontmatter field | Integration | Run meta --set status=accepted, verify field updated and body unchanged | Complete | Not Written |
| CLI-EV-007 | `2nb create --type --title` creates doc from template | Integration | Run create with type and title, verify new file with template frontmatter and UUID | Complete | Not Written |
| CLI-EV-008 | `2nb related --depth <n>` traverses link graph | Integration | Run related with depth=2, verify connected documents output | Complete | Not Written |
| CLI-EV-009 | `2nb lint <glob>` validates schemas and broken wikilinks | Integration | Run lint on vault, verify schema violations and broken links reported | Complete | Not Written |
| CLI-EV-010 | `2nb stale --since <days>` lists stale documents | Integration | Run stale with --since 30, verify only documents older than 30 days listed | Complete | Not Written |
| CLI-EV-011 | `2nb export-context --types` generates CLAUDE.md bundle | Integration | Run export-context with --types adr, verify CLAUDE.md-compatible output on stdout | Complete | Not Written |
| CLI-EV-012 | `2nb mcp-server` starts MCP server on stdio transport | Integration | Run mcp-server, send JSON-RPC initialize, verify valid MCP response | Complete | Not Written |
| CLI-EV-014 | `2nb graph <path>` outputs link graph as JSON adjacency list | Integration | Run graph on a document, verify JSON adjacency list output | Complete | Not Written |
| CLI-ST-001 | `--porcelain` suppresses color, progress, decorative output | Integration | Run commands with --porcelain, verify no ANSI codes or progress indicators | Complete | Not Written |
| CLI-UW-001 | Non-existent vault path prints error to stderr with exit 1 | Integration | Run command on non-existent path, verify stderr error and exit code 1 | Complete | Not Written |
| CLI-UW-002 | `meta --set` with schema violation prints error with exit 2 | Integration | Run meta --set with invalid enum value, verify stderr validation error and exit code 2 | Complete | Not Written |
| CLI-UW-003 | Missing index auto-builds before search with stderr notice | Integration | Delete index.db, run search, verify index rebuilt and notice printed to stderr | Complete | Not Written |

---

## 8. Knowledge Graph & Linking

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| LNK-UB-001 | Support wikilink syntax `[[target]]` for linking | Unit | Parse document with wikilinks, verify links extracted correctly | Complete | Not Written |
| LNK-UB-002 | Support heading-anchored wikilinks `[[target#heading]]` | Unit | Parse `[[doc#section]]`, verify target and heading both resolved | Complete | Not Written |
| LNK-UB-003 | Support aliased wikilinks `[[target\|display text]]` | Unit | Parse `[[doc\|alias]]`, verify target and display text extracted separately | Complete | Not Written |
| LNK-UB-004 | Maintain bidirectional link index (forward + backlinks) | Unit | Index documents with links, verify forward and backlink maps are complete | Complete | Not Written |
| LNK-UB-005 | Resolve wikilinks by UUID first, filename fallback | Unit | Create link by UUID and by filename, verify UUID resolution takes priority | Complete | Not Written |
| LNK-EV-001 | Opening document shows backlinks panel with linking documents | Manual | Open a document with backlinks, verify panel lists all documents linking to it | Complete | Not Written |
| LNK-EV-002 | Cmd+Click on wikilink navigates to linked document in new tab | Manual | Cmd+Click a wikilink, verify linked document opens in new tab | Partial | Not Written |
| LNK-EV-003 | Wikilink to non-existent doc marked unresolved with create offer | Manual | Type `[[nonexistent]]`, verify link marked as unresolved and create option offered | Partial | Not Written |
| LNK-EV-004 | Show Graph View renders force-directed graph of all documents | Manual | Select Show Graph View, verify interactive force-directed graph renders all linked docs | Complete | Not Written |
| LNK-EV-005 | Show Local Graph renders 2-hop neighborhood of current document | Manual | Select Show Local Graph, verify graph shows only docs within 2 hops | Partial | Not Written |
| LNK-ST-001 | Graph view updates in real time as links are added/removed | Manual | Open graph view, add a link in editor, verify graph updates without manual refresh | Partial | Not Written |
| LNK-ST-002 | Backlinks panel shows linking context (surrounding paragraph) | Manual | View backlinks panel, verify each backlink shows surrounding paragraph context | Partial | Not Written |
| LNK-UW-001 | Renamed target detected as broken link in next lint scan | Integration | Rename a file externally, run lint, verify broken link reported for old wikilinks | Complete | Not Written |
| LNK-UW-002 | Circular reference chain detected without infinite loop | Unit | Create A->B->C->A circular links, run graph traversal, verify detection without hang | Complete | Not Written |
| LNK-CX-001 | Clicking graph node centers and highlights direct connections | Manual | Click a node in graph view, verify graph centers on it and highlights connections | Partial | Not Written |

---

## 9. Frontmatter & Metadata

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| META-UB-001 | Parse YAML frontmatter delimited by `---` | Unit | Parse document with `---` delimited frontmatter, verify all fields extracted | Complete | Not Written |
| META-UB-002 | Support typed properties: text, number, date, datetime, bool, list, tags | Unit | Create frontmatter with each type, verify correct parsing and type inference | Complete | Not Written |
| META-UB-003 | Enforce schemas from `.2ndbrain/schemas.yaml` per document type | Unit | Validate document against schema, verify required fields and type constraints enforced | Complete | Not Written |
| META-UB-004 | Auto-populate `created` and `modified` datetime fields | Unit | Create and save a document, verify `created` set on creation and `modified` updated on save | Complete | Not Written |
| META-EV-001 | Creating from template populates all required schema fields | Integration | Create document from ADR template, verify all required fields present in frontmatter | Complete | Not Written |
| META-EV-002 | Properties panel validates field values against schema before save | Manual | Enter invalid enum value in properties panel, verify validation error before save | Complete | Not Written |
| META-EV-003 | Schema validation reports missing required fields and type mismatches | Integration | Run lint/validation, verify missing fields and wrong-type values reported | Complete | Not Written |
| META-EV-004 | Status change validated against state machine in schema | Integration | Attempt invalid status transition (e.g. proposed->superseded), verify rejection | Complete | Not Written |
| META-ST-001 | Properties panel shows type-appropriate controls per field | Manual | Open properties panel, verify date picker for dates, dropdown for enums, checkbox for bools | Complete | Not Written |
| META-ST-002 | Superseded status warns if `superseded-by` field is empty | Manual | Set status to superseded with empty superseded-by, verify warning displayed | Partial | Not Written |
| META-UW-001 | Malformed YAML shows parse error and preserves raw text | Integration | Open document with invalid YAML frontmatter, verify error banner and raw YAML preserved | Complete | Not Written |
| META-UW-002 | Type-mismatched field highlighted in properties panel | Manual | Set a date field to a non-date string, verify field highlighted with expected type shown | Partial | Not Written |

---

## 10. User Interface

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| UI-UB-001 | Sidebar with file tree, document outline, and backlinks panels | Manual | Verify sidebar displays all three panels: file tree, outline, and backlinks | Complete | Not Written |
| UI-UB-002 | Light and dark themes following macOS system appearance | Manual | Toggle macOS appearance, verify editor theme switches between light and dark | Complete | Not Written |
| UI-UB-003 | Command palette via Cmd+Shift+P with all available commands | Manual | Open command palette, verify all commands listed and fuzzy search works | Complete | Not Written |
| UI-EV-001 | Cmd+Shift+P opens command palette with fuzzy search | Manual | Press Cmd+Shift+P, verify palette opens with focus on fuzzy search input | Complete | Not Written |
| UI-EV-002 | Cmd+\\ toggles sidebar visibility | Manual | Press Cmd+\\, verify sidebar hides; press again, verify sidebar reappears | Complete | Not Written |
| UI-EV-003 | Split View shows source editor and preview side by side | Manual | Select Split View, verify editor and rendered preview displayed in side-by-side panes | Complete | Not Written |
| UI-EV-004 | Cmd+Shift+E switches to focus mode hiding all UI except content | Manual | Press Cmd+Shift+E, verify sidebar, tab bar, status bar, and toolbar all hidden | Complete | Not Written |
| UI-EV-005 | Cmd+, opens preferences window | Manual | Press Cmd+, verify preferences window opens | Partial | Not Written |
| UI-EV-006 | Dragging split divider resizes both panes proportionally | Manual | Drag the split view divider, verify both panes resize proportionally | Complete | Not Written |
| UI-EV-007 | Right-click shows context menu with formatting and AI actions | Manual | Right-click in editor, verify context menu with formatting, link insertion, AI actions | Partial | Not Written |
| UI-ST-001 | Focus mode hides sidebar, tab bar, status bar, toolbar | Manual | Enter focus mode, verify all four UI elements are hidden | Complete | Not Written |
| UI-ST-002 | Outline highlights heading nearest to cursor position | Manual | Move cursor between headings, verify outline highlight follows cursor position | Partial | Not Written |
| UI-ST-003 | Split view synchronizes scroll between source and preview | Manual | Scroll in source pane, verify preview pane scrolls to same position | Partial | Not Written |
| UI-UW-001 | Window below 600px collapses sidebar to single-pane layout | Manual | Resize window below 600px wide, verify sidebar auto-collapses | Partial | Not Written |
| UI-CX-001 | Focus mode: mouse at top edge reveals toolbar temporarily | Manual | Enter focus mode, move mouse to top edge, verify toolbar reveals temporarily | Not Started | Not Written |

---

## 11. macOS Platform Integration

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| MAC-UB-001 | Index vault documents in Spotlight via CoreSpotlight | Integration | Create documents, verify they appear in Spotlight search with title, tags, type, body text | Complete | Not Written |
| MAC-UB-002 | Quick Look extension renders markdown as formatted HTML | Manual | Select a vault .md file in Finder, press Space, verify formatted HTML preview | Not Started | Not Written |
| MAC-UB-003 | Sandboxed and notarized for Mac App Store and direct download | Inspection | Verify app is sandboxed, signed, and notarized for distribution | Not Started | Not Written |
| MAC-EV-001 | Spotlight index entry updated within 5 seconds of doc change | Integration | Modify a document, verify CoreSpotlight entry updated within 5 seconds | Complete | Not Written |
| MAC-EV-002 | Menu bar icon with query and vault search results dropdown | Manual | Click menu bar icon, type query, verify search results appear in dropdown | Not Started | Not Written |
| MAC-EV-003 | Global hotkey (Cmd+Shift+N) opens quick capture window | Manual | Press Cmd+Shift+N from another app, verify quick capture window appears | Not Started | Not Written |
| MAC-EV-004 | Copy places plain text and rendered HTML on clipboard | Manual | Copy text from editor, paste into rich-text app, verify formatted paste | Partial | Not Written |
| MAC-OF-001 | Touch ID offered for encrypted vault authentication | Manual | Enable vault encryption on Touch ID device, verify Touch ID prompt for unlock | Not Started | Not Written |
| MAC-OF-002 | Handoff advertises current document for continuation | Manual | Open document on Mac, verify it appears as Handoff activity on other Apple devices | Not Started | Not Written |
| MAC-UW-001 | Stale Spotlight index schedules re-index on next launch | Integration | Simulate indexing failure, relaunch app, verify full re-index triggered | Partial | Not Written |

---

## 12. Performance

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| PERF-UB-001 | Launch and display last document within 2 seconds on Apple Silicon | Manual | Cold launch on Apple Silicon, measure time until last document displayed, verify under 2s | Partial | Not Written |
| PERF-UB-002 | Under 200MB RAM with single vault of 5000 documents | Manual | Open vault with 5000 documents, check Activity Monitor, verify under 200MB | Partial | Not Written |
| PERF-UB-003 | Preview updates within 200ms for documents up to 50k words | Manual | Edit a 50k-word document, verify preview updates within 200ms of keystroke | Partial | Not Written |
| PERF-EV-001 | Open document renders within 500ms for files up to 1MB | Manual | Open a 1MB markdown file, measure time until rendered content visible, verify under 500ms | Partial | Not Written |
| PERF-EV-002 | Search returns first page within 300ms for 10k-document vaults | Integration | Build 10k-doc index, run search, measure response time, verify under 300ms | Complete | Not Written |
| PERF-EV-003 | Full index rebuild of 5000 documents within 60 seconds | Integration | Index 5000 documents, measure total rebuild time, verify under 60 seconds | Complete | Not Written |
| PERF-ST-001 | 20+ open tabs use lazy-loading and release memory for hidden tabs | Manual | Open 20+ tabs, verify memory usage is controlled and non-visible tabs are lazy-loaded | Partial | Not Written |
| PERF-ST-002 | Graph view with 1000+ nodes uses level-of-detail rendering | Manual | Open graph with 1000+ nodes, verify only viewport labels rendered at full detail | Not Started | Not Written |
| PERF-UW-001 | Document over 100MB opens in read-only streaming mode with warning | Integration | Attempt to open a 100MB+ file, verify read-only mode and large file warning | Not Started | Not Written |
| PERF-UW-002 | Embedding build processes in batches of 100 if memory exceeded | Integration | Build embeddings with constrained memory, verify batch processing at 100-doc increments | Not Started | Not Written |

---

## 13. Security & Privacy

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| SEC-UB-001 | All data stored locally within vault, no external transmission | Inspection | Review network calls, verify no data sent externally without user configuration | Complete | Not Written |
| SEC-UB-002 | Secret/password/token/key fields excluded from index and MCP | Unit | Index document with `secret` field, verify field absent from search index and MCP responses | Partial | Not Written |
| SEC-EV-001 | Vault encryption with AES-256 using user passphrase | Integration | Enable encryption, write document, verify file on disk is encrypted with AES-256 | Partial | Not Written |
| SEC-EV-002 | Lock vault clears decrypted content from memory | Integration | Lock vault, inspect memory, verify no decrypted content remains accessible | Not Started | Not Written |
| SEC-EV-003 | MCP server validates client authorization before executing tool | Integration | Send MCP tool invocation without auth, verify request rejected | Partial | Not Written |
| SEC-ST-001 | Locked vault rejects all read/write and shows only unlock prompt | Manual | Lock vault, attempt operations, verify all rejected and only unlock prompt visible | Not Started | Not Written |
| SEC-UW-001 | .env and credential files excluded from search and MCP | Integration | Add `.env` to vault, run search and MCP list, verify file excluded from both | Partial | Not Written |
| SEC-UW-002 | Path traversal in MCP rejected with security warning log | Integration | Send MCP request with `../../../etc/passwd`, verify rejection and logged warning | Partial | Not Written |

---

## 14. Error Handling & Recovery

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| ERR-UB-001 | Crash recovery journal in `.2ndbrain/recovery/` with unsaved states | Unit | Verify CrashJournal writes snapshots to `.2ndbrain/recovery/` directory | Complete | Not Written |
| ERR-UB-002 | Error logging to `.2ndbrain/logs/` with timestamps and stack traces | Unit | Trigger an error, verify log entry in `.2ndbrain/logs/` with timestamp and trace | Complete | Not Written |
| ERR-EV-001 | Post-crash launch shows recovery dialog with unsaved documents | Manual | Simulate crash with unsaved docs, relaunch, verify recovery dialog listing documents | Complete | Not Written |
| ERR-EV-002 | Recover restores document to last journaled state | Manual | Select Recover on a crash-recovery entry, verify document restored to journaled content | Complete | Not Written |
| ERR-EV-003 | Failed plugin load disables plugin and continues launching | Integration | Provide a broken plugin, launch editor, verify plugin disabled and app continues | Not Started | Not Written |
| ERR-ST-001 | Repeated background failures pause operation with retry notification | Manual | Simulate repeated indexing failures, verify operation paused with retry/diagnostic options | Not Started | Not Written |
| ERR-UW-001 | Failed save retains content in recovery journal with error message | Integration | Simulate filesystem error on save, verify content saved to recovery journal and error shown | Partial | Not Written |
| ERR-UW-002 | Conflicting edits present diff-based merge conflict dialog | Manual | Edit file in editor and externally simultaneously, verify merge conflict dialog appears | Not Started | Not Written |
| ERR-UW-003 | Corrupt frontmatter write-back restores from recovery journal | Integration | Simulate frontmatter corruption during write, verify pre-write backup restored | Not Started | Not Written |
| ERR-UW-004 | Exhausted undo disables control and shows "Nothing to undo" | Manual | Undo all actions until history empty, verify undo disabled and status bar message shown | Partial | Not Written |

---

## 15. Obsidian Vault Conversion

| Req ID | Description | Test Type | Test Description | Impl | Test |
|--------|-------------|-----------|------------------|------|------|
| OBS-EV-001 | Import Obsidian vault: scan, add UUIDs, normalize tags, init .2ndbrain, build index | Integration | Import an Obsidian vault, verify UUIDs added, #tags moved to frontmatter, .2ndbrain/ initialized, index built | Complete | Not Written |
| OBS-EV-002 | Export to Obsidian: copy files, create .obsidian/, convert UUID wikilinks to filename-based | Integration | Export vault, verify .obsidian/ created, wikilinks converted, optional frontmatter stripping | Complete | Not Written |
| OBS-EV-003 | Import preserves existing frontmatter fields not in 2ndbrain schema | Integration | Import vault with custom frontmatter fields, verify extra fields preserved after import | Complete | Not Written |
| OBS-EV-004 | Import maps Obsidian `aliases` to wikilink alias resolution | Integration | Import vault with aliases frontmatter, verify aliases resolve during wikilink indexing | Partial | Not Written |
| OBS-EV-005 | Import preserves `.canvas` files without modification | Integration | Import vault with .canvas files, verify files copied verbatim without changes | Partial | Not Written |
| OBS-EV-006 | `2nb import-obsidian <path>` performs GUI-equivalent import via CLI | Integration | Run CLI import, verify same results as GUI: UUIDs, tags, .2ndbrain/, index | Complete | Not Written |
| OBS-EV-007 | `2nb export-obsidian <path>` with optional `--strip-ids` flag | Integration | Run CLI export with and without --strip-ids, verify correct frontmatter handling | Complete | Not Written |
| OBS-ST-001 | Import shows progress indicator with documents processed count | Manual | Import a large vault, verify progress indicator shows N of M documents processed | Partial | Not Written |
| OBS-UW-001 | Conflicting filenames resolved via shortest unique path | Integration | Import vault with duplicate filenames in subdirectories, verify shortest-path resolution and logged ambiguities | Partial | Not Written |
| OBS-UW-002 | Obsidian-specific syntax preserved raw with unsupported features logged | Integration | Import vault with `^block-id` and `![[embed]]`, verify raw syntax preserved and logged | Partial | Not Written |
| OBS-UW-003 | YAML parse error skips document, logs error, continues processing | Integration | Import vault with one broken-YAML document, verify it is skipped and others process successfully | Complete | Not Written |

---

## Summary

| Area | Total | Complete | Partial | Not Started | Tests Written |
|------|-------|----------|---------|-------------|---------------|
| Document Management | 15 | 5 | 6 | 4 | 0 |
| Editor Core | 17 | 13 | 3 | 1 | 0 |
| Markdown Rendering | 12 | 7 | 3 | 2 | 0 |
| File System & Storage | 12 | 7 | 2 | 3 | 0 |
| Search & Discovery | 19 | 9 | 4 | 6 | 0 |
| AI Integration | 25 | 12 | 3 | 10 | 0 |
| CLI Interface | 20 | 20 | 0 | 0 | 0 |
| Knowledge Graph & Linking | 15 | 9 | 6 | 0 | 0 |
| Frontmatter & Metadata | 12 | 10 | 2 | 0 | 0 |
| User Interface | 15 | 9 | 5 | 1 | 0 |
| macOS Platform Integration | 10 | 2 | 2 | 6 | 0 |
| Performance | 10 | 2 | 5 | 3 | 0 |
| Security & Privacy | 8 | 1 | 5 | 2 | 0 |
| Error Handling & Recovery | 10 | 4 | 2 | 4 | 0 |
| Obsidian Vault Conversion | 11 | 6 | 5 | 0 | 0 |
| **Total** | **211** | **116** | **53** | **42** | **0** |

### Implementation Coverage

- **Complete**: 55% (116 of 211 requirements fully implemented)
- **Partial**: 25% (53 of 211 requirements partially implemented)
- **Not Started**: 20% (42 of 211 requirements not yet started)
- **Tests Written**: 0% (no formal test cases written yet)

### Priority Areas for Test Development

1. **CLI Interface** -- 20 complete requirements with 0 tests; highest ROI for automated integration testing
2. **Editor Core** -- 13 complete requirements covering undo/redo, formatting, list editing, autocomplete
3. **AI Integration (MCP tools)** -- 12 complete requirements including all 8 MCP tools ready for integration testing
4. **Frontmatter & Metadata** -- 10 complete requirements covering schema validation, parsing, and state machines
5. **Knowledge Graph & Linking** -- 9 complete requirements covering wikilink parsing and graph traversal

### Not Started Features (by area)

- **Search**: Vector embeddings, semantic search, hybrid RRF, fallback behavior
- **AI**: GGUF model loading, incremental embedding, LLM autocomplete, RAG Q&A, MCP status panel
- **Editor**: Vim mode
- **Rendering**: Mermaid diagrams, LaTeX math
- **Platform**: Quick Look extension, notarization, menu bar search, global hotkey, Touch ID, Handoff
- **Storage**: Autosave timer, low disk warning, file move conflict resolution
- **Performance**: Large file streaming mode, embedding batch processing, graph LOD rendering
- **Security**: Vault lock/unlock flow
- **Error Handling**: Plugin failure handling, merge conflict dialog, frontmatter corruption recovery

### Notes

- The reqs.md file defines 211 requirements. Two IDs are skipped in the numbering sequence (EDT-EV-010 and CLI-EV-013 do not exist), but the total count of defined requirements is 211.
- All CLI commands (`init`, `create`, `read`, `meta`, `index`, `search`, `lint`, `stale`, `related`, `graph`, `export-context`, `list`, `delete`, `mcp-server`, `import-obsidian`, `export-obsidian`) are implemented.
- All 8 MCP tools (`kb_search`, `kb_read`, `kb_related`, `kb_create`, `kb_update_meta`, `kb_structure`, `kb_delete`, `kb_list`) are implemented.
- GUI features present: tabs, editor, preview, search panel, quick-open, command palette, sidebar, graph view, backlinks, status bar, Spotlight indexing, crash recovery, file watcher, focus mode, vault creation, document deletion, frontmatter editing via properties panel, templates, index rebuild, Obsidian import/export.
