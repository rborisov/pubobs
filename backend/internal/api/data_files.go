package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/model"
)

// dataFileExtRE constrains an extension to something that can only ever be a
// file suffix. The values reach `git ls-files -- *.<ext>`, so anything with a
// path separator, a glob metacharacter or a leading dash is refused rather
// than passed through.
var dataFileExtRE = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

const maxDataFileExts = 20

// parseDataFileExts parses the `ext` query parameter — a comma-separated list
// of extensions, with or without leading dots, in any case. Returns an error
// rather than silently dropping bad entries: a typo'd extension that quietly
// syncs nothing is indistinguishable from a repo that has no such files.
func parseDataFileExts(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		e := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(part), "."))
		if e == "" {
			continue
		}
		if e == "md" {
			return nil, fmt.Errorf("md files sync as notes, not data files")
		}
		if !dataFileExtRE.MatchString(e) {
			return nil, fmt.Errorf("invalid extension %q", e)
		}
		if !seen[e] {
			out = append(out, e)
			seen[e] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ext parameter is required")
	}
	if len(out) > maxDataFileExts {
		return nil, fmt.Errorf("at most %d extensions", maxDataFileExts)
	}
	return out, nil
}

// handleListDataFiles returns the repo's non-note data files for the plugin's
// pull phase. Mirrors handleListFiles (reader role, same cache/credential
// path); the extension allowlist and size cap arrive as query parameters so
// they can be a plugin setting rather than server configuration.
func handleListDataFiles(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		repoID := chi.URLParam(r, "id")

		repo, err := deps.Store.GetRepo(r.Context(), repoID)
		if err != nil || repo == nil {
			writeError(w, http.StatusNotFound, "repo not found")
			return
		}
		role, _ := deps.Store.GetUserRole(r.Context(), claims.UserID, repoID)
		if !claims.IsAdmin && !model.RoleAtLeast(role, "reader") {
			writeError(w, http.StatusForbidden, "reader role required")
			return
		}

		exts, err := parseDataFileExts(r.URL.Query().Get("ext"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// An unparseable or absent max_bytes means "no client preference";
		// ListDataFiles clamps 0 to the server ceiling.
		maxBytes, _ := strconv.ParseInt(r.URL.Query().Get("max_bytes"), 10, 64)

		credJSON, err := decryptCreds(deps, repo.EncryptedCreds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cred decrypt failed")
			return
		}

		list, err := deps.Cache.ListDataFiles(r.Context(), repo, credJSON, exts, maxBytes)
		if err != nil {
			// Redacted: this error embeds git's raw stderr, and git quotes the
			// credentialed remote URL it was handed on any clone/fetch failure
			// ("unable to access 'https://user:token@host/...'"). This endpoint
			// only requires the reader role — deliberately lower-privileged
			// than the owner who supplied that credential.
			writeError(w, http.StatusBadGateway, "list data files failed: "+gitcache.Redact(err.Error()))
			return
		}
		deps.Store.TouchLastUsedAt(r.Context(), repoID)
		writeJSON(w, http.StatusOK, list)
	}
}
