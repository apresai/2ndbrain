import {
	App,
	FileSystemAdapter,
	ItemView,
	MarkdownRenderer,
	MarkdownView,
	Modal,
	Notice,
	Plugin,
	PluginSettingTab,
	Setting,
	SuggestModal,
	WorkspaceLeaf,
	addIcon,
	requestUrl
} from 'obsidian';
import type { Editor, Menu, TFile } from 'obsidian';
import { execFile } from 'child_process';
import { existsSync, mkdirSync, writeFileSync, chmodSync, rmSync } from 'fs';
import { join } from 'path';
import process from 'process';

interface BrainSettings {
	cliPath: string;
	firstRunComplete: boolean;
}

const DEFAULT_SETTINGS: BrainSettings = {
	cliPath: '2nb',
	firstRunComplete: false,
};

// View type for the vault-chat sidebar panel.
const VIEW_TYPE_CHAT = '2ndbrain-chat';

// Custom ribbon/tab icon: a head in right-facing profile with a brain inside,
// a monochrome stroke rendition of the SecondBrain app icon
// (app/Resources/AppIcon-1024.png). Inner SVG content for Obsidian's
// addIcon() 0-100 viewBox; currentColor so it follows the theme.
const BRAIN_HEAD_ICON = `<g stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke-width="7" d="M34 92 V70 C22 63 15 50 16 38 C18 21 32 9 49 9 C66 9 79 21 80 37 L88 52 C89 54.5 87.5 57 85 57 H80 V63 C80 69 75 73 69 73 H64 V92"/><path stroke-width="6" d="M35 47 C29 45 26 39 29 34 C27 27 32 21 39 21 C43 16 51 16 55 20 C61 19 66 24 65 30 C68 35 66 42 61 44 C59 48 52 50 48 47 C44 51 38 50 35 47"/><path stroke-width="5" d="M44 21 C45 26 41 29 42 33"/><path stroke-width="5" d="M55 25 C52 28 54 32 50 35"/><path stroke-width="5" d="M32 36 C36 36 39 40 37 44"/></g>`;

// Polish icon: a sparkle/star, the conventional "clean up / enhance" glyph.
// Registered via addIcon for the Polish button across the ribbon, the note
// header toolbar, and the right-click menu. currentColor follows the theme.
const POLISH_ICON = `<g stroke="currentColor" fill="none" stroke-linecap="round" stroke-linejoin="round"><path stroke-width="7" d="M50 14 C53 34 56 44 86 50 C56 56 53 66 50 86 C47 66 44 56 14 50 C44 44 47 34 50 14 Z"/><path stroke-width="4" d="M79 16 C80 24 81 27 90 29 C81 31 80 34 79 42 C78 34 77 31 68 29 C77 27 78 24 79 16 Z"/></g>`;

// AIStatus is the subset of `2nb ai status --json` the plugin reads.
interface AIStatus {
	provider?: string;
	embedding_model?: string;
	embed_available?: boolean;
	gen_available?: boolean;
	document_count?: number;
	embedding_count?: number;
	providers?: { name: string; reachable: boolean; reason?: string }[];
	// portability_status carries the vault index health, incl. the
	// upgrade_reindex_recommended / upgrade_reembed_recommended states a newer
	// 2nb sets when it changed indexing/embedding logic (see the reindex nudge).
	portability_status?: string;
	portability_action?: string;
}

// InstallStatus mirrors one entry of `2nb skills list --json` (the Go
// skills.InstallStatus struct). Used to check whether the Claude Code skill
// is installed for this user/project.
interface InstallStatus {
	slug: string;
	name: string;
	project_path?: string;
	user_path?: string;
	project_installed: boolean;
	user_installed: boolean;
	note?: string;
}

// ConfiguredStatus mirrors one entry of `2nb mcp configured --json` (the Go
// mcp.ConfiguredStatus struct). Reports whether the 2ndbrain MCP server is
// wired into the AI client config (durable), as opposed to running right now.
interface ConfiguredStatus {
	client: string;
	config_path: string;
	configured: boolean;
	scope?: string;
	server_key?: string;
	vault_path: string;
}

// GlobalInstructionsStatus mirrors one entry of `2nb instructions configured
// --all --json` (the Go instructions.Status). Reports whether the always-loaded
// 2nb reference block is present in the client's global agent memory file
// (~/.claude/CLAUDE.md). Only clients with a known memory file appear.
interface GlobalInstructionsStatus {
	client: string;
	file_path: string;
	installed: boolean;
	up_to_date: boolean;
	modified: boolean;
	installed_version?: string;
}

// ClientDef describes one AI client the plugin can set up (skill where
// applicable + MCP server). `key` is the `--client` value the CLI understands
// (`2nb setup --client <key>`, `2nb mcp configured --client <key>`); `skillSlug`
// is the `2nb skills` slug the client uses, omitted for MCP-only clients
// (Claude Desktop shares Claude Code's ~/.claude/skills folder). `note` is an
// extra one-line caveat shown under the MCP row; `absoluteCliPath` marks a GUI
// client whose minimal PATH means its MCP config must use the absolute 2nb path.
export interface ClientDef {
	key: string;
	name: string;
	skillSlug?: string;
	note?: string;
	absoluteCliPath?: boolean;
	// True for a client with a known global memory file (~/.claude/CLAUDE.md),
	// where `2nb setup` also installs the always-loaded 2nb reference block.
	globalInstructions?: boolean;
}

// MCP_CLIENTS is the set of AI clients the settings UI offers a per-client
// Configure row for. Mirrors the CLI's mcp.SupportedClients() that ship a
// plugin-relevant setup path (the cross-tool `.agents` target is CLI-only).
export const MCP_CLIENTS: ClientDef[] = [
	{ key: 'claude-code', name: 'Claude Code', skillSlug: 'claude-code', globalInstructions: true },
	{ key: 'warp', name: 'Warp', skillSlug: 'warp' },
	{
		key: 'claude-desktop',
		name: 'Claude Desktop',
		absoluteCliPath: true,
		globalInstructions: true,
		note: 'MCP only (Claude Desktop shares Claude Code’s skill). After configuring, quit and reopen Claude Desktop.',
	},
	{ key: 'codex', name: 'Codex', skillSlug: 'codex', globalInstructions: true, note: 'MCP registered via `codex mcp add`.' },
];

// ProductState mirrors one component of `2nb doctor --json` (the Go
// ProductState struct): a single product's install + version-parity state.
// status is one of: ok | outdated | missing | unknown | n/a.
interface ProductState {
	name: string;
	status: string;
	installed: boolean;
	version?: string;
	update_available: boolean;
	fix?: string;
}

// SuiteStatus mirrors `2nb doctor --json` (the Go SuiteStatus struct): the CLI,
// macOS app, and Obsidian plugin against the latest published release.
interface SuiteStatus {
	latest?: string;
	checked: boolean;
	detail?: string;
	in_sync: boolean;
	cli: ProductState;
	app: ProductState;
	plugin: ProductState;
}

// parseVersion extracts an x.y.z triple from a string like "2nb version 0.10.5".
// Returns null when there is no semver triple (e.g. a "dev" build), so callers
// treat the version as unknown rather than guessing.
export function parseVersion(s: string): string | null {
	const m = /(\d+)\.(\d+)\.(\d+)/.exec(s ?? '');
	return m ? `${m[1]}.${m[2]}.${m[3]}` : null;
}

// compareVersions compares two x.y.z strings: -1 if a<b, 0 if equal, 1 if a>b.
// Missing or non-numeric components count as 0, so a stray prefix ("v1.2.3") or
// junk never yields a NaN comparison (NaN > / < are both false, which would
// silently read as "equal").
export function compareVersions(a: string, b: string): number {
	const part = (s: string, i: number): number => {
		const n = Number(s.split('.')[i]);
		return Number.isFinite(n) ? n : 0;
	};
	for (let i = 0; i < 3; i++) {
		const x = part(a, i);
		const y = part(b, i);
		if (x > y) return 1;
		if (x < y) return -1;
	}
	return 0;
}

// needsManagedRefresh decides whether a plugin-managed 2nb copy should be
// re-downloaded: when a locally-installed system binary OR the latest published
// release is strictly newer than the managed copy. Pure (no network/fs), so the
// self-heal trigger is unit-tested. Decoupled from the plugin's own version — a
// plugin that is ahead of the latest CLI release does NOT keep re-downloading.
export function needsManagedRefresh(
	managed: string,
	system: string | null,
	latest: string | null
): boolean {
	if (system && compareVersions(system, managed) > 0) return true;
	if (latest && compareVersions(latest, managed) > 0) return true;
	return false;
}

// firstExistingSystem returns the first system-installed 2nb — Homebrew (ARM
// then Intel), then ~/go/bin for source builds — or null if none is present.
export function firstExistingSystem(
	existsFn: (p: string) => boolean,
	env: NodeJS.ProcessEnv
): string | null {
	const brewArm = '/opt/homebrew/bin/2nb';
	if (existsFn(brewArm)) return brewArm;

	const brewIntel = '/usr/local/bin/2nb';
	if (existsFn(brewIntel)) return brewIntel;

	const home = env.HOME || env.USERPROFILE;
	if (home) {
		const goBin = `${home}/go/bin/2nb`;
		if (existsFn(goBin)) return goBin;
	}
	return null;
}

// resolveCliPath resolves the 2nb binary path. Pure free function: it takes its
// filesystem probe (existsFn), environment (env), and (optionally) the parsed
// versions of the managed and system binaries, so it can be unit-tested without
// touching the real disk. GUI apps don't always inherit the shell PATH, so when
// the path is left at the default ('2nb') we probe the standard install
// locations.
//
// Selection: an explicit user path is honored verbatim. Otherwise a
// plugin-managed copy is preferred UNLESS a system binary is STRICTLY newer (by
// `versions`) — that is what stops a stale managed download from silently
// shadowing a fresh `brew upgrade`. A tie, an unknown version, or no version
// info at all (offline, or the legacy call that omits `versions`) keeps the
// managed copy, so users without Homebrew and users offline are never left
// worse off than before.
export function resolveCliPath(
	configured: string,
	existsFn: (p: string) => boolean,
	env: NodeJS.ProcessEnv,
	managedPath?: string,
	versions?: { managed?: string | null; system?: string | null }
): string {
	if (configured !== '2nb') {
		return configured;
	}

	const system = firstExistingSystem(existsFn, env);

	if (managedPath && existsFn(managedPath)) {
		if (
			system &&
			versions?.managed &&
			versions?.system &&
			compareVersions(versions.system, versions.managed) > 0
		) {
			return system; // a strictly newer system binary wins over a stale managed copy
		}
		return managedPath;
	}

	return system ?? '2nb';
}

