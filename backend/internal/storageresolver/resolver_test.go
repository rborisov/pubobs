// backend/internal/storageresolver/resolver_test.go
package storageresolver_test

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/model"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/pubobs/backend/internal/storageresolver"
	"github.com/pubobs/backend/internal/store"
	"github.com/stretchr/testify/require"
)

func newKey(t *testing.T) []byte {
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func TestResolver_localWhenNoDestination(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	st := store.New(d)
	ctx := context.Background()
	_, err = st.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	r, err := storageresolver.New(st, t.TempDir(), t.TempDir(), newKey(t))
	require.NoError(t, err)

	rs, err := r.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	_, isLocal := rs.(*renderstore.LocalRenderStore)
	require.True(t, isLocal, "repo with no destination resolves to the local store")

	as, err := r.AssetStoreFor(ctx, "r1")
	require.NoError(t, err)
	_, isEnc := as.(*renderstore.EncryptingStore)
	require.True(t, isEnc, "asset store is always EncryptingStore-wrapped")
}

func TestResolver_usesDestinationAfterRebuild(t *testing.T) {
	d, err := db.Open(":memory:")
	require.NoError(t, err)
	defer d.Close()
	st := store.New(d)
	ctx := context.Background()
	_, err = st.CreateRepo(ctx, "r1", "R1", "https://x/r1.git", "", "main")
	require.NoError(t, err)

	r, err := storageresolver.New(st, t.TempDir(), t.TempDir(), newKey(t))
	require.NoError(t, err)

	// Add a destination + assign the repo, then rebuild.
	require.NoError(t, st.CreateStorageDestination(ctx, &model.StorageDestination{
		ID: "d1", Name: "arch", S3Endpoint: "localhost:9000", S3Bucket: "b",
		S3AccessKey: "ak", S3SecretKey: "sk", S3Region: "us-east-1", S3UseSSL: false,
	}))
	destID := "d1"
	require.NoError(t, st.SetRepoStorageDestination(ctx, "r1", &destID))
	require.NoError(t, r.Rebuild(ctx))

	rs, err := r.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	_, isS3 := rs.(*renderstore.S3RenderStore)
	require.True(t, isS3, "repo assigned to an S3 destination resolves to an S3 store")
}
