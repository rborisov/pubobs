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
	link := "[[" + strings.TrimSuffix(notePath, ".md") + "]]"
	return fmt.Sprintf("---\ntype: comments\nnote: %s\n---\n\nComments on %s\n\n", notePath, link)
}

// ensureParentLink inserts a "Comments on [[parent]]" line into an existing
// comments file that predates the wikilink (so Obsidian surfaces the comments
// file as a backlink on the note). Idempotent: a no-op if the link is already
// present. The line goes in the preamble, before the first "### " record.
func ensureParentLink(content, notePath string) string {
	link := "[[" + strings.TrimSuffix(notePath, ".md") + "]]"
	if strings.Contains(content, link) {
		return content
	}
	line := "Comments on " + link + "\n\n"
	if strings.HasPrefix(content, "---\n") {
		if idx := strings.Index(content[4:], "\n---\n"); idx != -1 {
			end := 4 + idx + len("\n---\n")
			rest := strings.TrimLeft(content[end:], "\n")
			return content[:end] + "\n" + line + rest
		}
	}
	return line + content
}

// SanitizeCommentName makes a user-supplied display name safe to store in the
// pipe-delimited, "\n### "-record comments file: it strips line breaks,
// neutralizes the "|" field separator, removes a leading "#"/space run so the
// name can't start a "### " comment header, trims, and caps length. Returns ""
// for empty/whitespace-only input; callers apply their own default (e.g.
// "anonym").
func SanitizeCommentName(name string) string {
	name = strings.ReplaceAll(name, "\r\n", " ")
	name = strings.NewReplacer("\r", " ", "\n", " ", "|", "").Replace(name)
	name = strings.TrimLeft(name, "# \t")
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 100 {
		name = strings.TrimSpace(string(r[:100]))
	}
	return name
}

// sanitizeHeaderField strips characters that would break the single-line,
// pipe-delimited "### a | b | c | d" comment header: line breaks and the "|"
// field separator. Used for machine fields (email, commit SHA).
func sanitizeHeaderField(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "|", " ").Replace(s)
}

// neutralizeCommentDelimiter stops stored content from forging a new comment
// record: ParseComments splits records on "\n### ", so any such sequence (or a
// body that begins with "### ", which meets the delimiter at the header/body
// seam) would be parsed back as a separate, independently-attributed comment.
// A zero-width space defeats the split while leaving the text visually intact.
func neutralizeCommentDelimiter(s string) string {
	s = strings.ReplaceAll(s, "\n### ", "\n​### ")
	if strings.HasPrefix(s, "### ") {
		s = "​" + s
	}
	return s
}

// FormatComment formats a single comment block for appending to a comments file.
// noteCommitSHA is the git_commit_sha of the note at the time of posting.
func FormatComment(name, email, body, noteCommitSHA string, ts time.Time) string {
	name = SanitizeCommentName(name)
	email = sanitizeHeaderField(email)
	noteCommitSHA = sanitizeHeaderField(noteCommitSHA)
	body = neutralizeCommentDelimiter(strings.TrimSpace(body))
	return fmt.Sprintf("### %s | %s | %s | %s\n\n%s\n",
		name, ts.UTC().Format(time.RFC3339), email, noteCommitSHA, body)
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
