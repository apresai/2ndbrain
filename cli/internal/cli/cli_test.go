package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"./here", "./here"},
		{"", ""},
		{"~user", "~user"}, // only ~ and ~/ are expanded, not ~user
	}

	for _, tt := range tests {
		got := expandPath(tt.input)
		if got != tt.want {
			t.Errorf("expandPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateTitle(t *testing.T) {
	good := []string{
		"My Note",
		"Use JWT for Auth",
		"ADR 001: Database Choice",
		"Go and Swift's SQLite",
		"Version 2.0",
		"Notes on C++ (advanced)",
		"Hello, World",
		"A",
	}
	for _, title := range good {
		if err := validateTitle(title); err != nil {
			t.Errorf("validateTitle(%q) = %v, want nil", title, err)
		}
	}

	bad := []struct {
		title string
		desc  string
	}{
		{"-Bad", "starts with dash"},
		{"-", "single dash"},
		{"bad/path", "contains slash"},
		{"bad\\path", "contains backslash"},
		{"", "empty via regex"},
	}
	for _, tt := range bad {
		if err := validateTitle(tt.title); err == nil {
			t.Errorf("validateTitle(%q) should fail (%s)", tt.title, tt.desc)
		}
	}
}

func TestPreprocessArgs(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			// file= selects the fuzzy resolver, so the shim emits --resolve fuzzy.
			"read command with key=value",
			[]string{"2nb", "read", "file=My Note", "format=raw"},
			[]string{"2nb", "read", "--format", "raw", "--resolve", "fuzzy", "My Note"},
		},
		{
			"daily read command",
			[]string{"2nb", "daily:read"},
			[]string{"2nb", "daily", "read"},
		},
		{
			"daily append with content",
			[]string{"2nb", "daily:append", "content=- my bullet"},
			[]string{"2nb", "daily", "append", "--text", "- my bullet"},
		},
		{
			"property read",
			[]string{"2nb", "property:read", "name=status", "file=projects/gimage.md"},
			[]string{"2nb", "meta", "--resolve", "fuzzy", "projects/gimage.md", "--get", "status"},
		},
		{
			"property set",
			[]string{"2nb", "property:set", "name=status", "value=active", "file=projects/gimage.md"},
			[]string{"2nb", "meta", "--resolve", "fuzzy", "projects/gimage.md", "--set", "status=active"},
		},
		{
			"property remove",
			[]string{"2nb", "property:remove", "name=status", "file=projects/gimage.md"},
			[]string{"2nb", "meta", "--resolve", "fuzzy", "projects/gimage.md", "--remove", "status"},
		},
		{
			"unresolved links list",
			[]string{"2nb", "unresolved"},
			[]string{"2nb", "unresolved"},
		},
		{
			"unresolved links namespace",
			[]string{"2nb", "link:unresolved"},
			[]string{"2nb", "unresolved"},
		},
		{
			"search query",
			[]string{"2nb", "search", "query=gimage"},
			[]string{"2nb", "search", "gimage"},
		},
		{
			"task ref",
			[]string{"2nb", "task", "ref=note.md:12", "done"},
			[]string{"2nb", "task", "note.md", "12", "--done"},
		},
		{
			"move note",
			[]string{"2nb", "move", "file=note.md", "to=archive/"},
			[]string{"2nb", "move", "--resolve", "fuzzy", "note.md", "archive/"},
		},
		{
			"rename note",
			[]string{"2nb", "rename", "file=note.md", "name=new-note.md"},
			[]string{"2nb", "rename", "--resolve", "fuzzy", "note.md", "new-note.md"},
		},
		// Regression: a free-text query containing "=" must NOT be parsed as a
		// key=value param and silently dropped. Before the fix, "a=b test" was
		// split into key "a" (unrecognized) and the whole query vanished.
		{
			"search query containing equals passes through verbatim",
			[]string{"2nb", "search", "a=b test"},
			[]string{"2nb", "search", "a=b test"},
		},
		{
			"ask question containing equals passes through verbatim",
			[]string{"2nb", "ask", "what is x=y?"},
			[]string{"2nb", "ask", "what is x=y?"},
		},
		{
			"search query= convenience still maps to the positional",
			[]string{"2nb", "search", "query=a=b"},
			[]string{"2nb", "search", "a=b"},
		},
		// Regression: an unrecognized key=value on a STRUCTURED command is
		// preserved verbatim rather than dropped, so a config value with "="
		// (or any positional that happens to contain "=") survives.
		{
			"config set value with equals is preserved",
			[]string{"2nb", "config", "set", "ai.x", "k=v"},
			[]string{"2nb", "config", "set", "ai.x", "k=v"},
		},
		// vault= is honored for free-text commands too; the query and the
		// translated --vault flag both land after the command (cobra accepts the
		// flag in any position). The positional query precedes vault= here
		// because processed args keep their original relative order.
		{
			"search with a query containing equals plus vault=",
			[]string{"2nb", "search", "a=b", "vault=/tmp/v"},
			[]string{"2nb", "search", "a=b", "--vault", "/tmp/v"},
		},
		// Regression: a bare flag-word (done/todo/toggle/verbose/overwrite) that
		// is part of a free-text query must NOT be consumed as a flag, or
		// `2nb search verbose` / `2nb search done` silently loses the query word.
		{
			"search query word 'verbose' is not consumed as a flag",
			[]string{"2nb", "search", "verbose"},
			[]string{"2nb", "search", "verbose"},
		},
		{
			"search query word 'done' is not consumed as a flag",
			[]string{"2nb", "search", "done"},
			[]string{"2nb", "search", "done"},
		},
		{
			"ask question word 'toggle' is not consumed as a flag",
			[]string{"2nb", "ask", "toggle"},
			[]string{"2nb", "ask", "toggle"},
		},
		// The flag-word IS honored for the command that owns it.
		{
			"task done maps to --done",
			[]string{"2nb", "task", "note.md", "12", "done"},
			[]string{"2nb", "task", "note.md", "12", "--done"},
		},
		// Native flag-style invocations must pass through the shim unmangled:
		// a -prefixed flag (even with an attached =value) is never parsed as a
		// key=value param, and a bare positional after it survives.
		{
			"native --flag value passes through",
			[]string{"2nb", "search", "foo", "--threshold", "0.35", "--limit", "5"},
			[]string{"2nb", "search", "foo", "--threshold", "0.35", "--limit", "5"},
		},
		{
			"native --flag=value (attached equals) is not parsed as key=value",
			[]string{"2nb", "search", "foo", "--threshold=0.35"},
			[]string{"2nb", "search", "foo", "--threshold=0.35"},
		},
		{
			"native config doctor subcommand + flag passes through",
			[]string{"2nb", "config", "doctor", "--json"},
			[]string{"2nb", "config", "doctor", "--json"},
		},
		// --- Obsidian-CLI compatibility (this PR) ---
		{
			"path= selects strict exact resolution",
			[]string{"2nb", "read", "path=projects/alpha.md"},
			[]string{"2nb", "read", "--resolve", "exact", "projects/alpha.md"},
		},
		{
			"print alias maps to read",
			[]string{"2nb", "print", "file=Alpha"},
			[]string{"2nb", "read", "--resolve", "fuzzy", "Alpha"},
		},
		{
			"fm alias maps to meta",
			[]string{"2nb", "fm", "file=Alpha"},
			[]string{"2nb", "meta", "--resolve", "fuzzy", "Alpha"},
		},
		{
			"properties alias maps to meta",
			[]string{"2nb", "properties", "file=Alpha"},
			[]string{"2nb", "meta", "--resolve", "fuzzy", "Alpha"},
		},
		{
			"files alias maps to list",
			[]string{"2nb", "files"},
			[]string{"2nb", "list"},
		},
		{
			"files total maps to list --total",
			[]string{"2nb", "files", "total"},
			[]string{"2nb", "list", "--total"},
		},
		{
			"tasks total maps to --total",
			[]string{"2nb", "tasks", "total"},
			[]string{"2nb", "tasks", "--total"},
		},
		{
			"unresolved total maps to --total",
			[]string{"2nb", "unresolved", "total"},
			[]string{"2nb", "unresolved", "--total"},
		},
		{
			"create content + template + overwrite tokens",
			[]string{"2nb", "create", "My Note", "content=hello", "template=adr", "overwrite"},
			[]string{"2nb", "create", "My Note", "--content", "hello", "--type", "adr", "--overwrite"},
		},
		{
			"create append token",
			[]string{"2nb", "create", "My Note", "content=more", "append"},
			[]string{"2nb", "create", "My Note", "--content", "more", "--append"},
		},
		{
			"tags:rename old/new maps to tags rename",
			[]string{"2nb", "tags:rename", "old=foo", "new=bar"},
			[]string{"2nb", "tags", "rename", "foo", "bar"},
		},
		{
			"tag:add maps to tag add with note + tags",
			[]string{"2nb", "tag:add", "file=My Note", "tag=a,b"},
			[]string{"2nb", "tag", "add", "--resolve", "fuzzy", "My Note", "a,b"},
		},
		{
			"tag:remove maps to tag remove",
			[]string{"2nb", "tag:remove", "file=My Note", "tag=old"},
			[]string{"2nb", "tag", "remove", "--resolve", "fuzzy", "My Note", "old"},
		},
		{
			"daily:path maps to daily path",
			[]string{"2nb", "daily:path"},
			[]string{"2nb", "daily", "path"},
		},
		{
			"search-content forces bm25-only",
			[]string{"2nb", "search-content", "hello world"},
			[]string{"2nb", "search", "hello world", "--bm25-only"},
		},
		{
			"list-vaults maps to vault list",
			[]string{"2nb", "list-vaults"},
			[]string{"2nb", "vault", "list"},
		},
		{
			"add-vault drops --set-default and maps to vault create",
			[]string{"2nb", "add-vault", "path=/tmp/v", "--set-default"},
			[]string{"2nb", "vault", "create", "--resolve", "exact", "/tmp/v"},
		},
		{
			"vault= in first position is honored",
			[]string{"2nb", "vault=/tmp/v", "files", "total"},
			[]string{"2nb", "list", "--vault", "/tmp/v", "--total"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preprocessArgs(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("length mismatch: got %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("at index %d: got %q, want %q\nFull: got %v, want %v", i, got[i], tt.want[i], got, tt.want)
				}
			}
		})
	}
}
