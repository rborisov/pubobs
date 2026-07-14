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
			OwnerUserID          *string `json:"owner_user_id"`
			OwnerEmail           *string `json:"owner_email"`
			StrictCredentials    bool    `json:"strict_credentials"`
		}
		// Resolve owner emails once per distinct owner for the list's Owner
		// column, so the UI needn't load the full user directory to show them.
		ownerEmails := map[string]string{}
		for _, repo := range repos {
			if repo.OwnerUserID == nil {
				continue
			}
			if _, ok := ownerEmails[*repo.OwnerUserID]; ok {
				continue
			}
			if u, uerr := deps.Store.GetUserByID(r.Context(), *repo.OwnerUserID); uerr == nil && u != nil {
				ownerEmails[*repo.OwnerUserID] = u.Email
			}
		}
		out := make([]repoResp, len(repos))
		for i, repo := range repos {
			var ownerEmail *string
			if repo.OwnerUserID != nil {
				if e, ok := ownerEmails[*repo.OwnerUserID]; ok {
					ownerEmail = &e
				}
			}
			role := "admin"
			if !claims.IsAdmin {
				role = roles[repo.ID]
			}
			// IsCloned reflects whether this node currently has a local git
			// checkout for the repo (checked live via deps.Cache), not the
			// repos.local_path DB column: that column is never written by the
			// normal sync/browse/import paths (gitcache.Cache clones lazily
			// straight to disk), so it would always read as "not cloned" here.
			isCloned := false
			if deps.Cache != nil {
				isCloned = deps.Cache.IsCloned(repo.ID)
			}
			out[i] = repoResp{
				ID: repo.ID, Name: repo.Name, RemoteURL: repo.RemoteURL,
				DefaultBranch: repo.DefaultBranch, IsCloned: isCloned,
				Role: role, AllowGuest: repo.AllowGuest,
				StorageDestinationID: repo.StorageDestinationID, MigrationStatus: repo.MigrationStatus,
				OwnerUserID: repo.OwnerUserID, OwnerEmail: ownerEmail,
				StrictCredentials: repo.StrictCredentials,
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
