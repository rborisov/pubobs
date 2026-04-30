package auth_test

import (
	"testing"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestUserClaims_fields(t *testing.T) {
	uc := auth.UserClaims{
		Subject: "oidc-sub-123",
		Email:   "user@example.com",
		Name:    "Test User",
	}
	require.Equal(t, "oidc-sub-123", uc.Subject)
	require.Equal(t, "user@example.com", uc.Email)
}
