package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func requireRepoRole(ctx context.Context, deps *Deps, claims *auth.AccessClaims, repoID, required string) error {
	if claims.IsAdmin {
		return nil
	}
	role, err := deps.Store.GetUserRole(ctx, claims.UserID, repoID)
	if err != nil {
		return errors.New("role check failed")
	}
	if !model.RoleAtLeast(role, required) {
		return errors.New(required + " role required")
	}
	return nil
}

func requireAnyAdmin(claims *auth.AccessClaims, w http.ResponseWriter) bool {
	if claims.IsAdmin || claims.IsUserAdmin {
		return true
	}
	writeError(w, http.StatusForbidden, "admin required")
	return false
}

func requireRepoManage(ctx context.Context, deps *Deps, claims *auth.AccessClaims, repoID string, w http.ResponseWriter) bool {
	if claims.IsAdmin {
		return true
	}
	if !claims.IsUserAdmin {
		writeError(w, http.StatusForbidden, "admin required")
		return false
	}
	role, err := deps.Store.GetUserRole(ctx, claims.UserID, repoID)
	if err != nil || role != "admin" {
		writeError(w, http.StatusForbidden, "admin repo role required")
		return false
	}
	return true
}

func requireGroupAdmin(ctx context.Context, deps *Deps, claims *auth.AccessClaims, groupID string, w http.ResponseWriter) bool {
	if claims.IsAdmin {
		return true
	}
	if !claims.IsUserAdmin {
		writeError(w, http.StatusForbidden, "admin required")
		return false
	}
	ok, err := deps.Store.IsGroupAdmin(ctx, groupID, claims.UserID)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "group admin required")
		return false
	}
	return true
}