// pinVaultArgs prefixes a 2nb invocation with `--vault <path>` — the CLI's
// highest-priority vault source (root.go). The plugin ALWAYS pins commands to
// the open Obsidian vault's path so 2nb can never resolve a different vault from
// the Obsidian registry or the process cwd. The Obsidian vault and the 2nb vault
// stay joined at the hip.
export function pinVaultArgs(vaultPath: string, args: string[]): string[] {
	return ['--vault', vaultPath, ...args];
}

// formatIndexState turns embedding coverage into a plain-language verdict for
// the settings / wizard UI: is this vault embedded, partially embedded, not
// indexed yet, or empty?
export function formatIndexState(documentCount: number, embeddingCount: number): string {
	if (documentCount <= 0) return 'empty vault — no documents yet';
	if (embeddingCount <= 0) return `not indexed yet (${documentCount} documents) — run "Rebuild AI Index"`;
	if (embeddingCount < documentCount) return `partially embedded (${embeddingCount} / ${documentCount} documents)`;
	return `embedded (${embeddingCount} / ${documentCount} documents)`;
}

// describeComponent renders one component's settings-row description from its
// `2nb doctor` ProductState, using the same status vocabulary the CLI prints.
export function describeComponent(p: ProductState, latest?: string): string {
	// Only mention "latest" when something newer actually exists (status
	// outdated). When up to date, the CLI clamps latest so it's never below the
	// installed version, so "(latest X)" would be redundant ("up to date (latest
	// = same)") or — before the clamp — the contradictory "up to date (latest <X)".
	const suffix = latest ? ` (latest ${latest})` : '';
	switch (p.status) {
		case 'ok':
			return `v${p.version} — up to date`;
		case 'outdated':
			return `v${p.version} — update available${suffix}. Fix: ${p.fix}`;
		case 'missing':
			return `not installed. Fix: ${p.fix}`;
		case 'n/a':
			return 'macOS app only';
		case 'unknown':
			return p.version ? `v${p.version} — version not comparable` : (p.fix || 'unknown');
		default:
			return p.status;
	}
}

// filepathBase returns the final path segment of a vault-relative path.
export function filepathBase(path: string): string {
	const parts = path.split('/');
	return parts[parts.length - 1];
}

