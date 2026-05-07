package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pubobs/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestRequireAuth_valid(t *testing.T) {
	key := testKey()
	token, _ := auth.IssueAccessToken(key, "user-1", "alice@x.com", false, false, 3600*1e9)

	called := false
	handler := auth.RequireAuth(key)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		require.NotNil(t, claims)
		require.Equal(t, "user-1", claims.UserID)
		called = true
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	require.True(t, called)
}

func TestRequireAuth_missing(t *testing.T) {
	key := testKey()
	handler := auth.RequireAuth(key)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach handler")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireAuth_invalid(t *testing.T) {
	key := testKey()
	handler := auth.RequireAuth(key)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach handler")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer bad.token.here")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}
