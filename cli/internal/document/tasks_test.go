package document

import (
	"reflect"
	"testing"
)

func TestExtractTasks_OpenAndDone(t *testing.T) {
	body := "- [ ] open one\n- [x] done lower\n- [X] done upper"
	got := ExtractTasks(body)
	want := []Task{
		{Text: "open one", Done: false, Line: 1, Raw: "- [ ] open one"},
		{Text: "done lower", Done: true, Line: 2, Raw: "- [x] done lower"},
		{Text: "done upper", Done: true, Line: 3, Raw: "- [X] done upper"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractTasks mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestExtractTasks_BulletVariants(t *testing.T) {
	body := "- [ ] dash\n* [ ] star\n+ [x] plus done"
	got := ExtractTasks(body)
	if len(got) != 3 {
		t.Fatalf("expected 3 tasks across -,*,+ bullets, got %d: %#v", len(got), got)
	}
	if got[0].Text != "dash" || got[1].Text != "star" || got[2].Text != "plus done" {
		t.Errorf("bullet-variant text mismatch: %#v", got)
	}
	if got[2].Done != true {
		t.Errorf("expected '+ [x]' to be done, got %#v", got[2])
	}
}

func TestExtractTasks_IndentedAndNested(t *testing.T) {
	body := "- [ ] parent\n  - [x] child done\n    - [ ] grandchild\n\t- [ ] tab indented"
	got := ExtractTasks(body)
	if len(got) != 4 {
		t.Fatalf("expected 4 indented/nested tasks, got %d: %#v", len(got), got)
	}
	if !got[1].Done {
		t.Errorf("nested child should be done: %#v", got[1])
	}
	if got[0].Line != 1 || got[1].Line != 2 || got[2].Line != 3 || got[3].Line != 4 {
		t.Errorf("line numbers wrong for nested tasks: %#v", got)
	}
	if got[2].Text != "grandchild" {
		t.Errorf("indented text should be trimmed: %q", got[2].Text)
	}
}

func TestExtractTasks_IgnoresCodeFence(t *testing.T) {
	body := "- [ ] real task\n```\n- [ ] fenced not a task\n- [x] fenced done\n```\n- [x] another real"
	got := ExtractTasks(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks (fenced ones ignored), got %d: %#v", len(got), got)
	}
	if got[0].Text != "real task" || got[1].Text != "another real" {
		t.Errorf("wrong tasks survived the fence filter: %#v", got)
	}
	// Line numbers must still reflect absolute body line position.
	if got[0].Line != 1 || got[1].Line != 6 {
		t.Errorf("line numbers wrong around code fence: %#v", got)
	}
}

func TestExtractTasks_IgnoresTildeFenceAndIndentedFence(t *testing.T) {
	body := "- [ ] before\n  ~~~\n  - [ ] inside tilde fence\n  ~~~\n- [ ] after"
	got := ExtractTasks(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks (tilde/indented fence ignored), got %d: %#v", len(got), got)
	}
	if got[0].Text != "before" || got[1].Text != "after" {
		t.Errorf("wrong tasks survived tilde/indented fence: %#v", got)
	}
}

func TestExtractTasks_EmptyLabel(t *testing.T) {
	body := "- [ ] \n- [x]"
	got := ExtractTasks(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 tasks with empty labels, got %d: %#v", len(got), got)
	}
	if got[0].Text != "" || got[1].Text != "" {
		t.Errorf("empty-label tasks should have empty text: %#v", got)
	}
	if got[0].Done || !got[1].Done {
		t.Errorf("empty-label done state wrong: %#v", got)
	}
}

func TestExtractTasks_NotTasks(t *testing.T) {
	// None of these are GFM open/done checkboxes.
	body := "Just a paragraph.\n- a bullet, no box\n- [>] forwarded (out of scope)\n- [-] cancelled (out of scope)\n-[ ] no space before bracket\n- [ ]x bracket then text no space\n## A heading"
	got := ExtractTasks(body)
	if len(got) != 0 {
		t.Fatalf("expected 0 tasks for non-GFM lines, got %d: %#v", len(got), got)
	}
}

func TestExtractTasks_Mixed(t *testing.T) {
	body := "# Title\n\nSome intro text.\n\n- [ ] todo A\n- regular bullet\n- [x] done B\n\n```go\n// - [ ] not a task\n```\n\nMore prose with [x] inline bracket that is not a list item.\n\n* [ ] todo C"
	got := ExtractTasks(body)
	if len(got) != 3 {
		t.Fatalf("expected 3 real tasks in mixed doc, got %d: %#v", len(got), got)
	}
	want := []Task{
		{Text: "todo A", Done: false, Line: 5, Raw: "- [ ] todo A"},
		{Text: "done B", Done: true, Line: 7, Raw: "- [x] done B"},
		{Text: "todo C", Done: false, Line: 15, Raw: "* [ ] todo C"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed-doc mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestToggleTaskLine(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		want   string
		expect string
		ok     bool
	}{
		{"open->done toggle", "- [ ] thing", "toggle", "- [x] thing", true},
		{"done->open toggle", "- [x] thing", "toggle", "- [ ] thing", true},
		{"force done from open", "- [ ] thing", "done", "- [x] thing", true},
		{"force done already done", "- [X] thing", "done", "- [x] thing", true},
		{"force todo from done", "- [x] thing", "todo", "- [ ] thing", true},
		{"preserve indent and bullet", "  * [ ] nested", "done", "  * [x] nested", true},
		{"preserve trailing text", "- [ ] buy milk #errand", "toggle", "- [x] buy milk #errand", true},
		{"non-task line", "- just a bullet", "toggle", "- just a bullet", false},
		{"prose line", "hello world", "done", "hello world", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ToggleTaskLine(c.line, c.want)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (line=%q)", ok, c.ok, c.line)
			}
			if got != c.expect {
				t.Errorf("ToggleTaskLine(%q, %q) = %q, want %q", c.line, c.want, got, c.expect)
			}
		})
	}
}
