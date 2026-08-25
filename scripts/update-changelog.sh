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

# Hand-curated [Unreleased] entries are AUTHORITATIVE: they were written by a
# human for exactly this release and they BECOME the release entry. Extract them
# once, here, so the content selection below and the insertion at the end share
# one definition of "curated" (a second copy of the placeholder vocabulary would
# drift). Empty file == nothing curated.
CURATED_FILE=$(mktemp)
trap 'rm -f "$CURATED_FILE"' EXIT
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
    # Drop blank lines and the "nothing here yet" placeholders; anything that
    # survives is real, hand-written content.
    lines = [l for l in section.strip().splitlines()
             if l.strip() not in ('', '(empty - ready for next release)', '(ready for next release)')]
    body = '\n'.join(lines).strip()
open(out, 'w').write(body)
PYEOF

# Prepare the new version section content. Precedence, highest first:
#   1. an explicit changes file (the caller stated exactly what to ship)
#   2. curated [Unreleased] entries (folded in verbatim at the end)
#   3. the commit summarizer, a LAST RESORT for when nothing human-authored exists
# The summarizer must never run alongside curated entries: both describe the same
# commits, so the entry ends up saying everything twice under duplicate headings
# (that is what shipped in 0.20.0).
if [ -n "$CHANGES_FILE" ] && [ -f "$CHANGES_FILE" ]; then
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

# Create the new version entry in a temp file
TEMP_ENTRY=$(mktemp)
cat > "$TEMP_ENTRY" <<EOF

## [$VERSION] - $DATE

$CHANGES_CONTENT

EOF

# Use Python to insert the new version (more reliable than awk/sed for multiline)
python3 <<PYEOF
import re
import sys

# Read files
with open('$CHANGELOG', 'r') as f:
    original = f.read()

with open('$TEMP_ENTRY', 'r') as f:
    new_entry = f.read()

# The curated [Unreleased] entries extracted before the content selection above.
# They are folded into this release's entry verbatim; when they exist they ARE
# the entry (the summarizer was skipped), so nothing here can duplicate them.
with open('$CURATED_FILE', 'r') as f:
    curated = f.read().strip()

# Find the Unreleased section
unreleased_marker = '## [Unreleased]'
if unreleased_marker not in original:
    print("Error: Could not find [Unreleased] section in CHANGELOG.md", file=sys.stderr)
    sys.exit(1)

if curated:
    marker = '## [$VERSION] - $DATE'
    head, sep, tail = new_entry.partition(marker)
    if sep:
        new_entry = head + sep + '\n\n' + curated + tail
    else:
        new_entry = new_entry.rstrip('\n') + '\n\n' + curated + '\n'

# An empty CHANGES_CONTENT (the curated-only path) leaves a run of blank lines
# where the generated body would have gone; collapse them so the entry reads the
# same however it was assembled.
new_entry = re.sub(r'\n{3,}', '\n\n', new_entry)
# End with exactly one newline: the following section (when there is one) starts
# with its own '\n', so a second here would open a blank-line run at the seam.
new_entry = new_entry.rstrip('\n') + '\n'

# Split at the Unreleased marker and insert the entry after the reset placeholder
parts = original.split(unreleased_marker, 1)
before_unreleased = parts[0]
after_unreleased = parts[1]

next_section_idx = after_unreleased.find('\n## [')
after_next = '' if next_section_idx == -1 else after_unreleased[next_section_idx:]
updated = before_unreleased + unreleased_marker + '\n\n(empty - ready for next release)\n' + new_entry + after_next

# Write result
with open('$CHANGELOG', 'w') as f:
    f.write(updated)

print(f"✓ CHANGELOG.md updated with version $VERSION ($DATE)")
PYEOF

# Clean up
rm "$CHANGELOG.bak" "$TEMP_ENTRY"

echo ""
echo "New entry added:"
echo "## [$VERSION] - $DATE"
echo ""
echo "$CHANGES_CONTENT"
