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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pubobs/backend/internal/model"
)

// DefaultGitCloneTimeout bounds how long a full/initial clone (Clone) may
// run before it is killed and reported as a failure.
//
// Without any timeout, a stalled remote — e.g. a git server that accepts the
// TCP/TLS connection and then never responds to (or silently drops) an
// unauthenticated or badly-authenticated smart-HTTP request instead of
// returning a fast 401 — leaves the underlying `git` subprocess blocked on a
// socket read with no timeout of its own, bounded only by whatever the
// OS/kernel's TCP defaults happen to be (which can be tens of seconds to
// several minutes, and is not something this codebase controls or can rely
// on). That was the original incident this timeout mechanism was built to
// fix (see DefaultGitFetchTimeout).
//
// Clone gets its own, longer budget than FetchReset/push: unlike an
// incremental fetch (which only ever transfers a small delta and should
// normally complete in a couple of seconds), a full --depth=1 clone still
// has to transfer the *entire* current tree for a repo, which can
// legitimately take well over the original 25s budget for a repo with a
// large working tree or many/large files — a real production repo hit
// exactly this: its clone was being killed by the (at the time, shared)
// 25s timeout after making genuine progress, not because the remote was
// stalled. 3 minutes is generous enough for that without being unbounded.
//
// Trade-off, investigated explicitly rather than assumed away: this timeout
// is also (still) the only backstop for the *original* stalled-remote/bad-
// auth-that-never-responds incident, if it happens to occur on a repo's
// very first clone specifically. classifyGitError (see below) only detects
// auth failures that the remote actually reports via stderr/HTTP status
// (401/403, "Authentication failed", etc.) — that detection is instant and
// completely independent of this timeout, so a remote that *responds* with
// a rejection is unaffected by this value no matter how long it is (see
// TestClone_AuthFailureClassifiedFastEvenWithLongCloneTimeout). But a
// remote that silently hangs and never responds at all cannot be
// classified any other way than "hit the timeout", so raising this value
// does mean that narrow case (silent hang, specifically during an initial
// clone, specifically before any local clone exists yet to fall back on)
// now takes longer to surface than it did right after today's earlier fix.
// This is judged an acceptable trade because: (1) FetchReset — used for
// every request against a repo that has already been cloned once, i.e. the
// overwhelming majority of traffic — keeps the original short
// DefaultGitFetchTimeout, so a remote that starts silently hanging on an
// already-working repo still fails fast; (2) the failure is still bounded
// (3 minutes, not unbounded) and, combined with the temp-clone+atomic-
// rename fix in cache.go, no longer risks wedging the repo in a doomed
// retry loop even in that slower case; (3) an auth-hang on a repo's very
// first-ever clone is a narrower intersection of conditions than the
// general case this mechanism guards against.
const DefaultGitCloneTimeout = 3 * time.Minute

// DefaultGitFetchTimeout bounds how long an incremental network git
// operation — FetchReset (fetch+reset for an already-cloned repo) or a push
// (InitializeIfEmpty, AddCommitPush) — may run before it is killed and
// reported as a failure.
//
// These transfer only a small delta (a handful of changed files/commits at
// most: this app's own doc edits and comment appends), so unlike a full
// Clone they are expected to be fast every time; a hang here is far more
// likely to indicate the same "remote accepted the connection but never
// completed the request" failure mode this whole timeout mechanism was
// built to catch, so it keeps the original tight 25s budget rather than
// Clone's longer one.
const DefaultGitFetchTimeout = 25 * time.Second

// DefaultGitLocalOpTimeout bounds every local-only git invocation (rev-parse,
// add, commit, ls-files, show, ls-tree, log — anything that never talks to a
// remote) that previously ran under an unbounded context.Background(). These
// are normally sub-second even for a repo with dozens of changed/renamed/
// deleted paths, so this exists purely as a safety net: without it, a local
// git process that somehow wedges (e.g. filesystem-level contention, a huge
// pathological diff) hangs the request goroutine forever with nothing to
// show for it in the logs, indistinguishable from an external kill (OOM,
// container restart) until it's already too late to tell the two apart.
// Bounding it turns that silent hang into a loud, timestamped
// ErrGitOpTimedOut instead — the whole point of this mechanism, mirroring
// why DefaultGitCloneTimeout/DefaultGitFetchTimeout exist for network ops.
const DefaultGitLocalOpTimeout = 2 * time.Minute

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
// within its configured timeout (CloneTimeout or FetchTimeout). This is
// reported distinctly from a generic git failure so callers/logs can tell
// "the remote is slow/unreachable/stuck" apart from "git exited with an
// error".
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
	// CloneTimeout bounds Clone specifically.
	CloneTimeout time.Duration
	// FetchTimeout bounds FetchReset and push operations (InitializeIfEmpty,
	// AddCommitPush) — everything network-facing except the initial Clone.
	FetchTimeout time.Duration
	// LocalOpTimeout bounds every local-only invocation (see
	// DefaultGitLocalOpTimeout).
	LocalOpTimeout time.Duration

	// safeDirMu serializes trustDirectory's writes to the shared global
	// gitconfig: different repos share one GitRunner, and `git config
	// --global --add` is a read-modify-write against a single file that
	// two concurrent invocations (for two different repos self-healing at
	// once, e.g. right after an ownership reset) could otherwise race on.
	safeDirMu sync.Mutex
}

