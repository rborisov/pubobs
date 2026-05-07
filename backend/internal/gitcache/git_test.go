package gitcache_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/stretchr/testify/require"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@x.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@x.com",
	)
	require.NoError(t, cmd.Run())
}

// newBareRepo creates a temporary bare git repo and returns its path.
func newBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", bare).Run())
	return bare
}

// seedBareRepo clones the bare repo, adds a file, and pushes it.
func seedBareRepo(t *testing.T, bareURL string) {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "clone", bareURL, ".")
	os.WriteFile(filepath.Join(work, "hello.md"), []byte("# Hello"), 0644)
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "initial")
	runGit(t, work, "push", "origin", "HEAD:main")
}

func TestCloneAndListFiles(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()

	err := g.Clone(cloneDir, bareURL, "", "main")
	require.NoError(t, err)

	files, err := g.ListFiles(cloneDir)
	require.NoError(t, err)
	require.Contains(t, files, "hello.md")
}

func TestAddCommitPush(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()
	require.NoError(t, g.Clone(cloneDir, bareURL, "", "main"))

	require.NoError(t, os.MkdirAll(filepath.Join(cloneDir, "docs"), 0755))
	os.WriteFile(filepath.Join(cloneDir, "docs/note.md"), []byte("# Note"), 0644)

	sha, err := g.AddCommitPush(cloneDir, bareURL, "", "main", "pubobs: sync 2024-01-01 by alice")
	require.NoError(t, err)
	require.Len(t, sha, 40)
}

func TestLogFile(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()
	require.NoError(t, g.Clone(cloneDir, bareURL, "", "main"))

	commits, err := g.LogFile(cloneDir, "hello.md")
	require.NoError(t, err)
	require.Len(t, commits, 1)
	require.Equal(t, "initial", commits[0].Message)
}

func TestFetchReset(t *testing.T) {
	bareURL := newBareRepo(t)
	seedBareRepo(t, bareURL)

	cloneDir := t.TempDir()
	g := gitcache.NewGitRunner()
	require.NoError(t, g.Clone(cloneDir, bareURL, "", "main"))

	// Push a second commit to the remote
	work := t.TempDir()
	runGit(t, work, "clone", bareURL, ".")
	os.WriteFile(filepath.Join(work, "second.md"), []byte("# Two"), 0644)
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "second")
	runGit(t, work, "push", "origin", "HEAD:main")

	require.NoError(t, g.FetchReset(cloneDir, bareURL, "", "main"))
	_, err := os.Stat(filepath.Join(cloneDir, "second.md"))
	require.NoError(t, err, "second.md should exist after FetchReset")

	remoteHead, err := exec.Command("git", "-C", work, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	localHead, err := exec.Command("git", "-C", cloneDir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(string(remoteHead)), strings.TrimSpace(string(localHead)),
		"local HEAD should match remote HEAD after FetchReset")
}
