package gitcache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pubobs/backend/internal/model"
)

// SyncFile is one file in a sync payload from the plugin.
type SyncFile struct {
	Path      string
	MDContent string
}

// SyncAsset is a binary file (image, etc.) referenced by synced notes.
type SyncAsset struct {
	Path    string
	Content []byte // decoded binary
}

// AuthFailBackoff is how long getOrClone will short-circuit straight to a
// cached error for a repo after a network op most recently failed with an
// auth-classified error (ErrGitAuthFailed), instead of re-attempting the
// same doomed clone/fetch and paying a full DefaultGitOpTimeout again.
//
// Auth failures are not self-healing: unlike a corrupted local clone (which
// wipe-and-reclone genuinely fixes) or a transient network blip (which a
// retry genuinely fixes), bad/missing/expired credentials will fail exactly
// the same way on every attempt until an operator re-enters correct
// credentials via the repo's settings — so retrying immediately on every
// single incoming request just multiplies the cost of the same failure
// (e.g. every ListFiles call for the repo eating the full timeout) without
// any chance of a different outcome. The backoff is intentionally short
// (not the same as the repo cache TTL) so a credential fix takes effect on
// the next request shortly after being saved, rather than requiring a
// restart or a long wait. Exported as a var (rather than a const) so tests
// can shrink it to keep runtime fast.
var AuthFailBackoff = 30 * time.Second

// Cache manages per-repo local git clones.
type Cache struct {
	baseDir string
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
	git     *GitRunner

	authFailMu   sync.Mutex
	authFailedAt map[string]time.Time
	authFailErr  map[string]error
}

func NewCache(baseDir string) *Cache {
	return &Cache{
		baseDir:      baseDir,
		locks:        make(map[string]*sync.Mutex),
		git:          NewGitRunner(),
		authFailedAt: make(map[string]time.Time),
		authFailErr:  make(map[string]error),
	}
}

// SetGitTimeouts overrides how long Clone (cloneTimeout) and
// FetchReset/push (fetchTimeout) may each run before being killed and
// reported as a failure. Pass 0 for either to leave it at its package
// default (DefaultGitCloneTimeout / DefaultGitFetchTimeout).
func (c *Cache) SetGitTimeouts(cloneTimeout, fetchTimeout time.Duration) {
	c.git.CloneTimeout = cloneTimeout
	c.git.FetchTimeout = fetchTimeout
}

// SetLocalOpTimeout overrides how long any local-only git invocation (add,
// commit, rev-parse, ls-files, show, etc. — never Clone/FetchReset/push) may
// run before being killed and reported as ErrGitOpTimedOut. Pass 0 to leave
// it at DefaultGitLocalOpTimeout.
func (c *Cache) SetLocalOpTimeout(timeout time.Duration) {
	c.git.LocalOpTimeout = timeout
}

// recentAuthFailure returns the cached error from a recent auth failure for
// repoID, if one occurred within AuthFailBackoff, without touching git or
// the network at all.
func (c *Cache) recentAuthFailure(repoID string) error {
	c.authFailMu.Lock()
	defer c.authFailMu.Unlock()
	at, ok := c.authFailedAt[repoID]
	if !ok || time.Since(at) > AuthFailBackoff {
		return nil
	}
	return c.authFailErr[repoID]
}

// recordGitOpResult updates the auth-failure backoff state for repoID: a
// success (or any non-auth error) clears it, since only a confirmed auth
// rejection should suppress future retries.
func (c *Cache) recordGitOpResult(repoID string, err error) {
	c.authFailMu.Lock()
	defer c.authFailMu.Unlock()
	if errors.Is(err, ErrGitAuthFailed) {
		c.authFailedAt[repoID] = time.Now()
		c.authFailErr[repoID] = err
		return
	}
	delete(c.authFailedAt, repoID)
	delete(c.authFailErr, repoID)
}

func (c *Cache) repoLock(repoID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.locks[repoID]; !ok {
		c.locks[repoID] = &sync.Mutex{}
	}
	return c.locks[repoID]
}

func (c *Cache) repoDir(repoID string) string {
	return filepath.Join(c.baseDir, repoID)
}

