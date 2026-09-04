# Cycle 5 proof (untracked)

Base for every proof: `8ff7ec3` (Release v0.22.3).
Every run below used
`env -u AWS_BEARER_TOKEN_BEDROCK -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_PROFILE
2NB_BEDROCK_SKIP_KEYCHAIN=1 go test ...`, and the real-binary probes also set `HOME` to a
scratch directory.

Method note: the first F1 proof was run BEFORE committing, so `git checkout HEAD -- <files>`
restored the base (HEAD was still `8ff7ec3`) and the working changes were lost and redone.
Every later proof commits first, then checks out `8ff7ec3`, runs, and restores from HEAD.

## F1 (commit 8330f2e) `document: an unquoted frontmatter date is still a date`

Reverted `cli/internal/document/document.go` and `cli/internal/cli/meta.go` to `8ff7ec3`
(`frontmatter_scalar.go` is new, so it stayed; unused functions compile in Go).

    --- FAIL: TestParse_UnquotedFrontmatterDateIsStillADate
        unquoted timestamp:        CreatedAt = "", want "2020-01-01T00:00:00Z"
                                   ModifiedAt = "", want "2020-02-02T03:04:05Z"
        unquoted date, no time:    CreatedAt = "", want "2020-01-01T00:00:00Z"
        unquoted with an offset:   CreatedAt = "", want "2020-01-01T00:00:00+02:00"
    --- FAIL: TestParse_UnquotedScalarStringFields
        ID = "", want "7";  Title = "", want the normalized date
        Type = "", want "3";  Status = "", want "true"
    --- FAIL: TestExtractTagsAndAliases_CoerceScalarEntries
        Tags = [real], want [2026-09-04T00:00:00Z 42 true real]
        aliases = [plain], want [2026-09-04T00:00:00Z plain]
    --- FAIL: TestExtractTags_BareScalar
        "tags: 2026-09-04" -> Tags = [], want [2026-09-04T00:00:00Z]
        "tags: 42"         -> Tags = [], want [42]
    --- FAIL: TestContract_UnquotedFrontmatterDateReachesTheIndex
        unquoted.md created_at = "", want 2020-01-01T00:00:00Z
        unquoted.md modified_at = "", want 2020-01-01T00:00:00Z
        quoted=[2020-01-01T00:00:00Z 2020-01-01T00:00:00Z] unquoted=[ ]
    --- FAIL: TestContract_StaleFindsAnUnquotedDate
        stale --since 30 did not return unquoted.md; it returned map[quoted.md:true]
    --- FAIL: TestContract_MetaGetDateRoundTrip/unquoted.md
        meta --get modified = "2020-01-01 00:00:00 +0000 UTC", want 2020-01-01T00:00:00Z

GUARDS (pass at base, must keep passing, labelled as such in the test comments):

- `TestContract_MetaSetLeavesAnUnquotedDateAlone`: `meta --set status=complete` leaves the
  untouched `modified:` line byte-identical. It round-trips at base BY ACCIDENT (yaml.Marshal
  of a `time.Time` re-emits the unquoted timestamp), so nothing today would have caught
  normalizing the parsed map. Mutation proof: setting `meta["modified"]` to the normalized
  string inside `Parse` makes it fail with a requoted `modified: "2020-01-01T00:00:00Z"`,
  which is why the map is deliberately left alone.
- `TestParse_LeavesFrontmatterValuesUntouched`, `TestFrontmatterTime_RefusesNonDates`,
  `TestFrontmatterHelpers_MissingKey`: same class.

Two existing tests were UPDATED rather than left, because they pinned the behavior this
commit deliberately changes:

- `TestExtractAliases` pinned "drops non-strings". A non-string SCALAR is now coerced; only a
  non-scalar (a nested list or mapping) is dropped, and the new cases pin both.
- `generation_frontmatter_test.go` pinned `IndexGeneration != 3` exactly. Its claim is that
  the 0.22.3 boundary change got a generation of its OWN, so it now asserts `>= 3`; the exact
  equality made every later index-logic bump fail a test about a change it has nothing to do
  with.

## F2 production (commits fec4b08, e2aad13) and its enumeration test (5bd73e4)

Reverted the nine production files of both commits to `8ff7ec3`
(bench.go, config_cmd.go, mcp_setup.go, metrics.go, migrate.go, models.go, skills_cmd.go,
vault_cmd.go, mcp/tooldef.go) and ran the enumeration test. 17 subtests fail:

    --format json wrote NOTHING to stdout (the flag was silently ignored):
      models add, models remove, models enable, models disable, models enable-state,
      metrics clear, config set, vault create
    --format json did not produce a JSON document on stdout:
      vault set          invalid character 'R'   (Registered ...)
      mcp-setup          invalid character 'n' after top-level value
      migrate            invalid character 'V'   (Vault: ...)
      skills show        invalid character '-' in numeric literal (the SKILL.md frontmatter)
      config get         invalid character 'b'   (bedrock)
      models bench fav   invalid character 'A'   (Added ...)
      models bench unfav invalid character 'R'   (Removed ...)
      skills install     invalid character 'I'   (Installed ...)
      skills uninstall   invalid character 'U'   (Uninstalled ...)

`TestContract_EveryCommandIsClassifiedForFormat` is the completeness half and passes at base
by construction (it walks the tree, it does not run anything). It is the guard that makes the
fix stick: a command added tomorrow lands in neither map and fails it. Proved by mutation:
deleting one entry from `formatContractArgv` fails with
`"2nb aliases" is a runnable command with no format-contract classification`, and adding a
stale entry fails with `formatContractUnexercised names "2nb nope", which is not a runnable
command any more`.

