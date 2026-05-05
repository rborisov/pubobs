package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func newTestDeps(t *testing.T) *api.Deps {
	t.Helper()
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}

	return &api.Deps{
		Store:  store.New(d),
		Auth:   auth.NewSessionStore(),
		Config: &config.Config{SecretKey: key, BaseURL: "http://localhost:8080"},
	}
}

func TestHandleToken_validPKCE(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(t.Context(), "u1", "alice@x.com", "Alice")

	verifier := "s256testverifier0000000000000000000000000000"
	challenge := auth.ComputeChallenge(verifier)
	code := deps.Auth.StoreAuthCode("u1", challenge)

	body := `{"code":"` + code + `","code_verifier":"` + verifier + `"}`
	req := httptest.NewRequest("POST", "/auth/token", strings.NewReader(body))
	rr := httptest.NewRecorder()

	api.BuildRouter(deps).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["access_token"])
	require.NotEmpty(t, resp["refresh_token"])
}

func TestHandleToken_badVerifier(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(t.Context(), "u1", "alice@x.com", "Alice")

	code := deps.Auth.StoreAuthCode("u1", "challenge123")
	body := `{"code":"` + code + `","code_verifier":"wrongverifier"}`
	req := httptest.NewRequest("POST", "/auth/token", strings.NewReader(body))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleRefresh(t *testing.T) {
	deps := newTestDeps(t)
	deps.Store.UpsertUser(t.Context(), "u1", "alice@x.com", "Alice")

	refreshToken, err := auth.IssueRefreshToken(deps.Config.SecretKey, "u1", 24*3600*1e9)
	require.NoError(t, err)

	body := `{"refresh_token":"` + refreshToken + `"}`
	req := httptest.NewRequest("POST", "/auth/refresh", strings.NewReader(body))
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestAutoPromoteAdmin_firstLogin(t *testing.T) {
	deps := newTestDeps(t)
	deps.Config.AdminEmail = "boss@x.com"

	ctx := t.Context()
	user, err := deps.Store.UpsertUser(ctx, "u1", "boss@x.com", "Boss")
	require.NoError(t, err)
	require.False(t, user.IsInstanceAdmin)

	if deps.Config.AdminEmail != "" && user.Email == deps.Config.AdminEmail && !user.IsInstanceAdmin {
		admins, err := deps.Store.ListInstanceAdmins(ctx)
		require.NoError(t, err)
		if len(admins) == 0 {
			require.NoError(t, deps.Store.SetInstanceAdmin(ctx, user.ID, true))
		}
	}

	promoted, err := deps.Store.GetUserByID(ctx, "u1")
	require.NoError(t, err)
	require.True(t, promoted.IsInstanceAdmin)
}

func TestAutoPromoteAdmin_notFirstAdmin(t *testing.T) {
	deps := newTestDeps(t)
	deps.Config.AdminEmail = "second@x.com"
	ctx := t.Context()

	deps.Store.UpsertUser(ctx, "existing", "existing@x.com", "Existing")
	deps.Store.SetInstanceAdmin(ctx, "existing", true)

	user, _ := deps.Store.UpsertUser(ctx, "u2", "second@x.com", "Second")
	if deps.Config.AdminEmail != "" && user.Email == deps.Config.AdminEmail && !user.IsInstanceAdmin {
		admins, _ := deps.Store.ListInstanceAdmins(ctx)
		if len(admins) == 0 {
			deps.Store.SetInstanceAdmin(ctx, user.ID, true)
		}
	}

	notPromoted, _ := deps.Store.GetUserByID(ctx, "u2")
	require.False(t, notPromoted.IsInstanceAdmin)
}
