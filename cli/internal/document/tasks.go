package document

import (
	"regexp"
	"strings"
)

// Task is a single GFM checkbox item found in a document body.
type Task struct {
	// Text is the task label: everything after the checkbox marker, with
	// surrounding whitespace trimmed.
	Text string `json:"text"`
	// Done is true when the box is checked ([x] or [X]).
	Done bool `json:"done"`
	// Line is the 1-based line number within the body (frontmatter excluded)
	// where the checkbox appears.
	Line int `json:"line"`
	// Raw is the original line verbatim (no trimming), so a caller can splice
	// it back or flip just the marker character.
	Raw string `json:"raw"`
}

// taskLineRe matches a GFM checkbox line: optional indentation, a list bullet
// (-, *, or +), one space, then "[ ]", "[x]", or "[X]", then a space (or the
// end of the line for an empty label). Capture group 1 is the marker char (a
// space for open, x/X for done). The trailing portion is the task text.
//
// v1 scope is intentionally GFM open/done only. Custom statuses such as [>],
// [-], [/], or [!] (popularized by the Obsidian Tasks plugin) are NOT treated
// as tasks here and are left untouched.
var taskLineRe = regexp.MustCompile(`^(\s*)[-*+] \[([ xX])\](?:\s+(.*))?$`)

// ExtractTasks finds every GFM checkbox task in a markdown body and returns
// them in document order. Line numbers are 1-based within the body (the
// frontmatter-excluded text in Document.Body).
//
// Checkboxes inside fenced code blocks (``` or ~~~ at line start) are ignored
// so that a code sample documenting task syntax does not produce phantom tasks.
// Fence state is tracked line by line; the same lightweight scanner idea as
// maskCodeRegions in wikilink.go, but operating per line because ExtractTasks
// works line-oriented anyway.
//
// Indented and nested checkboxes are included: the indentation is consumed by
// the leading whitespace group but does not change whether the line is a task.
func ExtractTasks(body string) []Task {
	lines := strings.Split(body, "\n")
	tasks := make([]Task, 0)

	inFence := false
	for i, line := range lines {
		// Toggle fenced-code state on a fence marker at the start of the line.
		// The marker may be indented; trim leading space before checking so an
		// indented fence (common inside list items) still toggles.
		if isFenceMarker(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		m := taskLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		marker := m[2]
		tasks = append(tasks, Task{
			Text: strings.TrimSpace(m[3]),
			Done: marker == "x" || marker == "X",
			Line: i + 1,
			Raw:  line,
		})
	}

	return tasks
}

// isFenceMarker reports whether a line opens or closes a fenced code block,
// i.e. its first non-space run is ``` or ~~~ (an optional info string may
// follow). Indented fences (inside list items) are recognized.
func isFenceMarker(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// ToggleTaskLine flips the GFM checkbox marker on a single body line and
// returns the rewritten line. want controls the result: "done" forces [x],
// "todo" forces [ ], and "toggle" inverts the current state. ok is false when
// the line is not a GFM checkbox (callers should error rather than write).
//
// Only the marker character inside the brackets is changed; indentation, the
// bullet character, and the task text are preserved byte for byte. A done box
// is normalized to lowercase [x] when set.
func ToggleTaskLine(line, want string) (string, bool) {
	m := taskLineRe.FindStringSubmatchIndex(line)
	if m == nil {
		return line, false
	}
	// Group 2 (the marker char) spans bytes [m[4], m[5]); for a GFM checkbox
	// that is always exactly one character between the brackets.
	markerStart, markerEnd := m[4], m[5]
	current := line[markerStart:markerEnd]
	done := current == "x" || current == "X"

	var next bool
	switch want {
	case "done":
		next = true
	case "todo":
		next = false
	default: // toggle
		next = !done
	}

	marker := " "
	if next {
		marker = "x"
	}
	return line[:markerStart] + marker + line[markerEnd:], true
}
