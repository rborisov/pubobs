package gitcache

import (
	"fmt"
	"strings"
	"time"
)

// ParsedComment is a single comment parsed from a comments markdown file.
type ParsedComment struct {
	AuthorName  string
	AuthorEmail string
	CreatedAt   time.Time
	Body        string
}

// CommentsFilePath derives the comments file path from a note path.
// "path/to/note.md" → "path/to/note-comments.md"
func CommentsFilePath(notePath string) string {
	return strings.TrimSuffix(notePath, ".md") + "-comments.md"
}

// commentsFileHeader returns the frontmatter for a new comments file.
func commentsFileHeader(notePath string) string {
	return fmt.Sprintf("---\ntype: comments\nnote: %s\n---\n\n", notePath)
}

// FormatComment formats a single comment block for appending to a comments file.
func FormatComment(name, email, body string, ts time.Time) string {
	return fmt.Sprintf("### %s | %s | %s\n\n%s\n",
		name, ts.UTC().Format(time.RFC3339), email, strings.TrimSpace(body))
}

// ParseComments parses the contents of a comments markdown file into structured comments.
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

		fields := strings.SplitN(header, " | ", 3)
		if len(fields) != 3 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
		if err != nil {
			continue
		}
		out = append(out, ParsedComment{
			AuthorName:  strings.TrimSpace(fields[0]),
			AuthorEmail: strings.TrimSpace(fields[2]),
			CreatedAt:   ts,
			Body:        body,
		})
	}
	return out
}
