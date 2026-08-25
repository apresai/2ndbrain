#!/bin/bash
# Update CHANGELOG.md with new version entry
# Usage: ./scripts/update-changelog.sh <version> [changes-file]

set -e

VERSION="$1"
CHANGES_FILE="${2:-}"
CHANGELOG="CHANGELOG.md"
DATE=$(date +%Y-%m-%d)

if [ -z "$VERSION" ]; then
    echo "Usage: $0 <version> [changes-file]"
    echo "Example: $0 1.1.18"
    echo "         $0 1.1.18 /path/to/changes.txt"
    exit 1
fi

if [ ! -f "$CHANGELOG" ]; then
    echo "Error: $CHANGELOG not found"
    exit 1
fi

# Check if this version already exists in changelog
if grep -q "## \[$VERSION\]" "$CHANGELOG"; then
    echo "Version $VERSION already exists in CHANGELOG.md"
    echo "Skipping changelog update"
    exit 0
fi

# Hand-curated [Unreleased] entries are AUTHORITATIVE: a human wrote them for
# exactly this release, so they BECOME the release entry. Extract them once here
# so the content selection below and the insertion at the end share one
# definition of "curated" (a second copy of the placeholder vocabulary would
# drift). Empty file == nothing curated.
CURATED_FILE=$(mktemp)
TEMP_ENTRY=$(mktemp)
trap 'rm -f "$CURATED_FILE" "$TEMP_ENTRY" "$CHANGELOG.bak"' EXIT
python3 - "$CHANGELOG" "$CURATED_FILE" <<'PYEOF'
import sys

changelog, out = sys.argv[1], sys.argv[2]
text = open(changelog).read()
marker = '## [Unreleased]'
body = ''
if marker in text:
    after = text.split(marker, 1)[1]
    idx = after.find('\n## [')
    section = after if idx == -1 else after[:idx]
    # Drop only the "nothing here yet" placeholders, then trim the edges.
    # INTERNAL blank lines are content: they separate a curated entry's own
    # "### Added" / "### Fixed" groups, and squashing them jams those headings
    # against the preceding bullet.
    lines = [l for l in section.splitlines()
             if l.strip() not in ('(empty - ready for next release)', '(ready for next release)')]
    body = '\n'.join(lines).strip()
open(out, 'w').write(body)
PYEOF

# Fail before spending anything if the file is not the shape we can rewrite:
# the summarizer below can cost a real model call, and the insertion step needs
# this marker anyway.
if ! grep -q '^## \[Unreleased\]' "$CHANGELOG"; then
    echo "Error: Could not find [Unreleased] section in $CHANGELOG" >&2
    exit 1
fi

# Choose the new entry's content. Human-authored content always wins over the
# machine: the summarizer runs ONLY when neither an explicit changes file nor
# curated [Unreleased] entries exist. It must never run alongside curated
# entries, because both describe the same commits and the entry then says
# everything twice under duplicate headings (that is what shipped in 0.20.0).
# A changes file and curated entries are ADDITIVE, both being human-authored,
# so both land. The file supplies the body here; the curated text is folded in
# at the insertion step below.
if [ -n "$CHANGES_FILE" ] && [ ! -f "$CHANGES_FILE" ]; then
    # Asked for a changes file that is not there. Silently falling back would
    # ship an entry the caller never reviewed, so say so and stop.
    echo "Error: changes file not found: $CHANGES_FILE" >&2
    exit 1
fi

if [ -n "$CHANGES_FILE" ]; then
    echo "Using changes from $CHANGES_FILE"
    CHANGES_CONTENT=$(cat "$CHANGES_FILE")
elif [ -s "$CURATED_FILE" ]; then
    echo "Using the hand-curated [Unreleased] entries (skipping the commit summarizer)"
    CHANGES_CONTENT=""
