// backend/internal/api/admin_storage.go
package api

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/renderstore"
)

var errValidationMismatch = errors.New("validation object mismatch")

// S3ValidateFunc performs the S3 round-trip validation used by the storage
// admin handlers. It's a variable (rather than a plain function call) so tests
// can substitute a fake round-trip and assert on the effective settings that
// reached validation, without needing a live S3-compatible endpoint.
var S3ValidateFunc = validateS3Settings

// validateS3Settings does a write+read+delete round-trip against the
// candidate settings before they're persisted or swapped in, so a bad
// bucket/credential is caught immediately rather than silently breaking
// reads/writes after the swap. Callers must pass the merged/effective
// settings (e.g. current after applying the request body), not the raw
// request body, so that a blank s3_secret_key ("keep existing") validates
// against the real preserved secret rather than an empty string.
func validateS3Settings(s *model.StorageSettings) error {
	testStore, err := renderstore.NewS3(
		s.S3Endpoint, s.S3Bucket, s.S3AccessKey, s.S3SecretKey,
		s.S3Region, s.S3UseSSL, "pubobs-validate/",
	)
	if err != nil {
		return err
	}
	const testKey = "connectivity-check"
	payload := []byte("pubobs storage settings validation")
	if err := testStore.Write("_validate", testKey, payload); err != nil {
		return err
	}
	got, err := testStore.Read("_validate", testKey)
	if err != nil {
		return err
	}
	if string(got) != string(payload) {
		return errValidationMismatch
	}
	return testStore.Delete("_validate", testKey)
}

func dirSizeBytes(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
