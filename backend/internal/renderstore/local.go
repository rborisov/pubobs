// backend/internal/renderstore/local.go
package renderstore

import (
	"os"
	"path/filepath"
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
	p := s.filePath(repoID, notePath)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

func (s *LocalRenderStore) Read(repoID, notePath string) ([]byte, error) {
	data, err := os.ReadFile(s.filePath(repoID, notePath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func (s *LocalRenderStore) Delete(repoID, notePath string) error {
	err := os.Remove(s.filePath(repoID, notePath))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *LocalRenderStore) filePath(repoID, notePath string) string {
	return filepath.Join(s.baseDir, repoID, notePath+".enc")
}
