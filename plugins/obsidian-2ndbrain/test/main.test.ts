import { describe, it, expect, vi } from 'vitest';

// The `obsidian` package has no runtime outside the Obsidian app. Importing
// main.ts executes its top-level class declarations, several of which use
// `extends` against obsidian exports (Plugin, Modal, SuggestModal,
// PluginSettingTab) and `instanceof` (FileSystemAdapter). Those MUST be real
// constructable classes here or the import throws before any test runs. The
// vi.mock factory is hoisted, so it cannot close over outer variables.
vi.mock('obsidian', () => {
	class Plugin {
		app: any;
		manifest: any;
		constructor(app?: any, manifest?: any) {
			this.app = app;
			this.manifest = manifest;
		}
		addCommand() {}
		addStatusBarItem() {
			return { setText() {} };
		}
		addSettingTab() {}
		loadData() {
			return Promise.resolve({});
		}
		saveData() {
			return Promise.resolve();
		}
	}
	class Modal {
		app: any;
		contentEl: any = {};
		constructor(app?: any) {
			this.app = app;
		}
		open() {}
		close() {}
	}
	class SuggestModal {
		app: any;
		inputEl: any = {};
		constructor(app?: any) {
			this.app = app;
		}
		setPlaceholder() {}
		onOpen() {}
	}
	class PluginSettingTab {
		app: any;
		plugin: any;
		containerEl: any = {};
		constructor(app?: any, plugin?: any) {
			this.app = app;
			this.plugin = plugin;
		}
	}
	class FileSystemAdapter {
		getBasePath() {
			return '/tmp/vault';
		}
	}
	class Setting {
		constructor(_containerEl?: any) {}
		setName() {
			return this;
		}
		setDesc() {
			return this;
		}
		addText() {
			return this;
		}
	}
	const Notice = vi.fn();
	const MarkdownRenderer = { renderMarkdown: vi.fn() };
	class App {}
	// ItemView is extended by ChatView at module top level (like Modal), so
	// it must be a real constructable class. WorkspaceLeaf is only a type
	// annotation but exporting a class keeps the mock future-proof.
	class ItemView {
		leaf: any;
		containerEl: any = { children: [{}, {}] };
		constructor(leaf?: any) {
			this.leaf = leaf;
		}
	}
	class WorkspaceLeaf {}
	// MarkdownView is imported as a runtime value (getActiveViewOfType /
	// instanceof), so the mock must provide a constructable class even though
	// the pure-function tests never instantiate it.
	class MarkdownView {}
	const addIcon = vi.fn();

	return {
		Plugin,
		Modal,
		SuggestModal,
		PluginSettingTab,
		FileSystemAdapter,
		Setting,
		Notice,
		MarkdownRenderer,
		App,
		ItemView,
		WorkspaceLeaf,
		MarkdownView,
		addIcon,
	};
});

import {
	filepathBase,
	resolveCliPath,
	parseAskResponse,
	parseSearchResponse,
	parsePolishResponse,
	computeLineDiff,
	pinVaultArgs,
	formatIndexState,
	describeComponent,
	parseVersion,
	compareVersions,
	needsManagedRefresh,
	firstExistingSystem,
	trimChatHistory,
	mcpSnippetFor,
	MCP_CLIENTS,
	type ChatTurn,
	type DiffRow,
} from '../main.ts';

describe('trimChatHistory', () => {
	const turn = (role: ChatTurn['role'], content: string): ChatTurn => ({ role, content });

	it('keeps a short conversation unchanged', () => {
		const h = [turn('user', 'q1'), turn('assistant', 'a1')];
		expect(trimChatHistory(h)).toEqual(h);
	});

	it('caps the turn count keeping the most recent', () => {
		const h: ChatTurn[] = [];
		for (let i = 0; i < 20; i++) h.push(turn('user', `q${i}`));
		const got = trimChatHistory(h);
		expect(got).toHaveLength(12);
		expect(got[got.length - 1].content).toBe('q19');
	});

	it('truncates long turns code-point safely', () => {
		const long = 'ü'.repeat(1600); // multibyte chars
		const got = trimChatHistory([turn('user', long)]);
		expect(Array.from(got[0].content)).toHaveLength(1503); // 1500 + '...'
	});

	it('drops oldest turns to fit the total budget', () => {
		const h: ChatTurn[] = [];
		for (let i = 0; i < 10; i++) h.push(turn('user', `${i}:` + 'x'.repeat(1400)));
		const got = trimChatHistory(h);
		expect(got.length).toBeLessThan(10);
		expect(got[got.length - 1].content.startsWith('9:')).toBe(true);
	});

	it('empty stays empty', () => {
		expect(trimChatHistory([])).toEqual([]);
	});
});

