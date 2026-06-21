// backend/internal/renderstore/store.go
package renderstore

// RenderStore persists encrypted rendered HTML blobs keyed by repo + note path.
// Blobs are opaque bytes; no decryption happens server-side.
type RenderStore interface {
	Write(repoID, notePath string, data []byte) error
	Read(repoID, notePath string) ([]byte, error)
	Delete(repoID, notePath string) error
}
