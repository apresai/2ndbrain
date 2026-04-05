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

# Prepare the new version section content
if [ -n "$CHANGES_FILE" ] && [ -f "$CHANGES_FILE" ]; then
    echo "Using changes from $CHANGES_FILE"
    CHANGES_CONTENT=$(cat "$CHANGES_FILE")
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
import sys

# Read files
with open('$CHANGELOG', 'r') as f:
    original = f.read()

with open('$TEMP_ENTRY', 'r') as f:
    new_entry = f.read()

# Find the Unreleased section
unreleased_marker = '## [Unreleased]'
if unreleased_marker not in original:
    print("Error: Could not find [Unreleased] section in CHANGELOG.md", file=sys.stderr)
    sys.exit(1)

# Split at the Unreleased marker
parts = original.split(unreleased_marker, 1)
before_unreleased = parts[0]
after_unreleased = parts[1]

# Find the next version section (## [)
next_section_idx = after_unreleased.find('\n## [')
if next_section_idx == -1:
    # No more versions, append at end
    updated = before_unreleased + unreleased_marker + '\n\n(empty - ready for next release)\n' + new_entry + after_unreleased
else:
    # Insert between Unreleased and next version
    before_next = after_unreleased[:next_section_idx]
    after_next = after_unreleased[next_section_idx:]
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