describe('parseAskResponse with rewritten_query', () => {
	it('tolerates the additive multi-turn field', () => {
		const response = parseAskResponse(
			'{"mode":"hybrid","answer":"a","sources":["s.md"],"rewritten_query":"standalone q"}'
		);
		expect(response.answer).toBe('a');
		expect(response.sources).toEqual(['s.md']);
	});
});

describe('filepathBase', () => {
	it('returns the final path segment', () => {
		expect(filepathBase('notes/sub/file.md')).toBe('file.md');
	});

	it('returns the input when there is no separator', () => {
		expect(filepathBase('file.md')).toBe('file.md');
	});

	it('handles a leading-slash absolute path', () => {
		expect(filepathBase('/a/b/c.md')).toBe('c.md');
	});

	it('returns empty string for a trailing-slash path', () => {
		expect(filepathBase('a/b/')).toBe('');
	});
});

describe('resolveCliPath', () => {
	const env: NodeJS.ProcessEnv = { HOME: '/Users/test' };

	it('returns the configured path verbatim when it is not the default', () => {
		// existsFn should never matter when a custom path is configured.
		const exists = vi.fn().mockReturnValue(false);
		expect(resolveCliPath('/custom/bin/2nb', exists, env)).toBe('/custom/bin/2nb');
		expect(exists).not.toHaveBeenCalled();
	});

	it('falls back to Homebrew ARM path when present', () => {
		const exists = (p: string) => p === '/opt/homebrew/bin/2nb';
		expect(resolveCliPath('2nb', exists, env)).toBe('/opt/homebrew/bin/2nb');
	});

	it('falls back to Homebrew Intel path when ARM is absent', () => {
		const exists = (p: string) => p === '/usr/local/bin/2nb';
		expect(resolveCliPath('2nb', exists, env)).toBe('/usr/local/bin/2nb');
	});

	it('falls back to ~/go/bin/2nb when brew paths are absent', () => {
		const exists = (p: string) => p === '/Users/test/go/bin/2nb';
		expect(resolveCliPath('2nb', exists, env)).toBe('/Users/test/go/bin/2nb');
	});

	it('uses USERPROFILE when HOME is unset', () => {
		const winEnv: NodeJS.ProcessEnv = { USERPROFILE: '/Users/win' };
		const exists = (p: string) => p === '/Users/win/go/bin/2nb';
		expect(resolveCliPath('2nb', exists, winEnv)).toBe('/Users/win/go/bin/2nb');
	});

	it('falls back to bare "2nb" when nothing is found on disk', () => {
		const exists = () => false;
		expect(resolveCliPath('2nb', exists, env)).toBe('2nb');
	});

	it('does not probe ~/go/bin when no home is set and nothing else exists', () => {
		const exists = () => false;
		expect(resolveCliPath('2nb', exists, {})).toBe('2nb');
	});

	it('prefers a managed binary over brew/PATH when it exists', () => {
		const managed = '/vault/.obsidian/plugins/obsidian-2ndbrain/bin/2nb';
		// Both the managed binary and a brew path "exist" — managed must win.
		const exists = (p: string) => p === managed || p === '/opt/homebrew/bin/2nb';
		expect(resolveCliPath('2nb', exists, env, managed)).toBe(managed);
	});

	it('falls through past a managed path that does not exist', () => {
		const managed = '/vault/.obsidian/plugins/obsidian-2ndbrain/bin/2nb';
		const exists = (p: string) => p === '/opt/homebrew/bin/2nb'; // managed absent
		expect(resolveCliPath('2nb', exists, env, managed)).toBe('/opt/homebrew/bin/2nb');
	});

	it('still honors an explicit configured path over the managed binary', () => {
		const managed = '/vault/.obsidian/plugins/obsidian-2ndbrain/bin/2nb';
		const exists = vi.fn().mockReturnValue(true);
		// A user-configured path takes precedence over everything, including managed.
		expect(resolveCliPath('/custom/2nb', exists, env, managed)).toBe('/custom/2nb');
		expect(exists).not.toHaveBeenCalled();
	});

	// Version-aware selection: a managed copy must not shadow a STRICTLY newer
	// system binary (the stale-0.10.3-shadows-brew-0.10.5 bug).
	const managed = '/vault/.obsidian/plugins/obsidian-2ndbrain/bin/2nb';
	const both = (p: string) => p === managed || p === '/opt/homebrew/bin/2nb';

	it('prefers brew when the system binary is strictly newer than the managed copy', () => {
		expect(resolveCliPath('2nb', both, env, managed, { managed: '0.10.3', system: '0.10.5' }))
			.toBe('/opt/homebrew/bin/2nb');
	});

	it('keeps the managed copy when it is newer than or equal to the system binary', () => {
		expect(resolveCliPath('2nb', both, env, managed, { managed: '0.10.5', system: '0.10.5' })).toBe(managed);
		expect(resolveCliPath('2nb', both, env, managed, { managed: '0.10.6', system: '0.10.5' })).toBe(managed);
	});

	it('keeps the managed copy when a version is unknown (offline / dev build)', () => {
		// Either side unknown -> no strictly-newer proof -> managed stays (offline-safe).
		expect(resolveCliPath('2nb', both, env, managed, { managed: null, system: '0.10.5' })).toBe(managed);
		expect(resolveCliPath('2nb', both, env, managed, { managed: '0.10.3', system: null })).toBe(managed);
	});

	it('keeps the managed copy when versions are omitted entirely (legacy call)', () => {
		expect(resolveCliPath('2nb', both, env, managed)).toBe(managed);
	});

	it('uses the system binary when no managed copy exists, regardless of versions', () => {
		const sysOnly = (p: string) => p === '/opt/homebrew/bin/2nb';
		expect(resolveCliPath('2nb', sysOnly, env, managed, { managed: null, system: '0.10.5' }))
			.toBe('/opt/homebrew/bin/2nb');
	});
});

