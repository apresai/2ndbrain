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
	};
});

import {
	filepathBase,
	resolveCliPath,
	parseAskResponse,
	parseSearchResponse,
} from '../main.ts';

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
