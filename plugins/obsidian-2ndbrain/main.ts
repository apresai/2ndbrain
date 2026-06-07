import {
	App,
	FileSystemAdapter,
	MarkdownRenderer,
	Modal,
	Notice,
	Plugin,
	PluginSettingTab,
	Setting,
	SuggestModal,
	requestUrl
} from 'obsidian';
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

// AIStatus is the subset of `2nb ai status --json` the plugin reads.
interface AIStatus {
	provider?: string;
	embedding_model?: string;
	embed_available?: boolean;
	gen_available?: boolean;
	document_count?: number;
	embedding_count?: number;
	providers?: { name: string; reachable: boolean; reason?: string }[];
}

// resolveCliPath resolves the 2nb binary path. Pure free function: it takes
// its filesystem probe (existsFn) and environment (env) as parameters so it
// can be unit-tested without touching the real disk. GUI apps don't always
// inherit the shell PATH, so when the path is left at the default ('2nb') we
// probe the standard install locations before falling back to bare '2nb'
// (resolved via PATH at exec time).
export function resolveCliPath(
	configured: string,
	existsFn: (p: string) => boolean,
	env: NodeJS.ProcessEnv,
	managedPath?: string
): string {
	if (configured !== '2nb') {
		return configured;
	}

	// A plugin-managed binary (downloaded into the plugin folder) wins over
	// brew/PATH probing.
	if (managedPath && existsFn(managedPath)) {
		return managedPath;
	}

	// Standard macOS Homebrew paths (ARM then Intel).
	const brewArm = '/opt/homebrew/bin/2nb';
	if (existsFn(brewArm)) return brewArm;

	const brewIntel = '/usr/local/bin/2nb';
	if (existsFn(brewIntel)) return brewIntel;

	// ~/go/bin/2nb for developers building from source.
	const home = env.HOME || env.USERPROFILE;
	if (home) {
		const goBin = `${home}/go/bin/2nb`;
		if (existsFn(goBin)) return goBin;
	}

	return '2nb';
}

// pinVaultArgs prefixes a 2nb invocation with `--vault <path>` — the CLI's
// highest-priority vault source (root.go). The plugin ALWAYS pins commands to
// the open Obsidian vault's path so 2nb can never resolve a different vault from
// a stale ~/.2ndbrain-active-vault file or the process cwd. The Obsidian vault
// and the 2nb vault stay joined at the hip.
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

// filepathBase returns the final path segment of a vault-relative path.
export function filepathBase(path: string): string {
	const parts = path.split('/');
	return parts[parts.length - 1];
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

export default class BrainPlugin extends Plugin {
	settings!: BrainSettings;
	statusBarEl!: HTMLElement;

	async onload() {
		await this.loadSettings();

		// Add status bar item
		this.statusBarEl = this.addStatusBarItem();
		this.statusBarEl.setText('2ndbrain: Ready');

		// Add commands
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

		// Add setting tab
		this.addSettingTab(new BrainSettingTab(this.app, this));

		// Open the wizard once, on first run, after the workspace is ready.
		if (!this.settings.firstRunComplete) {
			this.app.workspace.onLayoutReady(() => {
				new SetupWizardModal(this.app, this).open();
			});
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

	// Helper to execute 2nb commands safely
	runCommand(args: string[]): Promise<string> {
		return new Promise((resolve, reject) => {
			const cliPath = resolveCliPath(this.settings.cliPath, existsSync, process.env, this.managedBinaryPath() ?? undefined);

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
			// resolve a different vault from ~/.2ndbrain-active-vault or the cwd.
			const vaultPath = adapter.getBasePath();

			// maxBuffer: the 1 MB default truncates large search/ask output and
			// rejects with a buffer error.
			// timeout: `index` can legitimately run for minutes on a large vault
			// (re-embedding through a remote provider), so it gets no timeout —
			// killing it mid-run would leave the index partially embedded.
			// Interactive search/ask are bounded so a hung CLI can't block the UI.
			const isIndex = args[0] === 'index';
			const options = { cwd: vaultPath, maxBuffer: 16 * 1024 * 1024, timeout: isIndex ? 0 : 120000 };

			execFile(cliPath, pinVaultArgs(vaultPath, args), options, (error, stdout, stderr) => {
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

				// Render Markdown answer
				const answerEl = resultContainer.createDiv({ cls: "brain-answer" });
				await this.plugin.renderMarkdown(response.answer, answerEl);

				// Render Sources Chips. `sources` is a string[] of vault-relative
				// paths (see parseAskResponse / cli/internal/cli/ask.go), so dedupe
				// on the path string itself and derive the label from it.
				if (response.sources && response.sources.length > 0) {
					const sourcesContainer = resultContainer.createDiv({ cls: "brain-sources-container" });
					sourcesContainer.createEl("div", { text: "Sources", cls: "brain-sources-title" });
					const sourcesList = sourcesContainer.createEl("ul", { cls: "brain-sources-list" });

					const seenPaths = new Set<string>();
					response.sources.forEach((source) => {
						if (seenPaths.has(source)) return;
						seenPaths.add(source);

						const li = sourcesList.createEl("li");
						const link = li.createEl("a", {
							cls: "brain-source-chip",
							href: "#"
						});

						link.createSpan({ text: "📄", cls: "brain-source-icon" });
						link.createSpan({ text: filepathBase(source) });

						link.addEventListener("click", (e) => {
							e.preventDefault();
							this.app.workspace.openLinkText(source, "", false);
							this.close();
						});
					});
				}
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
		this.plugin.aiStatus().then(status => {
			const state = status
				? formatIndexState(status.document_count ?? 0, status.embedding_count ?? 0)
				: 'index state unavailable (2nb CLI not reachable)';
			vaultSetting.setDesc(`${vaultName} — ${vaultPath} · ${state}`);
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
