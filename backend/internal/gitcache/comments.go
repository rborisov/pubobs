package gitcache

import (
	"fmt"
	"strings"
	"time"
)

// ParsedComment is a single comment parsed from a comments markdown file.
type ParsedComment struct {
	AuthorName    string
	AuthorEmail   string
	CreatedAt     time.Time
	Body          string
	NoteCommitSHA string // empty for comments written before SHA tracking was added
}

// CommentsFilePath derives the comments file path from a note path.
// "path/to/note.md" → "path/to/note-comments.md"
func CommentsFilePath(notePath string) string {
	return strings.TrimSuffix(notePath, ".md") + "-comments.md"
}

func commentsFileHeader(notePath string) string {
	return fmt.Sprintf("---\ntype: comments\nnote: %s\n---\n\n", notePath)
}

// FormatComment formats a single comment block for appending to a comments file.
// noteCommitSHA is the git_commit_sha of the note at the time of posting.
func FormatComment(name, email, body, noteCommitSHA string, ts time.Time) string {
	return fmt.Sprintf("### %s | %s | %s | %s\n\n%s\n",
		name, ts.UTC().Format(time.RFC3339), email, noteCommitSHA, strings.TrimSpace(body))
}

// ParseComments parses the contents of a comments markdown file into structured comments.
// The 4th header field (note commit SHA) is optional — legacy comments without it
// have NoteCommitSHA == "".
func ParseComments(content string) []ParsedComment {
	parts := strings.Split(content, "\n### ")
	start := 1
	if strings.HasPrefix(strings.TrimLeft(parts[0], "\r\n"), "### ") {
		parts[0] = strings.TrimPrefix(strings.TrimLeft(parts[0], "\r\n"), "### ")
		start = 0
	}
	var out []ParsedComment
	for _, part := range parts[start:] {
		nl := strings.Index(part, "\n")
		if nl == -1 {
			continue
		}
		header := strings.TrimSpace(part[:nl])
		body := strings.TrimSpace(part[nl+1:])

		fields := strings.SplitN(header, " | ", 4)
		if len(fields) < 3 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
		if err != nil {
			continue
		}
		var sha string
		if len(fields) == 4 {
			sha = strings.TrimSpace(fields[3])
		}
		out = append(out, ParsedComment{
			AuthorName:    strings.TrimSpace(fields[0]),
			AuthorEmail:   strings.TrimSpace(fields[2]),
			CreatedAt:     ts,
			Body:          body,
			NoteCommitSHA: sha,
		})
	}
	return out
}
