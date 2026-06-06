---
id: a4b27656-bebe-4bda-849d-bb7cc3bc1b57
title: "Migration Guide: Legacy 2nb to Obsidian-Native"
type: note
status: complete
---

# Migration Guide: Legacy 2nb to Obsidian-Native

This guide walks you through transitioning a legacy, UUID-first 2ndbrain vault to the version 0.5.0 Obsidian-native path-based layout.

## Do I Need to Migrate?

* **Yes:** If you have an existing vault initialized with 2ndbrain version 0.4.x or earlier that contains a `.2ndbrain/` folder and database, you must run the migration.
* **No:** If you are pointing 2ndbrain version 0.5.0 at a standard Obsidian vault that has never been processed by 2ndbrain before, simply run `2nb index` to generate the sidecar.

---

## Migration Walkthrough

The migration command updates the database structure to support path-based identity and configures the gitignore sidecar.

### 1. Dry-Run / Preview (Recommended)
Run the migration command with the `--dry-run` flag to preview the changes without committing them:

```bash
2nb migrate --vault /path/to/my-vault --dry-run
```

**Expected output:**
```
[dry-run] Scanning legacy vault at: /path/to/my-vault
[dry-run] Detected legacy database (schema v2)
[dry-run] 120 files identified for path-based mapping
[dry-run] Append ".2ndbrain/" to root .gitignore
[dry-run] Safe to proceed. No files will be modified.
```

### 2. Execute Migration
Run the command without the dry-run flag to apply the updates:

```bash
2nb migrate --vault /path/to/my-vault
```

**Expected output:**
```
Scanning legacy vault at: /path/to/my-vault
Upgrading database schema v2 to v3... Done
Ensured ".2ndbrain/" is listed in the root .gitignore
Migration complete. Run "2nb index" to rebuild the index and refresh embeddings.
```

---

## What Changes and What Is Preserved

### Preserved (Byte-Identical)
* All user markdown files (`*.md`) are preserved exactly as they are. 2ndbrain will not strip, edit, or modify any lines of text in your files during this migration.
* Frontmatter variables (including existing `id`, `type`, and `status` properties) are left untouched.

### Changed (Sidecar Reorganization)
* The SQLite database schema is updated to version 3 (adds the `aliases` table and `block_id` columns).
* The `.gitignore` file is updated to ensure the `.2ndbrain/` folder is excluded from version control.

Document identity is unchanged: 2ndbrain identifies documents by their vault-relative path (with a surrogate UUID generated at index time only for files that lack an `id`). After migrating, run `2nb index` to rebuild chunks, links, aliases, and embeddings.

---

## Safety and Rollback

### Pre-migration Backup
Before running the migration, create a backup copy of your database:

```bash
cp -r /path/to/my-vault/.2ndbrain /path/to/my-vault/.2ndbrain-backup
```

### Rollback Steps
If you need to revert the migration, restore the backup:

```bash
rm -rf /path/to/my-vault/.2ndbrain
mv /path/to/my-vault/.2ndbrain-backup /path/to/my-vault/.2ndbrain
```
