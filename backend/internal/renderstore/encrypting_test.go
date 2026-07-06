// backend/internal/renderstore/encrypting_test.go
package renderstore_test

import (
	"crypto/rand"
	"testing"

	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

func TestEncryptingStore_roundTrip(t *testing.T) {
	dir := t.TempDir()
	inner := renderstore.NewLocal(dir)
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)

	es, err := renderstore.NewEncryptingStore(inner, key)
	require.NoError(t, err)

	plaintext := []byte("hello asset bytes")
	require.NoError(t, es.Write("repo-1", "assets/img.png", plaintext))

	// The wrapper decrypts transparently.
	got, err := es.Read("repo-1", "assets/img.png")
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	// The underlying store holds ciphertext, not plaintext.
	raw, err := inner.Read("repo-1", "assets/img.png")
	require.NoError(t, err)
	require.NotEqual(t, plaintext, raw)

	// Missing key returns (nil, nil), matching the plain stores' contract.
	missing, err := es.Read("repo-1", "does/not/exist")
	require.NoError(t, err)
	require.Nil(t, missing)

	// Delete removes the entry.
	require.NoError(t, es.Delete("repo-1", "assets/img.png"))
	afterDelete, err := es.Read("repo-1", "assets/img.png")
	require.NoError(t, err)
	require.Nil(t, afterDelete)
}

func TestEncryptingStore_tamperedCiphertextFailsToDecrypt(t *testing.T) {
	dir := t.TempDir()
	inner := renderstore.NewLocal(dir)
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)

	es, err := renderstore.NewEncryptingStore(inner, key)
	require.NoError(t, err)

	require.NoError(t, es.Write("repo-1", "img.png", []byte("original")))

	raw, err := inner.Read("repo-1", "img.png")
	require.NoError(t, err)
	tampered := append([]byte{}, raw...)
	tampered[len(tampered)-1] ^= 0xFF // flip a bit in the auth tag
	require.NoError(t, inner.Write("repo-1", "img.png", tampered))

	_, err = es.Read("repo-1", "img.png")
	require.Error(t, err, "GCM must reject tampered ciphertext")
}

func TestNewEncryptingStore_rejectsWrongKeySize(t *testing.T) {
	dir := t.TempDir()
	inner := renderstore.NewLocal(dir)
	_, err := renderstore.NewEncryptingStore(inner, []byte("too-short"))
	require.Error(t, err)
}
