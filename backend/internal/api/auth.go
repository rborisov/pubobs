package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/pubobs/backend/internal/auth"
)

func handlePluginAuth(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirectURI := q.Get("redirect_uri")
		codeChallenge := q.Get("code_challenge")
		pluginState := q.Get("state")
		if redirectURI == "" || codeChallenge == "" {
			writeError(w, http.StatusBadRequest, "redirect_uri and code_challenge are required")
			return
		}
		sessionID := deps.Auth.StoreSession(codeChallenge, redirectURI, pluginState)
		authURL := deps.OIDC.AuthCodeURL(sessionID)
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func handleAuthCallback(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		code := r.URL.Query().Get("code")

		sess, ok := deps.Auth.GetSession(state)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid or expired session")
			return
		}
		claims, err := deps.OIDC.ExchangeCode(r.Context(), code)
		if err != nil {
			writeError(w, http.StatusBadGateway, "OIDC exchange failed")
			return
		}
		user, err := deps.Store.UpsertUser(r.Context(), claims.Subject, claims.Email, claims.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "upsert user failed")
			return
		}
		authCode := deps.Auth.StoreAuthCode(user.ID, sess.CodeChallenge)
		redirectURL := fmt.Sprintf("%s?code=%s&state=%s", sess.RedirectURI, authCode, sess.PluginState)
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

func handleToken(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code         string `json:"code"`
			CodeVerifier string `json:"code_verifier"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		userID, err := deps.Auth.ConsumeAuthCode(body.Code, body.CodeVerifier)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid code or verifier")
			return
		}
		user, err := deps.Store.GetUserByID(r.Context(), userID)
		if err != nil || user == nil {
			writeError(w, http.StatusInternalServerError, "user not found")
			return
		}
		issueTokenPair(w, deps, user.ID, user.Email, user.IsInstanceAdmin)
	}
}

func handleRefresh(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		userID, err := auth.VerifyRefreshToken(deps.Config.SecretKey, body.RefreshToken)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid refresh token")
			return
		}
		user, err := deps.Store.GetUserByID(r.Context(), userID)
		if err != nil || user == nil {
			writeError(w, http.StatusUnauthorized, "user not found")
			return
		}
		issueTokenPair(w, deps, user.ID, user.Email, user.IsInstanceAdmin)
	}
}

func issueTokenPair(w http.ResponseWriter, deps *Deps, userID, email string, isAdmin bool) {
	access, err := auth.IssueAccessToken(deps.Config.SecretKey, userID, email, isAdmin, 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue token failed")
		return
	}
	refresh, err := auth.IssueRefreshToken(deps.Config.SecretKey, userID, 30*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "issue refresh failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"expires_in":    int(24 * time.Hour / time.Second),
	})
}
