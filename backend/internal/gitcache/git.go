package gitcache

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pubobs/backend/internal/model"
)

// DefaultGitOpTimeout bounds how long any single network git operation
// (clone/fetch/push) may run before it is killed and reported as a failure.
//
// Without this, a stalled remote — e.g. a git server that accepts the TCP/TLS
// connection and then never responds to (or silently drops) an unauthenticated
// or badly-authenticated smart-HTTP request instead of returning a fast 401 —
// leaves the underlying `git` subprocess blocked on a socket read with no
// timeout of its own, bounded only by whatever the OS/kernel's TCP defaults
// happen to be (which can be tens of seconds to several minutes, and is not
// something this codebase controls or can rely on). 25s is generous for the
// small, --depth=1 markdown-notes clones this app deals with, while still
// being short enough that a bad-credentials or unreachable-remote failure
// surfaces to the caller quickly instead of silently eating a large chunk of
// the request's budget.
const DefaultGitOpTimeout = 25 * time.Second

// ErrGitAuthFailed signals that a git network operation (clone/fetch/push)
// failed because the remote rejected (or could not obtain) credentials —
// as opposed to a generic network error or local corruption. Distinguishing
// this matters because auth failures are not transient and will not be
// fixed by retrying or by gitcache's corrupted-clone self-heal (wipe +
// reclone): the wipe-and-reclone will just fail the exact same way, wasting
// another full timeout on every subsequent request until an operator fixes
// the repo's stored credentials.
var ErrGitAuthFailed = errors.New("authentication failed for repo remote — check stored credentials")

// ErrGitOpTimedOut signals that a git network operation did not complete
// within Timeout. This is reported distinctly from a generic git failure so
// callers/logs can tell "the remote is slow/unreachable/stuck" apart from
// "git exited with an error".
var ErrGitOpTimedOut = errors.New("git operation timed out")

// authFailurePatterns are substrings (matched case-insensitively) that git
// and common HTTP git servers (GitHub, GitLab, Gogs/Gitea, Bitbucket) use in
// stderr/response bodies when a request is rejected for bad or missing
// credentials, as opposed to a network-level or corruption failure.
var authFailurePatterns = []string{
	"authentication failed",
	"could not read username",
	"could not read password",
	"terminal prompts disabled",
	"invalid credentials",
	"invalid username or password",
	"access denied",
	"permission denied (publickey)",
	"the requested url returned error: 401",
	"the requested url returned error: 403",
	"http basic: access denied",
	"401 unauthorized",
	"403 forbidden",
}

// classifyGitError inspects a failed git invocation's error/stderr and wraps
// it with ErrGitAuthFailed when it looks like a credential rejection, so
// callers can distinguish "bad credentials, retrying won't help" from other
// failures without re-parsing error strings themselves.
func classifyGitError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	for _, pat := range authFailurePatterns {
		if strings.Contains(lower, pat) {
			return fmt.Errorf("%w: %v", ErrGitAuthFailed, err)
		}
	}
	return err
}

// GitRunner executes system git commands.
type GitRunner struct {
	// Timeout bounds network operations (Clone, FetchReset, push). Local-only
	// operations (rev-parse, ls-files, show, etc.) are never subject to it.
	Timeout time.Duration
}

func NewGitRunner() *GitRunner { return &GitRunner{Timeout: DefaultGitOpTimeout} }

// credentialedURL injects credentials into an HTTPS remote URL if a PAT is provided.
// credJSON is expected to be `{"username":"...","password":"..."}` — parsed simply.
func credentialedURL(remoteURL, credJSON string) string {
	url, _ := credentialedURLWithStatus(remoteURL, credJSON)
	return url
}

// credentialedURLWithStatus is credentialedURL plus a flag reporting whether
// credentials were actually applied. This lets callers tell "no credentials
// were ever stored for this repo" apart from "credentials are stored but
// couldn't be parsed/were incomplete" (e.g. missing password field, corrupt
// JSON) — the latter silently degrades to an anonymous request today, which
// is indistinguishable from an intentionally public repo unless logged.
func credentialedURLWithStatus(remoteURL, credJSON string) (url string, applied bool) {
	if credJSON == "" {
		return remoteURL, false
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
		return remoteURL, false
	}
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(remoteURL, scheme) {
			return scheme + username + ":" + password + "@" + remoteURL[len(scheme):], true
		}
	}
	return remoteURL, false
}

// warnIfCredsUnusable logs when a repo has non-empty stored credentials that
// nonetheless couldn't be turned into an authenticated URL (malformed JSON,
// or a username/password field missing/empty) — this is the operator-visible
// signal for "your stored credentials for this repo are broken", since
// otherwise the request just silently degrades to an anonymous one and looks
// identical in logs to a repo that was always intentionally public.
func warnIfCredsUnusable(remoteURL, credJSON string, applied bool) {
	if credJSON != "" && !applied {
		log.Printf("gitcache: repo remote %s has stored credentials but they could not be parsed into a username+password — attempting an unauthenticated request instead", remoteURL)
	}
}

func (g *GitRunner) run(dir string, args ...string) (string, error) {
	return g.runCtx(context.Background(), dir, args...)
}

// runNetwork runs a git command that talks to a remote, bounded by Timeout.
// A context deadline exceeded is reported as ErrGitOpTimedOut rather than a
// bare "signal: killed"/context error, and any error is passed through
// classifyGitError so credential-rejection failures come back as
// ErrGitAuthFailed instead of an opaque generic error.
func (g *GitRunner) runNetwork(dir string, args ...string) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = DefaultGitOpTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := g.runCtx(ctx, dir, args...)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%w after %s: git %s: %v", ErrGitOpTimedOut, timeout, args[0], err)
		}
		return "", classifyGitError(err)
	}
	return out, nil
}

func (g *GitRunner) runCtx(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	// git clone/fetch/push over HTTP(S) delegate the actual network I/O to a
	// `git-remote-http(s)` helper subprocess. When the context is cancelled,
	// exec.CommandContext only kills the direct `git` child — the helper
	// grandchild can be left running, still holding the stdout/stderr pipes
	// open, which would otherwise make cmd.Wait() (and thus this whole call)
	// block forever waiting for those pipes to reach EOF even though the
	// process we care about was already killed. WaitDelay bounds that: once
	// the context is done, Wait forcibly closes the I/O pipes if the
	// goroutines copying them haven't finished within this long.
	cmd.WaitDelay = 5 * time.Second
	configureProcessGroup(cmd)
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
	authedURL, applied := credentialedURLWithStatus(remoteURL, credJSON)
	warnIfCredsUnusable(remoteURL, credJSON, applied)
	_, err := g.runNetwork("", "clone", "--depth=1", "--branch", branch, "--single-branch", authedURL, dir)
	if err != nil {
		if errors.Is(err, ErrGitAuthFailed) || errors.Is(err, ErrGitOpTimedOut) {
			// Not worth a second attempt: a bad branch name wouldn't produce
			// an auth-shaped error or a hang, so retrying branchless here
			// would just pay the same doomed cost twice.
			return err
		}
		firstErr := err
		_, err = g.runNetwork("", "clone", "--depth=1", authedURL, dir)
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
	authedURL, applied := credentialedURLWithStatus(remoteURL, credJSON)
	warnIfCredsUnusable(remoteURL, credJSON, applied)
	if _, err := g.runNetwork(dir, "fetch", "--depth=1", authedURL, branch); err != nil {
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
	if _, err := g.runNetwork(dir, "push", authedURL, "HEAD:"+branch); err != nil {
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
	if _, err := g.runNetwork(dir, "push", authedURL, "HEAD:"+branch); err != nil {
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
