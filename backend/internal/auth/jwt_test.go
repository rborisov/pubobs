package auth_test

import (
	"testing"
	"time"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

func TestIssueAndVerifyAccessToken(t *testing.T) {
	key := testKey()
	token, err := auth.IssueAccessToken(key, "user-1", "alice@x.com", false, 24*time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := auth.VerifyAccessToken(key, token)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.UserID)
	require.Equal(t, "alice@x.com", claims.Email)
	require.False(t, claims.IsAdmin)
}

func TestAccessToken_expired(t *testing.T) {
	key := testKey()
	token, _ := auth.IssueAccessToken(key, "u1", "a@x.com", false, -1*time.Second)
	_, err := auth.VerifyAccessToken(key, token)
	require.Error(t, err)
}

func TestIssueAndVerifyRefreshToken(t *testing.T) {
	key := testKey()
	token, err := auth.IssueRefreshToken(key, "user-2", 30*24*time.Hour)
	require.NoError(t, err)

	userID, err := auth.VerifyRefreshToken(key, token)
	require.NoError(t, err)
	require.Equal(t, "user-2", userID)
}

func TestRefreshToken_wrongType(t *testing.T) {
	key := testKey()
	accessToken, _ := auth.IssueAccessToken(key, "u1", "a@x.com", false, time.Hour)
	_, err := auth.VerifyRefreshToken(key, accessToken)
	require.Error(t, err)
}
