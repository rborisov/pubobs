// backend/internal/renderstore/local.go
package renderstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalRenderStore stores encrypted render blobs as files under baseDir.
// File layout: <baseDir>/<repoID>/<notePath>.enc
type LocalRenderStore struct {
	baseDir string
}

func NewLocal(baseDir string) *LocalRenderStore {
	return &LocalRenderStore{baseDir: baseDir}
}

func (s *LocalRenderStore) Write(repoID, notePath string, data []byte) error {
	p, err := s.filePath(repoID, notePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

func (s *LocalRenderStore) Read(repoID, notePath string) ([]byte, error) {
	p, err := s.filePath(repoID, notePath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func (s *LocalRenderStore) Delete(repoID, notePath string) error {
	p, err := s.filePath(repoID, notePath)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// WalkEntries calls fn for every (repoID, notePath) currently stored,
// derived from the on-disk <baseDir>/<repoID>/<notePath>.enc layout. Used by
// the migration job to enumerate existing local data without needing a
// separate index of what's been synced.
func (s *LocalRenderStore) WalkEntries(fn func(repoID, notePath string) error) error {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, repoEntry := range entries {
		if !repoEntry.IsDir() {
			continue
		}
		repoID := repoEntry.Name()
		repoDir := filepath.Join(s.baseDir, repoID)
		err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(repoDir, path)
			if err != nil {
				return nil
			}
			notePath := strings.TrimSuffix(rel, ".enc")
			return fn(repoID, notePath)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// WalkRepoEntries calls fn for every notePath stored under repoID, derived
// from the on-disk <baseDir>/<repoID>/<notePath>.enc layout. A missing repo
// directory yields no entries and no error.
func (s *LocalRenderStore) WalkRepoEntries(repoID string, fn func(notePath string) error) error {
	repoDir := filepath.Join(s.baseDir, repoID)
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return nil
	}
	return filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(repoDir, path)
		if rerr != nil {
			return nil
		}
		return fn(strings.TrimSuffix(rel, ".enc"))
	})
}

func (s *LocalRenderStore) filePath(repoID, notePath string) (string, error) {
	p := filepath.Join(s.baseDir, repoID, notePath+".enc")
	// Ensure the resolved path stays within baseDir
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	base, err := filepath.Abs(s.baseDir)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, base+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path: %q escapes render store base dir", notePath)
	}
	return abs, nil
}