// getOrClone ensures the repo is cloned locally. Must be called with repo lock held.
//
// A local clone can be left corrupted or incomplete if a previous clone/fetch
// was interrupted mid-operation (e.g. by disk exhaustion or a container
// restart) — the .git directory exists on disk but git can no longer read it,
// or a lock file from the killed process is still sitting there. Without
// self-healing, that permanently wedges every future ListFiles/Sync call for
// that repo (FetchReset would fail forever) until a human manually deletes
// the on-disk clone. To recover automatically:
//  1. Clear any *.lock files under .git — safe unconditionally, since all git
//     operations for this repo are serialized through repoLock, so a lock
//     file found here can only be a leftover from a previously-killed process,
//     never one held by a process running concurrently with us.
//  2. Run a cheap local sanity check (resolve HEAD) before trusting the
//     existing clone. If it fails, the clone is corrupted/incomplete
//     independent of network state, so wipe it and clone fresh. A plain
//     network/auth failure during FetchReset (healthy local clone, remote
//     just unreachable) does NOT trigger a wipe — that would be wasteful and
//     wrong, discarding a good incremental cache over a transient issue.
func (c *Cache) getOrClone(repo *model.Repo, credJSON string) (string, error) {
	dir := c.repoDir(repo.ID)
	gitDir := filepath.Join(dir, ".git")

	// A recent, still-fresh auth failure for this repo means the last
	// clone/fetch attempt was rejected for bad/missing credentials. That
	// won't have changed on its own, so skip straight to the cached error
	// instead of re-running (and re-timing-out) the exact same doomed
	// network operation on every request.
	if err := c.recentAuthFailure(repo.ID); err != nil {
		return "", err
	}

	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		err := c.freshClone(dir, repo, credJSON)
		if err != nil {
			log.Printf("gitcache: fresh clone for %s failed: %v", repo.ID, err)
		}
		c.recordGitOpResult(repo.ID, err)
		return dir, err
	}

	if err := clearStaleGitLocks(gitDir); err != nil {
		log.Printf("gitcache: clear stale locks for %s: %v", repo.ID, err)
	}

	if !c.git.IsHealthy(dir) {
		log.Printf("gitcache: local clone for %s is corrupted/incomplete, re-cloning from scratch", repo.ID)
		err := c.freshClone(dir, repo, credJSON)
		if err != nil {
			log.Printf("gitcache: re-clone for %s failed, live clone left untouched: %v", repo.ID, err)
		}
		c.recordGitOpResult(repo.ID, err)
		return dir, err
	}

	if err := c.git.FetchReset(dir, repo.RemoteURL, credJSON, repo.DefaultBranch); err != nil {
		err = fmt.Errorf("fetch-reset %s: %w", repo.ID, err)
		c.recordGitOpResult(repo.ID, err)
		return "", err
	}
	c.recordGitOpResult(repo.ID, nil)
	return dir, nil
}

// freshCloneTmpSuffix names the sibling staging directory freshClone clones
// into before atomically moving it onto the live path. It's a fixed (not
// randomized) name because freshClone only ever runs with this repo's
// per-repo lock held (see getOrClone's doc comment), so no two attempts for
// the same repo ID can ever race on it.
const freshCloneTmpSuffix = ".tmp-clone"

// freshClone clones the repo into a temporary staging directory next to dir
// (dir+freshCloneTmpSuffix, deliberately in the same parent directory as dir
// so the final move is a same-filesystem os.Rename, not a copy) and only
// replaces dir with it once the clone — and, for a newly-empty remote, the
// initial push — has fully succeeded.
//
// This matters because a clone that fails or is killed partway through
// (hitting CloneTimeout on a repo whose full clone is legitimately slow,
// disk exhaustion, a container restart, etc.) must never leave the *live*
// path worse off than it found it. The previous approach — clone directly
// into dir, then best-effort os.RemoveAll(dir) if that failed — relied on
// that cleanup always fully succeeding, and left the live path sitting in a
// transitional, only-partially-written state for the entire duration of the
// clone (not just on failure). Worse, for a repo whose full clone can never
// finish inside the configured timeout, every attempt would repeat the
// exact same doomed clone/timeout forever with no way to make progress.
// Staging in a temp dir first means a failed/timed-out attempt only ever
// leaves the *temp* dir broken (cleaned up below via os.RemoveAll); the live
// path is never touched until a full clone has already succeeded, so it can
// never look "corrupted" as a side effect of an interrupted clone — it's
// simply left as whatever it was before this attempt (a valid previous
// clone, or nothing, if this was the repo's first-ever clone).
func (c *Cache) freshClone(dir string, repo *model.Repo, credJSON string) error {
	tmpDir := dir + freshCloneTmpSuffix

	// A previous attempt that was killed before reaching the rename/cleanup
	// step below (e.g. the whole process was killed by an OOM/container
	// restart mid-clone, not just this specific operation timing out) can
	// leave a stale tmp dir behind. Clear it so every attempt starts from a
	// clean slate — safe unconditionally, for the same reason clearing
	// stale *.lock files is: this repo's lock is held for the whole call.
	if err := os.RemoveAll(tmpDir); err != nil {
		return fmt.Errorf("remove stale tmp clone dir %s: %w", tmpDir, err)
	}
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", tmpDir, err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			os.RemoveAll(tmpDir)
		}
	}()

	if err := c.git.Clone(tmpDir, repo.RemoteURL, credJSON, repo.DefaultBranch); err != nil {
		return fmt.Errorf("clone %s: %w", repo.RemoteURL, err)
	}
	if err := c.git.InitializeIfEmpty(tmpDir, repo.RemoteURL, credJSON, repo.DefaultBranch); err != nil {
		return fmt.Errorf("initialize %s: %w", repo.RemoteURL, err)
	}

	// Only now — with a fully-successful clone sitting in tmpDir — do we
	// touch the live path at all. os.Rename within the same parent
	// directory is a single atomic syscall, so once it starts it cannot
	// leave dir half-written. The brief gap between removing whatever
	// (possibly corrupted) content previously lived at dir and the rename
	// can, at worst, leave dir transiently absent if the process is killed
	// in that exact window — which self-heals via the ordinary
	// not-yet-cloned path on the next attempt, never as "corrupted".
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove existing clone %s: %w", dir, err)
	}
	if err := os.Rename(tmpDir, dir); err != nil {
		return fmt.Errorf("move fresh clone into place %s: %w", dir, err)
	}
	succeeded = true
	return nil
}

