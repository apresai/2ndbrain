# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed
- **`2nb obsidian migrate-properties` added a time of day to dates that never had one.** A property written as a plain day, like `incident-date: "2026-07-14"`, came back as `2026-07-14T00:00:00Z`: a midnight nobody typed, on a value that was only supposed to have its quotes removed. Obsidian then shows that property as **Date and time** instead of **Date**. This affects only properties your own `.2ndbrain/schemas.yaml` declares as `date`, since `created` and `modified` are always written by 2nb as a full timestamp, so most vaults have nothing to repair. **This shipped in 0.23.0.** If you already ran the migration, `2nb polish <path> --undo` puts an affected note back the way it was, and rerunning the migration on 0.23.1 rewrites it correctly. `2nb meta --set` and the MCP `kb_update_meta` had the same fault on the same fields and are fixed with it
- A date property carrying a time but no timezone (`2026-07-19T17:07:29`, the shape Obsidian's own datetime editor writes) is still normalized to `2026-07-19T17:07:29Z` rather than kept as typed. That is deliberate: it states the timezone 2nb was already assuming, it does not change how Obsidian types the property, and it is the only spelling that settles after one pass. Kept as typed, the migration would rewrite the same note on every run and `2nb obsidian register-types` would stay blocked behind it forever

## [0.23.0] - 2026-09-04

This release is about one thing: **your notes now look right in Obsidian.** Every date 2nb ever wrote showed up in the Properties panel as plain text, with no date picker, no date sorting and no date-based query. That is fixed for new notes, there is a one-command repair for the notes you already have, and `2nb index` stops complaining about your template files. After upgrading, run `2nb index` once: dates that never parsed before now do, so `2nb stale` and `2nb list --sort modified` change for the better.

### Added
- **`2nb obsidian migrate-properties`** repairs the notes you already have, rewriting every quoted date into the plain form Obsidian reads as **Date and time**. It previews by default, so you see exactly what would change before anything is written. With `--write` it snapshots each note first, so `2nb polish <path> --undo` puts one back. It touches only `created`, `modified`, and any property your own `.2ndbrain/schemas.yaml` declares as a date; date-shaped properties you author are counted, reported, and left completely alone, because how you spell your own `date` field is your call. Everything else in a note comes back byte for byte, property order and comments included. Exactly two things change beyond the dates themselves, and the preview names both before you write anything: a comment sitting on the end of a date line it rewrites is not carried over, and if one of those dates was shared with another property through a YAML anchor, that other property is written out with the value spelled in full. A value that is not a real date is named and skipped rather than guessed at, and running it twice does nothing the second time
- **`2nb obsidian register-types`** tells Obsidian what kind each property is, so the right editor shows up even on a note where the property is empty. This is the one and only Obsidian setting 2nb will ever write, and only when you ask: it merges into `.obsidian/types.json` rather than replacing it (anything already declared keeps the type it has, and any other setting stored in that file comes back untouched), copies the old file into `.2ndbrain/recovery/obsidian/` first, refuses if Obsidian currently has the vault open unless you pass `--force`, refuses a `types.json` it cannot read, and refuses until you have run the migration. `id` is deliberately left undeclared: it is a UUID nobody reads, and declaring it would add a visible row to every note that has one

### Fixed
- **Every date 2nb wrote showed as plain Text in Obsidian.** Obsidian decides a property's type from how its value is written, and a date in quotes is text. 2nb quoted every one of them, so `created` and `modified` had no date picker, could not sort, and could not be queried by date. On one real vault that was 390 values across 195 notes. New notes are written the way Obsidian expects, and `2nb meta --set` and the MCP `kb_update_meta` can no longer quietly undo it on a note you edit later
- **A date written by Obsidian's own date picker was unreadable to `2nb stale`.** Obsidian writes `2026-09-04T12:34:56` with no timezone, and that exact spelling is one the YAML library does not recognize as a date, so it landed in the index as raw text: `2nb stale` reported the note as 0 days old forever, and `2nb list --sort modified` was ordering on text. A quoted `created: "2026-09-04"`, which is a shape 2nb itself wrote, had the same problem. Every ISO spelling now reads as the same instant, whatever separator, precision or timezone it carries, in quotes or out of them. A date you write in some other convention entirely, like `09/04/2026`, is still not read as a date and is still left exactly as you typed it
- **`2nb index` reported your template files as broken, on every single run.** A template's frontmatter holds `{{date}}` placeholders that are deliberately not valid YAML, and 2nb only knew to skip them if you had explicitly saved Obsidian's Templates folder setting. Most people never open that setting, so the exclusion never fired for them. A top-level `templates/` folder is now recognized on its own, provided Obsidian's Templates plugin is turned on and the folder actually exists. If you keep real notes in a folder named `templates` and do not use that plugin, nothing changes for you
- **A template could block a legitimate `2nb move`, and pollute link repair.** Templates were excluded from the index but not from the link resolver, so a template sharing a title with a real note made `[[that title]]` look ambiguous and `2nb move` refused it, and template files turned up as repair candidates in `lint`, `repair-links`, `relink` and `suggest-target`
- **A purge is no longer silent.** When a folder becomes a template folder, the notes already indexed under it are removed from the index. That was reported only in `--json`; it now prints in the ordinary output of the run that does it

## [0.22.4] - 2026-09-04

This release is mostly about one thing: **2nb was rewriting parts of your notes it had no business touching.** In three
cases that cost you content or left a note unreadable. If you keep horizontal rules in your notes, or tags that look
like dates or numbers, reindex after upgrading and check those notes first.

### Fixed
- **A note's whole body could be destroyed by an unrelated edit.** 2nb ended a note's properties at the first line beginning `---` anywhere in the file, without checking it was the end of the file, so an ordinary markdown horizontal rule (`----`, or a longer one), a closing `---` carrying a trailing space, or a line like `---more` cut the note short: everything below it was read as if it were not there. That alone would have been a display bug, but 2nb rewrites the whole file from what it read, so the next `2nb meta --set` or `2nb tag add` on that note SAVED the truncated version and the rest of your writing was gone. A fence line is now the three dashes and nothing else, at both ends of the block; a trailing space, tab, non-breaking space or zero-width character is still fine, since none of them is visible in an editor and 2nb already treats them that way on a blank line. Anything else on the line is your text, and a note whose properties are never closed is read as having no properties rather than as having no body. **This shipped in 0.22.3 and earlier.**
- **`2nb tag add` and `2nb tag remove` deleted your other tags.** A tag written without quotes that YAML reads as a date, a number or a true/false (`tags: [2026-09-04, 42, real]`) was invisible to the tag commands, which then wrote back only the tags they could see: one `2nb tag add extra` left the file holding `[real, extra]` and the other two were gone. Every tag survives now, and `2nb tags rename` reads the same way
- **An anchored property made a note unreadable when you edited it.** YAML lets a note reuse a value by naming it (`status: &s draft` with `other: *s`), and editing the named property left the reference behind with nothing to point at, so the file stopped being valid YAML: the note failed every read after that, disappeared from search, and `2nb meta` and `2nb tag` on it errored out. The reference is now resolved to the value it was standing for, so the other property keeps what it meant and the note stays readable

- **A frontmatter-only command now touches only the property you named.** Every other line in the block comes back byte for byte: its spelling, its quoting, its list style, any comment on it, and the fence lines above and below. Before this an unrelated `2nb meta --set status=complete` rewrote the lot, turning `modified: 2020-01-01` into `2020-01-01T00:00:00Z`, `id: 007` into `7`, `3.50` into `3.5`, a `[a, b]` list into a bulleted one, and dropping a trailing `# comment`. A note that uses Windows line endings keeps them too; an edit used to leave the properties with Unix endings and the body with Windows ones

- **A date written by Obsidian's own Date property was invisible to `2nb stale`.** Obsidian writes a date property unquoted (`modified: 2020-01-01T00:00:00Z`), and 2nb read only the quoted form its own `create` and `meta` commands write. The unquoted form was not rejected or reported: it simply left the note's created and modified dates EMPTY in the index, and `2nb stale` skips a note with no date, so the note never appeared however old it was. Notes 2nb created worked; notes you edited by hand in Obsidian, or imported from anywhere else, silently did not. Both forms now read as the same instant, in the index, in `2nb stale`, in `2nb list --sort modified`, and in `2nb meta --get modified`, which used to print Go's internal date format for the unquoted form
- **A title, type, status, id or tag written without quotes went missing.** A note whose title is a bare date or a bare number (`title: 2026-09-04`, the shape a daily note takes) was indexed with an EMPTY title, so it could not be found by name and showed up nameless in every listing, and a tag or alias written as a bare date, number or boolean was dropped from the note entirely. These fields keep the text your file actually shows: `title: 2026-09-04` is the title `2026-09-04`, the tag `- 2026-09-04` is the tag `2026-09-04`, and `id: 007` keeps its leading zeros. Only the date properties are normalized, because `stale` and `list --sort modified` have to compare instants
- **A property set to nothing became the word "null".** `title:` with no value, or `title: null`, or `title: ~`, indexed as the literal text, so a note with an emptied property was titled "null" in every listing and a blank entry in a tag list became a tag called "null". An empty property is empty

- **Seventeen commands ignored `--json` and `--format`.** Eight of them printed NOTHING at all to standard output whatever format you asked for, so a script saw a successful command with nothing to read: `2nb models add|remove|enable|disable|enable-state`, `2nb metrics clear`, `2nb config set` and `2nb vault create`. The other nine printed their usual human text for every format, so piping any of them to `jq` failed to parse on a command that reported success: `2nb migrate` (including `--dry-run`), `2nb mcp-setup`, `2nb vault set`, `2nb skills install`, `2nb skills uninstall`, `2nb skills show`, `2nb config get` and `2nb models bench fav|unfav`. All of them now render the format you asked for, or refuse it by name when there is nothing of that shape to render. `2nb skills show` is a document body like `2nb git diff`: the default form and `--format raw`/`md`/`text` emit the SKILL.md markdown, `--json` wraps it in a record, and the table formats are refused. `2nb models bench favs|history|compare` on an empty set emitted the bare word `null` instead of `[]`, like every other empty listing already does
- **A one-value machine format wrapped its value in quotes twice.** `2nb config get ai.provider --format csv` emitted `"""bedrock"""`, so a consumer had to strip CSV quoting and then JSON-unquote to read one word. A single scalar is now one plain cell; a map or a record is still compact JSON in a cell, which is what CSV output promises
- **`2nb models list --csv` and `--format tsv` printed `<nil>` in empty cells.** Sixteen rows of a 91-row catalog carried the literal four-character token, in the reachable, credentials and benchmark columns, where the same fields under `--json` are `null`. A spreadsheet or a dataframe read it as the text "<nil>". Those cells are now empty
- **`2nb models enable-state --help` did not say `--state` is required**, though the command refuses without it and `--provider` beside it is marked
- **A single-note reindex could fail with a database constraint error under load.** When another program held the index open at the wrong moment (the macOS app or the Obsidian plugin reading while the CLI reindexes one note, which is the normal state of this product), 2nb treated the momentary contention as "this note has never been indexed", gave it a new internal id, and then failed on a uniqueness constraint reporting a message about the constraint rather than the contention. It now waits and retries, which is what it does everywhere else contention can happen

### Changed
- `IndexGeneration` 3 to 4. **A reindex is recommended after upgrading** (`2nb index`), for two reasons. Notes whose dates, titles, types, statuses or tags were written unquoted now carry those values in the index, and that is a metadata-only pass: no embedding is regenerated for it, because the date and text fixes cannot change a note's text. Notes whose body was TRUNCATED by a bad closing fence are the exception: their text really did change (it was cut short before, and is whole now), so those notes are re-chunked and re-embedded, at whatever your embedding model charges. That is a small subset, only the notes that actually carried a horizontal rule or a fence with a trailing space, not a whole-vault rebuild, and it is the point of the pass: those notes were indexed from text your file does not contain. `2nb vault status` and `2nb ai status` prompt for it until you run it

## [0.22.3] - 2026-09-04

### Fixed
- **One bad note could throw away a whole re-embed.** A note whose frontmatter would not parse, or that could not be opened at all (a permission bit, a file another program held mid-save), counted as a FAILED embedding, and `2nb index --force-reembed` treats any failure as an incomplete run, so it restored the previous embeddings and exited non-zero. On a 314-note vault that meant three rebuilds in a row that each embedded 313 documents over 70 to 90 seconds and then discarded all 313. Nothing is ever sent to the provider for such a note, so neither kind counts as an embedding failure any more: the rebuild finishes, keeps every embedding you paid for, and names the notes to fix, in the `--force-reembed` summary, in `index --json` (`unparseable` and `unreadable`), and in the MCP `kb_index` result. A run that could not open a note still exits non-zero, because there is real work left, but it no longer undoes the work it did. A genuine provider failure is unchanged and still rolls back. Every command that indexes reports the same summary now, not just `2nb index`: the automatic first index behind `2nb search`, and `2nb import-obsidian`, used to say nothing at all about a note they skipped. `2nb index --doc` on a single broken note still fails loudly, because an editor saving one note needs to hear that it did not land, and the macOS app now shows that failure beside the Index state instead of only writing it to a debug log
- **A note that could not be READ was treated as a broken note, and deleted from the index.** A permission bit, a file locked mid-save by another program, or any transient I/O error made `2nb index` drop that note's index row, its chunks and its embeddings, because every failure to open a file was classified as a failure to parse it. The two are now told apart: a note whose CONTENT will not parse is still reported and dropped, while a note that merely could not be opened keeps everything it had, is listed separately under `unreadable`, and is retried on the next index. Re-embedding a note costs money, so this one could cost you real spend as well as search results
- **A note with an empty properties block would not parse, and one written the same way could lose its properties or its body.** A note that opens with `---` immediately followed by `---` has empty frontmatter, which is what Obsidian writes when you remove a note's last property, and what 2nb itself writes for an empty property set. 2nb searched past the empty block for a closing `---`, found the next horizontal rule in the body instead, and tried to read your prose as YAML: such a note failed on every index and every embedding pass, and it is the note that caused the rebuild failure above. Properties now end where they actually end. Properties are a contiguous block: 2nb reads the lines between the two `---` markers as properties only when there is no blank line among them, which is what tells a note's real properties apart from prose that happens to contain a colon. So a note holding a line like `Status: draft` above a horizontal rule keeps that line as text, and a note whose real title and tags start a line below the marker keeps them as properties. Before this, `2nb meta` and `2nb tag add` on the first kind REWROTE it on disk, lifting those lines out of the visible body and into a properties block and rewriting a value on the way (a `Status: 2026-09-04` line came back as `Status: 2026-09-04T00:00:00Z`), and on the second kind they threw the title and tags into the body instead. A blank line counts as blank whatever invisible character an editor left on it: a space, a tab, or a zero-width character you cannot see at all. Before this, a single tab on that line was enough to lose a note's real title and tags into its body on the next `2nb tag add`. Windows line endings are preserved exactly, and a note that ends right at its closing `---` is read the same way. 2nb also stopped WRITING the shape it would then misread: a note it saved with no properties and a body beginning `Status: draft` above a horizontal rule was read straight back with that line gone from the body and turned into a property, so an empty properties block is now always separated from the body by a blank line. Run `2nb index` once after upgrading, so notes written this way are indexed from the right text (see Changed)
- **`2nb models list --discover` and the app's Models tab re-walked the whole vendor catalog every time.** Both Bedrock planes across three regions, on every invocation, for a catalog that changes when AWS ships a model: about 11 seconds per reload of the Models tab. Discovery is now served from the same 24-hour cache `2nb models discover` reads, and says how old that answer is. `--refresh` re-walks the vendor APIs when you want the pool itself re-checked, on both `models list` and `models cost-preview`; the Validate pane's Refresh button already did
- **A provider you turned off was still queried, and still warned about it.** With OpenRouter disabled, one machine logged 19 "vendor discovery failed" warnings in a day, one per Models-tab refresh, about a provider whose models were then discarded from the result anyway. A disabled provider is now skipped entirely: no call, no warning. The setup wizard still shows them, since that is where you turn one back on
- **Obsidian's template folder is no longer indexed.** Templates are written with placeholders like `{{date}}` that the Templates plugin and Templater expand when you insert them, which is not valid YAML, so 2nb failed to parse them on every single run: one vault logged three "index file failed" lines per index, forever. 2nb now reads the template folder from Obsidian's own settings (and Templater's) and leaves it alone; `2nb vault status` names whatever it is excluding. Notes already indexed under a folder you later mark as your template folder are removed from the index too, and the run reports how many: marking the folder used to stop new files being indexed while everything already there went on appearing in search and in `2nb vault status` counts indefinitely, with no way to clear them short of deleting the index. A folder whose name merely starts the same way (`templates-archive` next to `templates`) is unaffected, and a vault that has never configured a template folder indexes exactly what it did before
- **A model's built-in facts still lost to a stale copy on a region-pinned row.** 0.22.2 made your own values win only where you actually typed them, but that rule only applied to catalog rows that matched a built-in row exactly. A row written by a probe that pinned a plane and a region matched nothing, so its stale copies stood: a real vault showed Nova with context length 2048 and a similarity threshold of 0.65 in `models list --json` while `ai status` correctly ignored the 0.65 and told you where it came from, so the two commands disagreed about the same model. The built-in values now win on every row of a model 2nb ships, whatever endpoint that row names, and the threshold shown is the one search actually uses
- **`index --json` disagreed with what the same run printed.** The JSON was built from the file-scanning phase alone, so a note that only failed while embeddings were being generated appeared in the terminal output and nowhere in the JSON. Both phases now feed one merged list, and the JSON carries the embedding counts (`embedded`, `embed_failed`, `embed_skipped`, `embed_retries`) it never exposed. Every key is always present, so a tool no longer has to tell "absent" apart from "zero"
- **The index summary printed every skipped note and then told you to re-run with `-v` to see them.** It now names up to five and mentions `-v` only when it actually held some back
- **Retries were counted for the terminal but not for agents.** Work driven through the MCP server recorded zero provider retries in `2nb metrics`, however many it actually rode out, so the retry history had a hole exactly the shape of agent-driven indexing and search. It counts them now, per call
- **The waiting line could promise more patience than the provider had.** It always said "retry N of 5", but calls to partner-hosted models give up after three, so a wait that was nearly over looked like it had two rounds left. It now quotes the real limit for the call in progress
- **`2nb move` could silently repoint a link instead of refusing.** The guard that stops an ambiguous move checked the note's FILENAME, while the docs promised it checked the name. Those are different: `2nb create` slugs the filename and keeps your title verbatim, so two notes you called "Dup" are `one/dup.md` and `two/dup.md`, and a `[[Dup]]` link names neither file. Such a link resolves to nothing, so it was not a backlink either, and the guard saw no ambiguity at all: the move went through, reported nothing skipped, and afterwards `[[Dup]]` pointed at the note that was NOT moved. The guard now asks the same resolver `lint` and the link repair tools use, so a link that names the moved note by a shared title or alias is reported by `--dry-run` and refuses a move without `--force`, exactly like a shared filename does. A unique name still moves without a word, and `--force` rewrites the same links it always did
- **`--format text` printed a Go struct dump.** `2nb list --format text` emitted `{cb9316a1-… resources/x.md Title note draft 2026-…}`: no field names, nothing to read, nothing to parse. `2nb folders` printed `{resources 5}`, `2nb models list` printed one such line per model, and `2nb config show` printed a memory address where its configuration should be. A list now renders one line per row of `name=value` pairs, a single record one `name: value` per line, and a map sorted by key, using the same field names as `--json`; empty and unset fields are left out instead of showing as gaps or `<nil>`. `text` is for reading: values are not quoted or escaped, so use `--format tsv` or `--json` when something else has to parse the output. A pointer field also stopped printing as a memory address in `--format csv` and `--format tsv`
- **`2nb export-context` ignored `--format` entirely.** `--json`, `--csv` and `--yaml` all printed the plain markdown bundle and exited 0, so piping the JSON form to `jq` failed to parse on a command that reported success. The bundle is a document body, so it behaves like `git diff` now: the default form and `--format raw`/`md`/`text` emit it, `--json` wraps it in a `{bundle, docs, chars}` record, and `--csv`/`--tsv`/`--yaml` are refused by name because a bundle is not a table. The bundle's own "N documents included" line also counts what it actually contains: a note whose file cannot be read is skipped with a warning, and the header used to count it anyway
- **An empty listing was `null` under `--yaml` and corrupt under `--csv`.** `2nb orphans --yaml` on a vault with no orphans printed `null` where `--json` printed `[]`, and `2nb tasks --yaml` printed `[]` for the same situation, so the same question got three different answers. Worse, `2nb orphans --format csv` printed the word `null` and `2nb tasks --format csv` printed `[]`: a JSON value in the middle of a CSV stream, which is what CSV output exists to avoid. An empty listing is now `[]` under `--yaml` and, under `--csv`/`--tsv`, the header row with no rows beneath it, so a spreadsheet or a dataframe still knows the columns. `2nb list --yaml` on an empty vault also stopped printing nothing at all
- **`--format text` and `--format csv` printed a Go type name where a field name belongs.** `2nb doctor --format text` printed one `SuiteStatus: {"latest":…}` blob instead of the fields inside it, and `2nb skills doctor --format text` printed a `Verification:` blob the same way, on the two commands most likely to be read that way. Both formats now flatten an embedded record exactly as `--json` always has, so the field names match across all three views, and a field `--json` hides no longer turns up in text or csv
- **`2nb move` counted one ambiguous link twice.** A note that wrote the same unresolved link twice had it reported twice by `--dry-run`, in the JSON, and in the "Skipped N ambiguous link(s)" line, and the refusal said two links where there was one. One link is one line

### Added
- **Slow AI calls now say what they are waiting for.** A throttled AWS account makes 2nb wait, sometimes for tens of seconds: measured on a real vault, a search whose query embedding took 10.8 seconds, single-note reindexes at 10 to 14 seconds, and a two-document embedding pass at 53.6 seconds, against 0.7 seconds for the same call on an unthrottled account. None of it was reported anywhere, so it looked like a hang. Any provider call still running after two seconds now prints one line naming what it is doing, how long it has waited, and, once a retry has actually happened, that the provider is throttling and which retry it is on. The line erases itself when the call returns, and appears only when the error stream is an interactive terminal, so it never lands in `--porcelain` output, in a log file, or anywhere a script is reading
- **`2nb metrics` counts provider retries.** Every retry is now recorded per operation (`embed_retries`, added to `.2ndbrain/metrics.db` without touching your existing history) and printed per build and in the aggregates, so a slow rebuild can be explained rather than guessed at: wall time alone cannot tell a slow model from a throttled account
- **The macOS app tells you when a note failed to index.** A note whose properties break as you save it dropped out of search silently, and stayed out until the next full index; the app wrote the reason to a debug log nobody reads. The Index section now names the note and the reason. A Rebuild names them too: a full index reports the notes it skipped rather than only the ones that failed while you were editing them, which matters because a note whose properties will not parse is dropped from the index and stops appearing in search, and the rebuild that dropped it still finishes successfully. The warning clears when that note indexes cleanly, when a rebuild finds nothing to report, or when you switch vaults, so it cannot outlive the note it is about or follow you into a vault that does not contain it. One broken note also no longer stops the rest of a batch of edits from being indexed
- **`2nb vault status` and `2nb ai status` say when your embedding concurrency is too high.** "N throttled retries in the last index (embed_concurrency is C, automatic is A); consider lowering ai.embed_concurrency", shown only when the last rebuild genuinely retried AND you had raised `ai.embed_concurrency` above the automatic value. At the automatic setting the throttling is your account's quota rather than a setting to change, so the line stays quiet

### Changed
- `IndexGeneration` 2 to 3. **A reindex is recommended after upgrading** (`2nb index`) so notes that open with an empty properties block are indexed from the text they actually contain; their stored embeddings were built from the old reading. Only those notes are re-read and re-embedded, so this costs a fraction of a full rebuild, and no re-embed of the whole vault is needed. `2nb vault status` and `2nb ai status` prompt for it until you run it

## [0.22.2] - 2026-09-03

### Fixed
- **A model's built-in facts could be frozen by a copy of themselves.** Every probe, promotion, benchmark or `models verify` run built its catalog row from the merged view of the catalog, so the built-in name, dimensions, context length, prices and curation were written into your own `models.yaml` as if you had typed them. From then on the copy won and no later correction to the built-in value could reach you: one real catalog reported Nova's context length as 2048 long after 2nb moved it to 8192, and that stale number spread to every per-region row `models discover` found next. Only `models add --name`, `--dimensions` and `--context-length` write those fields now, and each flag marks just the field it set; anything unmarked is treated as the copy it is, so an affected catalog heals itself the first time a probe writes to it and there is nothing to clean up. `models verify` and `models discover --validate` also stop writing a built-in context length into your file, which they did on every run. A model 2nb does not ship in its own catalog is unaffected: your row is the only copy of its facts, so it is kept whether or not it carries the mark. `models add` on a built-in model also stops writing the model id in as a name, which used to replace the real one
- **A saved similarity threshold now counts only when it is marked as yours.** 0.22.1 kept an unmarked value that differed from the built-in recommendation, on the theory that a difference meant a real measurement. It does not: a real vault carried 0.65, which is the value 2nb itself recommended for Nova until June 2026, and it differs from today's 0.25 only because the recommendation moved. At 0.65 an asymmetric embedding rejects every genuine match, so semantic search silently degraded to keyword-only while `ai status` reported it as a calibration nobody had taken. An unmarked value is ignored and the built-in recommendation applies. If you did measure one before 2nb started marking them, `2nb ai status` now prints a line naming the file, the value it ignored, and the two commands that save it back with provenance (`2nb models calibrate --save`, or `2nb models add <id> --similarity-threshold <v>`)
- **A semantic-only search result came back with no text.** A hit the keyword channel never returned carried an empty `content` with no `chunk_id` or `heading_path`, so the results semantic search exists to find were the ones with nothing to read, in `2nb search`, in `2nb search --json`, in MCP `kb_search`, and in the macOS app. Those rows now carry the first 200 characters of the matched section, with its id and heading, exactly as a keyword hit does. A note with no body still comes back with those fields empty, which is the honest answer
- **`--yaml` used its own field names.** It printed `modifiedat`, `daysstale`, `sourcepath` and `targetraw` where `--json` prints `modified_at`, `days_stale`, `source_path` and `target_raw`, so no consumer could read both views with one schema, and it printed every empty field the JSON view omits. `--yaml` is now the JSON view in YAML syntax: same names, same omissions, and a text value that looks like a number stays quoted. `2nb models list --yaml` gains the fields the JSON view computes as a result (`vendor`, `family`, `active`, `reachable`, `compatible`, `working`). The YAML files 2nb stores on disk (`models.yaml`, `config.yaml`, `schemas.yaml`) are unchanged
- **`2nb completion install` wrote to `~/.zshrc` even when that is not your shell config.** zsh reads `$ZDOTDIR/.zshrc` when `ZDOTDIR` is set, so anyone who sets it got a `~/.zshrc` their shell never sources, while the search for an existing completion directory read that same wrong file. The command now uses `$ZDOTDIR/.zshrc` when it is set and `~/.zshrc` otherwise, `--rc <path>` points it anywhere else, and `--no-rc` installs only the completion script and prints the two lines to add by hand. Whichever file it edits is copied to `<rc>.2nb-backup` before its first change; a re-run that changes nothing writes no backup

### Added
- Model catalog rows written by `models add --name`, `--dimensions` or `--context-length` carry an `authored_facts` list naming the fields you typed, which also appears in `models list --json`. It is what tells your own values apart from a copy of a built-in one, per field: an earlier attempt used a single marker for all three, so adding a context length claimed a name and a dimension count you never typed. Older 2nb versions ignore the new key

### Changed
- `2nb graph` takes a document path and always has, and returns that note's direct links only: one hop, not a traversal. `CLAUDE.md`, the agent skill and the CLI reference described it as the whole vault's link graph; the README already had the signature right. Use `2nb related <path> --depth N` to walk further

## [0.22.1] - 2026-09-03

### Fixed
- **`2nb models bench` reported the built-in similarity threshold as a calibration you had taken.** A benchmark, a probe, or a model promotion built its catalog row from the merged catalog, so the built-in recommendation was copied into your own `models.yaml`. `2nb ai status` then said "(user calibration)" for a number nobody measured, and that frozen copy shadowed any later correction to the built-in value. `models bench` defaults to `--summary-scope global`, so one run in one vault changed how every vault on the machine reported its threshold. `models calibrate --save` and `models add --similarity-threshold` are now the only two commands that can write the field, and both stamp it as yours. A value already in your catalog that is unstamped and identical to the built-in recommendation is treated as the copy it is, so an affected catalog heals itself the first time it is read, with nothing to clean up. A threshold you really did measure is kept, whether or not it predates the stamp. The macOS model picker's threshold field starts empty for the same reason: it used to be prefilled with the catalog's recommendation, so pressing Save without editing anything wrote a calibration you never chose. `ai status` also stops telling everyone with a high threshold to "clear the saved calibration": it names the file and row that carries one, or says the value is the model's own recommendation and there is nothing to clear
- **Reading a note's frontmatter was refused as if it were a write.** `2nb meta <path>`, `2nb meta <path> --get <key>`, and `2nb daily read` on a note that already exists all opened the write path, so on any vault Obsidian does not know they failed with "refusing to write" and demanded `--unconfigured`. The macOS app, the Obsidian plugin, and the MCP server pass `--vault` on every call, so this was easy to hit. Only actual writes are gated now: `meta --set` / `--remove`, creating today's daily note, and `daily append` / `daily prepend`. A `meta` flag combination that cannot run is also reported as a flag conflict instead of a vault refusal
- **`2nb migrate` told an up-to-date vault it was legacy.** Any vault with an index was announced as "Detected legacy database" plus "N files identified for path-based mapping", and the real run printed "Upgrading database schema to v3" while the current schema is v4. Nothing implements a path mapping; the schema has been path-based from the start. A vault already at the current schema now says so and exits without doing anything, in both modes; a genuinely older one is told what will actually happen. `migrate` also finds your vault the way every other command does, honoring `2NB_VAULT` and the vault Obsidian has open rather than only `--vault` and the current directory. The real run is gated like every other write, so a vault Obsidian does not know needs `--unconfigured` and a working directory that is only a vault by walking up is refused; `--dry-run` stays a plain read and needs neither
- **Listing notes by filter alone returned frontmatter JSON where the note's text should be.** `2nb search "type:adr"` and `2nb search "tag:security"` take the enumerate-by-filter path, and those rows carried the note's frontmatter blob as their `content` with `chunk_id` and `heading_path` empty. They now carry the first 200 characters of the note's first section, with that section's id and heading, so enumerating gives you readable text and something to cite
- `--format csv` and `--format tsv` rendered a row's `frontmatter` column as `map[]` on every row. Composite columns are compact JSON now, with sorted keys, so the column parses and a given row renders identically every time. A date column is plain RFC3339 text rather than Go's `2026-09-03 08:04:20 +0000 UTC`: any value that can render itself as text does so instead of going through the JSON encoder, which would quote it and leave `git activity --format csv` printing every date wrapped in doubled quotes
- **Fourteen commands quietly ignored `--format`.** `mcp status`, `mcp configured`, `git status`, `git activity`, `git show`, `index --doc`, `polish`, `polish --undo`, `relink`, `unlink`, `repair-links`, `suggest-links`, `suggest-target` and `config bedrock` honored `--json` and printed their human text for everything else, so `--csv`, `--tsv` and `--yaml` produced prose, and `--format raw`/`md` printed prose instead of the refusal 0.22.0 announced. All of them route every format through the shared writer now, so nothing is silently dropped (a payload that is not a list of rows renders in `csv`/`tsv` as a single JSON record, which is what that writer has always done for such a shape). `search` and `list` refuse `raw`/`md` up front, so the refusal no longer depends on whether the query happened to match anything, and never after paying for a query embedding, while `csv` and `tsv` keep returning an empty stream for zero rows. `suggest-links`, `suggest-target` and `polish` refuse the same pair the same way, before they ask a provider for anything, so a format that cannot render the result is never learned about after the call is paid for. `models bench`, `ai engine pull` and `ai engine rm` emit a stream of JSON events rather than one document, so they refuse any other format by name instead of ignoring it. `git diff` is the mirror image, since its output IS a body: it still emits the diff for `raw`, `md` and `text`, and `--json` still wraps it in a record, but `csv`, `tsv` and `yaml` are now refused by name rather than being handed the diff text, which was the same silent-wrong-shape problem in the other direction

### Added
- Search results carry a `frontmatter` field: the note's parsed frontmatter, on `2nb search --json` rows and on MCP `kb_search` rows alike, so metadata is available without a second read. It is additive and omitted when a note has none
- Model catalog rows written by `models calibrate --save` or `models add --similarity-threshold` carry a `threshold_source: user` stamp, which also appears in `models list --json`. Older 2nb versions ignore the new key

## [0.22.0] - 2026-09-03

### Fixed
- **`kb_replace_section` erased a section when its required `text` argument was missing, and reported success.** `ReplaceSection` treats empty replacement content as "replace the section with nothing", so an MCP client that omitted the argument destroyed the section's body and was told the write succeeded. Reproduced against 0.21.1 through a real MCP session: the section content was gone and the tool returned no error. It is refused now; passing an explicit empty string still clears a section, which is the deliberate form of that operation
- **Arguments an MCP tool declares required are now actually enforced, absent or blank.** Declaring `required` in an inputSchema constrains nothing at runtime: the transport passes whatever the client sent and a handler reading a missing argument gets the zero value. `kb_search` without a `query` returned EVERY document with `score: 0` dressed as ranked results, where `2nb search` refuses the same call. `kb_update_meta` without `fields` rewrote the note with the frontmatter it already had and reported success, so an agent believed it had written metadata that never changed. A whitespace-only value slipped past the `== ""` checks separately: on `kb_search` and `kb_ask` it reached FTS5 and leaked `SQL logic error: fts5: syntax error near ""` to the client, `kb_create` accepted a blank title and produced a note whose slug fell back to a UUID, and `kb_git_diff` accepted a blank path
- **A blank query is refused once, in the shared retrieval pipeline, rather than per surface.** `2nb search ""` had the same shape as the `kb_search` bug and was not covered by cobra's argument count, since one argument WAS supplied, just empty. That is what an empty shell variable produces, so the caller least able to notice got the most misleading answer. The guard lives in `internal/retrieve`, which exists precisely so the CLI and MCP paths cannot drift, and both of them plus `2nb ask` and `kb_ask` inherit it. An inline-filter-only query such as `2nb search "type:adr"` still enumerates that type, since an empty query with a filter is the documented enumerate-by-filter form and not a mistake. `2nb ask` refuses a blank question before opening a provider, so on a machine with no credentials the error names the actual problem rather than "no generation provider"
- **A brand-new vault told its user to pay for a re-embed it did not need.** The quick start that `2nb --help` prints (`vault create`, then `create`, then `index`) ended with `vault status` and `ai status` reporting "UPGRADE REEMBED RECOMMENDED: a newer 2nb improved chunking/embeddings for this vault", on a vault seconds old. `create` embeds inline without recording the logic generation, so by the time `index` ran there were already embeddings and no stamp, which is exactly what a vault from before the stamping mechanism looks like, and the check correctly refused to guess. A vault is now stamped at creation, and so is the empty index 2nb creates when it adopts an existing Obsidian vault for the first time, since an empty index is trivially current either way. A genuinely old vault, with embeddings and no stamp, is still flagged
- **The bare and `--porcelain` forms of the listing commands still printed `null` on an empty vault.** 0.21.1 fixed the explicit `--json` form of `tags`, `aliases`, `orphans`, `deadends` and `stale`, but those commands render JSON by default, and the fix matched only the `--json` flag. All three forms return `[]` now. `lint --json` on a clean vault likewise reports `issues: []` rather than `null`, which `jq '.issues[]'` rejected
- **A refused `move` no longer describes itself as done.** When the ambiguity guard stops a non-force move, the summary used to print "Moved: a.md -> b.md" and "Rewrote N link(s)" in the past tense before the error, on the one command whose write is the widest, so anyone reading stdout rather than the exit code concluded the move had happened. It now leads with "Refused: nothing was moved or rewritten", uses "Would move" phrasing, and the `--json` result carries `refused: true` so a machine consumer does not have to infer it from the exit code. The same summary also printed "Moved:" when the final rename itself failed; that case now says "Move FAILED", reports how many referencing notes had already been rewritten to the new path and so point at a file that is not there yet (or that none were changed), and carries `move_failed: true`
- `list --tag x` (or `--type`, `--status`) that matched nothing said "No documents yet. Create one with: 2nb create", on a vault holding hundreds of notes. It now says the filters matched nothing
- Every error message that began "Error: error:" has lost the duplicate. cobra prints the prefix itself and 52 call sites were adding their own
- `--format md` and `--format raw` on a value that has no document body (a search result list, a report) printed Go's internal rendering of the value, `[{uuid path title ...}]`, and exited 0. Those formats exist to emit a body; a value without one is now refused with a pointer at `--json`. `read --format md` and every other real body are unaffected, and `--format text` keeps its documented best-effort rendering. `read --chunk <heading> --format raw` now emits that section's text; on 0.21.1 it printed the same struct rendering
- An unknown `--format` value (a typo) silently fell through to JSON, changing the output shape without a word. It is refused now, with the valid set listed. `table` is refused too: it was accepted by name but nothing rendered it, so it fell through to JSON the same way
- **`--type`, `--status` and `--tag` were ignored by semantic search.** The filters were applied to the keyword leg in SQL, but the vector leg scores on embeddings alone and never saw them, so the moment rank fusion merged an unfiltered vector hit in, the filter was gone: `search --type adr` returned notes, `--status accepted` returned drafts, and a filter matching no document at all still returned results. Hybrid is the default mode, so this was the normal path whenever the semantic channel was healthy; `--bm25-only` was always correct. The vector candidates are now filtered on the same criteria, and the candidate pool is over-fetched when a filter is active so a selective filter does not starve the result set
- **A document that repeated a heading lost the earlier section from the index.** A chunk id is a hash of the document id and the heading path, and `chunks.id` is a primary key, so two sections sharing a path (a second `## Standup` under `# Log`, which is what every daily-note and meeting-note template produces) collided and the later one overwrote the earlier. That text was then absent from search and from RAG grounding, with no warning and a zero exit. Repeats now get distinct ids. The displayed heading path is unchanged, and the FIRST section keeps the id it already had, so only documents that actually repeat a heading are re-chunked

### Changed
- `EmbedGeneration` 2 to 3. **A re-embed is recommended after upgrading** (`2nb index --force-reembed`) so documents with repeated headings get their missing sections indexed. Vaults with no repeated headings are unaffected in content, but the generation stamp is what tells the CLI a vault predates the fix

## [0.21.1] - 2026-09-02

### Fixed
- **A search that matched nothing printed nothing at all to `--json`.** `2nb search --json` wrote zero bytes to stdout and exited 0 whenever a query had no hits, so piping it to `jq` failed on the ordinary case of a query that matched nothing. `2nb list --json` had the same early return and the same empty stdout when a filter matched no document. Both now emit a document: the full `{mode, warnings, results}` envelope, and `[]`
- The worst of that was silent degradation. The search envelope is also how a vault reports that its semantic channel has fallen back to keyword-only, so a degraded vault whose query happened to match nothing returned no `mode` and no `warnings` either, and an agent could not tell "nothing matched" from "semantic search is off". A zero-result search now carries both
- **Every `--json` listing returns `[]` for an empty result, never `null`.** `jq '.[]'` rejects `null`, so an empty vault or an unmatched filter could break a pipeline on nine commands: `stale`, `tags`, `aliases`, `orphans`, `deadends`, `unresolved`, `backlinks`, `links`, and the nested `edges` of `related` for a document with no links
- `warnings` is no longer omitted when empty, in `search --json` and `ask --json` alike. It was `omitempty`, so the documented `{mode, warnings, results}` envelope was really `{mode, results}` on every healthy query, and `.warnings[]` failed against the missing key. `sources` in the `ask` envelope is likewise `[]` rather than `null`. `rewritten_query` stays optional: it is present only on a multi-turn `ask` whose question was rewritten
- All of this is scoped to JSON. Every other format renders byte-for-byte what it rendered in 0.21.0, including the cases where that is nothing at all, because a literal `[]` in a csv or tsv stream would corrupt a consumer. Human output is unchanged too, with the empty-state hint staying on stderr

## [0.21.0] - 2026-09-01

### Added
- **Bedrock models are now identified by their route**, `id@plane/region`. The same model can be served on both Bedrock planes and in several US regions, and those endpoints have independent entitlement, so each is its own catalog entry. `2nb models discover` walks BOTH planes across us-east-1, us-east-2, and us-west-2 (listing is free and cached 24h) and names every route it finds; classic previously listed only your primary region, so a model served elsewhere was invisible. Measured on one real account, the old behavior discarded a route for 26 of 120 discovered models and hid 6 that exist in only one region
- Model slots name their route explicitly: `ai.{generation,embedding}_{plane,region}` and `ai.rerank.{plane,region}`. `2nb config set ai.generation_model xai.grok-4.6@mantle/us-west-2` writes all three keys at once, so a slot is never left half-routed. A bare model id still works when the model has one route

### Fixed
- MCP `tools/list` was a 24 KB payload of example-prompt essays, empty `annotations` objects, and empty `required` arrays (mcp-go always emits those). Descriptions are now short routing paragraphs, those empty fields are omitted on the wire, and the payload is under 16 KB. The same 22 `kb_*` tools; Claude Code still sees them. A Grok session that imports the Claude Code key `2ndbrain` still records `tool_count` 0: Grok prefixes tools as `<server>__<name>` and keeps only names matching `^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`, so `2ndbrain__kb_info` is dropped because it starts with a digit. `grok mcp doctor` does not prefix and still reports 22. Use the `twonb` snippet from `2nb mcp-setup`
- **A model reachable on more than one endpoint is no longer sent to the wrong one.** Naming a model that only exists on the mantle plane used to fall through to classic Converse, which answers `ValidationException: Invocation of model ID ... with on-demand throughput isn't supported`. Nothing warned first, because Bedrock readiness is a control-plane listing rather than a real call, so `ai status` reported the model reachable right up until a query failed. 2nb now refuses before sending anything and prints the `config set` command for each route to choose from
- A model listed on both planes kept only its classic entry and silently discarded the mantle one; a model listed in several regions kept only one of them. Both are preserved now
- `models discover` shows each row's region, so three genuinely different endpoints no longer render as three identical `classic` lines, and its freshness report lists every region walked instead of claiming one classic source while three were fetched
- A newly-listed model is announced as NEW once rather than once per route
- `models remove` reports when nothing matched and exits non-zero, instead of printing Removed for a route-qualified id it never found. `models test id@plane/region` probes that endpoint, so a mantle-discovered model no longer classic-probes into a 404. `models add` refuses a qualified form (it describes a model, not one endpoint); enable/disable accept one and apply it to every route of the model
- `models calibrate --save` no longer erases the stored verdict, price override, or enabled flag on that row
- An unrouted slot (a model with several endpoints and no pin) names the pick commands in `ask` and in `search --json` warnings, instead of a cause-free "provider not registered"

### Changed
- `2nb mcp-setup` lists all 22 tools (it still said 11) and prints a Grok `~/.grok/config.toml` snippet under `[mcp_servers.twonb]` (Grok rejects a server key that starts with a digit)
- A verify pass now records its verdict against the endpoint it actually probed, instead of clearing a region pin to signal "back to the default". That old self-heal cannot work when the region is part of a row's identity: clearing it wrote a second row rather than replacing one, and the stale pinned row won. Regaining access in your primary region now shows up as a fresh pass on that region's own row, which ranks above its siblings
- `models list` gained a ROUTE column, and `models verify` probes one best route per model by default (`--all-routes` for the full matrix), so a model with several endpoints does not silently multiply the number of billed probes
- The discovery cache version is bumped, so the first walk after upgrading re-fetches every plane and region. Cached entries written before this change hold rows with no route
- Benchmark history records plane and region, so two endpoints of one model compare as two rows
- `.2ndbrain/bench.db` migrates to schema 2 the first time any `2nb models bench` command opens it (`bench`, `fav`, `unfav`, `favs`, `history`, `compare`), adding plane and region columns to `runs`. Existing history is preserved. Runs recorded before the upgrade carry no route, so each is stamped once with the route your current config resolves for that model. That value is inferred, not measured: it keeps an old run grouped with its successors instead of splitting one model into a routed row and a route-less one, but a run measured against a different endpoint is now filed under today's route. The route rides along in `models bench history --json` and `models bench compare --json`, and the app's Benchmarks pane labels rows `model@plane/region`; the plain CLI tables print no route column
- Dependencies: the AWS SDK moved in range (`aws-sdk-go-v2` 1.43.8 to 1.45.1, `config` 1.32.39 to 1.33.2, `service/bedrock` 1.67.1 to 1.70.0, `bedrockruntime` 1.57.5 to 1.60.0, `bedrockagentruntime` 1.58.2 to 1.61.0) along with `smithy-go` 1.28.0 to 1.28.1 and `modernc.org/libc` 1.75.5 to 1.75.6, and transitives followed. The Obsidian plugin's build tooling moved too (`rolldown` 1.2.5 to 1.2.6 and its platform bindings); no plugin runtime dependency changed
- **Downgrade:** a catalog written by this version, then written by an older 0.20.x binary, loses its routes permanently. That older binary drops unknown YAML keys and replaces rows keyed on (provider, id). Recover with `2nb models discover`. Homebrew is the distribution; the blast radius is a catalog discover rebuilds. This is deliberate: a second catalog file plus a forever migration was the alternative, and it was rejected

## [0.20.1] - 2026-08-26

### Added
- The Go test suite now runs on every pull request (`.github/workflows/ci.yml`, macOS, `go vet` + `make -C cli test`) with no credentials in the environment. It previously ran nowhere but a maintainer's laptop at release time, which is why a transient provider blip was indistinguishable from a product failure

### Changed
- Dependencies: the AWS SDK moved in range (`aws-sdk-go-v2` 1.43.7 to 1.43.8, `config` 1.32.39, `service/bedrock` 1.66.7 to 1.67.1, `bedrockruntime` 1.57.5, `bedrockagentruntime` 1.58.2) along with `smithy-go` 1.27.8 to 1.28.0, and transitives followed

### Fixed
- The release pipeline's own test gate now runs. `.goreleaser.yaml` declared `cd cli && go test -short ./...` as a pre-release hook, but GoReleaser execs a hook without a shell, so `cd` ran as a program, exited 0, and the gate passed without compiling anything (both hooks "completed" in under 20ms on the last release, on a cold module cache). It uses `go -C cli` now. This is only safe to actually enable now that provider-dependent tests skip rather than fail without credentials
- Provider-readiness failures now report **why**. The old messages did not say: most were a variant of "provider not available" that named no cause, and `index --force-reembed` and `ai embed-probe` said `is not ready (check credentials)` for every failure, which sent users off to re-authenticate after a five-second network blip. A timeout now reads `is not ready (timeout). The probe timed out. …`, carrying the same remediation `models test` and the macOS app show for that code. Covers `index --force-reembed`, `ai embed-probe`, `ask`, `chat`, `polish`, `eval`, `eval answers`, `suggest-links`, `suggest-target`, and the MCP tools `kb_ask` / `kb_polish` / `kb_suggest_links`
- A provider that cannot classify its own failure (Ollama, OpenRouter, and the CLI-only llama-local) now says the cause was not reported, then points at the right thing for each: Ollama's daemon and endpoint, the bundled engine, or `OPENROUTER_API_KEY`. Where the old text did name a cause it named credentials, which two of the three do not have
- `2nb mcp doctor`'s AI-readiness line carries the cause too, and now distinguishes an embedder that never registered from one whose probe failed. Those are different problems and the old line named neither
- `2nb ai status` and `vault status` no longer guess. The per-provider `reason` said `credentials missing or region unreachable` for any failure, and the portability hint said "If using Ollama, start the daemon; if using Bedrock, check AWS credentials" regardless of which provider was active or what actually went wrong; both now carry the classified cause. The `portability_status` label itself is unchanged, since `config doctor` and the macOS app switch on it. Note that `reason` now ends in the machine-readable code in parentheses for a provider that can classify itself (`credentials were rejected (bad_credentials)`); anything matching the old fixed text should switch to that token
- The search/ask degradation warning names the cause: `semantic search disabled: provider "bedrock" not ready (bad_credentials) — falling back to keyword search`, where the code is the same vocabulary `ai status --json` reports. The stable `semantic search disabled: ` prefix agents match on is unchanged, and the remediation is deliberately NOT included, since this line prints on every degraded query
- A readiness `access_denied` no longer sends users to the Bedrock console's "Model access" page. The readiness probe is a control-plane listing, so a denial means the credentials lack `bedrock:ListFoundationModels`, which that page cannot grant. A Bedrock API key that is malformed, revoked, or expired is answered by the service with that same access-denied error, and is now classified as a credential problem rather than an IAM one, so the message points at the key instead of at permissions
- A transient Bedrock readiness failure (timeout, unreachable, throttled) is now held for 2 seconds instead of the full 30-second cache window, so one network blip stops pinning the provider at "unavailable" for the rest of it, without making a long-lived MCP session re-probe on every single tool call. Definitive failures (bad credentials, access denied) are still cached in full, and a cached failure now remembers its cause instead of degrading to the generic message when asked a second time
- Missing AWS credentials are classified as `bad_credentials` rather than `timeout`. The SDK's fallback to EC2 instance metadata is unreachable off EC2 and fails with a wrapped deadline error, which the deadline check saw first
- `ai status` probes its embedder, generator, and reranker concurrently instead of serially, and carries each resolved verdict to the surfaces that report it rather than re-deriving it. On a healthy or definitively-failing provider the cache already made the repeats free; the difference shows on a degraded network, where a repeat could miss the short transient TTL and pay another full probe
- `suggest-target`'s keyword tier now matches kebab-case targets. FTS5 treats `models-apresai` as one order-sensitive phrase, so it could never match a note reading "apresai models", which meant the word-reorder recovery the command documents as working offline did not work for the shape link targets actually take. One consequence worth knowing: a broken kebab-case link that previously produced no candidate can now produce a high-confidence one, and the macOS Notes tab's "Fix all" acts on high-confidence candidates
- Tests that need an AI provider now skip on whether an embedding actually lands, not on whether an environment variable happens to be set. A configured-but-unusable provider made ten tests fail where they should have skipped. Two tests also asserted contracts the code cannot satisfy: `--force-reembed` was documented as running without a provider (it refuses), and the hybrid-degradation test switched providers to force a dimension mismatch that never occurred (both sides kept the same 1024 dimensions)

## [0.20.0] - 2026-08-24

### Added
- `.cursor/BUGBOT.md` repo review instructions for Bugbot and Autofix, and a non-blocking README-currency annotation on user-facing PRs (#234, #235)

### Changed
- CLAUDE.md trimmed to rules, invariants, and pointers after it crossed the agent harness's 150k-character truncation limit; the reference detail moved into `docs/` (full `cli-reference.md`, plus deeper `ai-providers.md`, `macos-app.md`, `search-tuning.md`, and `release-playbook.md`). New `check-claude-md-size` (100k cap) and `check-docs-links` gates run in `make test`, release CI, and a blocking PR-time job (#236)
- `IndexGeneration` 1 → 2: link-resolution outcomes are persisted index state, so **a reindex is recommended after upgrading** (`2nb index`, free, no re-embed) for previously-unresolved encoded links to pick up their targets (#237)
- The index-generation CI guard now watches the store resolution files, so a resolution-logic change requires a generation bump or an explicit `Reindex-Not-Needed` trailer (#237)

### Fixed
- Percent-encoded markdown links (`[x](My%20Note.md)`, the form Obsidian generates for paths with spaces) are now first-class everywhere: they resolve as real links (backlinks, graph, `unresolved`, lint, and repair candidates all see them), and `move`/`rename` discovers, matches, and rewrites them, percent-encoding rewritten destinations as needed and never respelling an unchanged destination (#237)
- `repair-links`, `polish --repair-links`, and `relink` now emit syntax-aware destinations: a broken markdown link is repaired to a path-based destination that actually resolves (bare basename when it resolves back to that note, else the full vault-relative path) instead of a title+`.md` form no resolver tier accepts, while wikilinks keep the pretty title (#238)
- `relink` resolves `--to` before rewriting, so relinking a markdown link to a note picked by title now produces a resolving destination; an unresolvable `--to` keeps the verbatim rewrite and its warning (#238)
- A rewrite whose only effect would be respelling the same destination (raw spaces to `%20` or back) no longer counts as a change, so a folder-only move stops churning raw-authored referrers and `repair-links` cannot burn a note's `polish --undo` snapshot on a no-op (#237)
- Suppressed the phantom `no_change` skip when one repair fixed both the wikilink and markdown spellings of a name; a unique candidate whose rewrite genuinely changes nothing is now reported as a visible `no_change` skip instead of dropped silently (#237, #238)

## [0.19.2] - 2026-08-23

### Added
- `2nb models discover`: discovery as a verb with per-source cache ages, a NEW/GONE diff against a machine-local baseline, `--refresh`, `--add <id>` (persists the discovered row with its routing so mantle-plane ids invoke correctly), and `--validate` behind verify's cost gate (#225)
- macOS Testing tab consolidating everything measurable: Validate (vendor checkboxes, per-account access and working-set summaries), Benchmarks (model x probe runs, favorites management, a models x probes compare matrix, the embed-throughput curve, history), Performance (moved in from Health), and a Quality pane running the `2nb eval` retrieval scorecard, answer jury, and tune sweep behind estimate-derived cost gates; AI menu "Testing & Benchmarks…" (Cmd+Shift+T) (#224, #230)
- GUI Discover card in the Testing tab: per-source discovery-cache ages, Refresh, one-click Add and Add + Validate for NEW models, an informational GONE list, plus a CLI-backed discovery nudge banner in the Models tab (#229)
- Settings as a first-class sidebar tab: the same four-tab SettingsView hosted inline, with the Cmd+, window kept (#221)
- `eval --estimate`: print the projected cost of any eval run without calling a model; the Quality pane prices its confirm dialogs from it (#230)
- Timeout edges: Obsidian plugin per-command timeouts derived from the CLI's transport budgets, an app-side CLI hang watchdog, and a shared cold-start hint after 15 seconds of an in-flight probe (#223)

### Changed
- Timeout core: probe and doctor deadlines are now derived from the resolved route's transport worst case instead of flat caps, with the nesting pinned by tests, so a slow cold-starting model is never failed, only a hang (#222)
- README brought current for the models discover verb and the Testing and Settings tabs (#233)

### Fixed
- `models discover`: only discovery failures block the seen-baseline update, a model adopted into the catalog is never badged GONE, and the GONE list renders even when the pool comes back empty (#228)
- Review-finding sweeps across four rounds: zero-priced estimate gating, Discover refresh and spinner behavior, the shell-quoted setup hint, provider-qualified `--add` on cross-provider id collisions, the tune estimate fallback, watchdog containment, Validate request routing, bench single-flight, and the ask timeout budget (#226, #227, #231, #232)


## [0.19.1] - 2026-08-21

### Fixed
- Self-heal catalog rows whose invoke routing was stripped by a pre-0.19.0 save clobber: `mergeDiscovered` now grafts discovered routing hints onto a merged row's empty fields (authored fields still win), so a stripped mantle model (e.g. `xai.grok-4.6`) recovers on the next `models verify --discover` instead of shadowing its own cure
- `models bench` generate probe now pins reasoning effort to none (like the smoke probe), so always-reasoning mantle models no longer burn the budget on thinking and time out at the 90s HTTP limit; the RAG bench probe pins it too

### Changed
- Every generation token budget resized so a working model can never fail on truncation (always-reasoning classic models bill ~180 reasoning tokens with no off switch): bench generate 128 → 1024, RAG answers and RAG bench 1024 → 4096, chat history condenser 128 → 1024, suggest-target LLM verdict 400 → 1024, eval jury/QA generation → 1024, default generation opts 512 → 1024; budgets live as shared constants quoted by the cost specs and are pinned by `TestGenerationBudgetsPinned`
- `models wizard` default `--cost-cap` raised 0.10 → 0.50 to match the honest 1024-token probe estimates

### Removed
- Dead `catalogIndex` helper in the AI catalog; its no-duplicate-builtin-IDs invariant survives as a direct test


## [0.19.0] - 2026-08-20

### Added
- Grok 4.6 (`us.xai.grok-4.6`) as a builtin classic-plane Bedrock catalog entry via the Converse cross-region profile, with plane-safe pins so the mantle `xai.grok-4.6` id stays separately routable (#213)
- Mantle-plane model enumeration: `--discover` on `models list`, `models verify`, and `models cost-preview` now lists each mantle region's `/v1/models` catalog as probeable rows carrying routing hints, and a verified probe persists the routing into the user catalog (#218)
- New-model discovery nudge banner on the macOS Models tab: newly discovered probeable models are flagged with Validate and Dismiss actions, and first activation of a provider seeds silently instead of badging its whole catalog (#217)

### Changed
- Classic-plane Bedrock generation compatibility gate is now default-allow: only five static deny categories remain, so models from unrecognized vendors surface through discovery as probeable rows without a code change (#215)
- Generation probe output budget raised from 256 to 1024 tokens, with cost estimates scaled to the new budget (#216)
- `make release-app` is checkpointed, resumable, and optionally non-blocking: phases prove completion from artifacts, pending notarization submissions persist keyed to artifact sha256, `RELEASE_NOWAIT=1` submits and exits, and `make release-app-status` reports progress (#212)
- Hardened the mantle endpoint resolution (https and `*.api.aws` only), pinned the probe cost estimate to the probe budget, and added a verify cost cap plus routing logs (#214)

### Fixed
- Verify-setup reap decode failure in the macOS app; model probe failures now surface code-aware remediation advice keyed to the classified error (#219)
- A notarization submit that loses its stdout while the upload landed is recovered by adopting the matching submission from `notarytool history`; submits also pass `--no-s3-acceleration` (#211)


## [0.18.2] - 2026-08-20

### Added
- `prefer_stored_token` in `~/.config/2nb/bedrock.json` (off by default): makes the saved Bedrock key (file, then Keychain) outrank the `AWS_BEARER_TOKEN_BEDROCK` env var for 2nb only, so the env token keeps serving other tools in the shell. Applies to every 2nb surface, including the classic SDK path and the mantle plane; with no stored key the env var still applies, so the flag can never break auth. Set via `2nb config bedrock --set --prefer-stored-token` / `--no-prefer-stored-token` or the new Settings AI checkbox, and reported as `prefer_stored_token` in the status JSON. (#210)
- Internal `2NB_BEDROCK_IGNORE_PREFER_STORED=1` escape hatch restoring env-first for one process, used by the app's verify-before-accept so a candidate key probed via the env var is actually the key being tested. (#210)

### Changed
- The env-overrides-stored split-brain warning (`config bedrock`, `doctor` tier 1, Settings) downgrades to an informational both-keys note when `prefer_stored_token` is on, since the env var no longer overrides anything in 2nb. (#210)

### Fixed
- `doctor` now captures the env-vs-stored key divergence before provider initialization; previously, hydration under `prefer_stored_token` overwrote the env var first, making the Bedrock key-source check unreachable in exactly the scenario it renders for. (#210)
- `config bedrock` help text no longer claims the env var unconditionally wins over the stored key. (#210)


## [0.18.1] - 2026-08-20

### Added

- Multi-region Bedrock verification: `config bedrock --set --regions` / `--clear-regions` manage additional included regions; `models verify` and `models test` retry region-shaped failures (`not_found`, `invalid_request`, `access_denied`) across included regions, persist a passing non-primary region onto the catalog entry, and self-heal stale pins when the primary region passes again. Discovery unions listings across included regions, and generation/embedding now honor catalog region pins.
- New Models tab (`SimpleModelsView`) as the default Bedrock setup surface: sticky vendor checkboxes, one Validate action (cost preview + verify), and Answers/Search pickers limited to the working set of models this account has actually invoked. The full AI Hub moves behind a "Full catalog" disclosure.
- Working-set signal in the CLI: `models list --working-set` and a per-row `working` field expose which models carry a passing probe for this account.
- Bedrock key observability: token writes stamp `token_updated_at` in `bedrock.json`; `config bedrock` and `doctor` warn when an environment `AWS_BEARER_TOKEN_BEDROCK` overrides a different saved key (suffixes shown for both). The app surfaces the same warning and flags model-access verdicts that predate the current key.
- Always-visible Bedrock key-state chip on the Models tab, an "Open AI settings" action on bad-credentials failures, and an "Also verify in" region control on the Settings AI tab.
- Check all / Uncheck all bulk vendor toggles on both the Models tab and the Manage-vendors sheet.
- 24h disk cache for Bedrock model discovery so repeated GUI validation passes do not re-walk the control plane.
- `.release.yaml` declares `scripts/sign.env` as a required env file so release preflight fails fast on a fresh checkout.

### Changed

- Settings window tabs are now selection-bound (`SettingsTab`), so links from Home and the Models tab land on the intended tab instead of whatever macOS last restored.
- Home's AI card replaces the always-visible "Save as default" with a drift-gated "Reset to recommended defaults", and the AI settings link is no longer hidden once credentials work.
- `models policy show --json` emits a synthetic `known_vendors` row for every provider missing a configured policy, so one provider's policy can no longer shrink another provider's checkbox vocabulary.
- Dependencies: modernc.org/sqlite 1.53.0 to 1.57.0 (with libc and memory in range); Obsidian plugin lockfile refreshed.

### Fixed

- ATX headings inside fenced code blocks (``` / ~~~) are no longer parsed as headings, which created phantom H1s and reparented later sections in chunking and section replace. `EmbedGeneration` is bumped; existing vaults are prompted to run `2nb index --force-reembed`.
- Stale catalog region pins now re-check the primary region on every probe instead of persisting after the extra regions are removed.
- The re-validate request issued before the Models tab mounted was silently dropped; it now fires on mount.


## [0.18.0] - 2026-08-13

### Added
- macOS Settings window (Cmd+,) with General, AI, Advanced, and Integrations tabs — the first configuration surface that works with no vault bound, including a masked Bedrock key field that verifies a key before accepting it and a "Test everything" button wired to the new doctor self-test.
- `2nb doctor` now runs a real end-to-end self-test in two tiers: tier 1 probes the active embedding and generation models and reports a plain credential verdict (accepted/rejected/unreachable/unknown) with no vault required; tier 2 folds in `config doctor` and `mcp doctor` checks when a 2nb-indexed vault resolves. Each tier is bounded separately, and an expired check reports an inconclusive timeout rather than blaming the subsystem it was inspecting. `--json` adds `ok` and `selftest{...}`.
- `2nb doctor --versions` restores the free, parity-only behavior (no model calls, always exits 0) for automatic callers; the Obsidian plugin's Components section now uses it.
- `mcp doctor`'s `kb_search` check inspects the result payload, so a vault with embeddings that answers in keyword-only mode is a hard failure instead of a silent pass.
- `config bedrock --json` returns `token_suffix` (last 4 characters, suppressed under 12) so a GUI can render a recognizable masked key.
- `BedrockRegions.risk` gates the region picker with three verdicts — safe, breaks, and unverifiable — so a breaking region can't be saved silently when no vault is bound.

### Changed
- Dashboard sidebar goes from eight entries to five: Home, Models, Notes, Health, and Activity. Health groups Vault, Performance, and Updates; Activity groups Git and MCP Server. The existing views are hosted behind a segmented picker, so nothing was rewritten or dropped, and the group is no longer labelled "Advanced".
- "AI Settings" tab renamed to "Models" — it is the model catalog, not where an API key goes.
- Provider cards say "Show models / Hide models" instead of Enable/Disable, matching what `ai.<provider>.disabled` actually does (hides models from selection lists; does not stop an explicitly-chosen active provider).
- The rerank slot's picker action reads "Set Active + Turn On", since selecting a rerank model also writes `ai.rerank.enabled=true`.
- Home's AI card is provider-generic (names the active provider, shows raw active model IDs) and replaces the always-visible "Save as default" with a drift-gated "Reset to recommended defaults" that confirms exactly what it writes. When AI isn't ready, the card now links straight to the AI settings tab instead of dead-ending.
- Home's AI Clients card is a read-only summary; all client skill/MCP/instructions writes happen only on the Settings Integrations tab.
- Sync and Re-embed All are owned by Home; Vault status reports state instead of running operations.

### Removed
- Dead editor settings and the no-op "Test Connection…" / "AI Setup…" actions (`showAITest`, `showAISetupWizard`, `showModelWizard` were computed aliases that merely selected a tab); both actions now exist for real on the Settings AI tab.
- `PreferencesView.swift`, superseded by the native Settings window.


## [0.17.1] - 2026-08-12

### Added
- Machine-local Bedrock bearer token: set it in the macOS app Settings or via `2nb config bedrock --set`; stored in `~/.config/2nb/bedrock.json` (XDG-aware, optional region overlay, refused if world-readable) so the app and CLI reach Bedrock without shell credentials or vault config
- `2nb config bedrock` command to show or set the machine-local token (`--json` redacts the token)
- Dependency-audit CI workflow: blocked-package check (`blocked-packages.txt` + `scripts/check-blocked-deps.sh`) plus a two-pass OSV vulnerability scan

### Fixed
- CI dependency audit: corrected the osv-scanner release asset URL (404) and treat OSV exit code 128 as an empty result instead of a failure


## [0.17.0] - 2026-07-11

### Added
- `suggest-target --verdict`: recommendation envelope (`{recommendation, llm, candidates}`) emitting exactly one machine recommendation per broken link — `relink` to the top candidate at high (one-click) or medium (confirm) confidence, else `unlink`; an explicit model decline recommends `unlink` (measured trustworthy: 0 false promotions)
- Link-fix prompt eval harness: planted-truth corruption corpus with calibrated LLM judge for measuring `suggest-target --llm` promotion precision, documented in `docs/link-prompt-eval.md`
- Validation tab (macOS app): removal recommendations for dead links — "removable" badge with inline Unlink button, plus a bulk "Remove dead links" confirm sheet mirroring Fix all (removal is never silent and never part of Fix all)
- Validation tab: "Fix each (N)" queue stepping through all findings needing a decision (the exact complement of the Fix-all set) with progress indicator and Skip

### Changed
- `suggest-target` re-rank now uses the eval-selected `strict_plausibility` prompt: fail-closed, attaches a one-line reason, never invents paths, caps LLM promotions at medium confidence (the ≥0.95 auto-fix precision bar was measured and not met)
- `suggest-target` candidates are ordered confidence-first (tier then score), so `candidates[0]` is always the pipeline's best claim
- Validation tab bulk classification runs `suggest-target --llm --verdict` probes concurrently (bounded 4-wide), skipping generation calls when a high-confidence candidate already exists; the fix sheet reuses the bulk verdict instead of re-probing, and the recommended action is preselected from the verdict
- Validation tab: a failed classification probe renders a neutral "check failed" badge, never a removal recommendation


## [0.16.0] - 2026-07-11

### Added
- `suggest-target --source <path>`: context-aware candidate search that seeds semantic and BM25 lookups with the broken link's surrounding prose, not just the bare target; the source note is excluded from candidates and resolves leniently (a just-deleted source falls back to the cleaned raw path)
- `suggest-target --llm`: optional LLM re-rank of the grounded candidate shortlist when no candidate is already high-confidence, attaching a one-line reason to each pick; fail-closed, never invents paths, and caps promoted confidence at medium so LLM picks stay recommendations
- Validation tab: decision-class findings show the top 2-3 candidate Link buttons inline, and the Fix link sheet loads context-aware, LLM re-ranked suggestions with reasons

### Changed
- Validation "Fix all" now bulk-applies only high-confidence rewrites (drift repairs plus high-confidence relinks); medium and low confidence candidates are excluded from bulk apply and handled per-finding instead


## [0.15.1] - 2026-07-08

### Fixed
- Bedrock mantle plane (`openai.gpt-5.5`, `xai.grok-4.3`) now returns account-aware guidance when a model is entitlement-gated: a valid bearer token on an un-entitled account is classified as `access_denied` (via the response body's `error.code`) rather than `bad credentials`, so users with working credentials aren't sent to re-run `aws configure`.

### Changed
- Remediation for a gated mantle model points to **AWS Sales** and notes your other models still work, instead of the Bedrock console's "Model access" page (which does not govern mantle models).
- Probe results now carry the model's `invoke_strategy`, letting the macOS AI Hub suppress its "Open AWS console" button for mantle `access_denied` findings (the console can't unblock those models).


## [0.15.0] - 2026-07-07

### Added
- Bedrock mantle generation client with `openai.gpt-5.5` (us-east-2) and `xai.grok-4.3` (us-west-2) model entries, plus per-model Region/Endpoint support and the `bedrock_mantle_responses` invoke strategy.
- `models policy set/show/clear`: persistent enable-only vendor selection per provider, honored by dropdowns, the AI Hub catalog, and `models verify` (future discoveries arrive pre-disabled).
- `models verify --events` streaming NDJSON probe progress for the GUI, and `--enabled-only` to validate exactly the effectively-enabled models.
- `lint` now classifies each broken wikilink (`drift`/`ambiguous`/`missing`) with additive `--json` fields, and `suggest-target` scores candidates `high`/`medium`/`low` confidence.
- `suggest-target --source` excludes the note being fixed from its own candidates.
- AI Hub: **Validate models** with streamed access results, a summary-first collapsed catalog, a vendor policy sheet with disabled-model visibility, and class-aware Validation with one-click **Fix all** plus a **Fix each** walkthrough for decision-class broken links.

### Changed
- `lint`, `repair-links`, and `suggest-target` now resolve candidates against the same live-filesystem walk (`vault.CollectLiveDocs`), so they can never disagree over a note added or deleted since the last index.

### Fixed
- Link-fix no-ops in the app are classified: a stale finding (link already gone) re-lints and clears instead of stranding an open sheet.


## [0.14.0] - 2026-07-07

### Added
- `eval answers`: LLM-jury answer-quality scorecard grading real RAG answers 1-5 on correctness, completeness, and grounding, cost-gated with an optional judge panel (#172)
- `eval tune`: retrieval auto-tuning sweep over the cached QA set that suggests `config set` improvements when a config beats the current one beyond noise (#171)
- `models verify`: per-account batch access probe that persists pass/fail (with classified error codes) for the recommended and active models, surfaced in `models list` and `ai status` (#159)
- Structured taxonomy for model test-probe failures (`access_denied`, `throttled`, `bad_credentials`, etc.) with remediation hints in CLI output and `--json` (#157)
- Context-window hints for discovered Bedrock models (#160)
- Global instructions support for the Codex CLI (`~/.codex/AGENTS.md`) via `2nb instructions` and `2nb setup`, plus per-client status in the app and Obsidian plugin (#169)
- macOS app: Advanced settings panel exposing previously invisible AI config (similarity threshold, hybrid weights, RAG budgets, embed concurrency, dimensions, calibration, embed-probe) (#165)
- macOS app: benchmark run history in the model picker (#167)
- macOS app: cost-estimate confirmation before paid Test and Benchmark runs (#164)
- Opt-in skill-usage eval benchmarking whether agents actually invoke the 2nb skill (#173)

### Changed
- `models list` surfaces benchmark evidence in a BENCH column and ranks by measured quality with `--sort best` (#170)
- Refreshed the builtin Bedrock Anthropic catalog (adds Sonnet 5 and Opus 4.8) with curated recommendations and a `--recommended` filter (#158)
- macOS app: AI Hub catalog is curated by default, with the untested long tail behind "Show all models" (#163)
- macOS app: model rows show classified access states (no access, throttled, bad credentials) with actionable guidance (#166)
- Documentation reconciled against shipped behavior (#174)

### Fixed
- Home AI card now reflects the actual configured provider and models instead of assuming Bedrock defaults, and the reset button only appears when config drifts from defaults (#162)
- Unified pricing knowledge across commands, stopped clearing cached pricing on a lookup miss, and flagged a disabled active provider in `config doctor` (#161)


## [0.13.2] - 2026-07-05

### Added
- `2nb instructions` command (`install`/`configured`/`uninstall`) — manages a sentinel-delimited, version/sha-stamped "2ndbrain" reference block in an AI client's global agent memory file (`~/.claude/CLAUDE.md`). Idempotent, backup-safe, preserves surrounding user content, and is run by `2nb setup`. Supports `claude-code` and `claude-desktop`.
- Per-client **Global instructions** status in the macOS app and Obsidian plugin (from `2nb instructions configured --all --json`), shown per AI client and refreshed after Configure.

### Changed
- Release writes the local `SecondBrain-<VERSION>-arm64.dmg` to the gitignored `build/` directory and broadens the pre-release artifact sweep to also clear the legacy repo-root location and the retired `.zip` format.
- `create` now echoes the resulting slug and title; `2nb create --json` returns `path` and `title`.

### Fixed
- `meta` recovers the obsolete positional `meta set/get/remove <path>` form by rewriting it to the flag form (or erroring with a copy-pasteable hint) instead of Cobra's terse arg-count message.
- `delete` no longer hangs a non-interactive/agent session: the confirm prompt times out after 60s (or errors on a closed stdin) and reports the note was NOT removed.
- Release self-heals the intermittent `notarytool` SIGBUS (Bus error 10) by submitting without `--wait` and polling `notarytool info`, so a crashed status poll retries instead of aborting the release.


## [0.13.1] - 2026-07-05

### Added
- `ai engine rm` (aliases `remove`/`delete`) CLI command to delete cached local model weights and free disk, with `--json` reporting `{removed[], freed_bytes}`.

### Changed
- Hide the `llama-local` provider across the macOS GUI (provider card and Local models section in the AI Hub) behind a feature flag, since the `llama-server` engine binary is not yet provisioned; the CLI plumbing remains intact.
- Document `llama-local` as experimental and CLI-only in the README to match the release-gated GUI state.


## [0.13.0] - 2026-07-05

### Added
- **llama-local provider**: fully offline embeddings and generation via a bundled llama.cpp engine running Gemma weights, with GGUF weights downloaded and sha256-verified on demand (never bundled)
- **Local reranker** (bge-reranker-v2-m3 over the bundled engine) and an optional **Cohere Rerank 3.5** stage on Bedrock, both default OFF (measured to not help at current vault scale)
- **`2nb eval`**: user-facing vault search-quality scorecard reporting Recall@10 / R@1 / MRR@10 over a Q&A set generated from your own notes, gated by a cost preview and `--cost-cap`
- **`2nb ai engine`**: manage the bundled llama.cpp engine (`pull`/`serve`/`install`/`bootout`), with `--json` line-delimited download progress and a 60s idle watchdog
- Rerank model type and catalog entries, surfaced in `models list` and across the macOS AI Hub (Reranking group, Active rerank slot with on/off toggle)
- macOS AI Hub **"Download local models"** button that streams `ai engine pull --json` progress into the GUI
- `ai setup` llama-local branch with an opt-in prompt to download missing Gemma models

### Changed
- Extracted a shared `internal/retrieve` pipeline backing `search`, `ask`, and MCP, so hybrid fusion and the optional rerank stage stay consistent across every query path


## [0.12.5] - 2026-07-04

### Added
- Reindex-on-release detection: `2nb` now stamps the indexing/embedding logic generation into the index DB and detects when a release changed that logic (chunking, chunk→vector mapping) while keeping the same model/dimension, then prompts to reindex. Surfaced through `vault status`, `ai status`, and `config doctor`, plus a macOS app banner and an Obsidian plugin nudge. Always prompts, never auto-spends.
- Multi-axis retrieval + generation evaluation harness (LLM-as-jury) under `internal/eval` — QA-set sweep, generation-model scoring, and prompt A/B comparison (credential-gated).
- Embedding truncation observability so oversized-section drops are visible rather than silent.
- Multi-machine setup guide (`docs/multi-machine-setup.md`) and a portable, copy-paste CLAUDE.md snippet (`docs/claude-md-snippet.md`).

### Changed
- RAG generation prompt drops the "concisely" instruction (measured improvement in answer quality).
- Chunking now caps chunk size so oversized sections are split rather than truncated or rejected at embed time (Nova embeddings).

### Fixed
- Corrected the embedding `ContextLen` used for Nova, so chunk sizing matches the model's real context limit.
- Retry backoff on Bedrock embedding now honors context cancellation, so an aborted index/re-embed returns promptly instead of sleeping through its backoff.

### Removed
- Dead code in the embedding rate-limit path.

⚠ Reindex recommended after upgrading: `2nb index --force-reembed`


## [0.12.4] - 2026-07-01

### Changed
- Refreshed third-party SDK dependencies to their latest versions across the Go CLI (`go.mod`/`go.sum`), the macOS app (Swift Package dependencies), the Obsidian plugin (npm packages), and CI workflow tooling.


## [0.12.3] - 2026-06-30

### Changed
- MCP `kb_index` now shares the CLI's concurrent embed pass (`vault.EmbedDocuments`), so agent-driven reindexes get the same bounded-worker-pool speedup, honor `ai.embed_concurrency`, and cooperatively cancel when the client disconnects (#129).
- Bumped the `aws-sdk-go-v2` dependency family to the latest patch releases (#130).


## [0.12.2] - 2026-06-30

### Added
- MCP-driven operations now record token usage (input/output) and result/doc counts in the metrics observatory, so agent-driven `kb_ask`/`kb_search`/`kb_index` rows carry the same detail as the CLI path instead of all-zero values.

### Changed
- `kb_ask`, `kb_search`, and the reindexing write tools attach their actual usage and `result_count`/`docs_indexed`/`embedded`/`total_chars`/`mode` to each `metrics.db` row via the server's request context.


## [0.12.1] - 2026-06-29

### Added
- `2nb ai embed-probe`: discovers a safe `ai.embed_concurrency` for your account by ramping concurrency over a discarded sample of vault chunks and recommending the lowest level reaching ≥90% of peak throughput before throttling (`--levels`, `--sample`, `--yes`, `--json`).
- `ai.embed_concurrency` config setting (1–64) to cap the concurrent embed worker pool; defaults per provider (Bedrock 4, OpenRouter 3, Ollama 2).
- Token-usage tracking (input/output) across `index`/`reembed`/`search`/`ask`, surfaced in `2nb metrics` (`total_input_tokens`/`total_output_tokens`, per-op `tokens_in`/`tokens_out`) and the macOS Metrics tab. `ask` records the provider's actual generation usage when reported (Bedrock Converse via `ai.UsageGenerator`); other paths estimate at chars/4.

### Changed
- The bulk embed/re-embed pass now runs concurrently via a bounded worker pool instead of a sequential per-doc throttle, measured ~5x faster reembed (64s→12s on a 30-doc vault at concurrency 4).
- Bedrock embedding is self-correcting under load: retries now cover `ThrottlingException`, `ModelTimeoutException`, and `ServiceUnavailableException` with exponential backoff plus equal jitter (up to 5 attempts), so an over-set concurrency degrades to retries rather than failures.
- `metrics.db` migrated to schema v2, adding token columns via an idempotent `ALTER TABLE ADD COLUMN`, preserving existing history (old rows default to 0).


## [0.12.0] - 2026-06-29

### Added
- **Vault performance observatory** — a local `.2ndbrain/metrics.db` records index/reindex/reembed/search/ask operations (timing, throughput, doc/chunk/embed counts) automatically and best-effort, never failing the underlying op. Pruned to ~200 rows per operation type; query text is never stored.
- **`2nb metrics`** command (default `metrics show`) reports the last index build, live vault gauges (doc/chunk/embedded counts, coverage, index.db + WAL size, stale count, embedding model/dims), recent operations, and per-operation aggregates (count/avg/p50/avg-docs-per-sec). `metrics clear` wipes history; `--json` and `--limit` supported.
- **MCP-driven operations** are recorded to the observatory (`source=mcp`): `kb_search`, `kb_ask`, `kb_index`, and the reindexing write tools, via a single long-lived metrics DB held for the server's lifetime.
- **macOS Metrics tab** (`MetricsView`) surfaces the observatory: last build stats, live gauges, per-operation aggregates, and a recent-operations list with per-op icons, latency, and a source chip for non-CLI rows. Refreshes on appear and on demand (no polling).


## [0.11.1] - 2026-06-29

### Added
- `ai.rag_context_budget` and `ai.rag_note_budget` config keys to tune RAG context size (`config set`, reject negative/>400000, `0` resolves to default).
- `make clean-dmg` target that sweeps stale local `SecondBrain-*.dmg` installers; the app release now auto-sweeps prior DMGs before building.

### Changed
- `ask` / `kb_ask` now feed the **full matching note(s)** as parent-document RAG context (windowed around the matched heading only when a note exceeds the budget) instead of a from-the-top 2000-rune snippet, so answers deep in long notes are no longer truncated away. Shared via the new `internal/ragctx` package; vector-only hits now return the winning `chunk_id`/heading so they window precisely.
- MCP idle self-exit is now **opt-in and OFF by default**; enable an inactivity cap with `--idle-timeout <dur>` or `$2NB_MCP_IDLE_TIMEOUT`.

### Fixed
- `mcp-server` stays alive while its client is connected and exits promptly when the client closes the connection or dies, via a `getppid()` parent-death watchdog (`internal/mcp/parent.go`) — a closed or crashed session no longer leaves an orphan holding the index open.


## [0.11.0] - 2026-06-28

### Added
- Per-chunk vector search via sqlite-vec (vec0): exact in-DB SIMD KNN over `vec_chunks` is now the primary retrieval path, with the whole-doc brute-force as fallback.
- Configurable hybrid RRF weighting (`ai.bm25_weight` / `ai.vector_weight`) to bias fusion toward keyword or semantic recall.
- Nova asymmetric query purpose: queries embed with `GENERIC_RETRIEVAL` while documents stay `GENERIC_INDEX`, lifting MRR@10 (0.951→0.962) and Recall@10 (0.987→1.0).
- Matryoshka dimension validation: `config set ai.dimensions` checks the requested width against the active model's supported set (256/384/1024/3072 for Nova-2) and refuses unsupported widths.
- Mixed-dimension vault detection (`store.DistinctEmbeddingDims`) with loud degradation to BM25-only and a `--force-reembed` fix hint.
- `EmbedOpts` / `WithPurpose` embedding-options foundation in the provider interface.
- Cross-lingual retrieval guard plus reproducible asymmetry and cross-lingual eval harnesses (`internal/eval`).
- ADR recording the S3 Vectors and local vector-DB evaluation.

### Changed
- Migrated SQLite from CGO `mattn` to pure-Go `modernc.org/sqlite`: the CLI builds with `CGO_ENABLED=0`, cross-compiles to any GOOS/GOARCH with no C toolchain, and drops the `-tags fts5` requirement.
- Recalibrated the Nova-2 similarity threshold from `0.65` to `0.25` to match the asymmetric query purpose's collapsed cosine scale.
- Halved `ask`'s embedding loads to reduce vector-retrieval latency.

### Fixed
- Corrected Nova model catalog metadata (dimensions and recommended threshold) to reflect measured values.
- `models calibrate` now warns that its document-to-document sampling overstates the asymmetric search-time threshold.

## [0.10.9] - 2026-06-27

### Added
- `2nb setup` one-command front door to install the 2nb skill + MCP server for an AI client (`--client claude-code|claude-desktop|warp|agents|codex` or `--all`), each step idempotent and backup-safe.
- Multi-client MCP install: Claude Desktop (`claude_desktop_config.json`, absolute path, no `cwd`) and Codex (via `codex mcp add`) join Claude Code, Warp, and agents.
- `2nb mcp configured --all` per-client check reporting MCP-configured status across all supported clients.
- AI Clients card on the macOS app Home tab: per-client skill-installed + MCP-configured status with a single per-client Configure button and cross-dependency callout.
- Per-client AI Clients section in the Obsidian plugin settings with Configure and copy-setup-snippet actions, vault-pinned.
- Canonical self-hosted `2nb` agent skill at `.agents/skills/2nb/SKILL.md` (Warp's recommended primary) with `.warp/` and `.claude/` mirrors and a `make sync-skills` generator.
- Cross-tool `.agents/` paths taught to the skills and MCP registries.

### Changed
- Bumped the AWS SDK v2 module group and `golang.org/x/text` (freshness; no CVE).
- Synced README and CLAUDE.md app + plugin docs to the new multi-client setup and AI Clients UI.


## [0.10.8] - 2026-06-25

### Added
- Warp MCP client support: `mcp install --client warp` writes the server entry to `~/.warp/.mcp.json` (or `<vault>/.warp/.mcp.json` for `--scope project`), pinning the vault via `--vault` and Warp's `working_directory`.
- Skill freshness tracking: managed `SKILL.md` installs are now stamped with `x-2nb-version`/`x-2nb-content-sha`, and `skills doctor` reports whether a managed copy is up to date.

### Changed
- `skills list` self-heals a stale, unmodified managed skill install in place, so a `brew upgrade` keeps the agent skill current without clobbering hand-edited copies.
- MCP/skill "configured" reporting is now durable rather than tied to a running server.

### Fixed
- Firmed the vault-write guard: a cwd that resolves a vault only by walking up to a parent is now refused before any open, so a write (or a freshly minted `.2ndbrain/` sidecar) can never silently land in an unintended vault.
- Release: the hardened-runtime gate no longer misfires under `pipefail` when a piped reader exits early (SIGPIPE), which was failing otherwise-valid signed builds.


## [0.10.7] - 2026-06-24

### Fixed
- App self-heals the bundled `2nb` quarantine attribute at launch, so Gatekeeper can no longer block startup after a download or cask install (#96)
- Version-state staleness across CLI, app, and plugin: the 24h release cache is refetched when it's behind an install and a component is never shown a "latest" below its own version, eliminating "installed > latest" reports and phantom update prompts (#95)


## [0.10.6] - 2026-06-22

### Added
- Version-aware `2nb` CLI resolution in the Obsidian plugin (`resolveCliPath`): probes Homebrew, `~/go/bin`, and PATH, and a plugin-managed download wins over a system install only when it is at least as new, so a stale managed CLI can no longer shadow a fresh `brew upgrade`.
- Self-heal on load (`ensureCliFresh`) that re-downloads a managed CLI copy when it falls behind the system binary or the plugin's version floor.
- Unit tests covering CLI resolution and self-heal logic (`test/main.test.ts`).

### Changed
- Decoupled the self-heal version floor from the plugin version and hardened the resolution path.

### Removed
- Untracked the built `main.js` artifact from the plugin (now gitignored).


## [0.10.5] - 2026-06-22

### Added
- `2nb doctor` (alias `verify`) command that checks all three products (CLI, macOS app, Obsidian plugin) are installed and in sync with the latest release, reporting the exact fix command for any gap.
- A **Components** section in the Obsidian plugin settings showing each product's installed version, sync status against the latest release, and fix commands, sourced from `2nb doctor --json`.

### Changed
- The macOS app's Updates tab now sources CLI and plugin version parity from the `2nb update` doctor payload, so the dashboard's freshness checks can't disagree with the terminal; the app row stays authoritative from the running bundle.


## [0.10.4] - 2026-06-22

### Fixed
- Wikilinks that target a note by its title or alias (rather than filename) are now resolved correctly during `lint`, so valid links are no longer reported as broken.
- Same-document anchor links (e.g. `[[#heading]]` pointing within the current note) are now excluded from the link table, preventing them from being counted as inbound/outbound links.


## [0.10.3] - 2026-06-21

### Changed
- The active vault is now resolved solely from Obsidian's open-vault registry (`~/Library/Application Support/obsidian/obsidian.json`). A bare `2nb` command targets whatever vault Obsidian currently has open, keeping the CLI, GUI, and Obsidian plugin in sync with no pointer file to drift.
- `vault set` and `vault create` register a vault in `vault list` recents but no longer switch the active vault; open the folder in Obsidian (or pass `--vault`) to make it active.

### Removed
- The 2nb-managed active-vault pointer file (`~/.2ndbrain-active-vault`) and its `active_vault.go` resolution path. `~/.2ndbrain-vaults` recents remains as display-only data for `vault list`, never a resolution source.


## [0.10.2] - 2026-06-21

### Added
- `2nb update` command that checks whether a newer release is available, comparing the installed version against the latest published GitHub release (cached 24h) and printing the upgrade commands when behind; `--json` emits `{current, latest, update_available, checked, detail}`.
- Updates tab in the macOS dashboard showing the app, CLI, and Obsidian-plugin versions against the latest release, with one-click **Update CLI** and **Update plugin** actions and a copyable `brew upgrade --cask` for the app itself.
- CLI fallback that resolves the vault Obsidian currently has open (read from Obsidian's own registry) when no `--vault`, `2NB_VAULT`, active-vault pointer, or cwd-vault applies, so a bare `2nb` from a non-vault directory still targets the open vault.

### Changed
- The no-vault error is now actionable, telling the user how to set or open a vault instead of failing opaquely.

### Fixed
- Repaired launchd PATH resolution so the dashboard's Verify panel stops reporting false `2nb` CLI failures when the app is launched without a shell environment.


## [0.10.1] - 2026-06-21

### Added
- New CLI link-resolution commands for broken wikilinks: `relink` (repoint a broken link to an existing note), `unlink` (remove a link but keep its visible text), and `suggest-target` ("did you mean?" ranked candidates for a broken target).
- Deterministic link repair now folds hyphen/underscore/separator drift, so a spaced `[[Some Note Title]]` link matches a kebab-case `some-note-title.md` basename.
- macOS app: no-dead-end `LinkResolutionSheet` for broken-wikilink validation findings, offering Repair drift, Did you mean? (relink), Create the note, and Unlink so every finding has a real fix.
- macOS app: bulk "Repair drift links" button on the Validation tab to fix all separator-drift links at once.

### Fixed
- Index notes that merely mention "secret" in their body; notes are now excluded from indexing by type, not by name.

### Changed
- README documents the link-resolution commands and separator-drift repair workflow.


## [0.10.0] - 2026-06-20

### Added
- `2nb repair-links <path>` — deterministic, offline repair of broken `[[wikilinks]]` (canonicalizes a target only when it maps to exactly one note; ambiguous targets reported, never guessed). `--target` scopes the fix; `--write` applies and snapshots for `polish --undo`.
- `2nb mcp install` / `mcp uninstall` — idempotent, backup-first write/remove of the 2ndbrain server entry in `~/.claude.json`, preserving all unrelated keys.
- `2nb mcp doctor` — in-process end-to-end self-test of the MCP engine (tool count, real `kb_info`/`kb_list`/`kb_search` round-trips, AI/wiring/reliability signals).
- `2nb mcp reap` — terminate stale/orphaned `mcp-server` processes for the vault (SIGTERM, PID-reuse-safe, `--dry-run`).
- `2nb skills doctor [slug]` — verify an agent's skill is installed and the `2nb` it shells to resolves on PATH.
- `2nb vault checkpoint` — collapse and truncate the index WAL to shrink a parked `-wal` file (GUI-safe; reports `busy` instead of forcing).
- MCP server self-announcement via a one-line `instructions` string in the initialize response, so a connected-but-idle server isn't misread as absent.
- macOS app: Claude Code card on Home (skill-install, MCP-configured with one-click **Configure automatically**, a **Verify** self-test panel fanning out `skills doctor`/`mcp doctor`/`config doctor`/`models test`), plus a Reliability row with **Checkpoint WAL** / **Reap stale servers** buttons.
- macOS app: actionable lint findings — **Open in Obsidian** (`obsidian://` deep link) on every finding and **Set value…** / **Repair link** buttons for schema and broken-link findings.

### Changed
- `mcp-server` now self-exits after 30 min idle (override via `--idle-timeout` / `$2NB_MCP_IDLE_TIMEOUT`) so closed AI sessions don't leave orphans holding the index open; the client respawns on demand.
- Index DB hardening: named SQLite driver with WAL hygiene and busy-retry, so concurrent CLI/app access no longer fails on transient `SQLITE_BUSY`.


## [0.9.10] - 2026-06-20

### Added
- `polish --repair-links` deterministically repairs broken `[[wikilinks]]` to existing notes (case-drift and whitespace normalization), leaving ambiguous or unmatched targets untouched. The Obsidian plugin's Polish action now repairs links in place alongside the copy-edit.
- The macOS app auto-indexes notes edited in Obsidian: an FSEvents watcher incrementally re-indexes and re-embeds changed notes, and a startup sync catches up notes added or removed while the app was closed.

### Changed
- `SecondBrain.app` now bundles its own version-matched `2nb` CLI at `Contents/Resources/2nb` (signed and notarized with the app), and `CLIPath.resolve()` prefers it, so the app's AI, indexing, and lint calls always run a CLI matching the app.
- Renamed the app's "Rebuild" index action to "Sync" (incremental, hash-gated re-embed that reconciles deletions).
- Rewrote the Claude Code skill to teach the `2nb` CLI by example instead of deferring to `--help`.
- Lightened the DMG installer background for readable Finder labels and completed the release notes.


## [0.9.9] - 2026-06-14

### Added
- `.release.yaml` machine-readable release contract at the repo root, declaring every product with its install and verify commands for the release pipeline.
- Branded drag-to-Applications DMG installer with custom window background art (`scripts/make-dmg.sh`, `app/Resources/dmg-background.{svg,png}`).

### Changed
- The macOS app now ships as a Developer ID-signed, Apple-notarized, stapled `SecondBrain-<VERSION>-arm64.dmg` instead of a zip, so it launches with no Gatekeeper prompt and the cask installs from the DMG.


## [0.9.8] - 2026-06-14

### Added
- `polish --links` weaves grounded `[[wikilinks]]` to existing vault notes, gathering semantic + substring candidates, dropping ambiguous titles, and running a deterministic `StripInventedLinks` pass so no link points at a nonexistent note (`kb_polish` gains a matching `links` option).
- `polish --undo` restores the pre-polish snapshot (reindex + re-embed), refusing to clobber post-polish edits without `--force`; `polish --write` now records a snapshot under `.2ndbrain/recovery/polish/` before applying.
- One-click Polish in the Obsidian plugin on every surface (command/hotkey, sparkle ribbon icon, note-header toolbar action, right-click editor menu), running apply-then-review with a Keep/Undo diff modal serialized by a single-flight lock.

### Changed
- `polish --write` keeps emitting original + polished for audit while applying in place, and pairs with the new snapshot so the edit is reversible.
- Documented `polish --links`/`--undo` and the Obsidian Polish button in the README and project docs.


## [0.9.7] - 2026-06-14

Based on the diffs, here's the changelog entry:

```markdown
### Fixed
- **Security:** `ContainsPath` now symlink-resolves both the vault root and target path before its containment check, so an in-vault symlink (e.g. `<root>/escape` -> `/etc`) can no longer redirect an untrusted MCP write outside the vault. Resolving both sides also avoids falsely rejecting legitimate in-vault paths on macOS, where the vault root often lives under `/var` -> `/private/var`.
- `meta --set` no longer applies array-coercion to `status`: a schema that (pathologically) declares `status` as a list can no longer skip the status-transition validation.
- `tag remove` now dedupes the kept tags, so a note that already carried duplicate tags comes out clean after a removal (symmetric with `tag add`).
- `--copy` on an unsupported platform now returns its clear error via an extracted, unit-testable helper.
- `ai status`, `vault status`, and the root health report now log a warning when the embedding-counts query fails instead of silently discarding the error.

### Changed
- Hardened the shell-completion `2nb`-on-PATH version probe against load-induced flakiness: the per-binary `--version` probe now uses a 3s deadline and retries once (6s) only on timeout, keeping clean failures fast (single exec).
```


## [0.9.6] - 2026-06-14

### Added
- `tag add <note> <tag>...` and `tag remove <note> <tag>...` commands to add or remove frontmatter tags on a single note (dedupe, schema validation, reindex via the shared write path; tags accepted as separate args or comma-separated). Obsidian-CLI `tag:add`/`tag:remove` and `tag=` forms supported.

### Changed
- `meta --set` now coerces array-typed fields (`tags`, `aliases`, any schema `list`/`tags` field) to a YAML list with comma-split, replace semantics (`--set tags=a,b` → `[a, b]`, `--set tags=` clears).


## [0.9.5] - 2026-06-14

### Fixed
- `kb_update_meta` (MCP) now re-indexes the whole file (chunks, tags, links via `IndexSingleFile`) after a frontmatter update, so tag and status changes are immediately reflected in `kb_list` and `2nb list --tag`; re-embedding stays gated on the body content hash, so a metadata-only edit does not re-embed.

### Added
- MCP usage round-trip test suite (`usage_roundtrip_test.go`, `battery_usage_test.go`) covering write-tool → query index consistency, catching regressions where a write tool skips reindex. New `make test-usage` target.


## [0.9.4] - 2026-06-13

### Added
- Obsidian-CLI syntax compatibility: `2nb` now accepts `obsidian`-CLI-style invocations as a drop-in via an argv preprocessor — `key=value` arguments (`file=`, `path=`, `to=`, `content=`, `query=`, `template=`, `old=`/`new=`, etc.), boolean tokens (`total`, `append`, `overwrite`, `done`/`todo`/`toggle`), and colon-commands (`daily:read`/`daily:append`/`daily:path`, `property:read`/`property:set`/`property:remove`, `tags:rename`, `link:unresolved`/`link:orphans`/`link:deadends`, `search:context`).
- Shared fuzzy target resolver (`store.ResolveTarget`): exact path → shortest-unique basename/suffix → title → alias, with loud failure and candidate listing on ambiguity. `path=` resolves strictly, `file=` fuzzily, and a bare positional auto-detects; exposed via a hidden `--resolve exact|fuzzy|auto` flag.
- `--copy` global flag: writes a command's rendered output to the clipboard (macOS `pbcopy`; clear unsupported error elsewhere). Covers `read`/`print` bodies, `meta`/`property:read` values, `daily` paths, and any machine-format output (`--json`/`--csv`/`--format`).
- New output formats: `raw`/`md` emit a value's `Serialize()` output with no JSON wrapping (for piping a document body verbatim), `tsv` is tab-separated, and `text` is best-effort plain text. Listings add `paths` (one vault-relative path per line) and `tree` (indented directory hierarchy).
- `docs/obsidian-cli-mapping.md`: full Obsidian-CLI compatibility reference (command mapping table, accepted argument forms, intentional non-goals).

### Changed
- Compatibility command translations: `print` → `read`, `frontmatter`/`fm`/`properties` → `meta`, `files` → `list`, `search-content` → `search --bm25-only`, and `list-vaults`/`set-default-vault`/`add-vault` → `vault list`/`set`/`create`.
- The preprocessor only rewrites recognized command and parameter shapes: free-text `search`/`ask`/`chat` queries (including those containing `=`) are preserved, and unrecognized `key=value` arguments pass through verbatim rather than being dropped.


## [0.9.0] - 2026-06-13

### Added
- `config doctor` command that diagnoses AI-config problems (provider known/enabled, no orphaned model slot, `ai.dimensions` match, threshold resolution) with fix hints, and `config get --effective` to resolve `ai.similarity_threshold` through its full chain.
- `unresolved` command listing every broken wikilink across the vault (source doc paired with the unresolved `[[target]]`).
- Obsidian-CLI syntax compatibility: `2nb` now accepts `key=value` arguments and colon-commands (`daily:read`, `property:set`, `link:unresolved`, `search:context`, etc.) as a drop-in.
- `--format raw` global output mode that emits a value verbatim with no JSON wrapping, for piping a document body.
- `daily prepend` to insert content at the start of today's daily note.

### Changed
- `ai setup` and `models wizard` now share the same write path when setting active models (provider validation, disabled-flag clear, `ai.dimensions` resync).
- `move`/`rename` now rewrite markdown-style `[text](path.md)` links across the vault, not just `[[wikilinks]]`.
- `config doctor`/`config get` honor exit codes (genuine defects exit non-zero; unreachable provider is a non-failing warning).

### Fixed
- `daily` now honors Moment `[literal]` bracket-escaping in the date format.
- `mcp configured` detection hardened against vault-pin and symlink edge cases.
- `meta --remove` now syncs the `modified` timestamp; corrected empty-body join and `vault status` stale-doc count.


## [0.8.4] - 2026-06-13

### Added
- `daily` command: resolve, create, read, and append to today's daily note using Obsidian's daily-notes plugin config (folder, format, template), with safe fallback to defaults
- `move` / `rename` commands: link-aware note relocation that rewrites every `[[wikilink]]` across the vault, gated by `--dry-run`, crash-safe ordering, and an ambiguity guard
- `tasks` / `task` commands: list GFM checkbox tasks vault-wide (`--done`/`--todo`/`--path` filters) and toggle a single checkbox by line
- `tags rename` command: rename a frontmatter tag across every document that carries it, with `--dry-run` and per-file atomic writes
- `polish --write`: applies the AI-polished body in place (opt-in), still emitting original + polished for audit
- MCP `kb_*` twins: `kb_backlinks`, `kb_links`, `kb_tags`, `kb_tasks`, `kb_append`, and `kb_replace_section`

### Fixed
- `create` now dedupes duplicate-title filenames instead of silently overwriting an existing note
- `move` correctly masks multi-backtick inline code spans so links inside code are never rewritten
- MCP server now initializes AI providers so inline embeds run during indexing
- `move` drops a redundant reindex pass
- `daily` handles empty/missing daily-notes config without erroring; `deadends` predicate corrected


## [0.8.3] - 2026-06-13

### Added
- `append`, `prepend`, and `replace` commands for explicit, opt-in body writes; `replace --section <heading>` rewrites a single heading's content while leaving frontmatter untouched.
- `meta --get <key>` to read a single frontmatter field and `meta --remove <key>` (repeatable) to delete a field in place, preserving comment/key order and refusing identity and schema-required keys.
- Read-only structure and stats commands: `outline` (heading tree), `wordcount`/`wc`, `folders`, `tags`, and `aliases`.
- Read-only link-health commands: `backlinks`, `links` (with per-link `resolved` status), `orphans` (no inbound links), and `deadends` (no outbound links).

### Changed
- Documented the body-write invariant: `2nb` never rewrites a note's body except via the explicit `append`/`prepend`/`replace` commands.


## [0.8.2] - 2026-06-13

### Added
- Home dashboard "Claude Code" card showing Claude Code skill-installed status (with an Install button shelling `2nb skills install claude-code --user`) and MCP-server-configured status (with a Show-setup button).

### Changed
- MCP Server tab now leads with a durable "Configured in ~/.claude.json" banner (from `2nb mcp configured`) that reports setup state even when no server is running, distinguishing configured-but-idle from not-configured.


## [0.8.1] - 2026-06-12

### Added
- `2nb mcp configured` command (and `mcp__configured`) reporting whether the 2ndbrain MCP server is set up in the AI client config (`~/.claude.json`) for the vault, with `--json` output — a durable "is it set up?" check distinct from `mcp status`.
- `2nb create --path <subdir>` flag and a `path` argument on the `kb_create` MCP tool to file new documents under a vault-relative subdirectory (created if missing).
- Obsidian plugin settings rows for Claude Code skill install status and MCP server configured status, with Install and Copy-snippet actions.
- Quick start guide (`docs/quick-start.md`).

### Changed
- Reconciled README and project docs with the current implementation; documented `mcp configured` and `create --path` in the CLI command tables.


## [0.8.0] - 2026-06-10

### Added
- `2nb plugin install` and `2nb plugin status`: one-command Obsidian plugin installer that downloads the plugin bundle from the latest GitHub release into the vault's `.obsidian/plugins/` directory, with version comparison against the CLI (#30)
- Home screen Obsidian plugin row showing the installed plugin version with an Install/Update button that runs `2nb plugin install` (#31)
- Home screen Update CLI button that runs `brew upgrade apresai/tap/twonb` when the installed CLI is older than the app and Homebrew is present (#31)
- `make release-all`: single-command unified release that runs the test gate, bumps the version, tags, waits for CI, then signs, notarizes, and publishes the app and cask (`scripts/release-all.sh`) (#29)

### Changed
- Obsidian plugin version is now synced from the root `VERSION` file via `make version-plugin` (`scripts/sync-plugin-version.js`); release CI fails if the plugin manifest drifts, and the sync refuses to lower the plugin version (#28)


## [0.5.15] - 2026-06-10

### Added
- `2nb ask --history <path|->` enables true multi-turn conversations: prior turns (JSON `[{role, content}]`, `-` for stdin) condense follow-up questions into standalone retrieval queries (reported as `rewritten_query` in `--json`) and ground the answer (#25)
- `2nb chat` interactive multi-turn REPL over the same RAG pipeline as `ask --history`; conversation lives in-process only (#26)
- Obsidian plugin: ribbon icon (custom head-with-brain mark) toggles a right-sidebar vault-chat panel (#24)
- Obsidian plugin: chat panel holds a true multi-turn conversation, passing prior turns to `2nb ask --history -` via stdin with client-side history trimming; renders answers, degradation warnings, and source chips; degrades to single-shot with an upgrade hint on pre-`--history` CLIs (#27)


## [0.5.14] - 2026-06-09

### Added
- Semantic-search playbook and accuracy fixes in the generated 2nb SKILL.md for coding agents (#21)

### Changed
- `--vault` is now a true one-shot override: it no longer persists as the active vault for later commands (#23)
- Provider-disable and `config set` docs aligned with actual config-write behavior

### Fixed
- Unified vault resolution so all commands resolve the active vault through the same path (#23)
- AI config writes are now atomic and validated, preventing partial or corrupt `config.yaml` updates (#22)


## [0.5.13] - 2026-06-08

### Changed
- **The dashboard now keeps the terminal `2nb` pointed at the same vault.** When the app binds a vault (on launch, or via Open Vault), it now also sets that vault as the CLI's active vault, so a bare `2nb ask`/`search` in the terminal (with no `--vault`) resolves to the same vault the dashboard shows. Previously the app pinned the vault only for its own calls, so the terminal CLI could drift to a different vault.

### Fixed
- **The test suite no longer overwrites your active-vault setting.** Running `2nb`'s own test suite (a developer action) could clobber `~/.2ndbrain-active-vault`, which made a bare terminal `2nb ask` resolve to the wrong place. The tests are now fully sandboxed. No effect on normal use; included for completeness.

## [0.5.12] - 2026-06-07

### Fixed
- **Empty notes no longer show as a gap in the embedding status.** A blank (0-byte) note can't be embedded, so the dashboard and `2nb ai status` / `vault status` used to read "115 / 117" with a "2 empty notes skipped" caveat. The embedding ratio now counts only documents that have content, so a vault with blank notes reads "115 / 115" with a clean "OK," and `2nb` no longer keeps suggesting "run index" for notes it will always skip. Empty notes stay in the index, so links to empty stub notes still resolve.

### Changed
- **Release CI runs on Node 24.** Bumped the GitHub Actions in the release workflow (`actions/checkout`, `setup-go`, `setup-node`, `goreleaser-action`) to their current major versions ahead of GitHub's June 2026 removal of Node 20. No effect on the published CLI or app; build-side maintenance only.

## [0.5.11] - 2026-06-07

### Fixed
- **Embeddings status no longer reads "Stale" forever when a vault has empty notes.** A blank (0-byte) note — e.g. Obsidian's default `Untitled.md` — can't be embedded (Amazon Nova-2 rejects empty input), so it was permanently counted as "missing an embedding," leaving the dashboard stuck on "Stale" with the dead-end advice to run `2nb index` (which just skips it again). The status now treats empty notes as deliberately skipped: a vault whose only unembedded documents are empty notes reports a healthy "OK" with a one-line "N empty notes skipped" explanation instead of a false "Stale," and the "catch up" advice only appears when documents with real content are genuinely missing embeddings. `2nb ai status --json` gains a `vault_empty_docs` field.

## [0.5.10] - 2026-06-07

### Changed
- **The macOS app is now Apple-notarized — no more Gatekeeper warning on launch.** Previously the app shipped ad-hoc signed, so macOS showed an "Apple could not verify… / Move to Trash" dialog and you had to right-click → Open (or strip the quarantine attribute) to run it. The app is now Developer ID-signed and notarized by Apple, so `brew install --cask apresai/tap/secondbrain` installs an app that launches cleanly with no prompt. The project stays fully open source; signing happens on the maintainer's machine and no signing keys live in CI.

### Fixed
- **Release builds start from a clean app bundle.** `build-app-release` now removes any stale bundle before assembling, so a leftover file can't leak into a signed/notarized artifact.

## [0.5.9] - 2026-06-07

### Fixed
- **GUI now shows the real reason a `2nb` action failed, not flag-help noise.** When a command failed at runtime (e.g. a re-embed that couldn't complete), the CLI printed the error followed by its entire flag listing, and the macOS app — which scrapes the last line of stderr — displayed a stray flag description ("--yaml … Output as YAML") instead of the actual error. The CLI now sets cobra's `SilenceUsage`, so a runtime failure prints only the error message (and its "To fix" hints); genuine bad-flag mistakes still surface a clear "Error: unknown flag …" line.

### Added
- **macOS app warns when your `2nb` CLI is older than the app.** `brew upgrade --cask secondbrain` bumps the app but not the `twonb` formula, so you could silently run a new app against an old CLI — which is what made a re-embed fail with no obvious cause. Home now shows an orange banner when the installed CLI is behind, with the `brew upgrade apresai/tap/twonb` command to fix it.

## [0.5.8] - 2026-06-07

### Changed
- **macOS app: saving the Bedrock default now nudges you to re-embed when your stored embeddings no longer match.** If "Save as default" leaves the vault embedded with a different model or dimension (`dimension_break` / `mixed` / `model_mismatch`), the confirmation gently points you at **Re-embed All** instead of a plain "Saved." The wording is honest across all three cases: a dimension break drops you to keyword-only search, while a same-dimension mismatch keeps semantic search running on stale-model vectors (less accurate, not off).
- **macOS app: the index sheet title now stays "Re-embed All" for the whole run.** It previously reverted to "Rebuild Index" mid-run because the flag it read was cleared the moment the run started; the run mode is now carried on the progress state so the title, warning, and confirm copy stay accurate through every phase.

### Fixed
- **macOS app: a dashboard tab can no longer silently drop out of the sidebar.** A new parity test asserts the Home tab plus the Advanced group cover every `DashboardTab` case exactly once and that each tab has an icon, so a case added to the enum but forgotten in the sidebar is caught at test time.

## [0.5.7] - 2026-06-07

### Changed
- **macOS app: "Re-embed All" now warns it's a paid full regeneration.** The Rebuild confirmation sheet showed identical copy for an incremental Rebuild and a full Re-embed; it now reads "Re-embed All" with an orange note that it re-runs paid embedding calls for every document.
- **macOS app: every CLI failure is recorded to the per-vault `.2ndbrain/logs/` file.** Previously only index rebuilds wrote there; now any failed `2nb` action (Save, Test, etc.) logs argv + exit code + stderr to `secondbrain.log`, so the "read the logs to debug" workflow is complete.

## [0.5.6] - 2026-06-07

### Fixed
- **macOS app now shows *why* a CLI action failed.** Every `2nb` call that exits non-zero previously surfaced a useless "CLI exited with code 1" — so a failed Save/Test on the Home screen (or any AI Hub action) told you nothing. `CLIError.nonZeroExit` now carries the trimmed `2nb` stderr, so the actual reason (e.g. "bedrock not ready: AccessDeniedException…") reaches the error banner; an empty stderr still falls back to the exit code. Home also clears a stale failure message when you start a new Rebuild / Re-embed.

## [0.5.5] - 2026-06-07

### Changed
- **macOS app: a consolidated Home screen is now the default.** Home answers the three common-case questions on one surface — is this the vault Obsidian has open (a match badge), is AI set up and working (AWS Bedrock + Claude Haiku 4.5 + Amazon Nova-2, with a ready dot plus Save-as-default and Test buttons), and is the vault indexed (doc/embedding counts with Rebuild Index / Re-embed All). The five existing tabs (Vault Status, AI Settings, MCP Server, Git Integration, Validation) move under an **Advanced** sidebar section; nothing is removed.

### Fixed
- **Rebuild Index no longer hangs, and a vault with empty notes indexes cleanly.** Two bugs compounded: (1) `2nb index` tried to embed empty/whitespace-only notes (e.g. a blank `Untitled.md`), which Amazon Nova-2 rejects with a 400 `ValidationException` (`minLength: 1`) — so the embed count stayed pinned below 100% and `--force-reembed` reported "incomplete"; (2) the macOS app's `startIndex` blocked the main actor with `process.waitUntilExit()` and had no guard against overlapping runs, so the rebuild-progress sheet could freeze on "Running…" and never reach "Done". The CLI now **skips** empty documents (counted as skipped, not failed; nothing is sent to the provider), and the app runs `2nb index` without blocking the main actor, guards against concurrent rebuilds, keys the terminal phase off the process exit code, and surfaces the actual CLI error (not a bare exit code) on failure.

## [0.5.4] - 2026-06-07

### Fixed
- **macOS app AI now works.** A GUI app launched by launchd has no shell environment, so the user's AWS credentials (shell env vars, no `~/.aws/credentials`) were invisible to the `2nb` it spawned and every AI action failed with "bedrock not ready", while the CLI worked in a terminal. The Amazon Bedrock **API key** (bearer token) can now be stored env-independently with `2nb config set-key bedrock <token>` (macOS Keychain); `loadBedrockAWSConfig` exports it for the AWS SDK when `AWS_BEARER_TOKEN_BEDROCK` isn't already set (macOS only; env wins; SigV4 fallback unchanged).

### Changed
- macOS app: `SpotlightIndexer.indexAll` runs its file-read + YAML-parse loop on a background queue instead of the main thread, so opening a large vault no longer freezes the UI.
- `FrontmatterParser` bounds its YAML-AST walk with a recursion-depth guard against pathologically deep frontmatter.

## [0.5.3] - 2026-06-06

### Fixed
- macOS app no longer crashes on launch when indexing a vault that contains Obsidian **template files**. `FrontmatterParser` used `Yams.load`, whose YAML constructor traps (an uncatchable `fatalError`) on template placeholders like `date: {{date}}`; since indexing runs during vault open, a single template note crashed the whole app. The parser now walks the YAML AST (`Yams.compose`) directly, which can't trap, while preserving scalar type fidelity.

## [0.5.2] - 2026-06-06

The Obsidian vault and the 2nb vault are now joined at the hip: every client operates on the vault you have open in Obsidian, never a different one.

### Changed
- **Obsidian plugin** pins every `2nb` command to the open Obsidian vault via `--vault`, so it can no longer resolve a different vault from a stale active-vault file or the working directory. Settings and the setup wizard now show the bound vault and its index state.
- **macOS app** binds to the vault Obsidian currently has open (read from Obsidian's own `obsidian.json`) on launch, leads the Welcome screen with "Open your Obsidian vault", validates that an opened folder is a real Obsidian vault (has `.obsidian/`) and warns when it isn't the one Obsidian has open, and shows the active vault name in the sidebar.

### Removed
- The Obsidian plugin's "Custom Vault Path" setting — it was the only way the Obsidian vault and the 2nb vault could diverge.

## [0.5.1] - 2026-06-06

### Fixed
- `2nb lint` now recurses into subdirectories. The previous top-level `*.md` glob silently checked only files in the vault root and skipped every note (and every broken wikilink) in a subfolder.
- `2nb lint` no longer reports a missing frontmatter `id` as an error, consistent with the path-based identity model (a document's identity is its path; `id` is read if present but never required).
- `2nb lint` skips Obsidian template files whose frontmatter holds unresolved `{{placeholder}}` tokens, so template scaffolding no longer produces false-positive parse errors.

## [0.5.0] - 2026-06-06

Obsidian-native pivot: Obsidian stays the editor, the `2nb` CLI plus MCP server is the engine, and the macOS app becomes a configuration dashboard. Notes are never rewritten; all derived state lives in a gitignored `.2ndbrain/` sidecar.

### Added
- Read-only indexing of `.canvas` (JSON Canvas) and `.base` (YAML Bases) files as synthetic views; `meta` and `kb_update_meta` refuse to write them.
- Obsidian Flavored Markdown parsing: embeds, `[[note#^block]]` block references, `^block-id` definitions, inline `#tags`, `%% comment %%` stripping, and markdown-link extraction.
- `2nb migrate` to upgrade a legacy 2ndbrain vault to the Obsidian-native format (schema v3), with `--dry-run`; source markdown is never modified.
- Automatic `.obsidian` vault detection and sidecar creation, shortest-unique-path plus alias resolution, and YAML-AST frontmatter preservation.
- Schema v3: `aliases` table and `block_id` columns on `chunks` and `links`.
- Obsidian plugin (`obsidian-2ndbrain`): a thin wrapper over `2nb` that downloads and manages the CLI binary itself, ships a first-run setup wizard, and installs via BRAT with no npm build. Release CI now publishes plugin assets and `versions.json`.
- LLM-facing test battery (real-stdio MCP, migrate, RAG, canvas/base), JSON-envelope contract tests, and OFM unit tests.

### Changed
- AWS Bedrock is now the default provider: Claude Haiku 4.5 for generation and Amazon Nova-2 for embeddings.
- Ollama and OpenRouter are opt-in (disabled by default); the setup wizard enables them.
- Path-based identity: UUIDs in frontmatter are read for backward compatibility but never written, generated, or required.
- The macOS app is now a `NavigationSplitView` configuration and companion dashboard rather than an editor.
- Documentation (getting-started, user guide, plugin README, `CLAUDE.md`) synced to the shipped behavior.

### Removed
- Editor views from the macOS app (editor area, graph, sidebar, tabs, search panel, and related surfaces).
- The dead `embedding:` configuration block.

## [0.4.3] - 2026-05-17

### Fixed
- Release workflow now stages `VERSION` and `Version.swift` in the release commit, preventing version drift between tagged releases and source files.


## [0.4.2] - 2026-05-17

### Added
- AI Hub model catalog picker (`ModelCatalogPickerView`) with sidebar+detail layout, filters (type/provider/tier/enabled/tested/compatible), and Best/Cheapest/Fastest/Newest/Name sort modes
- Retrieval-quality probe for `models bench` that scores stored embeddings against resolved wikilinks (MRR@K, Recall@K) with zero API cost
- `SecondBrainCore.LineBuffer` utility extracted for streaming line buffering
- Extensive test coverage across CLI and app: AI Hub contract, core commands contract, catalog merge, user catalog, bench probes, MCP tools, force-reembed, embedding store, JSON decoding, wizard logic, view construction

### Changed
- Claude model defaults bumped to 4.6/4.7
- Safer `models test`/`bench`/`set-active` flows: `Set Active` gated on indexing state to prevent mixed-model embeddings; benchmark and cost-preview paths hardened
- CLAUDE.md trimmed and reorganized

### Removed
- Claude 3.5 entries dropped from default catalog


## [0.4.1] - 2026-04-24

### Fixed

- AI Hub: vendor batch enable/disable now correctly applies to discovered-only models that lack a builtin catalog entry


## [0.4.0] - 2026-04-24

### Added
- AI Hub catalog now groups models by vendor with collapsible disclosure sections showing model counts
- Search field in AI Hub filters the model catalog by model ID or vendor name
- Bulk "Enable all" / "Disable all" buttons per vendor group in AI Hub catalog
- Contract test suite verifying CLI output matches Swift decoder expectations for AI Hub


## [0.3.1] - 2026-04-24

### Fixed

- **AI Hub provider toggles**: `config set` now accepts `ai.bedrock.disabled`, `ai.openrouter.disabled`, and `ai.ollama.disabled` keys, enabling the AI Hub enable/disable provider cards to persist correctly
- **Log message redaction**: Unredacted AI Hub action logs in the macOS app (model IDs, provider names, CLI commands, error output were being suppressed by the OS privacy filter)


## [0.3.0] - 2026-04-24

## [0.3.0] - 2026-04-24

### Added
- **AI Hub** — unified AI configuration surface (AI menu > AI… · Cmd+Shift+,) combining provider setup, model wizard, and connection testing into a single panel with provider cards, active model selectors, and a full model catalog with Test/Set active/Enable/Disable/Discover actions

### Removed
- AI Setup Wizard, Model Wizard, and Test AI Connection as separate menu items/views (replaced by AI Hub)


## [0.2.18] - 2026-04-24

## [0.2.18] - 2026-04-24

### Fixed
- Model Wizard: prevent double-tap from triggering duplicate actions

### Changed
- Expanded Bedrock Converse model allowlist to support additional models


## [0.2.17] - 2026-04-24

## [0.2.17] - 2026-04-24

### Fixed
- GUI pipe-buffer deadlock that caused the app to hang when CLI commands produced large output


## [0.2.16] - 2026-04-24

## [0.2.16] - 2026-04-24

### Added
- `models wizard` CLI command — interactive end-to-end provider → discover → pick → cost preview → test → save flow with `--json` streaming events for GUI/automation
- Model Wizard panel in macOS editor (AI menu) — grouped model list with tier badges, scope picker, cost preview, and test-and-save flow
- `models cost-preview` command — estimate USD cost of running probes across one or more models without API calls
- Invoke strategy system — `InvokeStrategy` field on catalog entries routes models to the correct API dialect; adding new model variants no longer requires code changes
- Bedrock Converse strategy (`bedrock_converse`) as a first-class invoke target alongside existing provider-specific strategies
- Retrieval-quality probe — scores stored embeddings via MRR@K and Recall@K over resolved wikilink pairs at zero API cost
- Live catalog sync in macOS editor — CLI writes to the model catalog refresh the UI automatically via FSEvents without reopening the vault
- Enable/disable toggle for models — `models enable` / `models disable` commands hide models from selection dropdowns; `models list --enabled-only` filters accordingly

### Fixed
- MCP server: path traversal vulnerability in tool handlers
- Tag parsing: edge cases in frontmatter tag normalization
- Schema migration: data-integrity issue during version upgrades
- Merge conflict view: observer memory leak
- Graph traversal: duplicate node/edge deduplication
- `purgeStale`: stale document removal correctness
- OpenRouter: env variable resolution for API key
- Document create: missing transaction boundary
- Duplicate shortcut: filename collision on rapid invocation
- `import-obsidian`: now correctly honors the active vault instead of defaulting to cwd


## [0.2.15] - 2026-04-23

### Fixed

- Bedrock live pricing now correctly resolves model IDs to AWS offer file entries, fixing cases where pricing showed "unknown" for supported models (Nova, Titan, Cohere, TwelveLabs Marengo, and cross-region inference profiles)


## [0.2.14] - 2026-04-23

Based on the diff analysis, here is the CHANGELOG entry:

```markdown
## [0.2.14] - 2026-04-23

### Fixed
- Bedrock model discovery now skips legacy and lifecycle-blocked foundation models correctly
- Model type (embedding vs. generation) is now detected from `FoundationModelDetails` instead of inferred from model class, improving accuracy for text and multimodal models
- Bedrock `--discover` no longer includes non-text-input models in results
```


## [0.2.13] - 2026-04-22

### Added
- TwelveLabs Marengo embedding models via Bedrock InvokeModel (`models add --provider bedrock --type embedding --price-request`)
- Live pricing fetched from OpenRouter `/models` API and AWS pricing offer files with 24h disk cache
- Per-model pricing overrides via `models add` flags (`--price-in`, `--price-out`, `--price-request`)

### Changed
- `models list` and `ai status` now display live pricing data; falls back to stale cache then builtin metadata in air-gapped environments
- Bedrock provider expanded to support embedding model invocations

### Fixed
- Frontmatter parsing edge cases


## [0.2.12] - 2026-04-22

### Added
- Live pricing for `models list`, `ai status`, and `index` — fetched from OpenRouter and AWS pricing APIs with a 24-hour disk cache (`~/Library/Caches/2nb/pricing`); falls back to stale cache then builtin metadata when offline
- TwelveLabs Marengo embed family support via Bedrock InvokeModel (Marengo 2.7 and 3.0 request/response formats); add via `2nb models add <model-id> --provider bedrock --type embedding --price-request <USD>`
- `--price-request` flag on `models add` for per-request priced embedding models

### Fixed
- Bedrock model discovery failures for reasoning models, system prompts, variant IDs, and embedding formats


## [0.2.11] - 2026-04-22

### Fixed

- **Bedrock discovery**: context-window variant IDs (e.g. `model:0:24k`, `model:0:512`) are no longer returned as invokable models — they 404 when called directly
- **Bedrock discovery**: inference profiles are now type-classified correctly instead of being hardcoded as `generation`
- **Bedrock generation**: reasoning models (e.g. DeepSeek R1) that emit non-text content blocks first now extract the text response correctly
- **Bedrock generation**: models that reject system prompts now get a transparent retry without one, cached per process
- **Bedrock embeddings**: Cohere Embed v4's `{"embeddings": {"float": [...]}}` response shape is now parsed correctly alongside v3's flat array format
- **Bedrock embeddings**: inference profile geo-prefixed IDs (`us./eu./ap./global.`) are stripped before embed format detection; Titan image models now return a clear unsupported error instead of silently failing


## [0.2.10] - 2026-04-22

Now I have a complete picture of the changes. Here's the changelog entry:

```markdown
## [0.2.10] - 2026-04-22

### Added
- Bedrock `--discover` now merges system-defined inference profiles (`us.*`, `eu.*`, `ap.*`, `global.*`) with foundation models, returning the correct invokable IDs for newer Claude and Nova generation models
- Bedrock embedder supports multiple embedding API formats — Nova v2 (default), Titan v1, Titan v2, and Cohere v3 (batched, ≤96 texts per call) — detected automatically from the model ID
- New verified embedding models: `amazon.titan-embed-text-v2:0` (256/512/1024 configurable dims) and `cohere.embed-english-v3` / `cohere.embed-multilingual-v3`
- New verified generation models: Claude Haiku 4.5, Sonnet 4, and Opus 4 via cross-region Bedrock inference profile IDs (`us.anthropic.claude-*`)
- Bedrock generator retries automatically without temperature when a model rejects it (e.g. Claude Opus 4.7), caching the result for the process lifetime
```


## [0.2.9] - 2026-04-22

## [0.2.9] - 2026-04-22

### Added
- `models list --discover --promote` flag: discovered models that pass concurrent smoke-testing are automatically added to the user catalog


## [0.2.8] - 2026-04-22

### Changed
- `GenOpts.Temperature` is now `*float64`; pass `nil` to omit temperature from the request (model uses its default), or use `ai.Ptr(value)` to set an explicit value

### Fixed
- Bedrock generation no longer fails permanently when a model rejects the `temperature` inference parameter; the generator retries once without temperature and caches the result for the process lifetime


## [0.2.7] - 2026-04-22

### Fixed

- Bedrock provider no longer sends `Temperature` in `InferenceConfiguration` when not explicitly set, preventing API errors on models that reject a zero-value temperature field


## [0.2.6] - 2026-04-21

### Fixed
- `completion install` now works when gcloud, Homebrew, or other tools run `compinit` before 2ndbrain's completion block


## [0.2.5] - 2026-04-21

### Changed
- `completion install` is now hardened for real-world zsh setups: handles existing `fpath` entries, early-return guards in `.zshrc`, missing completion directories, and multiple `2nb` binaries on PATH

### Added
- Test suite for `completion install` covering edge cases in `.zshrc` parsing and completion directory detection


## [0.2.4] - 2026-04-21

## [0.2.4] - 2026-04-21

### Added
- `completion install` now automatically updates `~/.zshrc` with the required `fpath` entry and `compinit` block — no manual shell config edits needed after running the command
- Golden-path E2E battery test suite covering core CLI workflows (`cli/battery_test.go`)
- GUI test scripts for polish diff flow and vault-switch persistence

### Changed
- Updated `2ndbrain-skill.md` agent skill content with expanded MCP vs CLI guidance and test battery design


## [0.2.3] - 2026-04-19

### Fixed
- Frontmatter parser edge cases causing incorrect document metadata extraction
- Database migration reliability for vault upgrades
- MCP server tool responses for `kb_related`, `kb_search`, and `kb_index`
- Graph traversal returning incorrect depth results
- Swift `VaultManager` initialization sequence causing intermittent vault open failures

### Added
- AI provider availability tracking with per-provider health checks
- Rate limiting for AI provider requests to prevent throttling errors


## [0.2.2] - 2026-04-19

### Added
- `models calibrate` command: samples random document pairs from the active vault, computes cosine similarity distribution (p50/p90/p95/p99), and recommends a threshold at p95+0.01; supports `--samples`, `--save`, `--scope`, and `--seed` flags
- Per-model recommended similarity thresholds in the built-in model catalog (Nova-2: 0.65, Nemotron-VL: 0.60, nomic-embed-text: 0.50, mxbai/snowflake/bge-m3: 0.55, all-minilm: 0.35)
- `--similarity-threshold` flag on `models add` to persist a custom threshold to the user catalog
- `models list` now shows a THRESHOLD column with per-model recommendations
- `ai status` now reports the active similarity threshold and its source (vault config / user calibration / model recommendation / default)
- Search results now display RRF and cosine scores (`rrf=X.XXX, cos=Y.YYY`) inline for relevance transparency

### Changed
- Similarity threshold resolution follows a priority chain: vault `ai.similarity_threshold` → user catalog calibration → model's built-in recommendation → global default (0.20)


## [0.2.1] - 2026-04-19

## [0.2.1] - 2026-04-19

### Fixed
- Skills Install dialog now shows a persistent Close button in every installation state


## [0.2.0] - 2026-04-17

### Added
- **`vault` parent command** with five subcommands: `status`, `show`, `create`, `set`, `list`. Bare `2nb vault` prints a unified health report (docs, embedding coverage, portability, AI reachability, stale-doc count) mirroring the macOS editor's Vault Status panel.
- **`vault create`** initializes a new vault and activates it. Replaces `2nb init` (kept as a hidden deprecated alias).
- **`vault list`** shows recently-used vaults from `~/.2ndbrain-vaults`, with `*` marking the active one; stale paths are pruned on read.
- **State-aware next-step hint on bare `2nb`** — prints one of "create a note / ai setup / index / search" based on current vault state.
- **`2nb completion` command tree** — emitters for zsh / bash / fish / powershell plus a `completion install` subcommand that writes `~/.zsh/completions/_2nb` and prints the rc snippet to activate it.
- **Dynamic shell completion** on 15+ commands via Cobra `ValidArgsFunction` / `RegisterFlagCompletionFunc`: vault paths, markdown files (from the index), schema types/statuses, agent slugs, model IDs (filtered by `--provider`), AI providers, config keys, sort fields, bench probes, catalog scopes.
- **Homebrew auto-installs completions** — `brew install apresai/tap/twonb` now ships zsh/bash/fish completions via GoReleaser's `generate_completions_from_executable`.
- **Cobra `Example:` blocks** on `create`, `index`, `search`, `ask`, `list`, `read`, `ai setup`, `mcp-setup`, `skills install`, and the full `vault` subtree so `--help` shows real invocations.
- `ai.KnownProviders` and `settableConfigKeys` as canonical lists so provider names and config keys stay in sync across switch statements, error messages, and completion.
- `vault.FindVaultRoot` exported for read-only callers (e.g. completion) that need the vault root without paying a full `vault.Open`.

### Changed
- `2nb init` is a hidden deprecated alias that delegates to `vault create`; existing scripts keep working with a deprecation notice.
- Root `--help` gains a Quick Start block and an Examples section.
- `2nb` with no args uses `EmbeddingCounts()` in a single query instead of two separate `COUNT`s.
- `vault status` probes embedder and generator reachability concurrently, halving worst-case latency.
- `config get` / `config set` error message now reads from the canonical key list, so it stays accurate when keys are added.

### Fixed
- Shell completion for `config set`/`get` now suggests the keys the commands actually accept (`ai.bedrock.profile`, `ai.openrouter.api_key_env`, `ai.ollama.endpoint`, and so on) — previously the completion list had drifted from `setConfigValue`.
- Schema completers (`create --type`, `list --type`, `--status`, `meta --set`) bypass the full vault open and read `schemas.yaml` directly, so tab presses never run SQLite migrations or emit config-self-heal stderr.

## [0.1.16] - 2026-04-17

### Added
- `models add` command to add custom models to a user-maintained catalog (`~/.config/2nb/models.yaml` or per-vault `.2ndbrain/models.yaml`); entries appear in `models list` as `tier=user_verified` and in the AI setup wizard's Custom mode picker
- `models remove` command to remove models from the user catalog by model ID, provider, and scope
- User catalog layer merged into `BuildModelList`, supporting both global and per-vault scopes with conflict resolution


## [0.1.15] - 2026-04-17

### Added
- Obsidian-style force-directed document graph view with canvas renderer, inspector panel, zoom/pan/drag, hover and selection highlighting, and Barnes-Hut quadtree simulation (O(n log n) at scale)
- Graph inspector panel with mode, filter, force, and color-group controls; global/local view modes
- Vault Status panel (Vault menu) — unified health view showing index state, embedding portability, stale docs, and provider reachability with Rebuild Index and Re-embed All actions
- AI Test Connection panel (AI menu) — live model probe with latency display and link to AI Setup on failure

### Changed
- Menu bar reorganized into Notes, Vault, and AI menus; File menu renamed to Notes
- Preview mode is now read-only; removed the editable preview round-trip

### Removed
- Editable preview (Turndown.js contenteditable round-trip) — corrupted markdown containing Mermaid diagrams and produced WebKit rendering artifacts


## [0.1.14] - 2026-04-16

### Added
- Commit detail view: click any commit in Git Activity to see changed files and per-file unified diffs
- `2nb git show <hash>` CLI command with `--json` support
- Outline panel click-to-scroll navigation in the editor
- Syntax highlighting for fenced code blocks in the editor
- Wikilink parsing and location resolver with heading anchor support

### Fixed
- `git.Show()` mishandled pathological filenames and git-quoted paths
- Race condition in commit detail diff loading
- Sidebar selection reliability after document operations
- Tab bar dirty-state indicator edge cases


## [0.1.13] - 2026-04-15

## [0.1.13] - 2026-04-15

### Added
- `2nb index --force-reembed` flag: invalidates all stored embeddings and re-embeds from scratch (use after switching providers)
- `2nb ai status` now reports vault portability state — dimension mismatch, model mismatch, provider unavailable, mixed embeddings, unindexed — with one-line fix hints
- `VectorCompat` helper: `search` and `ask` automatically degrade to BM25-only with a stderr warning when stored embeddings are incompatible with the current provider
- Vault `.gitignore` initialized by `2nb init` now excludes `config.yaml`, `index.db` (+ WAL), `bench.db`, `logs/`, `recovery/`, `mcp/`, and `*.bak`
- `config.yaml` self-heals: missing or corrupt config regenerates from defaults; corrupt original preserved as `.bak`
- macOS app shows a yellow warning banner over search and Ask AI results when the CLI reports degraded vector mode
- macOS app AI status dot turns yellow on any non-OK portability state

### Changed
- **Breaking:** `2nb search --json` and `2nb ask --json` now return structured envelopes — `{mode, warnings, results}` and `{mode, warnings, answer, sources}` respectively; consumers must extract `.results` / `.answer` instead of decoding a raw array/object


## [0.1.12] - 2026-04-14

### Fixed
- Remove `nonisolated` from `WKScriptMessageHandler` conformance for Xcode 16.4 compatibility


## [0.1.11] - 2026-04-14

### Added
- SecondBrain.app distributed via Homebrew Cask (`brew install --cask apresai/tap/secondbrain`)
- GitHub Actions workflow builds, packages, and publishes the macOS app bundle on release tags
- Cask template (`casks/secondbrain.rb.tmpl`) for automated formula generation


## [0.1.10] - 2026-04-14

### Added
- Search results now display RRF and cosine similarity scores (`rrf=X.XXX, cos=Y.YYY`) for transparency into hybrid ranking
- Parent-command defaults: running `2nb ai`, `2nb models`, `2nb git`, `2nb mcp`, `2nb skills`, or `2nb config` without a subcommand now invokes the most useful read-only action instead of printing help

### Changed
- Expanded MCP tool descriptions and skill file content with richer LLM-facing context for all 16 tools

### Fixed
- Wikilink resolution correctness (title/filename matching edge cases)
- Vector search threshold filtering applied consistently across hybrid and standalone semantic queries


## [0.1.9] - 2026-04-14

## [0.1.9] - 2026-04-14

### Added
- **Git integration** — `2nb git activity`, `git diff`, `git status` CLI commands; sidebar modified/untracked indicators; Git Activity panel (Cmd+Shift+G) and diff viewer in editor
- **AI Polish** — `2nb polish` CLI command with diff preview; editor panel (Cmd+Option+P) with Accept / Open-as-new-tab / Reject flow
- **Suggest Links** — `2nb suggest-links` via vector search; editor panel (Cmd+Shift+L) with click-to-insert wikilinks
- **MCP observability** — sidecar status files per server process; `2nb mcp status` command; MCP Status panel (Cmd+Shift+M) in editor with per-client tool invocation history
- **Editable preview mode** — WYSIWYG editing in preview via WKWebView ↔ Turndown.js bridge; source/split/preview segmented control in toolbar
- **Merge conflict dialog** — side-by-side diff when FSEvents detects an external edit to a dirty tab
- **Autosave** — configurable interval (Off / 15s / 30s / 60s) in Preferences
- **Safety features** — pre-write crash snapshots, low-disk warning (<50 MB), filename collision suffix (-1, -2, …)
- **Directory tree sidebar** with tag split pane, multi-tag filter, and breadcrumb bar
- New, Save, and Share toolbar buttons in editor
- Incremental re-embed on document save
- High-resolution macOS application icon set (16 px – 1024 px)


## [0.1.8] - 2026-04-11

### Added
- **Editor Preferences** (Cmd+,): font family and size picker with live preview
- **Tag drill-down**: click any tag in the sidebar to browse a filtered document list with back navigation
- **Index rebuild dialog**: confirmation step, dual progress bars (indexing + embeddings), and post-rebuild stats summary
- **Structured CLI logging**: slog-based log output to `.2ndbrain/logs/cli.log`; `--verbose` additionally routes to stderr
- **GUI test suite for index operations** (`tests/gui-test-index.sh`)

### Changed
- Export controller expanded with additional format and output path handling
- Editor area layout and rendering improvements


## [0.1.7] - 2026-04-10

## [0.1.7] - 2026-04-10

### Added
- **AI Setup Wizard** — 4-step guided wizard for configuring AI providers, credentials, models, and running a connectivity test
- **Skills Install panel** — Tools menu panel for installing SKILL.md files for 8 AI coding agents
- **MCP Setup panel** — Tools menu panel showing MCP config snippets for 6 AI tools
- **Lint Results view** — Clickable lint issue list shelled out from `2nb lint --json`
- **App icon** — Custom app icon (1024px PNG + ICNS)
- **Swift test suite** — Unit tests covering JSON decoding, frontmatter parsing, markdown rendering, and wizard logic (636 lines across 4 test files)


## [0.1.6] - 2026-04-10

## [0.1.6] - 2026-04-10

### Added
- `skills` command — discover and display vault-specific Claude skill instructions
- Easy mode option to `ai setup` wizard for simplified provider configuration
- Command grouping in CLI help output for better discoverability
- Real-API integration tests for Bedrock, OpenRouter, graph traversal, MCP tools, and output formatters

### Changed
- OpenRouter easy mode default model updated to Gemma 4 31B
- Model test and bench probes now include a system prompt for more realistic evaluation

### Fixed
- `ai status` pricing now reads from the model catalog instead of calling provider `ListModels`, correcting displayed costs


## [0.1.5] - 2026-04-09

## [0.1.5] - 2026-04-09

### Changed
- `ai setup` rewritten as an interactive multi-provider wizard supporting Bedrock, OpenRouter, and Ollama with guided configuration and validation
- README updated with model catalog reference, benchmarking workflows, and Converse API documentation


## [0.1.4] - 2026-04-09

### Added
- `models test <model-id>` command to smoke-test any model with an embed or generate probe
- `models bench` command suite for benchmarking models against your vault with persistent history
- `models bench fav` / `models bench unfav` / `models bench favs` to manage benchmark favorites
- `models bench history` to review past benchmark runs
- `models bench compare` for side-by-side latency comparison of favorited models
- Benchmark history and favorites persisted in `.2ndbrain/bench.db`

### Changed
- Bedrock provider migrated from InvokeModel API to Converse API


## [0.1.3] - 2026-04-09

### Added
- `models list` now shows a rich, status-aware model catalog indicating which models are configured, available, and ready to use
- Model catalog with merge logic to combine built-in and runtime-discovered models across providers (Bedrock, OpenRouter, Ollama)


## [0.1.2] - 2026-04-07

## [0.1.2] - 2026-04-07

### Added
- OpenRouter retry logic with exponential backoff and request throttling
- Cost awareness for OpenRouter API usage (`ai status` and `ai cost` tracking)
- GitHub Actions release workflow improvements
- `index` command now reports embedding generation progress

### Fixed
- 7 GUI crash bugs across editor, properties, tabs, status bar, autocomplete, crash recovery, and app state
- Homebrew formula renamed to `twonb` (Ruby class names cannot start with a digit)

### Changed
- `.gitignore` simplified
- Press release updated to acknowledge Obsidian inspiration


## [0.1.1] - 2026-04-06

## [0.1.0] - 2026-04-04

### Added
- Go CLI (`2nb`) with 24 commands for vault management, search, and AI
- MCP server with 9 tools for Claude Desktop integration
- Native macOS editor (SwiftUI + AppKit) with tabs, search, graph view
- Hybrid search: BM25 (FTS5) + vector search with Reciprocal Rank Fusion
- RAG Q&A via `2nb ask` with source citations
- Three AI providers: AWS Bedrock, OpenRouter, Ollama (local)
- Local AI readiness check via `2nb ai local`
- Document types with schemas: ADR, Runbook, Note, Postmortem
- Wikilink resolution and link graph traversal
- Obsidian import/export with frontmatter normalization
- Spotlight indexing, crash recovery, file watching
- GUI: Ask AI panel, semantic search toggle, AI status indicator
