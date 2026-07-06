// backend/internal/renderstore/store.go
package renderstore

// RenderStore persists blobs keyed by repo + path. Render-blob values are
// opaque pre-encrypted bytes (client-side AES-GCM, server never decrypts);
// asset values are plaintext until wrapped in an EncryptingStore.
type RenderStore interface {
	Write(repoID, notePath string, data []byte) error
	Read(repoID, notePath string) ([]byte, error)
	Delete(repoID, notePath string) error
}

// New constructs a RenderStore based on storeType ("local" or "s3").
// For "local", localDir is the base directory for files (keyPrefix is
// ignored — callers use separate directories for separate namespaces).
// For "s3", the remaining parameters configure the S3-compatible endpoint;
// keyPrefix namespaces object keys (e.g. "renders/" vs "assets/") so a
// single bucket can serve both without collisions.
func New(storeType, localDir, endpoint, bucket, accessKey, secretKey, region string, useSSL bool, keyPrefix string) (RenderStore, error) {
	switch storeType {
	case "s3":
		return NewS3(endpoint, bucket, accessKey, secretKey, region, useSSL, keyPrefix)
	default:
		return NewLocal(localDir), nil
	}
}
