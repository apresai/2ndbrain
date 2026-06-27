# 2nb skill (in-repo, self-hosted)

`SKILL.md` in this directory is the `2nb` agent skill, placed here so any agent
(Warp, Claude Code, Cursor, ...) opened on the 2ndbrain repo discovers it by
walking up to the nearest `.agents/skills/<name>/SKILL.md`. The same content is
mirrored to `.warp/skills/2nb/SKILL.md` and `.claude/skills/2nb/SKILL.md` for
tools that look there first.

## It is a generated mirror — do not edit it directly

The source of truth is the Go-embedded file:

    cli/internal/skills/content/2ndbrain-skill.md

`go:embed` cannot reference files above its package directory, so the source has
to live there. `2nb skills install <agent>` and `2nb skills show` render that
embedded content (stamped with `x-2nb-version` + `x-2nb-content-sha` at install
time); the mirrors here are kept unstamped and byte-identical to the source.

To change the skill:

    # edit the source
    $EDITOR cli/internal/skills/content/2ndbrain-skill.md
    make sync-skills        # regenerate .agents/.warp/.claude mirrors
    make check-skills-sync  # the CI drift gate; must pass before commit

Release CI runs `make check-skills-sync` and fails if any mirror has drifted
from the embedded source, the same way it guards the Obsidian plugin manifest
against `VERSION`.