describe('parseVersion', () => {
	it('extracts the triple from `2nb version` output', () => {
		expect(parseVersion('2nb version 0.10.5')).toBe('0.10.5');
	});
	it('extracts a bare triple and ignores trailing junk', () => {
		expect(parseVersion('0.10.5\n')).toBe('0.10.5');
		expect(parseVersion('v0.10.5 (abc123)')).toBe('0.10.5');
	});
	it('returns null for a dev build or unparseable input', () => {
		expect(parseVersion('dev')).toBeNull();
		expect(parseVersion('')).toBeNull();
		expect(parseVersion(undefined as unknown as string)).toBeNull();
	});
});

describe('compareVersions', () => {
	it('orders by major, minor, then patch', () => {
		expect(compareVersions('0.10.5', '0.10.4')).toBe(1);
		expect(compareVersions('0.10.4', '0.10.5')).toBe(-1);
		expect(compareVersions('1.0.0', '0.99.99')).toBe(1);
		expect(compareVersions('0.11.0', '0.10.9')).toBe(1);
	});
	it('treats equal versions as 0', () => {
		expect(compareVersions('0.10.5', '0.10.5')).toBe(0);
	});
	it('treats missing components as 0', () => {
		expect(compareVersions('1', '1.0.0')).toBe(0);
	});
	it('treats non-numeric components as 0 (no NaN "equal" trap)', () => {
		// Without the guard, the non-numeric major would be NaN and the compare
		// would fall through to "equal" (0). With it, 'v1' -> 0, so it's LESS.
		expect(compareVersions('v1.2.3', '1.2.3')).toBe(-1); // 'v1' -> 0 < 1
		expect(compareVersions('1.2.x', '1.2.0')).toBe(0);   // 'x' -> 0, equal
		expect(compareVersions('1.3.0', 'junk')).toBe(1);    // 'junk' -> 0.0.0
	});
});