// clearStaleGitLocks removes leftover *.lock files under a repo's .git
// directory (e.g. index.lock, shallow.lock, HEAD.lock). These are only ever
// created transiently by a running git process; if one still exists here, it
// can only be a leftover from a previous process that was killed mid-operation,
// since callers always hold this repo's mutex across every git invocation.
func clearStaleGitLocks(gitDir string) error {
	var stale []string
	err := filepath.WalkDir(gitDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort; skip entries we can't stat
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".lock") {
			stale = append(stale, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range stale {
		if rmErr := os.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
			return rmErr
		}
	}
	return nil
}

// Sync writes files to the cache, removes deletedPaths, commits, and pushes.
// credJSON is the decrypted credentials string (may be empty for public repos).
// Returns the commit SHA.
func (c *Cache) Sync(ctx context.Context, repo *model.Repo, credJSON string, files []SyncFile, assets []SyncAsset, deletedPaths []string, commitMsg string) (string, error) {
	syncStart := time.Now()
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return "", err
	}

	for _, f := range files {
		fullPath := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(fullPath, []byte(f.MDContent), 0644); err != nil {
			return "", fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	for _, a := range assets {
		fullPath := filepath.Join(dir, a.Path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(fullPath, a.Content, 0644); err != nil {
			return "", fmt.Errorf("write asset %s: %w", a.Path, err)
		}
	}

	// Remove files the plugin reports as deleted from the working tree so
	// `git add -A` below actually stages the deletion. Without this, a
	// deletedPaths entry only ever removed the DB row and render-store blob
	// (see handleSync) — the .md file itself lived on in the git remote
	// forever, since git has no way to know it should be gone if it's never
	// actually removed from disk here.
	//
	// This loop and the AddCommitPush call below log their own start/end and
	// elapsed time (at least at debug granularity via Printf, matching this
	// package's existing style) specifically so that a future silent
	// process restart mid-Sync — the exact "no completed request log line"
	// symptom this comment is a response to — leaves a clear trail of which
	// phase was in flight, instead of a gap with no diagnostic signal at
	// all.
	if len(deletedPaths) > 0 {
		log.Printf("gitcache: sync %s: removing %d deleted path(s) from working tree", repo.ID, len(deletedPaths))
	}
	for _, p := range deletedPaths {
		fullPath := filepath.Join(dir, p)
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			log.Printf("gitcache: sync %s: remove %q failed: %v", repo.ID, p, err)
			return "", fmt.Errorf("remove %s: %w", p, err)
		}
	}

	commitStart := time.Now()
	sha, err := c.git.AddCommitPush(dir, repo.RemoteURL, credJSON, repo.DefaultBranch, commitMsg)
	c.recordGitOpResult(repo.ID, err)
	if err != nil {
		log.Printf("gitcache: sync %s: commit+push failed after %s: %v", repo.ID, time.Since(commitStart), err)
		return "", fmt.Errorf("commit+push: %w", err)
	}
	log.Printf("gitcache: sync %s: committed %s in %s (total sync %s), %d file(s), %d asset(s), %d deletion(s)",
		repo.ID, sha, time.Since(commitStart), time.Since(syncStart), len(files), len(assets), len(deletedPaths))
	return sha, nil
}

// ListFilePaths returns all tracked .md file paths in the repo without
// reading file contents. Prefer this over ListFiles when only path
// membership is needed (e.g. filtering a notes list).
func (c *Cache) ListFilePaths(ctx context.Context, repo *model.Repo, credJSON string) ([]string, error) {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return nil, err
	}

	return c.git.ListFiles(dir)
}

// ListFiles returns all .md files in the repo with their content and blob SHA.
func (c *Cache) ListFiles(ctx context.Context, repo *model.Repo, credJSON string) ([]model.FileEntry, error) {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return nil, err
	}

	paths, err := c.git.ListFiles(dir)
	if err != nil {
		return nil, err
	}

	var out []model.FileEntry
	for _, p := range paths {
		content, err := c.git.ReadFile(dir, p)
		if err != nil {
			return nil, err
		}
		sha, _ := c.git.BlobSHA(dir, p)
		out = append(out, model.FileEntry{Path: p, Content: content, SHA: sha})
	}
	return out, nil
}