// shellQuote POSIX single-quotes a string so it survives copy-paste into a shell
// verbatim, even when it contains spaces or shell metacharacters. Single quotes
// disable all interpretation; an embedded single quote is handled the POSIX way
// by closing the quote, adding an escaped quote, and reopening: ' -> '\''.
export function shellQuote(s: string): string {
	return `'${s.replace(/'/g, `'\\''`)}'`;
}

// mcpSnippetFor renders the manual MCP-setup snippet for a client — the "Copy
// setup snippet" fallback for when the one-click Configure (which shells
// `2nb setup --client <key>`) isn't an option. Pure and exported for tests.
//
//   - claude-code:    ~/.claude.json mcpServers JSON, the legacy shape (bare
//                     "2nb" on PATH, vault via cwd).
//   - claude-desktop: same mcpServers JSON, but Claude Desktop is a GUI app with
//                     a minimal PATH, so it uses the absolute cliPath and pins
//                     the vault via --vault (it supports no cwd/working_directory).
//   - warp:           ~/.warp/.mcp.json shape, vault pinned via both --vault and
//                     working_directory.
//   - codex:          a `codex mcp add` command line (Codex registers MCP servers
//                     via its own CLI, not a config file). The cliPath and
//                     vaultPath are shell-quoted so a path with spaces (an iCloud
//                     "Mobile Documents" vault, "My Vault", …) stays copy-paste
//                     safe. The JSON variants are already safe via JSON.stringify.
export function mcpSnippetFor(client: string, vaultPath: string, cliPath: string): string {
	switch (client) {
		case 'codex':
			return `codex mcp add 2ndbrain -- ${shellQuote(cliPath)} mcp-server --vault ${shellQuote(vaultPath)}`;
		case 'warp':
			return JSON.stringify({
				mcpServers: {
					'2ndbrain': {
						command: cliPath,
						args: ['mcp-server', '--vault', vaultPath],
						working_directory: vaultPath,
					},
				},
			}, null, 2);
		case 'claude-desktop':
			return JSON.stringify({
				mcpServers: {
					'2ndbrain': {
						command: cliPath,
						args: ['mcp-server', '--vault', vaultPath],
					},
				},
			}, null, 2);
		case 'claude-code':
		default:
			return JSON.stringify({
				mcpServers: {
					'2ndbrain': { command: '2nb', args: ['mcp-server'], cwd: vaultPath },
				},
			}, null, 2);
	}
}

interface SearchResult {
	doc_id: string;
	path: string;
	title: string;
	chunk_id: string;
	heading_path?: string;
	content?: string;
	score?: number;
	vector_score?: number;
	type: string;
	status?: string;
}

interface AskResponse {
	mode: string;
	warnings: string[];
	answer: string;
	// The CLI's `2nb ask --json` emits `sources` as a plain array of
	// vault-relative path strings, not objects. See cli/internal/cli/ask.go
	// (`Sources []string`).
	sources: string[];
}

interface SearchResponse {
	mode: string;
	warnings: string[];
	results: SearchResult[];
}

// parseAskResponse JSON.parses the `2nb ask --json` envelope and returns a
// normalized shape the AskAIModal renders. Isolating the parse/shape logic
// from Obsidian UI keeps it unit-testable. `sources` is always a string[].
export function parseAskResponse(json: string): AskResponse {
	const raw = JSON.parse(json) as Partial<AskResponse>;
	return {
		mode: raw.mode ?? '',
		warnings: Array.isArray(raw.warnings) ? raw.warnings : [],
		answer: raw.answer ?? '',
		sources: Array.isArray(raw.sources) ? raw.sources : [],
	};
}

// parseSearchResponse JSON.parses the `2nb search --json` envelope
// (`{mode, warnings, results}`) and returns the result list in a normalized
// shape the SemanticSearchModal renders.
export function parseSearchResponse(json: string): SearchResponse {
	const raw = JSON.parse(json) as Partial<SearchResponse>;
	return {
		mode: raw.mode ?? '',
		warnings: Array.isArray(raw.warnings) ? raw.warnings : [],
		results: Array.isArray(raw.results) ? raw.results : [],
	};
}

// PolishResult mirrors the CLI's `2nb polish --json` payload (cli/internal/cli
// PolishResult). original/polished are the document BODY only (frontmatter is
// excluded), so the diff is naturally frontmatter-free.
// LinkRepair mirrors the CLI's polish.LinkRepair: a broken [[wikilink]] that was
// rewritten to an existing note (new_target set) or left alone (reason set).
export interface LinkRepair {
	raw: string;
	new_target?: string;
	reason?: string;
}

export interface PolishResult {
	path: string;
	original: string;
	polished: string;
	provider: string;
	model: string;
	links_added?: string[];
	links_repaired?: LinkRepair[];
	links_skipped?: LinkRepair[];
	warning?: string;
}

// parsePolishResponse normalizes the `2nb polish --json` envelope, tolerant of
// extra fields (e.g. duration_ms) and missing ones. Pure and exported for tests.
export function parsePolishResponse(json: string): PolishResult {
	const raw = JSON.parse(json) as Partial<PolishResult>;
	return {
		path: raw.path ?? '',
		original: raw.original ?? '',
		polished: raw.polished ?? '',
		provider: raw.provider ?? '',
		model: raw.model ?? '',
		links_added: Array.isArray(raw.links_added) ? raw.links_added : [],
		links_repaired: Array.isArray(raw.links_repaired) ? raw.links_repaired : [],
		links_skipped: Array.isArray(raw.links_skipped) ? raw.links_skipped : [],
		warning: raw.warning ?? '',
	};
}

// DiffRow is one line of a unified diff: unchanged context, an addition, or a
// deletion, paired with the line text. Produced by computeLineDiff.
export type DiffRow = { type: 'context' | 'add' | 'del'; text: string };

// computeLineDiff produces a unified line diff (LCS) between a and b. Note-sized
// bodies make the O(n*m) table cheap; past a sane size it falls back to a plain
// before/after block so a pathological input can't freeze the renderer. Pure
// and exported for tests. Invariant: rows filtered to context+del rejoin to a,
// and context+add rejoin to b.
export function computeLineDiff(a: string, b: string): DiffRow[] {
	const o = a.split('\n');
	const p = b.split('\n');
	const n = o.length;
	const m = p.length;
	if (n * m > 4_000_000) {
		return [
			...o.map((t): DiffRow => ({ type: 'del', text: t })),
			...p.map((t): DiffRow => ({ type: 'add', text: t })),
		];
	}
	const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
	for (let i = n - 1; i >= 0; i--) {
		for (let j = m - 1; j >= 0; j--) {
			dp[i][j] = o[i] === p[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
		}
	}
	const rows: DiffRow[] = [];
	let i = 0;
	let j = 0;
	while (i < n && j < m) {
		if (o[i] === p[j]) {
			rows.push({ type: 'context', text: o[i] });
			i++;
			j++;
		} else if (dp[i + 1][j] >= dp[i][j + 1]) {
			rows.push({ type: 'del', text: o[i++] });
		} else {
			rows.push({ type: 'add', text: p[j++] });
		}
	}
	while (i < n) rows.push({ type: 'del', text: o[i++] });
	while (j < m) rows.push({ type: 'add', text: p[j++] });
	return rows;
}

// ChatTurn mirrors the CLI's ai.ChatTurn: one prior message passed back to
// `2nb ask --history` so follow-up questions carry conversational context.
export interface ChatTurn {
	role: 'user' | 'assistant';
	content: string;
}

// Client-side history caps mirroring the Go engine's (ai.TrimHistory):
// the CLI re-trims authoritatively, this just keeps stdin payloads small.
const MAX_HISTORY_TURNS = 12;
const MAX_HISTORY_TURN_CHARS = 1500;
const MAX_HISTORY_CHARS = 8000;

// trimChatHistory enforces the history caps: most recent turns, each
// truncated (code-point safe), oldest dropped to fit the total budget.
// Pure and exported for unit testing.
export function trimChatHistory(turns: ChatTurn[]): ChatTurn[] {
	let trimmed = turns.slice(-MAX_HISTORY_TURNS).map((t) => {
		const points = Array.from(t.content);
		return points.length > MAX_HISTORY_TURN_CHARS
			? { ...t, content: points.slice(0, MAX_HISTORY_TURN_CHARS).join('') + '...' }
			: t;
	});
	let total = trimmed.reduce((n, t) => n + t.content.length, 0);
	while (trimmed.length > 1 && total > MAX_HISTORY_CHARS) {
		total -= trimmed[0].content.length;
		trimmed = trimmed.slice(1);
	}
	return trimmed;
}

// renderAskResponse renders a parsed ask envelope into container: the CLI's
// degradation warnings (if any), the markdown answer, and deduped source
// chips. Shared by the Ask AI modal and the chat panel so the two surfaces
// can't drift. onSourceOpen runs after a source chip opens its note (the
// modal closes itself; the chat panel stays open).
async function renderAskResponse(
	plugin: BrainPlugin,
	container: HTMLElement,
	response: AskResponse,
	onSourceOpen?: () => void
): Promise<void> {
	// Surface the CLI's loud-degradation channel. Without this, a vault in
	// DIMENSION BREAK / provider-down state silently returns keyword-only
	// answers that look like normal semantic results.
	for (const warning of response.warnings) {
		container.createDiv({ text: warning, cls: 'brain-warning' });
	}

	const answerEl = container.createDiv({ cls: 'brain-answer' });
	await plugin.renderMarkdown(response.answer, answerEl);

	// `sources` is a string[] of vault-relative paths (see parseAskResponse /
	// cli/internal/cli/ask.go), so dedupe on the path string itself and derive
	// the label from it.
	if (response.sources && response.sources.length > 0) {
		const sourcesContainer = container.createDiv({ cls: 'brain-sources-container' });
		sourcesContainer.createEl('div', { text: 'Sources', cls: 'brain-sources-title' });
		const sourcesList = sourcesContainer.createEl('ul', { cls: 'brain-sources-list' });

		const seenPaths = new Set<string>();
		response.sources.forEach((source) => {
			if (seenPaths.has(source)) return;
			seenPaths.add(source);

			const li = sourcesList.createEl('li');
			const link = li.createEl('a', { cls: 'brain-source-chip', href: '#' });
			link.createSpan({ text: '📄', cls: 'brain-source-icon' });
			link.createSpan({ text: filepathBase(source) });

			link.addEventListener('click', (e) => {
				e.preventDefault();
				plugin.app.workspace.openLinkText(source, '', false);
				onSourceOpen?.();
			});
		});
	}
}

export default class BrainPlugin extends Plugin {
	settings!: BrainSettings;

	// Resolved 2nb binary path + the parsed versions behind the choice, filled
	// once by ensureCliFresh() at load. runCommand reads the cache via
	// resolvedCliPath(); until it's filled, resolution falls back to the sync
	// probe (no version info), which matches the pre-existing behavior.
	private resolvedCli?: string;
	private cliVersions?: { managed?: string | null; system?: string | null };
	private cliFreshChecked = false;
	statusBarEl!: HTMLElement;
	// Single-flight lock so the command, ribbon, header action, and right-click
	// menu can't launch overlapping polishes.
	private polishing = false;
	// Dedup: each markdown view gets at most one header Polish action.
	private polishViews = new WeakSet<MarkdownView>();

	async onload() {
		await this.loadSettings();

		// Keep the managed 2nb from shadowing a newer install (and self-heal a
		// stale one). Fire-and-forget so it never delays the UI; runCommand falls
		// back to the sync resolve until this fills the cache.
		void this.ensureCliFresh();

		// Add status bar item
		this.statusBarEl = this.addStatusBarItem();
		this.statusBarEl.setText('2ndbrain: Ready');

		// Chat panel: custom icon (the app's head-with-brain mark), the view
		// itself, and a ribbon button that toggles it open/closed.
		addIcon('2ndbrain-head', BRAIN_HEAD_ICON);
		this.registerView(VIEW_TYPE_CHAT, (leaf) => new ChatView(leaf, this));
		this.addRibbonIcon('2ndbrain-head', '2ndbrain: chat with your vault', () => {
			void this.toggleChatView();
		});

		// Add commands
		this.addCommand({
			id: 'chat',
			name: 'Open chat',
			callback: () => {
				void this.toggleChatView();
			}
		});

		this.addCommand({
			id: 'semantic-search',
			name: 'Semantic Search',
			callback: () => {
				new SemanticSearchModal(this.app, this).open();
			}
		});

		this.addCommand({
			id: 'ask-ai',
			name: 'Ask AI (RAG Q&A)',
			callback: () => {
				new AskAIModal(this.app, this).open();
			}
		});

		this.addCommand({
			id: 'find-similar',
			name: 'Find Similar Notes',
			checkCallback: (checking: boolean) => {
				const activeFile = this.app.workspace.getActiveFile();
				if (activeFile) {
					if (!checking) {
						new SemanticSearchModal(this.app, this, activeFile.basename).open();
					}
					return true;
				}
				return false;
			}
		});

		this.addCommand({
			id: 'rebuild-index',
			name: 'Rebuild AI Index',
			callback: async () => {
				new Notice("Rebuilding 2ndbrain index...");
				this.statusBarEl.setText('2ndbrain: Indexing...');
				try {
					await this.runCommand(['index']);
					new Notice("2ndbrain index rebuilt successfully!");
					this.statusBarEl.setText('2ndbrain: Indexed');
					setTimeout(() => {
						this.statusBarEl.setText('2ndbrain: Ready');
					}, 3000);
				} catch (err) {
					new Notice(`Index rebuild failed: ${(err as Error).message}`);
					this.statusBarEl.setText('2ndbrain: Index Failed');
					setTimeout(() => {
						this.statusBarEl.setText('2ndbrain: Ready');
					}, 3000);
				}
			}
		});

		// Setup wizard: install CLI → connect AI → index.
		this.addCommand({
			id: 'setup-wizard',
			name: 'Setup wizard',
			callback: () => new SetupWizardModal(this.app, this).open(),
		});

		// Polish: clean up the active note and add grounded links, then show a
		// diff with Undo. Exposed on every surface a user might reach for, since
		// it acts on the open note: command/hotkey, the note's header toolbar,
		// the left ribbon, and the right-click editor menu.
		addIcon('2ndbrain-polish', POLISH_ICON);

		this.addCommand({
			id: 'polish-current-note',
			name: 'Polish current note',
			checkCallback: (checking: boolean) => {
				const file = this.app.workspace.getActiveFile();
				const ok = !!file && file.extension === 'md' && !this.polishing;
				if (ok && !checking) void this.polishNote(file!);
				return ok;
			},
		});

		this.addRibbonIcon('2ndbrain-polish', '2ndbrain: polish current note', () => {
			const file = this.app.workspace.getActiveFile();
			if (file) void this.polishNote(file);
			else new Notice('2ndbrain: open a note to polish it.');
		});

		// Header toolbar action on every markdown pane. Obsidian has no "add an
		// action to all views" API, so attach to the active view as panes change
		// (deduped via polishViews); each element is torn down on unload.
		const attachPolishAction = () => {
			const view = this.app.workspace.getActiveViewOfType(MarkdownView);
			if (view) this.ensurePolishAction(view);
		};
		this.registerEvent(this.app.workspace.on('active-leaf-change', attachPolishAction));
		this.registerEvent(this.app.workspace.on('file-open', attachPolishAction));
		this.app.workspace.onLayoutReady(attachPolishAction);

		this.registerEvent(this.app.workspace.on('editor-menu', (menu: Menu, _editor: Editor, view) => {
			if (view instanceof MarkdownView && view.file && view.file.extension === 'md') {
				const file = view.file;
				menu.addItem((item) => item
					.setTitle('2ndbrain: Polish note')
					.setIcon('2ndbrain-polish')
					.onClick(() => void this.polishNote(file)));
			}
		}));

		// Add setting tab
		this.addSettingTab(new BrainSettingTab(this.app, this));

		// Open the wizard once, on first run, after the workspace is ready.
		if (!this.settings.firstRunComplete) {
			this.app.workspace.onLayoutReady(() => {
				new SetupWizardModal(this.app, this).open();
			});
		}
	}

	// toggleChatView opens the chat panel in the right sidebar, or closes it
	// if it's already open: the ribbon icon is both the launcher and the
	// kill switch. (The view's own tab close button also works.)
	async toggleChatView() {
		const existing = this.app.workspace.getLeavesOfType(VIEW_TYPE_CHAT);
		if (existing.length > 0) {
			existing.forEach((leaf) => leaf.detach());
			return;
		}
		const leaf = this.app.workspace.getRightLeaf(false);
		if (!leaf) {
			new Notice('2ndbrain: could not open the chat panel.');
			return;
		}
		await leaf.setViewState({ type: VIEW_TYPE_CHAT, active: true });
		await this.app.workspace.revealLeaf(leaf);
	}

	// ── Polish ───────────────────────────────────────────────────────────────

	// ensurePolishAction adds a one-click Polish button to a markdown view's
	// header toolbar, at most once per view. The element is removed on unload.
	private ensurePolishAction(view: MarkdownView) {
		if (this.polishViews.has(view)) return;
		this.polishViews.add(view);
		const el = view.addAction('2ndbrain-polish', 'Polish this note with 2ndbrain', () => {
			if (view.file) void this.polishNote(view.file);
		});
		this.register(() => el.remove());
	}

	// flushEditor writes the live editor buffer for `file` to disk before the CLI
	// touches it. Without it, Obsidian's pending debounced save could clobber the
	// CLI's external write (or vice-versa), losing either the polish or the user's
	// last keystrokes. The single most important data-safety guard here.
	//
	// It scans ALL markdown panes (not just the active one), because Polish can be
	// triggered on a background pane via the per-note header action, the ribbon, or
	// the right-click menu, where the target file is not the focused view. Edits
	// made DURING the multi-second AI call are not a concern: polishNote opens the
	// result modal first, and an open Obsidian Modal traps focus, so the editor
	// underneath is not editable while the CLI runs.
	private async flushEditor(file: TFile) {
		for (const leaf of this.app.workspace.getLeavesOfType('markdown')) {
			const view = leaf.view;
			if (view instanceof MarkdownView && view.file && view.file.path === file.path && view.editor) {
				await this.app.vault.modify(file, view.editor.getValue());
				return;
			}
		}
	}

	// polishNote runs `2nb polish <path> --write --json --links --repair-links`: it
	// copy-edits the note, repairs broken [[wikilinks]] to existing notes, and
	// weaves in grounded new links in place (snapshotting the original), then shows
	// a diff with Keep/Undo. Single-flight via this.polishing so the four trigger
	// surfaces can't race.
	async polishNote(file: TFile) {
		if (this.polishing) return;
		if (file.extension !== 'md') {
			new Notice('2ndbrain: Polish only works on markdown notes.');
			return;
		}
		this.polishing = true;
		this.statusBarEl.setText('2ndbrain: Polishing…');
		const modal = new PolishResultModal(this.app, this, file);
		modal.open();
		try {
			await this.flushEditor(file);
			const stdout = await this.runCommand(['polish', file.path, '--write', '--json', '--links', '--repair-links']);
			modal.showResult(parsePolishResponse(stdout));
			this.statusBarEl.setText('2ndbrain: Polished');
		} catch (err) {
			modal.close();
			const msg = (err as Error).message;
			if (/unknown flag/.test(msg)) {
				new Notice('Installed 2nb is too old for Polish (needs polish --links/--repair-links). Upgrade: brew upgrade apresai/tap/twonb');
			} else {
				new Notice(`Polish failed: ${msg}`);
			}
			this.statusBarEl.setText('2ndbrain: Polish failed');
		} finally {
			this.polishing = false;
			setTimeout(() => this.statusBarEl.setText('2ndbrain: Ready'), 3000);
		}
	}

	// undoPolish reverts the most recent polish of file via `2nb polish <path>
	// --undo`. Returns true if reverted. If the note was edited after polishing,
	// the CLI refuses without --force; we confirm, then retry forced. The TFile
	// is captured at polish time so undo targets the right note regardless of
	// which pane is active now.
	async undoPolish(file: TFile, force = false): Promise<boolean> {
		try {
			await this.flushEditor(file);
			const args = ['polish', file.path, '--undo'];
			if (force) args.push('--force');
			await this.runCommand(args);
			new Notice('Reverted to the pre-polish version.');
			return true;
		} catch (err) {
			const msg = (err as Error).message;
			if (!force && /changed since/i.test(msg)) {
				const go = await confirmModal(
					this.app,
					'Discard your edits?',
					'You changed this note since polishing. Undo will discard those changes and restore the pre-polish version. Continue?'
				);
				if (go) return this.undoPolish(file, true);
				return false;
			}
			if (/no polish snapshot|nothing to undo/i.test(msg)) {
				new Notice('Nothing to undo for this note.');
				return false;
			}
			new Notice(`Undo failed: ${msg}`);
			return false;
		}
	}

	async loadSettings() {
		this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
	}

	async saveSettings() {
		await this.saveData(this.settings);
	}

	// ── CLI binary management ────────────────────────────────────────────────

	// pluginDir returns the absolute path of this plugin's folder, or null if
	// the vault isn't on a local filesystem (mobile / unknown adapter).
	private pluginDir(): string | null {
		const adapter = this.app.vault.adapter;
		if (!(adapter instanceof FileSystemAdapter) || !this.manifest.dir) return null;
		return join(adapter.getBasePath(), this.manifest.dir);
	}

	// managedBinaryPath is where the plugin keeps a 2nb binary it downloaded.
	managedBinaryPath(): string | null {
		const dir = this.pluginDir();
		return dir ? join(dir, 'bin', '2nb') : null;
	}

	// resolvedCliPath returns the path runCommand will exec: the version-aware
	// choice cached by ensureCliFresh, else a synchronous resolve (which, with no
	// version info yet, matches the pre-existing managed-wins behavior).
	resolvedCliPath(): string {
		return (
			this.resolvedCli ??
			resolveCliPath(
				this.settings.cliPath,
				existsSync,
				process.env,
				this.managedBinaryPath() ?? undefined,
				this.cliVersions
			)
		);
	}

	// binVersion returns the parsed x.y.z version of the 2nb binary at an
	// absolute path, or null if it can't run / parse. Local exec only, so it is
	// offline-safe.
	private binVersion(absPath: string): Promise<string | null> {
		return new Promise((resolve) => {
			execFile(absPath, ['--version'], (err, stdout) => {
				resolve(err ? null : parseVersion(stdout));
			});
		});
	}

	// cliVersion returns the parsed version of the RESOLVED 2nb (the one
	// runCommand uses), or null if it isn't runnable. Used to explain a doctor
	// failure without needing the doctor subcommand itself.
	async cliVersion(): Promise<string | null> {
		try {
			return parseVersion(await this.runCommand(['--version']));
		} catch {
			return null;
		}
	}

	// ensureCliFresh keeps a plugin-managed 2nb from silently shadowing a newer
	// install. Once per session (called fire-and-forget from onload, so it never
	// blocks the UI): it reads the managed and first system binary versions,
	// caches the version-aware resolution, and — when the managed copy is older
	// than an available system binary OR than the latest published release —
	// quietly re-downloads it. The floor is the latest RELEASE, not the plugin's
	// own version, so a plugin that is ahead of the latest CLI does not
	// re-download every launch. It NEVER deletes the managed copy, so an offline
	// user keeps a working CLI; a failed re-download just leaves the (still
	// usable) stale copy, and the Components panel explains it.
	async ensureCliFresh(): Promise<void> {
		if (this.cliFreshChecked) return;
		this.cliFreshChecked = true;
		if (this.settings.cliPath !== '2nb') return; // explicit user path: hands off

		const managed = this.managedBinaryPath();
		const mgdVer = managed && existsSync(managed) ? await this.binVersion(managed) : null;
		const system = firstExistingSystem(existsSync, process.env);
		const sysVer = system ? await this.binVersion(system) : null;

		this.cliVersions = { managed: mgdVer, system: sysVer };
		this.resolvedCli = resolveCliPath(
			this.settings.cliPath, existsSync, process.env, managed ?? undefined, this.cliVersions
		);

		if (!managed || !mgdVer) return; // no managed copy to heal

		// Refresh when the managed copy is behind a local system binary OR the
		// latest published release. The floor is the latest RELEASE, not the
		// plugin's own version, so a plugin that is ahead of the latest CLI does
		// not re-download the same build every launch. Skip the network probe
		// when a local system binary already proves a newer option exists.
		const sysNewer = !!sysVer && compareVersions(sysVer, mgdVer) > 0;
		const latestVer = sysNewer ? null : await this.latestReleaseVersion();
		if (!needsManagedRefresh(mgdVer, sysVer, latestVer)) return;

		try {
			await this.downloadCli();
			const fresh = existsSync(managed) ? await this.binVersion(managed) : null;
			this.cliVersions = { managed: fresh, system: sysVer };
			this.resolvedCli = resolveCliPath(
				this.settings.cliPath, existsSync, process.env, managed ?? undefined, this.cliVersions
			);
		} catch {
			// Offline / download failed: keep the stale managed copy. The resolver
			// already routes to a newer system binary if one exists, and the
			// Components panel surfaces the staleness with a fix.
		}
	}

	// latestReleaseVersion returns the parsed version of the latest published
	// 2nb release, or null when offline / unavailable. Used by ensureCliFresh to
	// decide whether a managed copy is genuinely behind.
	private async latestReleaseVersion(): Promise<string | null> {
		try {
			const rel = await requestUrl({
				url: 'https://api.github.com/repos/apresai/2ndbrain/releases/latest',
				throw: false,
			});
			if (rel.status !== 200) return null;
			const tag = (rel.json as { tag_name?: string })?.tag_name;
			return tag ? parseVersion(tag) : null;
		} catch {
			return null;
		}
	}

	// checkCli reports whether a 2nb binary is actually runnable (managed,
	// configured, or on PATH) by invoking `2nb --version`.
	async checkCli(): Promise<boolean> {
		try {
			await this.runCommand(['--version']);
			return true;
		} catch {
			return false;
		}
	}

	// aiStatus returns the parsed `2nb ai status --json`, or null if the CLI
	// isn't reachable / the output can't be parsed.
	async aiStatus(): Promise<AIStatus | null> {
		try {
			return JSON.parse(await this.runCommand(['ai', 'status', '--json'])) as AIStatus;
		} catch {
			return null;
		}
	}

	// skillsListMap parses `2nb skills list --json` once and keys it by slug, so
	// the per-client settings UI can look up every client's skill status in a
	// single CLI call. Returns null if the CLI isn't reachable / can't parse
	// (e.g. a pre-skills CLI), so callers can distinguish "no" from "unknown".
	async skillsListMap(): Promise<Record<string, InstallStatus> | null> {
		try {
			const arr = JSON.parse(await this.runCommand(['skills', 'list', '--json'])) as InstallStatus[];
			const map: Record<string, InstallStatus> = {};
			for (const s of arr) map[s.slug] = s;
			return map;
		} catch {
			return null;
		}
	}

	// mcpConfiguredMap parses `2nb mcp configured --all --json` once and keys it
	// by client, so the per-client settings UI can look up every client's
	// MCP-configured status in a single CLI call. Returns null if the CLI isn't
	// reachable / lacks the `mcp configured` subcommand (pre-0.8.x CLI).
	async mcpConfiguredMap(): Promise<Record<string, ConfiguredStatus> | null> {
		try {
			const arr = JSON.parse(await this.runCommand(['mcp', 'configured', '--all', '--json'])) as ConfiguredStatus[];
			const map: Record<string, ConfiguredStatus> = {};
			for (const s of arr) map[s.client] = s;
			return map;
		} catch {
			return null;
		}
	}

	// globalInstructionsMap parses `2nb instructions configured --all --json` once
	// and keys it by client, so the settings UI can show each client's
	// global-instructions status in one CLI call. Returns null when the CLI isn't
	// reachable / lacks the `instructions` command (pre-0.13.2 CLI).
	async globalInstructionsMap(): Promise<Record<string, GlobalInstructionsStatus> | null> {
		try {
			const arr = JSON.parse(await this.runCommand(['instructions', 'configured', '--all', '--json'])) as GlobalInstructionsStatus[];
			const map: Record<string, GlobalInstructionsStatus> = {};
			for (const s of arr) map[s.client] = s;
			return map;
		} catch {
			return null;
		}
	}

	// skillInstalled reports whether the Claude Code skill is installed (user
	// or project scope). Delegates to skillsListMap. Returns null when the
	// status is unknown (CLI unreachable / pre-skills), so the caller can
	// distinguish "no" from "unknown".
	async skillInstalled(): Promise<boolean | null> {
		const map = await this.skillsListMap();
		if (map === null) return null;
		const cc = map['claude-code'];
		return !!(cc && (cc.user_installed || cc.project_installed));
	}

	// mcpConfigured reports whether the 2ndbrain MCP server is configured in the
	// Claude Code client config for the open vault. Delegates to mcpConfiguredMap.
	// Returns null when the status is unknown (CLI unreachable / pre-0.8.x).
	async mcpConfigured(): Promise<boolean | null> {
		const map = await this.mcpConfiguredMap();
		if (map === null) return null;
		const cc = map['claude-code'];
		return cc ? cc.configured : false;
	}

	// configureClient shells `2nb setup --client <key>`: installs the agent skill
	// (where applicable) and writes the MCP-server config for that client, both
	// idempotent and backup-first on the CLI side. runCommand pins --vault to the
	// open Obsidian vault (pinVaultArgs), so setup always targets this vault.
	async configureClient(key: string): Promise<void> {
		await this.runCommand(['setup', '--client', key]);
	}

	// suiteStatus reports the CLI, macOS app, and Obsidian plugin against the
	// latest release (`2nb doctor --json`). Returns null if the CLI isn't
	// reachable / lacks the `doctor` subcommand (pre-0.10.x CLI).
	async suiteStatus(): Promise<SuiteStatus | null> {
		try {
			return JSON.parse(await this.runCommand(['doctor', '--json'])) as SuiteStatus;
		} catch {
			return null;
		}
	}

	private runTool(cmd: string, args: string[]): Promise<string> {
		return new Promise((resolve, reject) => {
			execFile(cmd, args, (err, stdout, stderr) => {
				if (err) reject(new Error(stderr || err.message));
				else resolve(stdout);
			});
		});
	}

	// downloadCli fetches the matching 2nb release binary into the plugin's
	// bin/ folder and makes it runnable. macOS only (the release targets
	// darwin). Because the release isn't notarized, we ad-hoc sign the binary
	// and clear its quarantine xattr — both required for a downloaded CLI to
	// exec without a Gatekeeper block.
	async downloadCli(): Promise<void> {
		if (process.platform !== 'darwin') {
			throw new Error('automatic download is macOS-only — install 2nb manually (e.g. `brew install apresai/tap/2nb`)');
		}
		const dir = this.pluginDir();
		if (!dir) throw new Error('cannot locate the plugin directory');

		const binDir = join(dir, 'bin');
		const arch = process.arch === 'arm64' ? 'arm64' : 'x86_64';

		// Resolve the latest published CLI release tag at runtime rather than
		// pinning to the plugin's own version: the plugin and CLI version
		// independently, and the plugin may be ahead of the latest CLI release.
		const rel = await requestUrl({ url: 'https://api.github.com/repos/apresai/2ndbrain/releases/latest', throw: false });
		if (rel.status !== 200) {
			throw new Error(`could not find the latest 2nb release (HTTP ${rel.status}). Install manually: brew install apresai/tap/2nb`);
		}
		const tag = (rel.json as { tag_name?: string })?.tag_name;
		if (!tag) {
			throw new Error('latest 2nb release has no tag — install manually: brew install apresai/tap/2nb');
		}
		const version = tag.replace(/^v/, '');
		const asset = `2nb_${version}_Darwin_${arch}.tar.gz`;
		const url = `https://github.com/apresai/2ndbrain/releases/download/${tag}/${asset}`;

		new Notice(`Downloading 2nb ${version} (${arch})…`);
		const resp = await requestUrl({ url, throw: false });
		if (resp.status !== 200) {
			throw new Error(`download failed (HTTP ${resp.status}) — ${url}`);
		}

		mkdirSync(binDir, { recursive: true });
		const tarPath = join(binDir, asset);
		writeFileSync(tarPath, Buffer.from(resp.arrayBuffer));
		try {
			await this.runTool('/usr/bin/tar', ['-xzf', tarPath, '-C', binDir]);
		} finally {
			rmSync(tarPath, { force: true });
		}

		const bin = join(binDir, '2nb');
		if (!existsSync(bin)) throw new Error('binary not found after extracting the archive');
		// Tidy the archive's extra docs out of the plugin folder.
		for (const extra of ['README.md', 'LICENSE', 'LICENSE.md', 'LICENSE.txt', 'CHANGELOG.md']) {
			rmSync(join(binDir, extra), { force: true });
		}
		chmodSync(bin, 0o755);
		// Best-effort: ad-hoc sign + clear quarantine (release isn't notarized).
		await this.runTool('/usr/bin/codesign', ['-s', '-', '--force', bin]).catch(() => undefined);
		await this.runTool('/usr/bin/xattr', ['-d', 'com.apple.quarantine', bin]).catch(() => undefined);
		new Notice('2nb CLI installed into the plugin folder.');
	}

	async markFirstRunComplete(): Promise<void> {
		if (!this.settings.firstRunComplete) {
			this.settings.firstRunComplete = true;
			await this.saveSettings();
		}
	}

	// Helper to execute 2nb commands safely. When stdin is provided it is
	// written to the child and the pipe is closed: closing is mandatory,
	// because flags like `ask --history -` read stdin to EOF and would
	// otherwise hang into the timeout.
	runCommand(args: string[], stdin?: string): Promise<string> {
		return new Promise((resolve, reject) => {
			const cliPath = this.resolvedCliPath();

			// If the user configured a custom binary path (not the default
			// '2nb', which we resolve via PATH at exec time), validate it up
			// front so a typo surfaces a clear message instead of an obscure
			// spawn failure.
			if (this.settings.cliPath !== '2nb' && !existsSync(cliPath)) {
				const msg = `2nb CLI not found at configured path "${cliPath}". Update the path in 2ndbrain settings.`;
				new Notice(msg);
				reject(new Error(msg));
				return;
			}

			const adapter = this.app.vault.adapter;
			if (!(adapter instanceof FileSystemAdapter)) {
				reject(new Error("Vault is not stored on a local filesystem."));
				return;
			}
			// The open Obsidian vault is the only vault 2nb may touch. Pin every
			// command to its path via --vault (pinVaultArgs) so the CLI can never
			// resolve a different vault from the Obsidian registry or the cwd.
			const vaultPath = adapter.getBasePath();

			// maxBuffer: the 1 MB default truncates large search/ask output and
			// rejects with a buffer error.
			// timeout: `index` can legitimately run for minutes on a large vault
			// (re-embedding through a remote provider), so it gets no timeout —
			// killing it mid-run would leave the index partially embedded.
			// Interactive search/ask are bounded so a hung CLI can't block the UI.
			const isIndex = args[0] === 'index';
			const options = { cwd: vaultPath, maxBuffer: 16 * 1024 * 1024, timeout: isIndex ? 0 : 120000 };

			const child = execFile(cliPath, pinVaultArgs(vaultPath, args), options, (error, stdout, stderr) => {
				if (error) {
					if ((error as any).code === 'ENOENT') {
						reject(new Error(`Could not find 2nb CLI at "${cliPath}". Please ensure it is installed or configure the path in settings.`));
						return;
					}
					// A timeout sends SIGTERM, surfacing as error.killed with empty
					// stderr — give the user a clear cause instead of "Command failed".
					if ((error as any).killed) {
						reject(new Error(`2nb ${args[0]} timed out. For a large vault, run "2nb ${args[0]}" in a terminal instead.`));
						return;
					}
					reject(new Error(stderr || error.message));
					return;
				}
				resolve(stdout);
			});

			if (stdin !== undefined && child.stdin) {
				// Swallow stream errors: a child that exits without reading
				// stdin (e.g. an older 2nb rejecting --history) makes the pipe
				// emit EPIPE asynchronously, OUTSIDE execFile's callback. With
				// no listener that's an uncaught exception in Obsidian's
				// renderer; the command failure itself still rejects normally.
				child.stdin.on('error', () => {});
				// No deadlock risk: execFile drains stdout/stderr continuously,
				// and history payloads (a few KB) fit the pipe buffer anyway.
				child.stdin.write(stdin);
				child.stdin.end();
			}
		});
	}

	// Wrapper for markdown rendering
	async renderMarkdown(markdown: string, el: HTMLElement): Promise<void> {
		await MarkdownRenderer.renderMarkdown(markdown, el, "", this);
	}
}