describe('needsManagedRefresh', () => {
	it('refreshes when a system binary is strictly newer', () => {
		expect(needsManagedRefresh('0.10.3', '0.10.5', null)).toBe(true);
	});
	it('refreshes when the latest release is strictly newer', () => {
		expect(needsManagedRefresh('0.10.3', null, '0.10.5')).toBe(true);
	});
	it('does NOT refresh when the managed copy is newest (plugin ahead of CLI)', () => {
		// The floor is the latest RELEASE, not the plugin version: an up-to-date
		// managed copy must not re-download every launch.
		expect(needsManagedRefresh('0.10.5', '0.10.5', '0.10.5')).toBe(false);
		expect(needsManagedRefresh('0.11.0', '0.10.5', '0.10.5')).toBe(false);
	});
	it('does NOT refresh when offline and no newer local binary (both null)', () => {
		expect(needsManagedRefresh('0.10.3', null, null)).toBe(false);
	});
});

describe('firstExistingSystem', () => {
	const env: NodeJS.ProcessEnv = { HOME: '/Users/test' };
	it('returns Homebrew ARM first, then Intel, then ~/go/bin', () => {
		expect(firstExistingSystem(() => true, env)).toBe('/opt/homebrew/bin/2nb');
		expect(firstExistingSystem(p => p === '/usr/local/bin/2nb', env)).toBe('/usr/local/bin/2nb');
		expect(firstExistingSystem(p => p === '/Users/test/go/bin/2nb', env)).toBe('/Users/test/go/bin/2nb');
	});
	it('returns null when no system binary exists', () => {
		expect(firstExistingSystem(() => false, env)).toBeNull();
		expect(firstExistingSystem(() => false, {})).toBeNull();
	});
});

describe('pinVaultArgs', () => {
	// The core "joined at the hip" guarantee: every 2nb invocation is pinned to
	// the open Obsidian vault via --vault, the CLI's highest-priority source.
	it('prepends --vault <path> ahead of the subcommand', () => {
		expect(pinVaultArgs('/Users/chad/dev/obsidian', ['search', 'q', '--json'])).toEqual([
			'--vault', '/Users/chad/dev/obsidian', 'search', 'q', '--json',
		]);
	});

	it('keeps --vault first even for a bare command', () => {
		const out = pinVaultArgs('/v', ['index']);
		expect(out[0]).toBe('--vault');
		expect(out[1]).toBe('/v');
		expect(out[2]).toBe('index');
	});

	it('does not mutate the caller\'s args array', () => {
		const args = ['ai', 'status', '--json'];
		pinVaultArgs('/v', args);
		expect(args).toEqual(['ai', 'status', '--json']);
	});
});

describe('formatIndexState', () => {
	it('reports embedded when coverage is complete', () => {
		expect(formatIndexState(113, 113)).toBe('embedded (113 / 113 documents)');
	});

	it('reports embedded when embeddings exceed docs (stale extras tolerated)', () => {
		expect(formatIndexState(113, 114)).toBe('embedded (114 / 113 documents)');
	});

	it('reports partial coverage', () => {
		expect(formatIndexState(113, 112)).toBe('partially embedded (112 / 113 documents)');
	});

	it('reports not-indexed when there are docs but zero embeddings', () => {
		expect(formatIndexState(113, 0)).toContain('not indexed yet (113 documents)');
	});

	it('reports an empty vault when there are no documents', () => {
		expect(formatIndexState(0, 0)).toContain('empty vault');
	});
});

describe('describeComponent', () => {
	it('renders up-to-date WITHOUT a latest suffix (no "up to date (latest X)")', () => {
		const desc = describeComponent(
			{ name: 'cli', status: 'ok', installed: true, version: '0.10.6', update_available: false },
			'v0.10.5', // even a (pre-clamp) lower latest must not leak into the ok row
		);
		expect(desc).toBe('v0.10.6 — up to date');
		expect(desc).not.toContain('latest');
	});

	it('renders outdated with the fix command', () => {
		const desc = describeComponent(
			{ name: 'app', status: 'outdated', installed: true, version: '0.10.3', update_available: true, fix: 'brew upgrade --cask apresai/tap/secondbrain' },
			'v0.10.4',
		);
		expect(desc).toContain('update available');
		expect(desc).toContain('brew upgrade --cask apresai/tap/secondbrain');
	});

	it('renders missing with the install fix', () => {
		const desc = describeComponent(
			{ name: 'app', status: 'missing', installed: false, update_available: false, fix: 'brew install --cask apresai/tap/secondbrain' },
			'v0.10.4',
		);
		expect(desc).toContain('not installed');
		expect(desc).toContain('brew install --cask apresai/tap/secondbrain');
	});

	it('renders n/a as macOS-only', () => {
		expect(describeComponent({ name: 'app', status: 'n/a', installed: false, update_available: false }, 'v0.10.4'))
			.toContain('macOS app only');
	});

	it('renders unknown with the hint when no version', () => {
		expect(describeComponent({ name: 'plugin', status: 'unknown', installed: false, update_available: false, fix: 'open a vault' }, undefined))
			.toContain('open a vault');
	});
});

