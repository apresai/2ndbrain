#!/usr/bin/env bash
# Unit tests for scripts/update-changelog.sh, the release step that folds the
# hand-curated [Unreleased] section into the new version entry.
#
# Network-free and model-free: each case builds a throwaway git repo with a
# previous version tag (so the commit-summarizer branch is genuinely REACHABLE)
# and puts a `claude` stub first on PATH that records whether it was invoked.
# That is what makes "the summarizer did not run" an assertion rather than an
# accident of the fixture.
#
# The bug these guard: 0.20.0 shipped an entry carrying BOTH the curated
# section and a summarizer-written set describing the same commits, so the
# release notes said everything twice under duplicate headings.
set -euo pipefail
cd "$(dirname "$0")/.."

SCRIPT="$(pwd)/scripts/update-changelog.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }
pass=0

# 1. The script parses.
bash -n "$SCRIPT" || fail "bash -n"
pass=$((pass+1))

# make_repo <dir> <unreleased-body>
# A throwaway repo with one commit, a v0.1.0 tag, and a CHANGELOG whose second
# "## [" heading is [0.1.0] — the shape update-changelog.sh reads PREV_VERSION
# from, so the summarizer branch is reachable unless the script chooses not to.
make_repo() {
  local dir="$1" unreleased="$2"
  mkdir -p "$dir/bin"
  cat > "$dir/CHANGELOG.md" <<EOF
# Changelog

## [Unreleased]

${unreleased}

## [0.1.0] - 2026-01-01

### Added
- the first release
EOF
  # A stub that stands in for Claude Code and leaves a trace when called.
  cat > "$dir/bin/claude" <<'EOF'
#!/usr/bin/env bash
echo "SUMMARIZER_RAN" >> "$CLAUDE_STUB_LOG"
echo "### Added"
echo "- generated bullet the summarizer invented"
EOF
  chmod +x "$dir/bin/claude"
  # -c flags keep the fixture hermetic against ambient global git config
  # (signing / forced annotated tags would otherwise fail here).
  local G=(git -C "$dir" -c commit.gpgsign=false -c tag.gpgSign=false -c tag.forceSignAnnotated=false)
  "${G[@]}" init -q
  "${G[@]}" config user.email t@example.com
  "${G[@]}" config user.name Test
  "${G[@]}" add -A
  "${G[@]}" commit -qm "initial"
  "${G[@]}" tag -a v0.1.0 -m v0.1.0
  # A later commit so `git log v0.1.0..HEAD` is non-empty (the summarizer
  # branch bails early on an empty commit range).
  echo x > "$dir/later.txt"
  "${G[@]}" add -A
  "${G[@]}" commit -qm "feat: something after the tag"
}

run_script() {
  local dir="$1"; shift
  ( cd "$dir" && CLAUDE_STUB_LOG="$dir/stub.log" PATH="$dir/bin:$PATH" bash "$SCRIPT" "$@" >/dev/null )
}