// ── Semantic Search Modal ───────────────────────────────────────────────────

class SemanticSearchModal extends SuggestModal<SearchResult> {
	private plugin: BrainPlugin;
	private timeoutId: any = null;
	private initialQuery: string;

	constructor(app: App, plugin: BrainPlugin, initialQuery: string = "") {
		super(app);
		this.plugin = plugin;
		this.initialQuery = initialQuery;
		this.setPlaceholder("Type to search semantically...");
	}

	onOpen() {
		super.onOpen();
		if (this.initialQuery) {
			setTimeout(() => {
				this.inputEl.value = this.initialQuery;
				this.inputEl.dispatchEvent(new Event('input'));
			}, 50);
		}
	}

	getSuggestions(query: string): Promise<SearchResult[]> {
		if (query.trim().length < 2) {
			return Promise.resolve([]);
		}

		return new Promise((resolve) => {
			if (this.timeoutId) {
				clearTimeout(this.timeoutId);
			}

			this.timeoutId = setTimeout(async () => {
				try {
					const stdout = await this.plugin.runCommand(['search', '--json', query]);
					resolve(parseSearchResponse(stdout).results);
				} catch (err) {
					new Notice(`Search error: ${(err as Error).message}`);
					resolve([]);
				}
			}, 300);
		});
	}

