package api

import "testing"

func TestValidRepoPath(t *testing.T) {
	valid := []string{
		"note.md",
		"notes/sub/note.md",
		"attachments/diagram.png",
		"данные/файл.csv",
		"a b/c d.json",
		"_pubobs/obsidian.css",
	}
	for _, p := range valid {
		if !validRepoPath(p) {
			t.Errorf("validRepoPath(%q) = false, want true", p)
		}
	}

	invalid := []string{
		"",                       // empty
		"/etc/passwd",            // absolute
		"../outside.md",          // escapes the clone
		"notes/../../outside.md", // escapes after descending
		"..",                     // bare parent
		"./notes/a.md",           // dot segment
		"notes//a.md",            // empty segment
		".git/config",            // git internals
		"notes/.git/hooks/pre-push",
		"C:\\Windows\\x.md", // windows absolute
		"notes\\a.md",       // backslash separator
		"notes/a\x00.md",    // NUL byte
	}
	for _, p := range invalid {
		if validRepoPath(p) {
			t.Errorf("validRepoPath(%q) = true, want false", p)
		}
	}
}