describe('parseAskResponse', () => {
	// Regression guard for the Ask-AI source contract bug: the CLI emits
	// `sources` as a plain string[] of vault-relative paths, not objects. The
	// old modal did `filepathBase(source.path)` which threw `undefined.split`
	// on a string. This test reproduces the exact render path (one chip label
	// per source via filepathBase) and asserts it neither throws nor loses data.
	it('parses real CLI JSON with string sources usable for one chip per path', () => {
		const json =
			'{"mode":"hybrid","warnings":[],"answer":"hi","sources":["a.md","notes/b.md"]}';

		const response = parseAskResponse(json);

		expect(response.answer).toBe('hi');
		expect(response.mode).toBe('hybrid');
		expect(response.sources).toEqual(['a.md', 'notes/b.md']);

		// Exactly the modal's chip-labeling logic. If `sources` ever reverts to
		// objects, filepathBase(obj) throws here and this test fails.
		let labels: string[] = [];
		expect(() => {
			labels = response.sources.map(filepathBase);
		}).not.toThrow();
		expect(labels).toEqual(['a.md', 'b.md']);
	});

	it('dedupes sources on the path string (modal behavior)', () => {
		const json =
			'{"mode":"hybrid","warnings":[],"answer":"x","sources":["a.md","a.md","b.md"]}';
		const response = parseAskResponse(json);

		const seen = new Set<string>();
		const chips: string[] = [];
		response.sources.forEach((source) => {
			if (seen.has(source)) return;
			seen.add(source);
			chips.push(filepathBase(source));
		});
		expect(chips).toEqual(['a.md', 'b.md']);
	});

	it('tolerates a missing sources field', () => {
		const json = '{"mode":"keyword","warnings":["embeddings unavailable"],"answer":"x"}';
		const response = parseAskResponse(json);
		expect(response.sources).toEqual([]);
		expect(response.warnings).toEqual(['embeddings unavailable']);
	});
});

describe('parseSearchResponse', () => {
	it('maps the {mode,warnings,results} envelope to the result list', () => {
		const json = JSON.stringify({
			mode: 'hybrid',
			warnings: [],
			results: [
				{
					doc_id: 'd1',
					path: 'notes/a.md',
					title: 'A',
					chunk_id: 'c1',
					heading_path: 'Intro',
					type: 'note',
					vector_score: 0.82,
				},
				{
					doc_id: 'd2',
					path: 'b.md',
					title: 'B',
					chunk_id: 'c2',
					type: 'adr',
				},
			],
		});

		const response = parseSearchResponse(json);
		expect(response.mode).toBe('hybrid');
		expect(response.results).toHaveLength(2);
		expect(response.results[0].path).toBe('notes/a.md');
		expect(response.results[0].vector_score).toBe(0.82);
		expect(response.results[1].type).toBe('adr');
	});

	it('returns an empty result list when results is missing', () => {
		const response = parseSearchResponse('{"mode":"keyword","warnings":[]}');
		expect(response.results).toEqual([]);
	});
});