func NewGitRunner() *GitRunner {
	return &GitRunner{
		CloneTimeout:   DefaultGitCloneTimeout,
		FetchTimeout:   DefaultGitFetchTimeout,
		LocalOpTimeout: DefaultGitLocalOpTimeout,
	}
}

// cloneTimeout returns the effective Clone timeout, falling back to the
// default if unset/invalid.
func (g *GitRunner) cloneTimeout() time.Duration {
	if g.CloneTimeout > 0 {
		return g.CloneTimeout
	}
	return DefaultGitCloneTimeout
}

// fetchTimeout returns the effective FetchReset/push timeout, falling back
// to the default if unset/invalid.
func (g *GitRunner) fetchTimeout() time.Duration {
	if g.FetchTimeout > 0 {
		return g.FetchTimeout
	}
	return DefaultGitFetchTimeout
}

// localOpTimeout returns the effective local-op timeout, falling back to
// the default if unset/invalid.
func (g *GitRunner) localOpTimeout() time.Duration {
	if g.LocalOpTimeout > 0 {
		return g.LocalOpTimeout
	}
	return DefaultGitLocalOpTimeout
}

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

// run executes a local-only git command (never touches the network),
// bounded by localOpTimeout() so a wedged local process surfaces as an
// explicit ErrGitOpTimedOut instead of hanging the caller indefinitely.
func (g *GitRunner) run(dir string, args ...string) (string, error) {
	timeout := g.localOpTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := g.runCtx(ctx, dir, args...)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%w after %s: git %s: %v", ErrGitOpTimedOut, timeout, args[0], err)
	}
	return out, err
}

// runNetwork runs a git command that talks to a remote, bounded by timeout
// (the caller resolves this to either cloneTimeout() or fetchTimeout()
// depending on which operation it's running). A context deadline exceeded is
// reported as ErrGitOpTimedOut rather than a bare "signal: killed"/context
// error, and any error is passed through classifyGitError so
// credential-rejection failures come back as ErrGitAuthFailed instead of an
// opaque generic error.
func (g *GitRunner) runNetwork(dir string, timeout time.Duration, args ...string) (string, error) {
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
	timeout := g.cloneTimeout()
	_, err := g.runNetwork("", timeout, "clone", "--depth=1", "--branch", branch, "--single-branch", authedURL, dir)
	if err != nil {
		if errors.Is(err, ErrGitAuthFailed) || errors.Is(err, ErrGitOpTimedOut) {
			// Not worth a second attempt: a bad branch name wouldn't produce
			// an auth-shaped error or a hang, so retrying branchless here
			// would just pay the same doomed cost twice.
			return err
		}
		firstErr := err
		_, err = g.runNetwork("", timeout, "clone", "--depth=1", authedURL, dir)
		if err != nil {
			return fmt.Errorf("clone with branch %q failed (%v); branchless clone also failed: %w", branch, firstErr, err)
		}
	}
	return err
}

// dubiousOwnershipMarker is the substring git (2.35.2+) prints when it
// refuses to operate on a repository directory owned by a different user
// than the process's effective UID ("detected dubious ownership in
// repository at ..."). This is a *configuration* mismatch, not on-disk
// corruption — retrying or wiping-and-recloning does nothing to fix it (the
// new clone would immediately hit the exact same check), yet its exit
// status/output shape is otherwise indistinguishable from any other git
// failure to a naive "non-zero exit means unhealthy" check. Distinguishing
// it matters in this app specifically: the backend container runs as root,
// but its data volumes can easily end up owned by a different UID (e.g. a
// leftover `chown` from an install/update flow written for an older,
// non-root image revision), so this is not a hypothetical edge case here.
const dubiousOwnershipMarker = "detected dubious ownership"

