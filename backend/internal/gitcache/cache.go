package gitcache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pubobs/backend/internal/model"
)

// SyncFile is one file in a sync payload from the plugin.
type SyncFile struct {
	Path        string
	MDContent   string
	HTMLContent string
}

// SyncAsset is a binary file (image, etc.) referenced by synced notes.
type SyncAsset struct {
	Path    string
	Content []byte // decoded binary
}

// Cache manages per-repo local git clones.
type Cache struct {
	baseDir string
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
	git     *GitRunner
}

func NewCache(baseDir string) *Cache {
	return &Cache{
		baseDir: baseDir,
		locks:   make(map[string]*sync.Mutex),
		git:     NewGitRunner(),
	}
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
func (c *Cache) getOrClone(repo *model.Repo, credJSON string) (string, error) {
	dir := c.repoDir(repo.ID)
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := c.git.Clone(dir, repo.RemoteURL, credJSON, repo.DefaultBranch); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("clone %s: %w", repo.RemoteURL, err)
		}
	} else {
		if err := c.git.FetchReset(dir, repo.RemoteURL, credJSON); err != nil {
			return "", fmt.Errorf("fetch-reset %s: %w", repo.ID, err)
		}
	}
	return dir, nil
}

// Sync writes files to the cache, commits them, and pushes.
// credJSON is the decrypted credentials string (may be empty for public repos).
// Returns the commit SHA.
func (c *Cache) Sync(ctx context.Context, repo *model.Repo, credJSON string, files []SyncFile, assets []SyncAsset, commitMsg string) (string, error) {
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
		if f.HTMLContent != "" {
			htmlPath := strings.TrimSuffix(fullPath, ".md") + ".html"
			if err := os.WriteFile(htmlPath, []byte(f.HTMLContent), 0644); err != nil {
				return "", fmt.Errorf("write html %s: %w", f.Path, err)
			}
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

	sha, err := c.git.AddCommitPush(dir, repo.RemoteURL, credJSON, repo.DefaultBranch, commitMsg)
	if err != nil {
		return "", fmt.Errorf("commit+push: %w", err)
	}
	return sha, nil
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

// History returns the commit log for a specific file.
func (c *Cache) History(ctx context.Context, repo *model.Repo, credJSON, filePath string) ([]model.Commit, error) {
	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return nil, err
	}
	return c.git.LogFile(dir, filePath)
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

// AppendComment appends a comment to the note's companion comments file,
// commits the change, and pushes to the remote.
func (c *Cache) AppendComment(ctx context.Context, repo *model.Repo, credJSON, notePath, authorName, authorEmail, body string) error {
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
	block := FormatComment(authorName, authorEmail, body, time.Now().UTC())

	var content string
	if len(existing) == 0 {
		content = commentsFileHeader(notePath) + block
	} else {
		content = string(existing) + block
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return err
	}

	_, err = c.git.AddCommitPush(dir, repo.RemoteURL, credJSON, repo.DefaultBranch,
		fmt.Sprintf("pubobs: comment on %s", notePath))
	return err
}
