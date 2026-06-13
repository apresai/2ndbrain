package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tasks contract tests.
//
// `2nb tasks` (list) and `2nb task <path> <line>` (toggle) drive the same argv
// dispatch a user or the GUI would send through runCLIArgs. Bodies are seeded
// with `replace --text`, which round-trips verbatim after the frontmatter (the
// probe in development confirmed line N of the seed maps to line N of the body),
// so task line numbers in these assertions are deterministic.
//
// Provider is pinned to "no-provider" via noProviderContractVault so writeBody's
// inline re-embed is a skipped no-op (no AWS creds in CI). No mocks.

// seedTaskNote creates a note whose body is the given lines joined with "\n".
func seedTaskNote(t *testing.T, root, title string, lines ...string) string {
	t.Helper()
	return createContractNote(t, root, title, strings.Join(lines, "\n"))
}

func tasksJSON(t *testing.T, root string, argv ...string) []TaskRow {
	t.Helper()
	full := append([]string{"tasks"}, argv...)
	full = append(full, "--json", "--porcelain")
	out, err := runCLIArgs(t, root, full...)
	if err != nil {
		t.Fatalf("tasks %v: %v\n%s", argv, err, out)
	}
	var rows []TaskRow
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("tasks %v JSON: %v\n%s", argv, err, out)
	}
	return rows
}

func TestContract_TasksListsAll(t *testing.T) {
	root, _ := noProviderContractVault(t)
	seedTaskNote(t, root, "Todo One", "- [ ] alpha", "- [x] beta")
	seedTaskNote(t, root, "Todo Two", "intro", "- [ ] gamma")

	rows := tasksJSON(t, root)
	if len(rows) != 3 {
		t.Fatalf("expected 3 tasks across the vault, got %d: %#v", len(rows), rows)
	}

	texts := map[string]bool{}
	for _, r := range rows {
		texts[r.Text] = true
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !texts[want] {
			t.Errorf("task %q missing from listing: %#v", want, rows)
		}
	}
}

func TestContract_TasksDoneFilter(t *testing.T) {
	root, _ := noProviderContractVault(t)
	seedTaskNote(t, root, "Filter Done", "- [ ] open one", "- [x] done one", "- [x] done two")

	rows := tasksJSON(t, root, "--done")
	if len(rows) != 2 {
		t.Fatalf("--done expected 2 completed tasks, got %d: %#v", len(rows), rows)
	}
	for _, r := range rows {
		if !r.Done {
			t.Errorf("--done returned an open task: %#v", r)
		}
	}
}

func TestContract_TasksTodoFilter(t *testing.T) {
	root, _ := noProviderContractVault(t)
	seedTaskNote(t, root, "Filter Todo", "- [ ] open one", "- [ ] open two", "- [x] done one")

	rows := tasksJSON(t, root, "--todo")
	if len(rows) != 2 {
		t.Fatalf("--todo expected 2 open tasks, got %d: %#v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Done {
			t.Errorf("--todo returned a completed task: %#v", r)
		}
	}
}

func TestContract_TasksDoneAndTodoMutuallyExclusive(t *testing.T) {
	root, _ := noProviderContractVault(t)
	seedTaskNote(t, root, "Excl", "- [ ] x")
	if _, err := runCLIArgs(t, root, "tasks", "--done", "--todo"); err == nil {
		t.Fatalf("expected error when --done and --todo are combined")
	}
}

