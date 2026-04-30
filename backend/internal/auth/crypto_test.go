package auth_test

import (
	"testing"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecryptCreds(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	plaintext := `{"username":"x-access-token","password":"ghp_secret123"}`

	enc, err := auth.EncryptCreds(key, plaintext)
	require.NoError(t, err)
	require.NotEqual(t, plaintext, enc)

	dec, err := auth.DecryptCreds(key, enc)
	require.NoError(t, err)
	require.Equal(t, plaintext, dec)
}

func TestEncryptCreds_differentNonceEachTime(t *testing.T) {
	key := make([]byte, 32)
	plaintext := "secret"

	enc1, _ := auth.EncryptCreds(key, plaintext)
	enc2, _ := auth.EncryptCreds(key, plaintext)
	require.NotEqual(t, enc1, enc2, "each encryption should use a unique nonce")
}

func TestDecryptCreds_wrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 0xFF

	enc, _ := auth.EncryptCreds(key1, "secret")
	_, err := auth.DecryptCreds(key2, enc)
	require.Error(t, err)
}