	renderSuggestion(value: SearchResult, el: HTMLElement) {
		const title = value.title || filepathBase(value.path);
		const displayTitle = value.heading_path ? `${title} > ${value.heading_path}` : title;
		el.createEl("div", { text: displayTitle, cls: "suggestion-title" });
		
		let subtitle = `${value.path} (${value.type})`;
		if (value.vector_score && value.vector_score > 0) {
			subtitle += ` • similarity: ${Math.round(value.vector_score * 100)}%`;
		}
		el.createEl("small", { text: subtitle, cls: "suggestion-note" });
	}

	onChooseSuggestion(item: SearchResult, evt: MouseEvent | KeyboardEvent) {
		const target = item.heading_path ? `${item.path}#${item.heading_path}` : item.path;
		this.app.workspace.openLinkText(target, "", false);
	}
}

// ── Ask AI Modal ─────────────────────────────────────────────────────────────

class AskAIModal extends Modal {
	private plugin: BrainPlugin;

	constructor(app: App, plugin: BrainPlugin) {
		super(app);
		this.plugin = plugin;
	}

	onOpen() {
		const { contentEl } = this;
		contentEl.empty();
		contentEl.addClass("brain-ask-modal");

		contentEl.createEl("h2", { text: "Ask AI (RAG Q&A)" });

		const inputContainer = contentEl.createDiv({ cls: "brain-input-container" });
		const input = inputContainer.createEl("input", {
			type: "text",
			placeholder: "Ask a question about your vault...",
			cls: "brain-ask-input"
		});
		input.focus();

		const button = inputContainer.createEl("button", { text: "Ask", cls: "brain-ask-button" });
		const resultContainer = contentEl.createDiv({ cls: "brain-result-container brain-hidden" });
		
		// Create premium loader structure
		const loader = contentEl.createDiv({ cls: "brain-loader-container brain-hidden" });
		loader.createDiv({ cls: "brain-spinner" });
		loader.createDiv({ cls: "brain-loader-text", text: "Retrieving context and generating answer..." });

		const executeAsk = async () => {
			const query = input.value.trim();
			if (!query) return;

			loader.removeClass("brain-hidden");
			resultContainer.addClass("brain-hidden");
			resultContainer.empty();

			try {
				const stdout = await this.plugin.runCommand(['ask', '--json', query]);
				const response = parseAskResponse(stdout);

				loader.addClass("brain-hidden");
				resultContainer.removeClass("brain-hidden");

				// Warnings + answer + source chips, shared with the chat panel.
				// Opening a source closes the modal.
				await renderAskResponse(this.plugin, resultContainer, response, () => this.close());
			} catch (err) {
				loader.addClass("brain-hidden");
				resultContainer.removeClass("brain-hidden");
				resultContainer.createEl("div", { text: `Error: ${(err as Error).message}`, cls: "brain-error" });
			}
		};

		button.addEventListener("click", executeAsk);
		input.addEventListener("keydown", (e) => {
			if (e.key === "Enter") {
				executeAsk();
			}
		});
	}

