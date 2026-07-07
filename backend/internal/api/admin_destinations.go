// backend/internal/api/admin_destinations.go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/model"
)

type destinationResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	S3Endpoint  string `json:"s3_endpoint"`
	S3Bucket    string `json:"s3_bucket"`
	S3AccessKey string `json:"s3_access_key"`
	S3Region    string `json:"s3_region"`
	S3UseSSL    bool   `json:"s3_use_ssl"`
}

func toDestinationResponse(d *model.StorageDestination) destinationResponse {
	return destinationResponse{
		ID: d.ID, Name: d.Name, S3Endpoint: d.S3Endpoint, S3Bucket: d.S3Bucket,
		S3AccessKey: d.S3AccessKey, S3Region: d.S3Region, S3UseSSL: d.S3UseSSL,
	}
}

type destinationBody struct {
	Name        string `json:"name"`
	S3Endpoint  string `json:"s3_endpoint"`
	S3Bucket    string `json:"s3_bucket"`
	S3AccessKey string `json:"s3_access_key"`
	S3SecretKey string `json:"s3_secret_key"`
	S3Region    string `json:"s3_region"`
	S3UseSSL    bool   `json:"s3_use_ssl"`
}

// validateBody runs the S3 round-trip on a candidate destination by adapting
// it to the model.StorageSettings shape S3ValidateFunc expects.
func validateDestinationBody(b destinationBody) error {
	return S3ValidateFunc(&model.StorageSettings{
		StoreType: "s3", S3Endpoint: b.S3Endpoint, S3Bucket: b.S3Bucket,
		S3AccessKey: b.S3AccessKey, S3SecretKey: b.S3SecretKey,
		S3Region: b.S3Region, S3UseSSL: b.S3UseSSL,
	})
}

func handleAdminListDestinations(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		dests, err := deps.Store.ListStorageDestinations(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list destinations failed")
			return
		}
		out := make([]destinationResponse, 0, len(dests))
		for _, d := range dests {
			out = append(out, toDestinationResponse(d))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleAdminCreateDestination(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		var b destinationBody
		if err := readJSON(r, &b); err != nil || b.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		if err := validateDestinationBody(b); err != nil {
			writeError(w, http.StatusBadRequest, "S3 settings invalid: "+err.Error())
			return
		}
		d := &model.StorageDestination{
			ID: uuid.NewString(), Name: b.Name, S3Endpoint: b.S3Endpoint, S3Bucket: b.S3Bucket,
			S3AccessKey: b.S3AccessKey, S3SecretKey: b.S3SecretKey, S3Region: b.S3Region, S3UseSSL: b.S3UseSSL,
		}
		if err := deps.Store.CreateStorageDestination(r.Context(), d); err != nil {
			writeError(w, http.StatusInternalServerError, "create destination failed")
			return
		}
		if err := deps.Resolver.Rebuild(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "rebuild resolver failed")
			return
		}
		writeJSON(w, http.StatusCreated, toDestinationResponse(d))
	}
}

func handleAdminUpdateDestination(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		existing, err := deps.Store.GetStorageDestination(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		var b destinationBody
		if err := readJSON(r, &b); err != nil || b.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		existing.Name = b.Name
		existing.S3Endpoint = b.S3Endpoint
		existing.S3Bucket = b.S3Bucket
		existing.S3AccessKey = b.S3AccessKey
		if b.S3SecretKey != "" {
			existing.S3SecretKey = b.S3SecretKey // blank = keep existing
		}
		existing.S3Region = b.S3Region
		existing.S3UseSSL = b.S3UseSSL
		// Validate the merged candidate (with the effective secret).
		if err := S3ValidateFunc(&model.StorageSettings{
			StoreType: "s3", S3Endpoint: existing.S3Endpoint, S3Bucket: existing.S3Bucket,
			S3AccessKey: existing.S3AccessKey, S3SecretKey: existing.S3SecretKey,
			S3Region: existing.S3Region, S3UseSSL: existing.S3UseSSL,
		}); err != nil {
			writeError(w, http.StatusBadRequest, "S3 settings invalid: "+err.Error())
			return
		}
		if err := deps.Store.UpdateStorageDestination(r.Context(), existing); err != nil {
			writeError(w, http.StatusInternalServerError, "update destination failed")
			return
		}
		if err := deps.Resolver.Rebuild(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "rebuild resolver failed")
			return
		}
		writeJSON(w, http.StatusOK, toDestinationResponse(existing))
	}
}

func handleAdminDeleteDestination(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.ClaimsFromContext(r.Context())
		if !requireAdmin(claims, w) {
			return
		}
		id := chi.URLParam(r, "id")
		count, err := deps.Store.CountReposUsingDestination(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "check destination usage failed")
			return
		}
		if count > 0 {
			writeError(w, http.StatusConflict, "destination in use — reassign its repos first")
			return
		}
		if err := deps.Store.DeleteStorageDestination(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "delete destination failed")
			return
		}
		if err := deps.Resolver.Rebuild(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "rebuild resolver failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
