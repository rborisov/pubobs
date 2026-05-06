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
		type repoResp struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			RemoteURL     string `json:"remote_url"`
			DefaultBranch string `json:"default_branch"`
			IsCloned      bool   `json:"is_cloned"`
			Role          string `json:"role"`
			AllowGuest    bool   `json:"allow_guest"`
		}
		out := make([]repoResp, len(repos))
		for i, repo := range repos {
			role := "admin"
			if !claims.IsAdmin {
				role, _ = deps.Store.GetUserRole(r.Context(), claims.UserID, repo.ID)
			}
			out[i] = repoResp{
				ID: repo.ID, Name: repo.Name, RemoteURL: repo.RemoteURL,
				DefaultBranch: repo.DefaultBranch, IsCloned: repo.LocalPath != nil,
				Role: role, AllowGuest: repo.AllowGuest,
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
