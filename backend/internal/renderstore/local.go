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
