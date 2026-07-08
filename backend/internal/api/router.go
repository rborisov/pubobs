package api

import (
	"compress/gzip"
	"fmt"
	"net/http"
	"strings"

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

// maintenanceMode blocks non-essential routes while an in-app self-update
// (deps.Update) is applying — the container is about to be rebuilt and
// restarted out from under the current process, so serving normal traffic
// during that window would just produce confusing partial failures. Update
// endpoints, auth, and the health check stay reachable throughout (see
// UpdateManager.MaintenanceActive) so the admin UI can keep polling status.
// deps.Update is nil in most existing tests, which construct Deps directly.
func maintenanceMode(deps *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if deps.Update != nil && deps.Update.MaintenanceActive(r.URL.Path) {
				w.Header().Set("Retry-After", "60")
				if r.URL.Path == "/" || (!strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/pub/")) {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(http.StatusServiceUnavailable)
					fmt.Fprint(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>PubObs Maintenance</title><style>body{margin:0;font-family:system-ui,sans-serif;background:#f5f7fb;color:#1f2937;display:grid;min-height:100vh;place-items:center;padding:24px}.panel{max-width:420px;background:#fff;border:1px solid #dbe3ef;border-radius:16px;padding:28px;box-shadow:0 12px 32px rgba(15,23,42,.08)}h1{margin:0 0 12px;font-size:1.4rem}p{margin:0;color:#526070;line-height:1.6}</style></head><body><div class="panel"><h1>PubObs is updating</h1><p>The site is temporarily in maintenance mode while a new version is being applied. Please try again in a minute.</p></div></body></html>`)
					return
				}
				writeError(w, http.StatusServiceUnavailable, "PubObs is updating. Please try again shortly.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func BuildRouter(deps *Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(maintenanceMode(deps))
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
		r.Get("/api/admin/update/check", handleAdminUpdateCheck(deps))
		r.Post("/api/admin/update/start", handleAdminUpdateStart(deps))
		r.Get("/api/admin/update/status", handleAdminUpdateStatus(deps))
	})

	// Public reader (no auth)
	r.Get("/pub/{repoId}", handlePubListNotes(deps))
	r.Get("/pub/{repoId}/notes/*", handlePubGetNote(deps))
	r.Get("/pub/{repoId}/assets/*", handlePubGetAsset(deps))
	r.Get("/pub/{repoId}/render/*", handlePubGetRender(deps))

	r.Handle("/*", noCacheFS(http.FileServer(http.FS(frontend.FS()))))

	return r
}