func TestContract_TasksPathScope(t *testing.T) {
	root, _ := noProviderContractVault(t)
	// One note under projects/, one at the root.
	scoped, err := runCLIArgs(t, root, "create", "Scoped Note", "--path", "projects", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("create scoped: %v\n%s", err, scoped)
	}
	var scopedDoc struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(scoped, &scopedDoc); err != nil {
		t.Fatalf("create scoped JSON: %v\n%s", err, scoped)
	}
	if _, err := runCLIArgs(t, root, "replace", scopedDoc.Path, "--text", "- [ ] scoped task"); err != nil {
		t.Fatalf("seed scoped body: %v", err)
	}
	seedTaskNote(t, root, "Root Note", "- [ ] root task")

	// Whole vault sees both.
	if all := tasksJSON(t, root); len(all) != 2 {
		t.Fatalf("expected 2 tasks vault-wide, got %d: %#v", len(all), all)
	}

	// --path projects/ sees only the scoped one.
	rows := tasksJSON(t, root, "--path", "projects/")
	if len(rows) != 1 {
		t.Fatalf("--path projects/ expected 1 task, got %d: %#v", len(rows), rows)
	}
	if rows[0].Text != "scoped task" {
		t.Errorf("--path scope returned wrong task: %#v", rows[0])
	}

	// --path to the single file works too.
	fileRows := tasksJSON(t, root, "--path", scopedDoc.Path)
	if len(fileRows) != 1 || fileRows[0].Text != "scoped task" {
		t.Errorf("--path <file> scope wrong: %#v", fileRows)
	}
}

func TestContract_TaskFlipsToDone(t *testing.T) {
	root, _ := noProviderContractVault(t)
	// Body: line 1 intro, line 2 open task, line 3 done task.
	path := seedTaskNote(t, root, "Toggle Note", "intro", "- [ ] flip me", "- [x] already")

	out, err := runCLIArgs(t, root, "task", path, "2", "--done", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("task --done: %v\n%s", err, out)
	}
	var res struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Done bool   `json:"done"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("task result JSON: %v\n%s", err, out)
	}
	if !res.Done || res.Line != 2 {
		t.Fatalf("task --done result wrong: %#v", res)
	}

	// Read the body back and assert line 2 now carries [x].
	rows := tasksJSON(t, root)
	var found bool
	for _, r := range rows {
		if r.Path == path && r.Line == 2 {
			found = true
			if !r.Done {
				t.Fatalf("line 2 should be done after --done flip: %#v", r)
			}
			if r.Text != "flip me" {
				t.Fatalf("task text changed during flip: %#v", r)
			}
		}
	}
	if !found {
		t.Fatalf("flipped task not found on re-read: %#v", rows)
	}
}

func TestContract_TaskToggleRoundtrip(t *testing.T) {
	root, _ := noProviderContractVault(t)
	path := seedTaskNote(t, root, "Roundtrip Note", "- [ ] thing")

	// Default action is toggle: open -> done.
	if _, err := runCLIArgs(t, root, "task", path, "1"); err != nil {
		t.Fatalf("toggle 1: %v", err)
	}
	if rows := tasksJSON(t, root); len(rows) != 1 || !rows[0].Done {
		t.Fatalf("after first toggle expected done: %#v", rows)
	}
	// Toggle again: done -> open.
	if _, err := runCLIArgs(t, root, "task", path, "1"); err != nil {
		t.Fatalf("toggle 2: %v", err)
	}
	if rows := tasksJSON(t, root); len(rows) != 1 || rows[0].Done {
		t.Fatalf("after second toggle expected open: %#v", rows)
	}
}

func TestContract_TaskNonTaskLineErrors(t *testing.T) {
	root, _ := noProviderContractVault(t)
	// Line 1 is prose, not a checkbox.
	path := seedTaskNote(t, root, "Not A Task", "just prose here", "- [ ] real task")

	if _, err := runCLIArgs(t, root, "task", path, "1"); err == nil {
		t.Fatalf("expected error toggling a non-task line")
	}
	// A line past the end of the body also errors.
	if _, err := runCLIArgs(t, root, "task", path, "999"); err == nil {
		t.Fatalf("expected error for an out-of-range line")
	}
	// A non-numeric line errors.
	if _, err := runCLIArgs(t, root, "task", path, "abc"); err == nil {
		t.Fatalf("expected error for a non-numeric line")
	}
}
