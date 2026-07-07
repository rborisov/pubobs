package api

import (
	"net/http"

	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

func handleListRepos(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		var repos []*model.Repo
		var err error
		if claims.IsAdmin {
			repos, err = deps.Store.ListRepos(r.Context())
		} else {
			repos, err = deps.Store.ListUserRepos(r.Context(), claims.UserID)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list repos failed")
			return
		}

		// Resolve roles for all repos in one batched query instead of one
		// round trip per repo — with many repos this was the slow part of
		// loading a member's dashboard on the single shared SQLite connection.
		var roles map[string]string
		if !claims.IsAdmin {
			repoIDs := make([]string, len(repos))
			for i, repo := range repos {
				repoIDs[i] = repo.ID
			}
			roles, err = deps.Store.GetUserRolesForRepos(r.Context(), claims.UserID, repoIDs)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "list repos failed")
				return
			}
		}

		type repoResp struct {
			ID                   string  `json:"id"`
			Name                 string  `json:"name"`
			RemoteURL            string  `json:"remote_url"`
			DefaultBranch        string  `json:"default_branch"`
			IsCloned             bool    `json:"is_cloned"`
			Role                 string  `json:"role"`
			AllowGuest           bool    `json:"allow_guest"`
			StorageDestinationID *string `json:"storage_destination_id"`
			MigrationStatus      string  `json:"migration_status"`
		}
		out := make([]repoResp, len(repos))
		for i, repo := range repos {
			role := "admin"
			if !claims.IsAdmin {
				role = roles[repo.ID]
			}
			out[i] = repoResp{
				ID: repo.ID, Name: repo.Name, RemoteURL: repo.RemoteURL,
				DefaultBranch: repo.DefaultBranch, IsCloned: repo.LocalPath != nil,
				Role: role, AllowGuest: repo.AllowGuest,
				StorageDestinationID: repo.StorageDestinationID, MigrationStatus: repo.MigrationStatus,
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
