package api

import (
	"context"
	"errors"

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
