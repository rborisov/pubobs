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
func (g *GitRunner) ListFiles(dir string) ([]string, error) {
	out, err := g.run(dir, "ls-files", "--", "*.md")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
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
