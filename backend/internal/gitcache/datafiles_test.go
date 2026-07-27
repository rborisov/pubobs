package gitcache_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
	"github.com/stretchr/testify/require"
)

// seedDataRepo builds a bare remote containing a mix of notes, data files,
// metadata and a binary, and returns its URL.
func seedDataRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	require.NoError(t, exec.Command("git", "init", "--bare", bare).Run())

	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x.com",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x.com")
		require.NoError(t, cmd.Run(), strings.Join(args, " "))
	}
	run("clone", bare, ".")
	for path, content := range files {
		full := filepath.Join(work, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	run("add", ".")
	run("commit", "-m", "seed")
	run("push", "origin", "HEAD:main")
	return bare
}

func TestListDataFiles(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{
		"note.md":              "# Note",
		"data/table.csv":       "a,b\n1,2\n",
		"data/config.json":     "{\"k\":1}\n",
		"views/tasks.base":     "filters:\n  - done\n",
		"stack.yaml":           "key: value\n",
		"_pubobs/obsidian.css": "body{}",
		"readme.txt":           "not requested",
	})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "",
		[]string{"csv", "json", "base", "yaml", "yml"}, 5<<20)
	require.NoError(t, err)

	paths := map[string]string{}
	for _, f := range got.Files {
		paths[f.Path] = f.Content
	}
	require.Len(t, paths, 4)
	require.Contains(t, paths, "data/table.csv")
	require.Contains(t, paths, "data/config.json")
	require.Contains(t, paths, "views/tasks.base")
	require.Contains(t, paths, "stack.yaml")
	require.NotContains(t, paths, "note.md", "notes are not data files")
	require.NotContains(t, paths, "readme.txt", "unrequested extensions are excluded")
	require.NotContains(t, paths, "_pubobs/obsidian.css", "_pubobs metadata is never a data file")

	require.Equal(t, "a,b\n1,2\n", paths["data/table.csv"],
		"content must be byte-exact, including the trailing newline")

	for _, f := range got.Files {
		require.NotEmpty(t, f.SHA)
		require.Greater(t, f.Size, int64(0))
	}
}

// The plugin's isDataFilePath lowercases a file's extension before matching,
// so a vault file named TABLE.CSV is pushed into the repo as a data file. If
// the listing side stayed case-sensitive (git's default fnmatch pathspec) that
// file would sync one way only and never receive remote updates. Distinct base
// names are used deliberately: t.TempDir() is case-insensitive on macOS, so
// "table.csv" and "TABLE.CSV" would collide on disk.
func TestListDataFiles_matchesExtensionCaseInsensitively(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{
		"data/table.csv":  "a,b\n1,2\n",
		"data/UPPER.CSV":  "c,d\n3,4\n",
		"data/Mixed.Json": "{\"k\":1}\n",
	})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "", []string{"csv", "json"}, 5<<20)
	require.NoError(t, err)

	paths := map[string]string{}
	for _, f := range got.Files {
		paths[f.Path] = f.Content
	}
	require.Contains(t, paths, "data/table.csv")
	require.Contains(t, paths, "data/UPPER.CSV", "an uppercase extension must still be listed")
	require.Contains(t, paths, "data/Mixed.Json", "a mixed-case extension must still be listed")
	require.Equal(t, "c,d\n3,4\n", paths["data/UPPER.CSV"])
}

// Notes are excluded from the data-file list by suffix, and that exclusion has
// to be case-insensitive now that the pathspec is: an "md" ext never reaches
// here through the HTTP layer (parseDataFileExts refuses it), but the guard is
// what keeps a note out of the list if it ever did.
func TestListDataFiles_excludesNotesRegardlessOfCase(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{
		"note.md":  "# Note",
		"OTHER.MD": "# Other",
		"a.csv":    "x\n",
	})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "", []string{"md", "csv"}, 5<<20)
	require.NoError(t, err)

	require.Len(t, got.Files, 1)
	require.Equal(t, "a.csv", got.Files[0].Path)
}

func TestListDataFiles_skipsOversized(t *testing.T) {
	big := strings.Repeat("x", 2048)
	bare := seedDataRepo(t, map[string]string{
		"small.csv": "a,b\n",
		"big.csv":   big,
	})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "", []string{"csv"}, 1024)
	require.NoError(t, err)

	require.Len(t, got.Files, 1)
	require.Equal(t, "small.csv", got.Files[0].Path)
	require.Len(t, got.Skipped, 1)
	require.Equal(t, "big.csv", got.Skipped[0].Path)
	require.Equal(t, "too_large", got.Skipped[0].Reason)
	require.Equal(t, int64(2048), got.Skipped[0].Size)
}

func TestListDataFiles_skipsNonUTF8(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{
		"ok.csv":  "a,b\n",
		"bad.csv": "\xff\xfe\x00binary",
	})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "", []string{"csv"}, 5<<20)
	require.NoError(t, err)

	require.Len(t, got.Files, 1)
	require.Equal(t, "ok.csv", got.Files[0].Path)
	require.Len(t, got.Skipped, 1)
	require.Equal(t, "bad.csv", got.Skipped[0].Path)
	require.Equal(t, "not_utf8", got.Skipped[0].Reason)
}

// maxBytes is clamped to the hard ceiling: a client cannot raise its own limit
// past what the server is willing to read into memory.
func TestListDataFiles_clampsMaxBytes(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{"a.csv": "x\n"})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	for _, maxBytes := range []int64{0, -1, gitcache.MaxDataFileBytes * 10} {
		got, err := c.ListDataFiles(context.Background(), repo, "", []string{"csv"}, maxBytes)
		require.NoError(t, err)
		require.Len(t, got.Files, 1, "maxBytes=%d must fall back to the ceiling", maxBytes)
	}
}

func TestListDataFiles_noExtensionsReturnsNothing(t *testing.T) {
	bare := seedDataRepo(t, map[string]string{"a.csv": "x\n"})
	repo := &model.Repo{ID: "r1", RemoteURL: bare, DefaultBranch: "main"}
	c := gitcache.NewCache(t.TempDir())

	got, err := c.ListDataFiles(context.Background(), repo, "", nil, 5<<20)
	require.NoError(t, err)
	require.Empty(t, got.Files)
}