// IsCloned reports whether a local git checkout currently exists on disk for
// this repo. The local clone lives in this node's cache directory and is
// created lazily on first access (sync, browse, or import) and removed by
// eviction once the repo goes stale, so this is checked live against the
// filesystem rather than a persisted flag.
func (c *Cache) IsCloned(repoID string) bool {
	_, err := os.Stat(filepath.Join(c.repoDir(repoID), ".git"))
	return err == nil
}

// Evict removes the local clone for a repo.
func (c *Cache) Evict(repoID string) error {
	lock := c.repoLock(repoID)
	lock.Lock()
	defer lock.Unlock()
	return os.RemoveAll(c.repoDir(repoID))
}

// ReadAsset reads a binary asset file from the local clone.
func (c *Cache) ReadAsset(repoID, assetPath string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(c.repoDir(repoID), assetPath))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ReadRenderedHTML reads the rendered HTML file from the local clone.
// Returns ("", nil) when the file doesn't exist yet (note not yet synced with renderer).
func (c *Cache) ReadRenderedHTML(repoID, mdPath string) (string, error) {
	htmlPath := filepath.Join(c.repoDir(repoID), strings.TrimSuffix(mdPath, ".md")+".html")
	data, err := os.ReadFile(htmlPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DiskUsage returns free bytes and percentage on the cache volume.
func (c *Cache) DiskUsage() (freeBytes int64, freePct float64, err error) {
	return diskUsage(c.baseDir)
}

// ReadRawFile reads the raw content of a file from the local clone.
// Returns ("", nil) when the file does not exist.
func (c *Cache) ReadRawFile(repoID, filePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(c.repoDir(repoID), filePath))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// HeadSHA returns the current HEAD commit SHA of a locally cached repo.
// Returns ("", err) if the repo hasn't been cloned yet.
func (c *Cache) HeadSHA(repoID string) (string, error) {
	return c.git.RevParseHEAD(c.repoDir(repoID))
}

// CommentCounts walks the repo's local clone for "*-comments.md" files and
// returns a map of note path (".md") -> number of comments. Only files that
// exist are read, so cost scales with the number of commented notes. Returns
// an empty map (no error) when the repo isn't cloned locally yet.
func (c *Cache) CommentCounts(repoID string) (map[string]int, error) {
	counts := map[string]int{}
	root := c.repoDir(repoID)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return counts, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "-comments.md") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		notePath := strings.TrimSuffix(filepath.ToSlash(rel), "-comments.md") + ".md"
		counts[notePath] = len(ParseComments(string(data)))
		return nil
	})
	return counts, err
}

// AppendComment appends a comment to the note's companion comments file,
// commits the change, and pushes to the remote.
// noteCommitSHA is the git_commit_sha of the note at the time of posting.
func (c *Cache) AppendComment(ctx context.Context, repo *model.Repo, credJSON, notePath, authorName, authorEmail, body, noteCommitSHA string) error {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return err
	}

	commentsPath := CommentsFilePath(notePath)
	fullPath := filepath.Join(dir, commentsPath)

	existing, err := os.ReadFile(fullPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	block := FormatComment(authorName, authorEmail, body, noteCommitSHA, time.Now().UTC())

	var content string
	if len(existing) == 0 {
		content = commentsFileHeader(notePath) + block
	} else {
		content = ensureParentLink(string(existing), notePath) + block
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return err
	}

	_, err = c.git.AddCommitPush(dir, repo.RemoteURL, credJSON, repo.DefaultBranch,
		fmt.Sprintf("pubobs: comment on %s", notePath))
	c.recordGitOpResult(repo.ID, err)
	return err
}
