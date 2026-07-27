package gitcache

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/pubobs/backend/internal/model"
)

// MaxDataFileBytes is the hard per-file ceiling for data files, in either
// direction. A client may request a smaller limit; it may not raise this one.
// It bounds how much a single request can make the server read into memory
// (the list endpoint holds every matched file's content at once) and how large
// a file a sync payload can cause to be written into a clone.
const MaxDataFileBytes = 25 << 20 // 25 MB

// DataFile is a non-note repo file synced verbatim to the vault.
type DataFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA     string `json:"sha"`
	Size    int64  `json:"size"`
}

// SkippedDataFile records a file that matched the extension allowlist but was
// deliberately not returned. Reported rather than dropped silently, so the
// plugin can tell the user why a file they expected never appeared.
type SkippedDataFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Reason string `json:"reason"` // "too_large" | "not_utf8"
}

type DataFileList struct {
	Files   []DataFile        `json:"files"`
	Skipped []SkippedDataFile `json:"skipped"`
}

// ListDataFiles returns the repo's tracked files whose extension is in exts,
// excluding notes and _pubobs/ metadata.
//
// Content is read from the working tree rather than through GitRunner.ReadFile
// (`git show HEAD:<path>`) because every GitRunner invocation returns
// strings.TrimSpace'd output. For markdown that only costs a trailing newline;
// for a CSV or JSON file it means the vault copy never matches the repo copy
// byte-for-byte, so every sync round-trip would report a spurious change. The
// working tree is hard-reset to the remote tip by getOrClone immediately
// above, so it is exactly HEAD's content.
func (c *Cache) ListDataFiles(ctx context.Context, repo *model.Repo, credJSON string, exts []string, maxBytes int64) (DataFileList, error) {
	out := DataFileList{Files: []DataFile{}, Skipped: []SkippedDataFile{}}
	if len(exts) == 0 {
		return out, nil
	}
	if maxBytes <= 0 || maxBytes > MaxDataFileBytes {
		maxBytes = MaxDataFileBytes
	}

	lock := c.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()

	dir, err := c.getOrClone(repo, credJSON)
	if err != nil {
		return DataFileList{}, err
	}
	paths, err := c.git.ListFilesByExt(dir, exts)
	if err != nil {
		return DataFileList{}, err
	}

	for _, p := range paths {
		if strings.HasPrefix(p, "_pubobs/") || strings.HasSuffix(p, ".md") {
			continue
		}
		full := filepath.Join(dir, p)
		info, err := os.Stat(full)
		if err != nil {
			// Most commonly: tracked but absent from the working tree, nothing
			// to send. This branch also covers genuine I/O errors (permission
			// denied, disk failure, etc.); log so an unreadable file is
			// diagnosable in production instead of silently vanishing from
			// both Files and Skipped.
			log.Printf("gitcache: list data files for %s: stat %q failed: %v", repo.ID, p, err)
			continue
		}
		if info.Size() > maxBytes {
			out.Skipped = append(out.Skipped, SkippedDataFile{Path: p, Size: info.Size(), Reason: "too_large"})
			continue
		}
		content, err := os.ReadFile(full)
		if err != nil {
			log.Printf("gitcache: list data files for %s: read %q failed: %v", repo.ID, p, err)
			continue
		}
		if !utf8.Valid(content) {
			out.Skipped = append(out.Skipped, SkippedDataFile{Path: p, Size: info.Size(), Reason: "not_utf8"})
			continue
		}
		sha, _ := c.git.BlobSHA(dir, p)
		out.Files = append(out.Files, DataFile{Path: p, Content: string(content), SHA: sha, Size: info.Size()})
	}
	return out, nil
}
