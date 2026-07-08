package gitcache

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pubobs/backend/internal/model"
)

// GitRunner executes system git commands.
type GitRunner struct{}

func NewGitRunner() *GitRunner { return &GitRunner{} }

// credentialedURL injects credentials into an HTTPS remote URL if a PAT is provided.
// credJSON is expected to be `{"username":"...","password":"..."}` — parsed simply.
func credentialedURL(remoteURL, credJSON string) string {
	if credJSON == "" {
		return remoteURL
	}
	var username, password string
	for _, field := range []struct {
		key string
		dst *string
	}{
		{"\"username\":", &username}, {"\"password\":", &password},
	} {
		if idx := strings.Index(credJSON, field.key); idx >= 0 {
			rest := credJSON[idx+len(field.key):]
			rest = strings.TrimSpace(rest)
			if len(rest) > 0 && rest[0] == '"' {
				end := strings.Index(rest[1:], "\"")
				if end >= 0 {
					*field.dst = rest[1 : end+1]
				}
			}
		}
	}
	if username == "" || password == "" {
		return remoteURL
	}
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(remoteURL, scheme) {
			return scheme + username + ":" + password + "@" + remoteURL[len(scheme):]
		}
	}
	return remoteURL
}

func (g *GitRunner) run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=pubobs",
		"GIT_AUTHOR_EMAIL=pubobs@localhost",
		"GIT_COMMITTER_NAME=pubobs",
		"GIT_COMMITTER_EMAIL=pubobs@localhost",
		"GIT_TERMINAL_PROMPT=0",
	)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", args[0], err, errOut.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// Clone clones remoteURL into dir using a shallow single-branch clone.
func (g *GitRunner) Clone(dir, remoteURL, credJSON, branch string) error {
	authedURL := credentialedURL(remoteURL, credJSON)
	_, err := g.run("", "clone", "--depth=1", "--branch", branch, "--single-branch", authedURL, dir)
	if err != nil {
		firstErr := err
		_, err = g.run("", "clone", "--depth=1", authedURL, dir)
		if err != nil {
			return fmt.Errorf("clone with branch %q failed (%v); branchless clone also failed: %w", branch, firstErr, err)
		}
	}
	return err
}

// IsHealthy reports whether an existing local clone is in a usable state, by
// checking that git can resolve HEAD to a real commit. A clone left behind by
// an interrupted operation (e.g. disk exhaustion or a killed process mid-clone
// or mid-fetch) can have a .git directory on disk that git itself can no
// longer read — missing objects, a dangling HEAD, or an incomplete ref. This
// is a cheap, local-only check: it never touches the network.
func (g *GitRunner) IsHealthy(dir string) bool {
	_, err := g.run(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// FetchReset fetches the latest commit (depth=1) for branch and hard-resets to it.
// Fetching by name (no :<dst>) avoids non-fast-forward rejection on shallow clones.
func (g *GitRunner) FetchReset(dir, remoteURL, credJSON, branch string) error {
	authedURL := credentialedURL(remoteURL, credJSON)
	if _, err := g.run(dir, "fetch", "--depth=1", authedURL, branch); err != nil {
		return err
	}
	_, err := g.run(dir, "reset", "--hard", "FETCH_HEAD")
	return err
}

// InitializeIfEmpty creates an initial empty commit and pushes it when the
// local clone has no HEAD (i.e. the remote repo was empty at clone time).
// It is a no-op when the repo already has commits.
func (g *GitRunner) InitializeIfEmpty(dir, remoteURL, credJSON, branch string) error {
	if _, err := g.run(dir, "rev-parse", "HEAD"); err == nil {
		return nil
	}
	if _, err := g.run(dir, "commit", "--allow-empty", "-m", "pubobs: initialize"); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}
	authedURL := credentialedURL(remoteURL, credJSON)
	if _, err := g.run(dir, "push", authedURL, "HEAD:"+branch); err != nil {
		return fmt.Errorf("initial push: %w", err)
	}
	return nil
}

// AddCommitPush stages all changes, commits, pushes, and returns the commit SHA.
func (g *GitRunner) AddCommitPush(dir, remoteURL, credJSON, branch, message string) (string, error) {
	if _, err := g.run(dir, "add", "-A"); err != nil {
		return "", err
	}
	status, _ := g.run(dir, "status", "--porcelain")
	if status == "" {
		return g.run(dir, "rev-parse", "HEAD")
	}
	if _, err := g.run(dir, "commit", "-m", message); err != nil {
		return "", err
	}
	authedURL := credentialedURL(remoteURL, credJSON)
	if _, err := g.run(dir, "push", authedURL, "HEAD:"+branch); err != nil {
		return "", err
	}
	return g.run(dir, "rev-parse", "HEAD")
}

// ListFiles returns all tracked .md file paths relative to the repo root.
//
// Uses -z (NUL-delimited output) rather than parsing plain newline-separated
// output. Without -z, git applies its default core.quotePath=true behavior:
// any path containing non-ASCII bytes (or other "unusual" characters) gets
// C-style-quoted and octal-escaped, e.g. а filename "аппаратура.md" would be
// printed as `"\320\260\320\277\320\277\320\260\321\200\320\260\321\202\321\203\321\200\320\260.md"`
// (literal surrounding quote characters plus octal escapes). Since that
// output was previously split on plain newlines with no unquoting/unescaping
// step, non-ASCII filenames came back garbled from ListFiles itself, and
// later got passed as-is into `git show HEAD:<path>` (ReadFile) and
// `git ls-tree ... -- <path>` (BlobSHA), which fail outright because no file
// actually has literal quote characters in its name. -z sidesteps the whole
// quoting/escaping class of bugs (non-ASCII, embedded quotes/backslashes,
// etc.) by having git emit raw bytes with NUL terminators instead.
func (g *GitRunner) ListFiles(dir string) ([]string, error) {
	out, err := g.run(dir, "ls-files", "-z", "--", "*.md")
	if err != nil {
		return nil, err
	}
	out = strings.TrimSuffix(out, "\x00")
	if out == "" {
		return nil, nil
	}
	parts := strings.Split(out, "\x00")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

// ReadFile returns the content of a tracked file at HEAD.
func (g *GitRunner) ReadFile(dir, path string) (string, error) {
	return g.run(dir, "show", "HEAD:"+path)
}

// BlobSHA returns the git blob SHA for a tracked file at HEAD.
func (g *GitRunner) BlobSHA(dir, path string) (string, error) {
	out, err := g.run(dir, "ls-tree", "--format=%(objectname)", "HEAD", "--", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RevParseHEAD returns the current HEAD commit SHA.
func (g *GitRunner) RevParseHEAD(dir string) (string, error) {
	return g.run(dir, "rev-parse", "HEAD")
}

// LogFile returns the commit history for a specific file path.
func (g *GitRunner) LogFile(dir, path string) ([]model.Commit, error) {
	out, err := g.run(dir, "log",
		"--format=%H%x1f%ae%x1f%an%x1f%aI%x1f%s",
		"--", path)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var commits []model.Commit
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		t, _ := time.Parse(time.RFC3339, parts[3])
		commits = append(commits, model.Commit{
			SHA:         parts[0],
			AuthorEmail: parts[1],
			AuthorName:  parts[2],
			Date:        t,
			Message:     parts[4],
		})
	}
	return commits, nil
}
