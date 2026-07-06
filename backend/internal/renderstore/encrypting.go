// backend/internal/renderstore/encrypting.go
package renderstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

// EncryptingStore wraps a RenderStore, transparently encrypting values with
// AES-GCM before writing and decrypting after reading. Used for assets,
// which (unlike render blobs) have no client-side encryption of their own —
// the server holds the key here, unlike the zero-knowledge render-blob flow.
type EncryptingStore struct {
	inner RenderStore
	key   []byte // exactly 32 bytes (AES-256)
}

// NewEncryptingStore wraps inner with AES-GCM encryption using key (must be
// 32 bytes).
func NewEncryptingStore(inner RenderStore, key []byte) (*EncryptingStore, error) {
	if len(key) != 32 {
		return nil, errors.New("encrypting store: key must be 32 bytes")
	}
	return &EncryptingStore{inner: inner, key: key}, nil
}

func (e *EncryptingStore) Write(repoID, notePath string, data []byte) error {
	gcm, err := e.gcm()
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return e.inner.Write(repoID, notePath, ciphertext)
}

func (e *EncryptingStore) Read(repoID, notePath string) ([]byte, error) {
	ciphertext, err := e.inner.Read(repoID, notePath)
	if err != nil {
		return nil, err
	}
	if ciphertext == nil {
		return nil, nil
	}
	gcm, err := e.gcm()
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("encrypting store: ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}

func (e *EncryptingStore) Delete(repoID, notePath string) error {
	return e.inner.Delete(repoID, notePath)
}

func (e *EncryptingStore) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