	onClose() {
		const { contentEl } = this;
		contentEl.empty();
	}
}

// ── Polish Result Modal ──────────────────────────────────────────────────────

// PolishResultModal opens in a spinner state while polish runs, then shows a
// colored line diff of the applied change with Keep / Undo. It holds the TFile
// captured at polish time so Undo targets the right note even if the user has
// switched panes since.
class PolishResultModal extends Modal {
	constructor(app: App, private plugin: BrainPlugin, private file: TFile) {
		super(app);
	}

	onOpen() {
		this.contentEl.addClass('brain-ask-modal');
		this.contentEl.createEl('h2', { text: 'Polishing note…' });
		const loader = this.contentEl.createDiv({ cls: 'brain-loader-container' });
		loader.createDiv({ cls: 'brain-spinner' });
		loader.createDiv({ cls: 'brain-loader-text', text: 'Copy-editing and resolving links…' });
	}

	showResult(result: PolishResult) {
		const c = this.contentEl;
		c.empty();
		c.createEl('h2', { text: 'Polished' });
		c.createDiv({ cls: 'brain-loader-text', text: `${result.provider} / ${result.model}` });
		if (result.links_added && result.links_added.length > 0) {
			c.createDiv({ cls: 'brain-loader-text', text: `Added links: ${result.links_added.join(', ')}` });
		}
		if (result.links_repaired && result.links_repaired.length > 0) {
			const repaired = result.links_repaired
				.map((r) => `[[${r.raw}]] → [[${r.new_target}]]`)
				.join(', ');
			c.createDiv({ cls: 'brain-loader-text', text: `Repaired links: ${repaired}` });
		}
		if (result.links_skipped && result.links_skipped.length > 0) {
			c.createDiv({
				cls: 'brain-loader-text',
				text: `${result.links_skipped.length} broken link(s) left for you to fix: ${result.links_skipped.map((r) => `[[${r.raw}]]`).join(', ')}`,
			});
		}
		if (result.warning) {
			c.createDiv({ cls: 'brain-warning', text: result.warning });
		}

		if (result.original.trim() === result.polished.trim()) {
			c.createDiv({ cls: 'brain-diff-context', text: 'No changes were needed; the note was already clean.' });
		} else {
			const diff = c.createDiv({ cls: 'brain-diff' });
			for (const row of computeLineDiff(result.original, result.polished)) {
				const line = diff.createDiv({ cls: `brain-diff-line brain-diff-${row.type}` });
				line.createSpan({
					cls: 'brain-diff-gutter',
					text: row.type === 'add' ? '+' : row.type === 'del' ? '-' : ' ',
				});
				line.createSpan({ cls: 'brain-diff-text', text: row.text || ' ' });
			}
		}

		const actions = c.createDiv({ cls: 'brain-input-container brain-polish-actions' });
		const keep = actions.createEl('button', { text: 'Keep', cls: 'brain-ask-button' });
		const undo = actions.createEl('button', { text: 'Undo', cls: 'brain-ask-button brain-btn-secondary' });
		keep.addEventListener('click', () => this.close());
		undo.addEventListener('click', async () => {
			undo.disabled = true;
			undo.textContent = 'Reverting…';
			const ok = await this.plugin.undoPolish(this.file);
			if (ok) {
				this.close();
			} else {
				undo.disabled = false;
				undo.textContent = 'Undo';
			}
		});
	}

	onClose() {
		this.contentEl.empty();
	}
}

// confirmModal is a small yes/no modal returning a Promise<boolean>. It resolves
// false if dismissed without choosing, so a closed dialog never undoes anything.
function confirmModal(app: App, title: string, message: string): Promise<boolean> {
	return new Promise((resolve) => {
		const modal = new Modal(app);
		let decided = false;
		modal.contentEl.addClass('brain-ask-modal');
		modal.contentEl.createEl('h2', { text: title });
		modal.contentEl.createEl('p', { text: message });
		const row = modal.contentEl.createDiv({ cls: 'brain-input-container brain-polish-actions' });
		const cancel = row.createEl('button', { text: 'Cancel', cls: 'brain-ask-button brain-btn-secondary' });
		const go = row.createEl('button', { text: 'Discard and undo', cls: 'brain-ask-button' });
		cancel.addEventListener('click', () => { decided = true; resolve(false); modal.close(); });
		go.addEventListener('click', () => { decided = true; resolve(true); modal.close(); });
		modal.onClose = () => {
			modal.contentEl.empty();
			if (!decided) resolve(false);
		};
		modal.open();
	});
}

// ── Chat Panel (sidebar view) ────────────────────────────────────────────────

// ChatView is a right-sidebar panel for conversational Q&A against the vault.
// The conversation is real: each message passes the prior turns to
// `2nb ask --history -` (stdin), which condenses follow-ups into standalone
// retrieval queries and grounds answers in the retrieved documents. The
// conversation lives in the view and resets when the panel is closed.
// Toggled by the ribbon icon (open/close) and the "Open chat" command.
class ChatView extends ItemView {
	private plugin: BrainPlugin;
	private messagesEl!: HTMLElement;
	private inputEl!: HTMLInputElement;
	private sendBtn!: HTMLButtonElement;
	private emptyStateEl: HTMLElement | null = null;
	// The real conversation: passed to `2nb ask --history -` with every send so
	// follow-ups carry context. Turns are recorded only after a successful
	// answer, so a failed ask never poisons the conversation.
	private history: ChatTurn[] = [];
	// Set once when the installed CLI rejects --history (pre-multi-turn 2nb)
	// so later sends skip straight to single-shot instead of failing again.
	private historyUnsupported = false;

	constructor(leaf: WorkspaceLeaf, plugin: BrainPlugin) {
		super(leaf);
		this.plugin = plugin;
	}

	getViewType(): string {
		return VIEW_TYPE_CHAT;
	}

	getDisplayText(): string {
		return '2ndbrain Chat';
	}

	getIcon(): string {
		return '2ndbrain-head';
	}

