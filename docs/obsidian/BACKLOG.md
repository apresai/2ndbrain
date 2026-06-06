# 0.5.0 Backlog — deferred review findings

MEDIUM/LOW findings from the chad-review of the 0.5.0 Obsidian-compliance work. CRITICAL/HIGH were fixed before merge; these are follow-ups that don't block the PR.

## Swift app (dead code from the editor → dashboard pivot)
- **MEDIUM — Crash-recovery dialog is orphaned.** `AppState.showRecoveryDialog` is still set (on vault open with leftover recovery entries, and on frontmatter parse failure), but ContentView no longer renders the recovery alert. Either remove the recovery machinery (`recoveryEntries`, `dismissRecovery`, `recoverDocument`) now that the app doesn't edit documents, or re-add a minimal recovery surface.
- **MEDIUM — `MergeConflictView` + `DiffView` are an unreachable island.** The merge-conflict dialog was presented by the deleted editor; `DiffView` is now used only by `MergeConflictView`. Remove both (and the AppState merge-controller wiring) unless a dashboard surface will present them.
- **MEDIUM — Dead editor-state cluster in `AppState`.** `openDocument`, `jumpToHeading`, `editorScrollTarget`, `pendingFirstResponder`, `updateOutline`, `activeTabIndex`, `openDocuments` mutate each other but nothing renders them. Prune.
- **LOW — Stale `AppDelegate` File→Notes rename** still re-applies on every menu-tracking/activation, but there's no Notes editing surface anymore.
- **LOW — Stale comments** referencing `EditorArea` (AppState.swift) and `PolishView` (DiffView.swift).
- **LOW — `MCPStatusView` hides its only in-pane action** ("Connect AI Tools…") when inline; still reachable via the AI menu.

## Obsidian plugin
- **MEDIUM — `runCommand` is untested.** The vitest suite covers the pure functions but not the ENOENT/timeout error mapping or the chip render path (`openLinkText`). Add tests with `execFile` mocked to error/timeout.
- **LOW — Search-error `Notice` spam:** every failing debounced keystroke fires a new Notice. Debounce/dedupe error notices.
- **LOW — Deprecated `MarkdownRenderer.renderMarkdown`** → `MarkdownRenderer.render`; pass a real `sourcePath` so relative links in answers resolve.
- **LOW — `.npmrc legacy-peer-deps=true` is install-global.** Prefer a scoped `overrides`/`peerDependencyRules` so it can't silently swallow a future real peer conflict.

## Go CLI
- **MEDIUM — Coverage:** `applyMigration` half-applied path (the `isDup` tolerance branch) and the `indexFile` "reuse existing surrogate ID by path" branch (re-index ID stability) are unasserted.
- **LOW — `indexFile` ID lookup** treats any DB error like "not found" and mints a new UUID; distinguish `sql.ErrNoRows` from real errors.
- **LOW — `migrate.go`** ignores `Scan` errors on the raw version/count read (`_ = ...Scan`); an unreadable `schema_version` prints "schema v0" in dry-run. Surface it.
- **LOW — `StripComments`** still blanks a `%%` that appears inside a code fence (documented v1 limitation); make it fence-aware if it ever bites.
- **LOW — `ResolveLinks`** now resolves a name-vs-title collision to the name match (old code left it unresolved). Arguably more Obsidian-correct; pin the intended behavior with a test.

## Docs
- **LOW — `reqs.md OBN-EV-005`** wording ("canvas link connections … in the links database") reads as if edges become link rows; only `file`-node `[[wikilinks]]` do. Tighten wording to match `canvas-and-bases.md`.

## Provider-simplification + plugin-binary follow-ups (deferred)
- **Gemini provider** — explicitly out of scope for now (decided: don't add a provider that isn't already present). Revisit if a non-AWS, single-API-key default is wanted.
- **Cross-platform CLI builds** — the release is macOS-only (darwin amd64/arm64), so the plugin's "Download CLI" is macOS-only. Add Windows/Linux GoReleaser builds to extend it (note: CGO + sqlite/fts5 needs care on those targets).
- **macOS notarization** — not done (no Apple Developer account in CI). The plugin ad-hoc signs + strips quarantine, which works for an exec'd CLI; notarizing the release would remove even that step. Forks/local builds are unaffected.
- **Community Plugin store submission** — currently BRAT / manual install. Store review scrutinizes binary-downloading plugins; keep the download consent-gated.
- **Auto-index / watch** — the plugin still requires a manual "Index now" / Rebuild; a debounced incremental reindex on vault changes would remove that friction.
- **Wizard `runCommand` / downloadCli coverage** — `downloadCli` (requestUrl + tar + codesign + xattr) is exercised manually, not unit-tested; only `resolveCliPath`'s managed-binary branch is covered.
- **Plugin download integrity (LOW)** — `downloadCli` fetches + ad-hoc-signs + de-quarantines the binary without verifying it against the release `checksums.txt`. The URL is hardcoded HTTPS to the official repo (no MITM/arbitrary-host risk) and a same-origin checksums file wouldn't defend against the only realistic threat (release/account compromise), so this is defense-in-depth, not an exploitable bug. Could add sha256 verification if a signed checksum source becomes available.
