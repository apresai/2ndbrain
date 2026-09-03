# PROOF.md (untracked): every new or extended test proven to fail at dd70c88

Baseline: dd70c88 (Release v0.22.2). Method, per test: after the fix commit
exists, `git checkout dd70c88 -- <non-test files the test exercises>`, run the
test (must FAIL), `git checkout HEAD -- <the same files>`, run again (must
PASS). `git checkout -- <file>` with no revision restores from HEAD and proves
nothing, so it is never used here.

---

## Commit 1: `output: --format text renders readable lines, never a Go struct dump` (2f33aec)

Reverted: `cli/internal/output/formatter.go`

### `TestWrite_TextStructSliceIsNamedPairs` (internal/output)
FAIL at dd70c88, first failing lines:
```
formatter_test.go:203: line 0 is still a Go struct dump: "{a.md <nil> 2026-09-03 13:22:25 +0000 UTC map[alpha:one zeta:1]  0}"
formatter_test.go:207: text output carries a heap address:
    {b.md 0xfee5288ad0 0001-01-01 00:00:00 +0000 UTC map[]  2}
formatter_test.go:216: row 1 missing "path=a.md"
```
PASS at HEAD.

### `TestWrite_TextSingleStructIsNamedLines` (internal/output)
FAIL at dd70c88:
```
formatter_test.go:252: nested pointer rendered as a heap address:
    {vault 0xfee528e1e0 <nil>}
```
PASS at HEAD.

### `TestWrite_TextMapIsSorted` (internal/output)
FAIL at dd70c88:
```
formatter_test.go:278: map text = "map[alpha:one mid:true zeta:1]\n", want "alpha: one\nmid: true\nzeta: 1\n"
```
PASS at HEAD.

### `TestWriteDelimited_PointerCellsRenderTheirValue` (internal/output)
FAIL at dd70c88:
```
formatter_test.go:572: csv rendered a pointer as a heap address:
    reachable,count,missing
    0xfee5289108,0xfee5289100,<nil>
```
PASS at HEAD.

### `TestContract_TextFormatRendersReadableLines` (internal/cli)
FAIL at dd70c88, all four subtests:
```
format_coverage_test.go:187: line is a Go struct dump, not text: "{27480240-... doc.md Doc note draft }"   [list]
format_coverage_test.go:187: line is a Go struct dump, not text: "{(root) 1}"                                [folders]
format_coverage_test.go:187: line is a Go struct dump, not text: "{/var/folders/... 002 0x2deeebb32000}"     [config show]
format_coverage_test.go:187: line is a Go struct dump, not text: "{amazon.nova-2-... <nil> <nil> ... true}"   [models list]
```
PASS at HEAD.

---

## Commit 2: `cli: the move ambiguity guard asks the resolver, so a title-named bare link is caught` (c7b1489)

Reverted: `cli/internal/cli/move.go`

### `TestContract_Move_AmbiguousResolverOnlyLink` (internal/cli), both subtests
FAIL at dd70c88:
```
move_contract_test.go:560: dry-run should report [[Dup]] as ambiguous: {Moved:{From:one/dup.md To:one/renamed.md} Rewritten:[] SkippedAmbiguous:[] Failed:[] DryRun:true Refused:false MoveFailed:false}   [shared title]
move_contract_test.go:560: dry-run should report [[Duplicate]] as ambiguous: {... SkippedAmbiguous:[] ... Refused:false ...}                                                                              [shared alias]
```
PASS at HEAD.

### `TestContract_Move_UniqueTitleLinkStillMoves` (internal/cli)
Regression guard, not a defect reproduction: it PASSES at dd70c88 and at HEAD.
It exists so the widened guard cannot start refusing a unique name. Recorded
here explicitly so its passing baseline is not mistaken for an unproven test.
The same role is played by the pre-existing
`TestContract_Move_RewritesReferencingNotes` (asserts `skipped_ambiguous`
empty) and `TestContract_Move_AmbiguousBareLink` (the byte-exact case), both
still green.

### Live reproduction at dd70c88 (scratch vault, isolated HOME)
```
one/dup.md (title Dup), two/dup.md (title Dup), ref.md holding [[Dup]]
$ 2nb move one/dup.md one/renamed.md --json
{"moved":{...},"rewritten":[],"skipped_ambiguous":[],"failed":[],"dry_run":false,"refused":false,"move_failed":false}
exit=0   # the file moved; [[Dup]] now resolves to two/dup.md
```