	async onOpen() {
		const container = this.containerEl.children[1];
		container.empty();
		container.addClass('brain-chat-panel');

		this.messagesEl = container.createDiv({ cls: 'brain-chat-messages' });
		this.emptyStateEl = this.messagesEl.createDiv({
			cls: 'brain-chat-empty',
			text: 'Ask anything about your vault. Follow-up questions carry the conversation, and answers cite the source notes they came from.',
		});

		const inputRow = container.createDiv({ cls: 'brain-input-container brain-chat-input-row' });
		this.inputEl = inputRow.createEl('input', {
			type: 'text',
			placeholder: 'Ask your vault...',
			cls: 'brain-ask-input',
		});
		this.sendBtn = inputRow.createEl('button', { text: 'Ask', cls: 'brain-ask-button' });

		this.sendBtn.addEventListener('click', () => void this.send());
		this.inputEl.addEventListener('keydown', (e) => {
			// isComposing: Enter that confirms an IME composition must not send.
			if (e.key === 'Enter' && !e.isComposing) void this.send();
		});
		this.inputEl.focus();
	}

	private async send() {
		const query = this.inputEl.value.trim();
		if (!query || this.sendBtn.disabled) return;

		this.emptyStateEl?.remove();
		this.emptyStateEl = null;
		this.inputEl.value = '';
		this.sendBtn.disabled = true;

		this.messagesEl.createDiv({ cls: 'brain-msg brain-msg-user', text: query });

		const aiMsg = this.messagesEl.createDiv({ cls: 'brain-msg brain-msg-ai' });
		const loader = aiMsg.createDiv({ cls: 'brain-loader-container' });
		loader.createDiv({ cls: 'brain-spinner' });
		loader.createDiv({
			cls: 'brain-loader-text',
			text: this.history.length > 0 ? 'Thinking...' : 'Searching your vault...',
		});
		this.scrollToBottom();

		try {
			// With history, pass the conversation via `--history -` on stdin:
			// the CLI condenses the follow-up into a standalone retrieval query
			// and grounds the answer in the retrieved documents.
			let stdout: string;
			if (this.history.length > 0 && !this.historyUnsupported) {
				const stdin = JSON.stringify(trimChatHistory(this.history));
				try {
					stdout = await this.plugin.runCommand(['ask', '--json', '--history', '-', query], stdin);
				} catch (err) {
					// A 2nb older than `ask --history` rejects the flag. Degrade
					// to a single-shot ask so the panel keeps working, and say so.
					if (!(err as Error).message.includes('unknown flag')) throw err;
					this.historyUnsupported = true;
					aiMsg.createDiv({
						text: 'Installed 2nb is too old for multi-turn chat (needs ask --history). Answering without conversation context. Upgrade: brew upgrade apresai/tap/twonb',
						cls: 'brain-warning',
					});
					stdout = await this.plugin.runCommand(['ask', '--json', query]);
				}
			} else {
				stdout = await this.plugin.runCommand(['ask', '--json', query]);
			}
			const response = parseAskResponse(stdout);
			loader.remove();
			// The panel stays open when a source note is opened (no onSourceOpen).
			await renderAskResponse(this.plugin, aiMsg, response);

			// Record the exchange only after the answer rendered; answer text
			// only, never sources or warnings. A render failure drops the turn
			// so the history never references content the user did not see.
			this.history.push({ role: 'user', content: query }, { role: 'assistant', content: response.answer });
			this.history = trimChatHistory(this.history);
		} catch (err) {
			loader.remove();
			aiMsg.createDiv({ text: `Error: ${(err as Error).message}`, cls: 'brain-error' });
		} finally {
			this.sendBtn.disabled = false;
			this.scrollToBottom();
			this.inputEl.focus();
		}
	}

	private scrollToBottom() {
		this.messagesEl.scrollTo({ top: this.messagesEl.scrollHeight });
	}

	async onClose() {
		// Nothing to release: runCommand owns its subprocess lifecycle and the
		// DOM is dropped with the leaf.
	}
}

// ── Setting Tab ──────────────────────────────────────────────────────────────

class BrainSettingTab extends PluginSettingTab {
	plugin: BrainPlugin;

	constructor(app: App, plugin: BrainPlugin) {
		super(app, plugin);
		this.plugin = plugin;
	}