CURATED='### Added
- a curated addition (#122)

### Fixed
- a hand-written entry that must survive verbatim (#123)'

# 2. Curated content present: it becomes the entry and the summarizer is SKIPPED.
d=$(mktemp -d); make_repo "$d" "$CURATED"
run_script "$d" 0.2.0
out="$d/CHANGELOG.md"
[ -f "$d/stub.log" ] && fail "summarizer ran despite curated [Unreleased] content"
pass=$((pass+1))
grep -q "a hand-written entry that must survive verbatim (#123)" "$out" \
  || fail "curated bullet was dropped"
pass=$((pass+1))
[ "$(grep -c 'a hand-written entry that must survive' "$out")" -eq 1 ] \
  || fail "curated bullet appears more than once"
pass=$((pass+1))
# One release entry, one "### Fixed" heading inside it — the 0.20.0 defect was
# two heading sets in a single entry.
entry=$(sed -n '/## \[0\.2\.0\]/,/## \[0\.1\.0\]/p' "$out")
[ "$(printf '%s\n' "$entry" | grep -c '^### Fixed')" -eq 1 ] \
  || fail "expected exactly one '### Fixed' heading in the 0.2.0 entry"
pass=$((pass+1))
printf '%s\n' "$entry" | grep -q "generated bullet the summarizer invented" \
  && fail "summarizer content leaked into a curated entry"
pass=$((pass+1))
# Curated content keeps its own shape: both groups present, and the blank line
# that separates them survives (squashing it jams '### Fixed' onto the bullet
# above, which is how every future multi-section entry would have rendered).
printf '%s\n' "$entry" | grep -q '^### Added' || fail "curated '### Added' group lost"
pass=$((pass+1))
printf '%s\n' "$entry" | grep -B1 '^### Fixed' | head -1 | grep -q '^[[:space:]]*$' \
  || fail "blank line before a curated '### Fixed' was squashed"
pass=$((pass+1))
# The Unreleased section is reset for the next cycle.
grep -q '(empty - ready for next release)' "$out" || fail "Unreleased was not reset"
pass=$((pass+1))
# No run of consecutive blank lines INSIDE the new entry, where the skipped
# generated body would otherwise have left a gap. Scoped to the entry on
# purpose: real changelogs carry double blank lines between historical entries
# (the long-standing house style), so a whole-file assertion would fail for
# reasons this script never caused.
# (Checked with awk, not a multi-line grep pattern: grep splits a pattern
# containing newlines into several patterns, one of which is empty, so such a
# check matches every file and would pass or fail for the wrong reason.)
runs=$(printf '%s\n' "$entry" | awk 'BEGIN{n=0} /^[[:space:]]*$/{n++; if(n>=2){print NR; exit}} !/^[[:space:]]*$/{n=0}')
[ -z "$runs" ] || fail "blank-line run inside the new entry (entry line $runs)"
pass=$((pass+1))
rm -rf "$d"

# 3. No curated content: the summarizer still runs (it is the fallback, not dead code).
d=$(mktemp -d); make_repo "$d" "(empty - ready for next release)"
run_script "$d" 0.2.0
grep -q SUMMARIZER_RAN "$d/stub.log" 2>/dev/null \
  || fail "summarizer did not run when there was nothing curated to use"
pass=$((pass+1))
grep -q "generated bullet the summarizer invented" "$d/CHANGELOG.md" \
  || fail "summarizer output missing from the entry"
pass=$((pass+1))
rm -rf "$d"

# 4. Explicit changes file wins, and curated content is still folded in (the
#    behavior PR #237 added, which this fix must not regress).
d=$(mktemp -d); make_repo "$d" "$CURATED"
printf '### Added\n- from the explicit changes file\n' > "$d/changes.txt"
run_script "$d" 0.2.0 "$d/changes.txt"
[ -f "$d/stub.log" ] && fail "summarizer ran despite an explicit changes file"
pass=$((pass+1))
grep -q "from the explicit changes file" "$d/CHANGELOG.md" || fail "changes-file content missing"
pass=$((pass+1))
grep -q "a hand-written entry that must survive verbatim (#123)" "$d/CHANGELOG.md" \
  || fail "curated content dropped when a changes file was supplied"
pass=$((pass+1))
# A changes file is ADDITIVE to curated entries (both are human-authored), so
# this path legitimately carries the changes file's headings plus the curated
# ones; what it must never carry is summarizer text.
grep -q "generated bullet the summarizer invented" "$d/CHANGELOG.md" \
  && fail "summarizer content leaked into a changes-file entry"
pass=$((pass+1))
rm -rf "$d"

# 5. [Unreleased] as the LAST section: the no-following-version branch, which
#    this change rewrote. A first release has exactly this shape.
d=$(mktemp -d); mkdir -p "$d/bin"
cat > "$d/CHANGELOG.md" <<EOF
# Changelog

## [Unreleased]

${CURATED}
EOF
cp /dev/null "$d/bin/claude"; chmod +x "$d/bin/claude"
run_script "$d" 0.2.0
grep -q '## \[0.2.0\]' "$d/CHANGELOG.md" || fail "entry missing when [Unreleased] is the last section"
pass=$((pass+1))
grep -q "a hand-written entry that must survive verbatim (#123)" "$d/CHANGELOG.md" \
  || fail "curated content dropped when [Unreleased] is the last section"
pass=$((pass+1))
grep -q '(empty - ready for next release)' "$d/CHANGELOG.md" \
  || fail "[Unreleased] not reset when it was the last section"
pass=$((pass+1))
rm -rf "$d"

# 6. A changelog with no [Unreleased] section fails loudly and leaves no mess.
d=$(mktemp -d); mkdir -p "$d/bin"
printf '# Changelog\n\n## [0.1.0] - 2026-01-01\n\n### Added\n- first\n' > "$d/CHANGELOG.md"
before=$(cat "$d/CHANGELOG.md")
if run_script "$d" 0.2.0 2>/dev/null; then fail "expected a non-zero exit with no [Unreleased] section"; fi
pass=$((pass+1))
[ "$(cat "$d/CHANGELOG.md")" = "$before" ] || fail "changelog was modified despite the error"
pass=$((pass+1))
[ -f "$d/CHANGELOG.md.bak" ] && fail "left a CHANGELOG.md.bak behind on the error path"
pass=$((pass+1))
rm -rf "$d"

# 7. A changes file that does not exist is an error, not a silent fallback:
#    falling through would ship an entry the caller never reviewed.
d=$(mktemp -d); make_repo "$d" "$CURATED"
before=$(cat "$d/CHANGELOG.md")
if run_script "$d" 0.2.0 "$d/nope.txt" 2>/dev/null; then
  fail "expected a non-zero exit for a missing changes file"
fi
pass=$((pass+1))
[ "$(cat "$d/CHANGELOG.md")" = "$before" ] || fail "changelog modified despite the missing changes file"
pass=$((pass+1))
rm -rf "$d"

echo "changelog-script-test: $pass checks passed"
