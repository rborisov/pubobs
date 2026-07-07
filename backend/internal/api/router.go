package api

import (
	"compress/gzip"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/pubobs/backend/frontend"
	"github.com/pubobs/backend/internal/auth"
)

func noCacheFS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		next.ServeHTTP(w, r)
	})
}

// decompressRequestBody transparently gunzips request bodies sent with
// Content-Encoding: gzip (e.g. the Obsidian plugin's sync payload, which can
// run tens of MB uncompressed) before they reach any handler.
func decompressRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid gzip body")
				return
			}
			defer gz.Close()
			r.Body = gz
		}
		next.ServeHTTP(w, r)
	})
}

func BuildRouter(deps *Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(decompressRequestBody)

	// Auth (unauthenticated)
	r.Get("/auth/providers", handleListProviders(deps))
	r.Get("/auth/plugin", handlePluginAuth(deps))
	r.Get("/auth/callback", handleAuthCallback(deps))
	r.Post("/auth/token", handleToken(deps))
	r.Post("/auth/refresh", handleRefresh(deps))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth(deps.Config.SecretKey))

		r.Get("/api/me", handleMe(deps))
		r.Get("/api/me/folder-mappings", handleListFolderMappings(deps))
		r.Put("/api/me/folder-mappings/{repoID}", handleUpsertFolderMapping(deps))
		r.Get("/api/repos", handleListRepos(deps))

		r.Post("/api/repos/{id}/sync", handleSync(deps))
		r.Get("/api/repos/{id}/files", handleListFiles(deps))
		r.Get("/api/repos/{id}/notes", handleListNotes(deps))
		r.Get("/api/repos/{id}/notes/*", handleNoteGet(deps))
		r.Post("/api/repos/{id}/notes/*", handleNotePost(deps))

		// Admin (instance_admin only)
		r.Get("/api/admin/health", handleAdminHealth(deps))
		r.Post("/api/admin/repos", handleAdminCreateRepo(deps))
		r.Put("/api/admin/repos/{id}", handleAdminUpdateRepo(deps))
		r.Delete("/api/admin/repos/{id}", handleAdminDeleteRepo(deps))
		r.Put("/api/admin/repos/{id}/guest-access", handleAdminSetRepoGuestAccess(deps))
		r.Post("/api/admin/repos/{id}/import", handleAdminImportRepo(deps))
		r.Get("/api/admin/repos/{id}/access", handleAdminListRepoAccess(deps))
		r.Post("/api/admin/repos/{id}/access", handleAdminGrantAccess(deps))
		r.Delete("/api/admin/repos/{id}/access/{accessID}", handleAdminRevokeAccess(deps))
		r.Get("/api/admin/users", handleAdminListUsers(deps))
		r.Post("/api/admin/users/{id}/admin", handleAdminSetAdmin(deps))
		r.Post("/api/admin/users/{id}/ban", handleAdminSetBan(deps))
		r.Post("/api/admin/users/{id}/user-admin", handleAdminSetUserAdmin(deps))
		r.Get("/api/admin/allowlist", handleAdminListAllowlist(deps))
		r.Post("/api/admin/allowlist", handleAdminAddAllowlistEntry(deps))
		r.Delete("/api/admin/allowlist/{id}", handleAdminRemoveAllowlistEntry(deps))
		r.Post("/api/admin/groups", handleAdminCreateGroup(deps))
		r.Get("/api/admin/groups", handleAdminListGroups(deps))
		r.Delete("/api/admin/groups/{id}", handleAdminDeleteGroup(deps))
		r.Post("/api/admin/groups/{id}/members", handleAdminAddGroupMember(deps))
		r.Get("/api/admin/groups/{id}/members", handleAdminListGroupMembers(deps))
		r.Delete("/api/admin/groups/{id}/members/{userID}", handleAdminRemoveGroupMember(deps))
		r.Put("/api/admin/groups/{id}/members/{userID}/role", handleAdminSetGroupMemberRole(deps))
		r.Get("/api/admin/storage-destinations", handleAdminListDestinations(deps))
		r.Post("/api/admin/storage-destinations", handleAdminCreateDestination(deps))
		r.Put("/api/admin/storage-destinations/{id}", handleAdminUpdateDestination(deps))
		r.Delete("/api/admin/storage-destinations/{id}", handleAdminDeleteDestination(deps))
		r.Put("/api/admin/repos/{id}/storage", handleAdminAssignRepoStorage(deps))
		r.Get("/api/admin/storage-usage", handleAdminStorageUsage(deps))
	})

	// Public reader (no auth)
	r.Get("/pub/{repoId}", handlePubListNotes(deps))
	r.Get("/pub/{repoId}/notes/*", handlePubGetNote(deps))
	r.Get("/pub/{repoId}/assets/*", handlePubGetAsset(deps))
	r.Get("/pub/{repoId}/render/*", handlePubGetRender(deps))

	r.Handle("/*", noCacheFS(http.FileServer(http.FS(frontend.FS()))))

	return r
}