Stability: the exercised half was run 12 times (Go randomizes subtest map order). The first
runs were flaky and both causes were fixed in the FIXTURE, not worked around:

- every command that mutates a note body now has its own target note (`task doc.md 3` failed
  whenever `replace` had already shortened the body);
- `import-obsidian` now gets its own vault (see NEEDS-DECISION below);
- the `fixture.model` catalog row is seeded so `models remove` does not depend on
  `models add` having gone first.

## Indexer fix (commit e237609)

Reverted `cli/internal/vault/indexer.go` to `8ff7ec3`:

    --- FAIL: TestIndexFile_ReportsAFailedIDLookup
        error should name the id lookup that failed, got: begin transaction: sql: database is closed

Honest scope of that test: it drives the branch with a real closed database rather than a real
SQLITE_BUSY. Forcing a busy deterministically needs either a fake driver (this repo forbids
mocks) or out-waiting the 5s busy_timeout four times. What it pins is the behavior that
matters: a lookup failure is REPORTED against the lookup instead of being swallowed into a new
uuid that then fails on the path UNIQUE constraint, which RetryBusy cannot retry.

## F3 (commit 734eead)

Reverted `cli/internal/output/formatter.go` to `8ff7ec3`:

    --- FAIL: TestDelimited_NilPointerIsAnEmptyCell/csv
        csv still renders a nil pointer as Go syntax
        the all-nil row should carry empty cells, got ["all-nil" "<nil>" "<nil>" "<nil>"]
    --- FAIL: TestDelimited_NilPointerIsAnEmptyCell/tsv   (same two)
    --- FAIL: TestText_NilPointerInAMapIsAnEmptyValue
        --format text still renders a nil pointer as Go syntax
    --- FAIL: TestContract_ModelsListCarriesNoGoNilToken/csv
        --format csv carries the Go token <nil>; a nil pointer is an empty cell
    --- FAIL: TestContract_ModelsListCarriesNoGoNilToken/tsv   (same)

Note on coverage: the pre-existing `TestContract_TextFormatRendersReadableLines` already
asserted `models list --format text` carries no `<nil>` and PASSED at base, because
`structTextPairs` skips nil struct fields entirely. It is not F3 coverage. The text half is
driven through a MAP payload, where `mapTextPairs` calls `delimitedCell` with no such skip.

Three existing tests pinned the old token and were updated to pin the empty cell:
`TestWriteDelimited_EmptyAndNilComposites` (a nil MAP still renders `null`, which is JSON),
`TestWriteDelimited_NilTextMarshalerPointer`, `TestWriteDelimited_PointerCellsRenderTheirValue`.
The TextMarshaler one also gained a second column: a one-column row whose only cell is empty
writes as a blank LINE and encoding/csv's reader skips blank lines, so the old fixture would
have tested CSV's own edge case rather than this rendering.

## F4 (commit 5e52aac)

Reverted `cli/internal/cli/models.go` to `8ff7ec3`:

    --- FAIL: TestEnableStateHelpMarksStateRequired
        --state is enforced as required but its help does not say so:
        "State: default, enabled, disabled"

Confirmed against the built binary before and after:

    before: --state string      State: default, enabled, disabled
    after:  --state string      State: default, enabled, disabled (required)

## NEEDS-DECISION: `import-obsidian` rewrites the TARGET vault's existing notes

Found while stabilizing the enumeration test; verified by reading
`cli/internal/cli/import_obsidian.go` and reproduced as an intermittent failure in that test
before the fixture was changed.

`runImportObsidian` resolves a target vault (`--target`, else `--vault`, else `2NB_VAULT`,
else the cwd vault), copies the source markdown into it with `copyMarkdownFiles`, and then
`filepath.Walk`s the WHOLE TARGET, stamping `id`, `type`, `status`, `created`, `modified` and
`title` into every note that lacks them. It is not scoped to the files it copied.

Consequence on a target vault that already holds notes without an `id`, which is every
vanilla Obsidian vault (the path-based identity model states an id is "read and preserved when
present but never required"): every such note gets a FRESH uuid written into its frontmatter,
different from the surrogate id its index row already carries. The row is then orphaned, and
the next single-file reindex of that note (`tag add`, `task`, `append`, an editor save through
the app's watcher) fails with
`upsert document: constraint failed: UNIQUE constraint failed: documents.path`.

Observed directly: after an `import-obsidian` into the shared test vault, `tag add` on an
unrelated note in that vault failed with exactly that error, with the note's file claiming an
id no index row held.

Proposed fix, NOT implemented (it changes a documented legacy-conversion command's behavior
and is outside this cycle's fence): scope the rewrite walk to the paths `copyMarkdownFiles`
actually copied, rather than the whole target vault.

The enumeration test works around it honestly rather than silently: `import-obsidian` is
exercised against its OWN vault, with the reason written at the call site.

## Note on credentials during the gate

The gate ran `make test` under the spec's prescribed form
(`env -u AWS_BEARER_TOKEN_BEDROCK -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_PROFILE
2NB_BEDROCK_SKIP_KEYCHAIN=1`), which is what was asked for. That form does NOT isolate `HOME`,
so `~/.config/2nb/bedrock.json` stayed above the Keychain in the precedence chain and was
readable by any test that does not set its own `HOME`. `internal/ai` at 72s and the root
package at 160s are consistent with real provider calls. Every ad-hoc CLI repro in this cycle
DID isolate `HOME` to a scratch directory. Worth tightening the `go test` guidance in a later
cycle's spec.
