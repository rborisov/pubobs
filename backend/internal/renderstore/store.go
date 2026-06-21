// backend/internal/renderstore/store.go
package renderstore

// RenderStore persists encrypted rendered HTML blobs keyed by repo + note path.
// Blobs are opaque bytes; no decryption happens server-side.
type RenderStore interface {
	Write(repoID, notePath string, data []byte) error
	Read(repoID, notePath string) ([]byte, error)
	Delete(repoID, notePath string) error
}

// New constructs a RenderStore based on storeType ("local" or "s3").
// For "local", renderDir is the base directory for files.
// For "s3", the remaining parameters configure the S3-compatible endpoint.
func New(storeType, renderDir, endpoint, bucket, accessKey, secretKey, region string, useSSL bool) (RenderStore, error) {
	switch storeType {
	case "s3":
		return NewS3(endpoint, bucket, accessKey, secretKey, region, useSSL)
	default:
		return NewLocal(renderDir), nil
	}
}