else
    # Try to auto-generate changelog using Claude Code
    echo "Generating changelog from git commits..."

    # Find the previous version in the changelog
    PREV_VERSION=$(grep -m 2 "^## \[" "$CHANGELOG" | tail -1 | sed 's/## \[\(.*\)\].*/\1/')

    if [ -n "$PREV_VERSION" ] && git rev-parse "v$PREV_VERSION" >/dev/null 2>&1; then
        echo "Found previous version: $PREV_VERSION"

        # Get commits since last version
        COMMITS=$(git log --oneline "v$PREV_VERSION..HEAD" 2>/dev/null)

        if [ -n "$COMMITS" ] && command -v claude >/dev/null 2>&1; then
            echo "Using Claude Code to summarize changes..."

            # Create a temp file with the git log
            TEMP_LOG=$(mktemp)
            echo "Git commits since v$PREV_VERSION:" > "$TEMP_LOG"
            echo "" >> "$TEMP_LOG"
            git log --oneline "v$PREV_VERSION..HEAD" >> "$TEMP_LOG"
            echo "" >> "$TEMP_LOG"
            echo "Git diff summary:" >> "$TEMP_LOG"
            git diff --stat "v$PREV_VERSION..HEAD" >> "$TEMP_LOG"

            # Use Claude Code to generate changelog
            CLAUDE_OUTPUT=$(claude -p "Analyze these git commits and generate a concise CHANGELOG entry in Keep a Changelog format. Use these categories: Added, Changed, Fixed, Removed. Be specific but brief. Only include categories that have changes. Format as markdown with ### headers.

$(cat "$TEMP_LOG")

Return ONLY the changelog content, no explanations." 2>/dev/null)

            rm "$TEMP_LOG"

            if [ -n "$CLAUDE_OUTPUT" ]; then
                echo "✓ Generated changelog with Claude Code"
                CHANGES_CONTENT="$CLAUDE_OUTPUT"
            else
                echo "⚠ Claude Code didn't return content, using default"
                CHANGES_CONTENT="### Changed
- Build number incremented to $VERSION (automatic versioning from git commit count)"
            fi
        else
            # Fallback: use commit messages directly
            echo "Claude Code not available, using commit messages..."
            CHANGES_CONTENT="### Changed"
            while IFS= read -r commit; do
                CHANGES_CONTENT="$CHANGES_CONTENT
- $commit"
            done <<< "$COMMITS"
        fi
    else
        echo "No previous version tag found, using default changelog entry"
        CHANGES_CONTENT="### Changed
- Build number incremented to $VERSION (automatic versioning from git commit count)"
    fi
fi

# Create backup
cp "$CHANGELOG" "$CHANGELOG.bak"

# The body of the new entry (may be empty on the curated-only path).
printf '%s' "$CHANGES_CONTENT" > "$TEMP_ENTRY"

# Insert the new version with Python (more reliable than awk/sed for multiline).
# Values arrive as argv, never interpolated into the program text: a quote in
# VERSION would otherwise end a string literal and run as code.
python3 - "$CHANGELOG" "$TEMP_ENTRY" "$CURATED_FILE" "$VERSION" "$DATE" <<'PYEOF'
import sys

changelog, body_file, curated_file, version, date = sys.argv[1:6]

with open(changelog) as f:
    original = f.read()
with open(body_file) as f:
    body = f.read().strip()
# The curated [Unreleased] entries extracted before the content selection above.
# Folded in as written (internal blank lines intact). When they exist the
# summarizer was skipped, so they cannot be duplicated by generated text; an
# explicit changes file is additive to them, both being human-authored.
with open(curated_file) as f:
    curated = f.read().strip()

unreleased_marker = '## [Unreleased]'
if unreleased_marker not in original:
    print(f"Error: Could not find [Unreleased] section in {changelog}", file=sys.stderr)
    sys.exit(1)

# Assemble: heading, then the body (if any), then the curated entries (if any).
# Joining the present pieces beats emitting placeholders and collapsing blank
# runs afterwards, which would also squash blank lines inside a fenced code
# block in a changes file.
sections = [s for s in (body, curated) if s]
new_entry = f'\n## [{version}] - {date}\n\n' + '\n\n'.join(sections) + '\n'

# End with exactly one newline: the following section (when there is one) starts
# with its own '\n', so a second here would open a blank-line run at the seam.
new_entry = new_entry.rstrip('\n') + '\n'

# Split at the Unreleased marker and insert the entry after the reset placeholder
before_unreleased, after_unreleased = original.split(unreleased_marker, 1)
next_section_idx = after_unreleased.find('\n## [')
after_next = '' if next_section_idx == -1 else after_unreleased[next_section_idx:]
updated = (before_unreleased + unreleased_marker
           + '\n\n(empty - ready for next release)\n' + new_entry + after_next)

with open(changelog, 'w') as f:
    f.write(updated)

print(f"✓ {changelog} updated with version {version} ({date})")
PYEOF

# Clean up (the EXIT trap covers the same files if we die before here)
rm -f "$CHANGELOG.bak" "$TEMP_ENTRY" "$CURATED_FILE"

# Echo the entry AS WRITTEN, read back from the file. `make release` commits,
# tags, and pushes moments after this, so this is the operator's only chance to
# see what is shipping; printing $CHANGES_CONTENT instead would show an empty
# body on the curated-only path, where the content came from the curated file.
echo ""
echo "New entry added:"
awk -v ver="## [$VERSION] - $DATE" '
  index($0, ver) == 1 { show = 1 }
  show && /^## \[/ && index($0, ver) != 1 { exit }
  show { print }
' "$CHANGELOG"
