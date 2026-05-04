package gitcache

import (
	"testing"
	"time"
)

func TestParseComments_empty(t *testing.T) {
	got := ParseComments("")
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestParseComments_noComments(t *testing.T) {
	content := "---\ntype: comments\nnote: foo.md\n---\n\n"
	got := ParseComments(content)
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestParseComments_oneComment(t *testing.T) {
	content := "---\ntype: comments\nnote: foo.md\n---\n\n" +
		"### Alice | 2026-05-04T10:00:00Z | alice@example.com\n\nHello world\n"
	got := ParseComments(content)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	c := got[0]
	if c.AuthorName != "Alice" {
		t.Errorf("name: got %q", c.AuthorName)
	}
	if c.AuthorEmail != "alice@example.com" {
		t.Errorf("email: got %q", c.AuthorEmail)
	}
	if c.Body != "Hello world" {
		t.Errorf("body: got %q", c.Body)
	}
	wantTS := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	if !c.CreatedAt.Equal(wantTS) {
		t.Errorf("ts: got %v, want %v", c.CreatedAt, wantTS)
	}
}

func TestParseComments_noFrontmatter(t *testing.T) {
	content := "### Alice | 2026-05-04T10:00:00Z | alice@example.com\n\nHello\n"
	got := ParseComments(content)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Body != "Hello" {
		t.Errorf("body: got %q", got[0].Body)
	}
}

func TestParseComments_twoComments(t *testing.T) {
	content := "---\ntype: comments\nnote: foo.md\n---\n\n" +
		"### Alice | 2026-05-04T10:00:00Z | alice@example.com\n\nFirst\n" +
		"### Bob | 2026-05-04T11:00:00Z | bob@example.com\n\nSecond\n"
	got := ParseComments(content)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Body != "First" {
		t.Errorf("first body: got %q", got[0].Body)
	}
	if got[1].AuthorName != "Bob" {
		t.Errorf("second name: got %q", got[1].AuthorName)
	}
}

func TestFormatComment_roundtrip(t *testing.T) {
	ts := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	formatted := FormatComment("Alice", "alice@example.com", "Hello world", ts)
	got := ParseComments("---\ntype: comments\nnote: foo.md\n---\n\n" + formatted)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Body != "Hello world" {
		t.Errorf("body: got %q", got[0].Body)
	}
	wantTS := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	if !got[0].CreatedAt.Equal(wantTS) {
		t.Errorf("ts: got %v, want %v", got[0].CreatedAt, wantTS)
	}
}

func TestCommentsFilePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"note.md", "note-comments.md"},
		{"Daily Notes/2026-05-04.md", "Daily Notes/2026-05-04-comments.md"},
		{"path/to/deep/note.md", "path/to/deep/note-comments.md"},
	}
	for _, c := range cases {
		got := CommentsFilePath(c.in)
		if got != c.want {
			t.Errorf("CommentsFilePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