describe('parsePolishResponse', () => {
	it('parses a full polish envelope and tolerates extra fields', () => {
		const json = JSON.stringify({
			path: 'note.md',
			original: 'teh original body',
			polished: 'The original body.',
			provider: 'bedrock',
			model: 'haiku',
			links_added: ['Auth Flow'],
			links_repaired: [{ raw: 'auth flow', new_target: 'Auth Flow' }],
			links_skipped: [{ raw: 'Nonexistent', reason: 'no_match' }],
			warning: '',
			duration_ms: 1234, // extra field must be ignored, not rejected
		});
		const r = parsePolishResponse(json);
		expect(r.path).toBe('note.md');
		expect(r.original).toBe('teh original body');
		expect(r.polished).toBe('The original body.');
		expect(r.provider).toBe('bedrock');
		expect(r.model).toBe('haiku');
		expect(r.links_added).toEqual(['Auth Flow']);
		expect(r.links_repaired).toEqual([{ raw: 'auth flow', new_target: 'Auth Flow' }]);
		expect(r.links_skipped).toEqual([{ raw: 'Nonexistent', reason: 'no_match' }]);
	});

	it('defaults missing fields rather than throwing', () => {
		const r = parsePolishResponse('{"path":"x.md"}');
		expect(r.original).toBe('');
		expect(r.polished).toBe('');
		expect(r.provider).toBe('');
		expect(r.links_added).toEqual([]);
		expect(r.links_repaired).toEqual([]);
		expect(r.links_skipped).toEqual([]);
		expect(r.warning).toBe('');
	});
});

describe('mcpSnippetFor', () => {
	const vault = '/Users/chad/dev/obsidian';
	const cli = '/opt/homebrew/bin/2nb';

	it('renders the legacy ~/.claude.json shape for claude-code (bare "2nb" + cwd)', () => {
		const snippet = mcpSnippetFor('claude-code', vault, cli);
		// Must match the legacy hardcoded shape: bare "2nb" command, mcp-server
		// arg, vault pinned via cwd (NOT --vault, NOT the absolute cliPath).
		expect(JSON.parse(snippet)).toEqual({
			mcpServers: {
				'2ndbrain': { command: '2nb', args: ['mcp-server'], cwd: vault },
			},
		});
		expect(snippet).not.toContain(cli);
	});

	it('uses the absolute cliPath (not bare "2nb") and no cwd for claude-desktop', () => {
		const snippet = mcpSnippetFor('claude-desktop', vault, cli);
		const parsed = JSON.parse(snippet);
		const entry = parsed.mcpServers['2ndbrain'];
		// GUI app, minimal PATH: absolute command, vault via --vault, no cwd.
		expect(entry.command).toBe(cli);
		expect(entry.command).not.toBe('2nb');
		expect(entry.args).toEqual(['mcp-server', '--vault', vault]);
		expect(entry.cwd).toBeUndefined();
		expect('working_directory' in entry).toBe(false);
	});

	it('emits the ~/.warp/.mcp.json shape with working_directory for warp', () => {
		const snippet = mcpSnippetFor('warp', vault, cli);
		expect(snippet).toContain('working_directory');
		const entry = JSON.parse(snippet).mcpServers['2ndbrain'];
		expect(entry.working_directory).toBe(vault);
		expect(entry.args).toEqual(['mcp-server', '--vault', vault]);
	});

	it('emits a `codex mcp add` command line for codex (paths shell-quoted)', () => {
		const snippet = mcpSnippetFor('codex', vault, cli);
		// Paths with no spaces are still single-quoted (harmless, uniform).
		expect(snippet).toBe(`codex mcp add 2ndbrain -- '${cli}' mcp-server --vault '${vault}'`);
		expect(snippet.startsWith('codex mcp add')).toBe(true);
	});

	it('shell-quotes paths with spaces so the codex command stays copy-paste safe', () => {
		const spacedVault = '/path with spaces/vault';
		const spacedCli = '/abs path/2nb';
		const snippet = mcpSnippetFor('codex', spacedVault, spacedCli);
		// Each spaced path must be wrapped in single quotes so the shell treats it
		// as one argument; an unquoted space would split the command and break it.
		expect(snippet).toContain(`'${spacedCli}'`);
		expect(snippet).toContain(`'${spacedVault}'`);
		expect(snippet).toBe(
			`codex mcp add 2ndbrain -- '${spacedCli}' mcp-server --vault '${spacedVault}'`,
		);
		// The bare (unquoted) path must NOT appear loose in the command.
		expect(snippet).not.toContain(`-- ${spacedCli} `);
	});

	it('escapes an embedded single quote the POSIX way', () => {
		const snippet = mcpSnippetFor('codex', "/Users/o'brien/vault", cli);
		// ' -> '\'' so the closing/opening quotes wrap an escaped literal quote.
		expect(snippet).toContain(`'/Users/o'\\''brien/vault'`);
	});

	it('defaults unknown clients to the claude-code JSON shape', () => {
		expect(mcpSnippetFor('mystery', vault, cli)).toBe(mcpSnippetFor('claude-code', vault, cli));
	});

	it('every MCP_CLIENTS key produces a non-empty snippet', () => {
		for (const c of MCP_CLIENTS) {
			expect(mcpSnippetFor(c.key, vault, cli).length).toBeGreaterThan(0);
		}
	});
});