	display(): void {
		const { containerEl } = this;
		containerEl.empty();

		containerEl.createEl('h2', { text: '2ndbrain AI Settings' });

		// CLI binary status + managed download.
		const cliSetting = new Setting(containerEl)
			.setName('2nb CLI binary')
			.setDesc('Checking…')
			.addButton(btn => btn
				.setButtonText('Download / update CLI')
				.onClick(async () => {
					btn.setDisabled(true).setButtonText('Downloading…');
					try {
						await this.plugin.downloadCli();
					} catch (e) {
						new Notice(`Download failed: ${(e as Error).message}`);
					} finally {
						btn.setDisabled(false).setButtonText('Download / update CLI');
						this.display();
					}
				}));
		this.plugin.checkCli().then(ok => {
			cliSetting.setDesc(ok
				? 'A working 2nb binary was found.'
				: 'No 2nb binary found. Download a managed copy (macOS), or install via `brew install apresai/tap/2nb`.');
		});

		new Setting(containerEl)
			.setName('2nb CLI Path')
			.setDesc('Path to the 2nb Go binary. Defaults to "2nb" (searches standard paths and user PATH).')
			.addText(text => text
				.setPlaceholder('2nb')
				.setValue(this.plugin.settings.cliPath)
				.onChange(async (value) => {
					this.plugin.settings.cliPath = value.trim() || '2nb';
					await this.plugin.saveSettings();
				}));

		// Which vault 2nb operates on — always the open Obsidian vault — and its
		// index state. Read-only and verifiable: 2nb is pinned to this path via
		// --vault on every call, so the Obsidian vault and the 2nb vault cannot
		// diverge (no "custom vault path" override exists by design).
		const adapter = this.plugin.app.vault.adapter;
		const vaultPath = adapter instanceof FileSystemAdapter ? adapter.getBasePath() : '(vault is not on a local filesystem)';
		const vaultName = this.plugin.app.vault.getName();
		const vaultSetting = new Setting(containerEl)
			.setName('Vault')
			.setDesc(`${vaultName} — ${vaultPath} · checking index…`);
		// Holder positioned right after the Vault row so the async reindex nudge
		// (below) lands next to it rather than at the end of the settings pane.
		const reindexNudgeEl = containerEl.createDiv();
		this.plugin.aiStatus().then(status => {
			const state = status
				? formatIndexState(status.document_count ?? 0, status.embedding_count ?? 0)
				: 'index state unavailable (2nb CLI not reachable)';
			vaultSetting.setDesc(`${vaultName} — ${vaultPath} · ${state}`);

			// A newer 2nb changed indexing/embedding logic for this vault → prompt
			// a reindex/re-embed (the CLI shows the same in `vault status`).
			const ps = status?.portability_status;
			if (ps === 'upgrade_reembed_recommended' || ps === 'upgrade_reindex_recommended') {
				const forceReembed = ps === 'upgrade_reembed_recommended';
				const label = forceReembed ? 'Re-embed' : 'Reindex';
				new Setting(reindexNudgeEl)
					.setName('Index update available')
					.setDesc(forceReembed
						? 'A newer 2nb improved chunking and embeddings for this vault. Re-embed to apply them.'
						: 'A newer 2nb improved indexing for this vault. Reindex to apply it.')
					.addButton(btn => btn
						.setButtonText(label)
						.setCta()
						.onClick(async () => {
							btn.setDisabled(true).setButtonText('Working…');
							try {
								await this.plugin.runCommand(forceReembed ? ['index', '--force-reembed'] : ['index']);
								new Notice('2ndbrain: reindex complete.');
								reindexNudgeEl.empty(); // clears once applied
							} catch (e) {
								new Notice(`2ndbrain: reindex failed — ${e}`);
								btn.setDisabled(false).setButtonText(label);
							}
						}));
			}
		});

		// AI client integrations: one sub-section per supported client (Claude
		// Code, Warp, Claude Desktop, Codex), each with its skill status (where the
		// client ships one) and MCP-configured status. "Configure" is now the
		// primary action — it shells `2nb setup --client <key>`, which installs the
		// skill (where applicable) and writes the MCP config in one idempotent,
		// backup-first step (so it's safe to run on an existing config). "Copy
		// setup snippet" stays as a manual fallback. MCP "configured" (not
		// "running") is the durable signal: each client launches the server on
		// demand, so a running-check would read red whenever the client is closed.
		// The status maps are fetched ONCE up front (one CLI call each) and shared
		// across every client row.
		containerEl.createEl('h3', { text: 'AI client integrations' });
		const mcpMapPromise = this.plugin.mcpConfiguredMap();
		const skillsMapPromise = this.plugin.skillsListMap();
		const giMapPromise = this.plugin.globalInstructionsMap();

		for (const c of MCP_CLIENTS) {
			containerEl.createEl('h4', { text: c.name });

			// Skill row only for clients that ship a 2nb skill.
			let skillRow: Setting | null = null;
			if (c.skillSlug) {
				const slug = c.skillSlug;
				skillRow = new Setting(containerEl).setName('Agent skill').setDesc('Checking…');
				void skillsMapPromise.then(map => {
					if (map === null) {
						skillRow!.setDesc('Status unavailable (2nb CLI not reachable, or too old for `skills list`).');
						return;
					}
					const st = map[slug];
					const installed = !!(st && (st.user_installed || st.project_installed));
					skillRow!.setDesc(installed
						? `Installed. ${c.name} can use the 2ndbrain skill.`
						: 'Not installed. Click Configure to install it.');
				});
			}

			// MCP row: status + Configure (one-click) + Copy setup snippet (manual).
			const mcpRow = new Setting(containerEl)
				.setName('MCP server')
				.setDesc('Checking…')
				.addButton(btn => btn
					.setButtonText('Configure')
					.onClick(async () => {
						btn.setDisabled(true).setButtonText('Configuring…');
						try {
							await this.plugin.configureClient(c.key);
							new Notice(`${c.name}: 2ndbrain set up.${c.note ? ' ' + c.note : ''}`);
						} catch (e) {
							new Notice(`${c.name} setup failed: ${(e as Error).message}`);
						} finally {
							btn.setDisabled(false).setButtonText('Configure');
							this.display();
						}
					}))
				.addButton(btn => btn
					.setButtonText('Copy setup snippet')
					.onClick(async () => {
						const snippet = mcpSnippetFor(c.key, vaultPath, this.plugin.resolvedCliPath());
						try {
							await navigator.clipboard.writeText(snippet);
							new Notice(c.key === 'codex'
								? 'Copied. Run it in a terminal to register the 2ndbrain MCP server with Codex.'
								: `Copied the ${c.name} MCP config snippet to the clipboard.`);
						} catch {
							new Notice(`Add this manually:\n\n${snippet}`);
						}
					}));
			void mcpMapPromise.then(map => {
				const noteSuffix = c.note ? ` ${c.note}` : '';
				if (map === null) {
					mcpRow.setDesc(`Status unavailable (2nb CLI not reachable, or too old for \`mcp configured\`).${noteSuffix}`);
					return;
				}
				const st = map[c.key];
				if (st && st.configured) {
					const scope = st.scope ? `, ${st.scope} scope` : '';
					mcpRow.setDesc(`Configured in ${st.config_path}${scope}.${noteSuffix}`);
				} else {
					mcpRow.setDesc(`Not configured. Click Configure to wire ${c.name} to this vault.${noteSuffix}`);
				}
			});

			// Global-instructions row for clients with a global memory file: the
			// always-loaded 2nb reference block Configure also installs (via setup).
			if (c.globalInstructions) {
				const giRow = new Setting(containerEl).setName('Global instructions').setDesc('Checking…');
				void giMapPromise.then(map => {
					if (map === null) {
						giRow.setDesc('Status unavailable (2nb CLI not reachable, or too old for `instructions`).');
						return;
					}
					const st = map[c.key];
					const file = st?.file_path ?? (c.key === 'codex' ? '~/.codex/AGENTS.md' : '~/.claude/CLAUDE.md');
					if (st && st.installed && st.up_to_date && !st.modified) {
						giRow.setDesc(`Installed in ${file}. ${c.name} loads the 2nb reference on every session.`);
					} else if (st && st.installed) {
						giRow.setDesc(st.modified
							? `Installed but hand-edited in ${file}. Configure to refresh (backs up first).`
							: `Installed but out of date in ${file}. Configure to refresh.`);
					} else {
						giRow.setDesc(`Not installed. Click Configure to add the 2nb block to ${file}.`);
					}
				});
			}
		}

		// Components: are the CLI, macOS app, and Obsidian plugin all installed
		// and in sync with the latest release? `2nb doctor --json` is the single
		// source (the same check is `2nb doctor` in a terminal). The three
		// products bump together but a release can publish one without another, so
		// this surfaces drift and the command to fix each gap.
		containerEl.createEl('h3', { text: 'Components' });
		const cliRow = new Setting(containerEl).setName('CLI (2nb)').setDesc('Checking…');
		const appRow = new Setting(containerEl).setName('macOS app').setDesc('Checking…');
		const pluginRow = new Setting(containerEl).setName('Obsidian plugin').setDesc('Checking…');
		// The "Update plugin" button is added only when the plugin is actually
		// behind (below) — an always-present button implies a self-update the
		// plugin can't do, and (without the CLI's no-downgrade guard) could even
		// reinstall an older release over a newer one.
		const addUpdateButton = () => pluginRow.addButton(btn => btn
			.setButtonText('Update plugin')
			.onClick(async () => {
				btn.setDisabled(true).setButtonText('Updating…');
				try {
					await this.plugin.runCommand(['plugin', 'install']);
					new Notice('Obsidian plugin updated. Reload Obsidian (Cmd+R) to apply.');
				} catch (e) {
					new Notice(`Plugin update failed: ${(e as Error).message}`);
				} finally {
					btn.setDisabled(false).setButtonText('Update plugin');
					this.display();
				}
			}));
		this.plugin.suiteStatus().then(async suite => {
			if (!suite) {
				// doctor failed: degrade per-row and explain WHY with a
				// doctor-independent `--version` probe, instead of one blanket
				// "unavailable" on every row.
				const floor = this.plugin.manifest.version;
				const path = this.plugin.resolvedCliPath();
				const v = await this.plugin.cliVersion();
				if (!v) {
					cliRow.setDesc(`Not reachable at ${path}. Install 2nb (e.g. \`brew install apresai/tap/2nb\`) or set the path above.`);
				} else if (compareVersions(v, floor) < 0) {
					cliRow.setDesc(`2nb ${v} at ${path} is older than this panel needs (≥ ${floor}). Use "Download / update CLI" above, or \`brew upgrade apresai/tap/twonb\`.`);
				} else {
					cliRow.setDesc(`2nb ${v} reachable, but the doctor check failed. Reopen settings to retry.`);
				}
				// These two are knowable without doctor.
				pluginRow.setDesc(`v${this.plugin.manifest.version} installed.`);
				appRow.setDesc(`Needs 2nb ≥ ${floor} to report — run \`2nb doctor\`.`);
				return;
			}
			cliRow.setDesc(describeComponent(suite.cli, suite.latest));
			appRow.setDesc(describeComponent(suite.app, suite.latest));
			pluginRow.setDesc(describeComponent(suite.plugin, suite.latest));
			if (suite.plugin.status === 'outdated') addUpdateButton();
		}).catch(e => {
			// suiteStatus/cliVersion swallow their own errors today, so this is a
			// belt-and-suspenders guard: a future throw shows an error in the rows
			// instead of an unhandled renderer rejection.
			const msg = `Status check failed: ${(e as Error).message}`;
			cliRow.setDesc(msg);
			appRow.setDesc(msg);
			pluginRow.setDesc(msg);
		});
	}
}

// ── Setup Wizard ─────────────────────────────────────────────────────────────

// SetupWizardModal walks a new user through: install the CLI → connect AI
// (AWS Bedrock by default) → index the vault. It re-renders after each step so
// completing one reveals the next.
class SetupWizardModal extends Modal {
	private plugin: BrainPlugin;

	constructor(app: App, plugin: BrainPlugin) {
		super(app);
		this.plugin = plugin;
	}

	onOpen() {
		void this.render();
	}

	onClose() {
		this.contentEl.empty();
		// A deliberate dismissal counts as "seen" so the wizard doesn't re-open
		// on every launch. It's always reachable via the "Setup wizard" command.
		void this.plugin.markFirstRunComplete();
	}

	private async render() {
		const { contentEl } = this;
		contentEl.empty();
		contentEl.addClass('brain-wizard');
		contentEl.createEl('h2', { text: '2ndbrain setup' });

		// Step 1 — CLI binary.
		const hasCli = await this.plugin.checkCli();
		const s1 = contentEl.createDiv({ cls: 'brain-wizard-step' });
		s1.createEl('h3', { text: hasCli ? '✓ 1. 2nb CLI installed' : '1. Install the 2nb CLI' });
		if (!hasCli) {
			s1.createEl('p', {
				text: 'The plugin runs the 2nb command-line engine. Download it (macOS), or install with Homebrew: brew install apresai/tap/2nb',
			});
			const dl = s1.createEl('button', { text: 'Download 2nb CLI' });
			dl.addEventListener('click', async () => {
				dl.disabled = true;
				dl.textContent = 'Downloading…';
				try {
					await this.plugin.downloadCli();
					await this.render();
				} catch (e) {
					new Notice((e as Error).message);
					dl.disabled = false;
					dl.textContent = 'Retry download';
				}
			});
			return; // gate the remaining steps until a CLI is available
		}

		// Step 2 — AI provider.
		const status = await this.plugin.aiStatus();
		const aiReady = !!status?.embed_available;
		const s2 = contentEl.createDiv({ cls: 'brain-wizard-step' });
		s2.createEl('h3', { text: aiReady ? '✓ 2. AI ready (Bedrock)' : '2. Connect AI (AWS Bedrock)' });
		if (!aiReady) {
			s2.createEl('p', {
				text: '2ndbrain uses AWS Bedrock by default (Claude Haiku 4.5 + Nova embeddings). In a terminal run `2nb ai setup`, or make sure your AWS credentials are available (AWS_PROFILE or ~/.aws/credentials) and enable model access in the AWS console (Bedrock → Model access). Keyword search works without AI.',
			});
			const v = s2.createEl('button', { text: 'Verify again' });
			v.addEventListener('click', () => void this.render());
		}

		// Step 3 — Index. Reuse the ai status from step 2 to show whether THIS
		// vault is already embedded, so the user knows if they still need to index.
		const s3 = contentEl.createDiv({ cls: 'brain-wizard-step' });
		const docs = status?.document_count ?? 0;
		const embedded = status?.embedding_count ?? 0;
		const alreadyIndexed = docs > 0 && embedded >= docs;
		s3.createEl('h3', { text: alreadyIndexed ? '✓ 3. Vault indexed' : '3. Index your vault' });
		s3.createEl('p', {
			text: status
				? `Current: ${formatIndexState(docs, embedded)}. Re-run any time from the command palette.`
				: 'Build the search index over your notes. Re-run any time from the command palette.',
		});
		const ix = s3.createEl('button', { text: alreadyIndexed ? 'Re-index' : 'Index now' });
		ix.addEventListener('click', async () => {
			ix.disabled = true;
			ix.textContent = 'Indexing…';
			try {
				await this.plugin.runCommand(['index']);
				new Notice('Vault indexed.');
				await this.plugin.markFirstRunComplete();
				this.close();
			} catch (e) {
				new Notice(`Index failed: ${(e as Error).message}`);
				ix.disabled = false;
				ix.textContent = 'Retry';
			}
		});

		const finish = contentEl.createEl('button', { text: 'Finish', cls: 'brain-wizard-finish' });
		finish.addEventListener('click', async () => {
			await this.plugin.markFirstRunComplete();
			this.close();
		});
	}
}
