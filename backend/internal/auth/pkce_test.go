package auth_test

import (
	"testing"
	"time"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestPKCEChallenge(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := auth.ComputeChallenge(verifier)
	require.Equal(t, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", challenge)
}

func TestSessionStore_StoreAndConsume(t *testing.T) {
	ss := auth.NewSessionStore()

	sessionID := ss.StoreSession("challenge123", "http://localhost:12345/callback", "plugin-state-xyz", "oidc")
	require.NotEmpty(t, sessionID)

	sess, ok := ss.GetSession(sessionID)
	require.True(t, ok)
	require.Equal(t, "challenge123", sess.CodeChallenge)
	require.Equal(t, "plugin-state-xyz", sess.PluginState)
}

func TestSessionStore_StoreCode_and_Consume(t *testing.T) {
	ss := auth.NewSessionStore()

	code := ss.StoreAuthCode("user-123", "challenge123")
	require.NotEmpty(t, code)

	_ = code

	verifier2 := "s256testverifier0000000000000000000000000000"
	challenge2 := auth.ComputeChallenge(verifier2)
	code2 := ss.StoreAuthCode("user-456", challenge2)

	uid, err := ss.ConsumeAuthCode(code2, verifier2)
	require.NoError(t, err)
	require.Equal(t, "user-456", uid)

	// Code is single-use
	_, err = ss.ConsumeAuthCode(code2, verifier2)
	require.Error(t, err)
}

func TestSessionStore_ExpiredCode(t *testing.T) {
	ss := auth.NewSessionStore()
	ss.SetCodeTTL(1 * time.Millisecond)

	code := ss.StoreAuthCode("user-789", "anychallenge")
	time.Sleep(5 * time.Millisecond)

	_, err := ss.ConsumeAuthCode(code, "anyverifier")
	require.Error(t, err)
}