describe('MCP_CLIENTS', () => {
	it('covers the four plugin-relevant clients with the right skill mapping', () => {
		const byKey = Object.fromEntries(MCP_CLIENTS.map((c) => [c.key, c]));
		expect(Object.keys(byKey).sort()).toEqual(['claude-code', 'claude-desktop', 'codex', 'warp']);
		// Claude Code + Warp + Codex ship a skill; Claude Desktop is MCP-only
		// (it shares Claude Code's skill folder).
		expect(byKey['claude-code'].skillSlug).toBe('claude-code');
		expect(byKey['warp'].skillSlug).toBe('warp');
		expect(byKey['codex'].skillSlug).toBe('codex');
		expect(byKey['claude-desktop'].skillSlug).toBeUndefined();
		expect(byKey['claude-desktop'].absoluteCliPath).toBe(true);
	});
});

describe('computeLineDiff', () => {
	// Invariant: context+del rows rejoin to the original; context+add to the new.
	const reconstruct = (rows: DiffRow[], keep: DiffRow['type'][]) =>
		rows.filter((r) => keep.includes(r.type)).map((r) => r.text).join('\n');

	it('marks every row as context for identical input', () => {
		const rows = computeLineDiff('a\nb\nc', 'a\nb\nc');
		expect(rows.every((r) => r.type === 'context')).toBe(true);
	});

	it('reports a pure insertion as added rows, originals as context', () => {
		const rows = computeLineDiff('a\nc', 'a\nb\nc');
		expect(rows.filter((r) => r.type === 'add').map((r) => r.text)).toEqual(['b']);
		expect(rows.filter((r) => r.type === 'del')).toHaveLength(0);
	});

	it('reports a pure deletion as removed rows', () => {
		const rows = computeLineDiff('a\nb\nc', 'a\nc');
		expect(rows.filter((r) => r.type === 'del').map((r) => r.text)).toEqual(['b']);
		expect(rows.filter((r) => r.type === 'add')).toHaveLength(0);
	});

	it('reports a one-line replacement as exactly one del + one add', () => {
		const rows = computeLineDiff('teh quick brown fox', 'The quick brown fox');
		expect(rows.filter((r) => r.type === 'del')).toHaveLength(1);
		expect(rows.filter((r) => r.type === 'add')).toHaveLength(1);
	});

	it('preserves the reconstruction invariant across a block edit', () => {
		const a = 'intro\nold one\nold two\nshared\ntail';
		const b = 'intro\nnew one\nshared\nnew tail\nextra';
		const rows = computeLineDiff(a, b);
		expect(reconstruct(rows, ['context', 'del'])).toBe(a);
		expect(reconstruct(rows, ['context', 'add'])).toBe(b);
	});

	it('handles empty-vs-text and text-vs-empty', () => {
		expect(computeLineDiff('', 'a\nb').filter((r) => r.type === 'add')).toHaveLength(2);
		expect(computeLineDiff('a\nb', '').filter((r) => r.type === 'del')).toHaveLength(2);
	});

	it('falls back to a plain del+add block for pathologically large inputs', () => {
		// n*m must exceed 4_000_000 to trigger the fallback (2001*2001 ≈ 4.004M).
		const a = Array.from({ length: 2001 }, (_, i) => `a${i}`).join('\n');
		const b = Array.from({ length: 2001 }, (_, i) => `b${i}`).join('\n');
		const rows = computeLineDiff(a, b);
		expect(rows.filter((r) => r.type === 'context')).toHaveLength(0);
		expect(rows.filter((r) => r.type === 'del')).toHaveLength(2001);
		expect(rows.filter((r) => r.type === 'add')).toHaveLength(2001);
		// The fallback still satisfies the reconstruction invariant.
		expect(reconstruct(rows, ['context', 'del'])).toBe(a);
		expect(reconstruct(rows, ['context', 'add'])).toBe(b);
	});
});
