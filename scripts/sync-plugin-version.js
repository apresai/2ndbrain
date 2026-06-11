#!/usr/bin/env node
// Sync the Obsidian plugin's version fields (manifest.json, package.json,
// package-lock.json) from the root VERSION file. Invoked by
// `make version-plugin` (and through it every bump-* / set-version target).
//
// Refuses to LOWER the plugin version: Obsidian and BRAT only treat version
// increases as updates, so an accidental downgrade (e.g. a plain bump-build
// while the plugin sits above the product line) would ship a release that
// existing plugin users never receive. Override only for deliberate repair
// work with FORCE_PLUGIN_DOWNGRADE=1.
'use strict';
const fs = require('fs');

const version = fs.readFileSync('VERSION', 'utf8').trim();
const manifestPath = 'plugins/obsidian-2ndbrain/manifest.json';
const files = [manifestPath, 'plugins/obsidian-2ndbrain/package.json'];
const lockPath = 'plugins/obsidian-2ndbrain/package-lock.json';

function parse(v) {
	const parts = String(v).split('.').map(Number);
	return parts.length === 3 && parts.every(Number.isFinite) ? parts : null;
}

const next = parse(version);
if (!next) {
	console.error(`sync-plugin-version: VERSION ${JSON.stringify(version)} is not x.y.z`);
	process.exit(1);
}

const current = parse(JSON.parse(fs.readFileSync(manifestPath, 'utf8')).version);
if (current && !process.env.FORCE_PLUGIN_DOWNGRADE) {
	for (let i = 0; i < 3; i++) {
		if (next[i] > current[i]) break;
		if (next[i] < current[i]) {
			console.error(
				`sync-plugin-version: refusing to lower the plugin version ${current.join('.')} -> ${version}.\n` +
					`Obsidian/BRAT users on ${current.join('.')} would never see this release as an update.\n` +
					`Pick a higher version (e.g. make set-version V=x.y.z) or, to force, FORCE_PLUGIN_DOWNGRADE=1.`
			);
			process.exit(1);
		}
	}
}

for (const f of files) {
	const j = JSON.parse(fs.readFileSync(f, 'utf8'));
	j.version = version;
	fs.writeFileSync(f, JSON.stringify(j, null, 2) + '\n');
}
if (fs.existsSync(lockPath)) {
	const j = JSON.parse(fs.readFileSync(lockPath, 'utf8'));
	j.version = version;
	if (j.packages && j.packages['']) j.packages[''].version = version;
	fs.writeFileSync(lockPath, JSON.stringify(j, null, 2) + '\n');
}
console.log('Plugin version:', version);