// isDubiousOwnershipError reports whether err is git's dubious-ownership
// rejection specifically, as opposed to any other failure (corruption,
// missing objects, etc.). Split out from IsHealthy as its own pure function
// so the classification itself — the exact thing that needs to distinguish
// "config mismatch, self-heal and retry" from "actually corrupted, wipe and
// re-clone" — is unit-testable without needing a real UID/ownership
// mismatch, which generally requires root privileges to set up.
func isDubiousOwnershipError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), dubiousOwnershipMarker)
}

// IsHealthy reports whether an existing local clone is in a usable state, by
// checking that git can resolve HEAD to a real commit. A clone left behind by
// an interrupted operation (e.g. disk exhaustion or a killed process mid-clone
// or mid-fetch) can have a .git directory on disk that git itself can no
// longer read — missing objects, a dangling HEAD, or an incomplete ref. This
// is a cheap, local-only check: it never touches the network.
//
// A "dubious ownership" rejection (see dubiousOwnershipMarker) is handled
// separately from genuine corruption: rather than reporting the clone
// unhealthy (which would trigger a wasteful full re-clone that hits the
// exact same ownership check immediately afterwards), this marks the
// directory as a trusted git.safe.directory and retries once. Only a
// still-failing retry — or any other kind of failure — is treated as real
// corruption.
func (g *GitRunner) IsHealthy(dir string) bool {
	_, err := g.run(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if err == nil {
		return true
	}
	if !isDubiousOwnershipError(err) {
		return false
	}
	log.Printf("gitcache: %s rejected by git as a dubious-ownership repository (owner mismatch, not corruption) — marking it as a trusted safe.directory and retrying", dir)
	if trustErr := g.trustDirectory(dir); trustErr != nil {
		log.Printf("gitcache: failed to mark %s as a safe.directory after a dubious-ownership rejection: %v", dir, trustErr)
		return false
	}
	_, err = g.run(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		log.Printf("gitcache: %s still unhealthy after marking it as a safe.directory: %v", dir, err)
	}
	return err == nil
}

// trustDirectory records dir in git's global safe.directory list so a
// UID/ownership mismatch between the current process and the directory's
// owner no longer blocks git operations against it. This is a targeted,
// per-directory grant (as opposed to the blanket `safe.directory=*` this
// image's Dockerfile also sets as the primary defense) so this code-level
// self-heal is safe to keep even if that image-level configuration were
// ever accidentally dropped.
func (g *GitRunner) trustDirectory(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve absolute path for %s: %w", dir, err)
	}
	g.safeDirMu.Lock()
	defer g.safeDirMu.Unlock()
	_, err = g.run("", "config", "--global", "--add", "safe.directory", abs)
	return err
}

// FetchReset fetches the latest commit (depth=1) for branch and hard-resets to it.
// Fetching by name (no :<dst>) avoids non-fast-forward rejection on shallow clones.
func (g *GitRunner) FetchReset(dir, remoteURL, credJSON, branch string) error {
	authedURL, applied := credentialedURLWithStatus(remoteURL, credJSON)
	warnIfCredsUnusable(remoteURL, credJSON, applied)
	if _, err := g.runNetwork(dir, g.fetchTimeout(), "fetch", "--depth=1", authedURL, branch); err != nil {
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
	if _, err := g.runNetwork(dir, g.fetchTimeout(), "push", authedURL, "HEAD:"+branch); err != nil {
		return fmt.Errorf("initial push: %w", err)
	}
	return nil
}

// AddCommitPush stages all changes, commits, pushes, and returns the commit
// SHA. Used both for note syncs and for AppendComment (posting a single
// comment). Both push a small incremental diff (edited/added markdown files,
// or one appended comment block) on top of an already-cloned repo — the same
// small-and-fast size/timing profile as FetchReset — so this shares
// fetchTimeout() rather than warranting its own budget the way the initial
// Clone does.
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
	if _, err := g.runNetwork(dir, g.fetchTimeout(), "push", authedURL, "HEAD:"+branch); err != nil {
		return "", err
	}
	// Bound loose-object growth. A repo that's synced/commented on regularly
	// never idles long enough to be evicted, so without this every commit's
	// objects would accumulate in .git/objects forever (git gc is run nowhere
	// else). "--auto" is a cheap no-op until git's own thresholds are exceeded;
	// a gc failure must not fail the sync/comment, so it's best-effort.
	_, _ = g.run(dir, "gc", "--auto")
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
