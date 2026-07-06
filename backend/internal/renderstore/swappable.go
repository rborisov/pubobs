// backend/internal/renderstore/swappable.go
package renderstore

import "sync"

// SwappableStore lets the active backend be swapped at runtime (e.g. when an
// admin changes storage settings) without restarting the process. It
// implements RenderStore itself, delegating to whichever store is currently
// set — existing callers that just call Write/Read/Delete don't need to
// change at all.
type SwappableStore struct {
	mu      sync.RWMutex
	current RenderStore
}

func NewSwappableStore(initial RenderStore) *SwappableStore {
	return &SwappableStore{current: initial}
}

// Swap atomically redirects all subsequent calls to next.
func (s *SwappableStore) Swap(next RenderStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = next
}

// Current returns the store currently active — used where callers need to
// inspect the concrete backend (e.g. type-asserting to *S3RenderStore for
// disk-usage reporting).
func (s *SwappableStore) Current() RenderStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *SwappableStore) Write(repoID, notePath string, data []byte) error {
	s.mu.RLock()
	cur := s.current
	s.mu.RUnlock()
	return cur.Write(repoID, notePath, data)
}

func (s *SwappableStore) Read(repoID, notePath string) ([]byte, error) {
	s.mu.RLock()
	cur := s.current
	s.mu.RUnlock()
	return cur.Read(repoID, notePath)
}

func (s *SwappableStore) Delete(repoID, notePath string) error {
	s.mu.RLock()
	cur := s.current
	s.mu.RUnlock()
	return cur.Delete(repoID, notePath)
}
