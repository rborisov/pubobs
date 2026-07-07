// backend/internal/storageresolver/resolver.go
package storageresolver

import (
	"context"
	"fmt"
	"sync"

	"github.com/pubobs/backend/internal/renderstore"
	"github.com/pubobs/backend/internal/store"
)

// storeSet is the render + asset stores for one destination (or local).
// assetPlain is the un-encrypted base; asset is the EncryptingStore wrapping it.
type storeSet struct {
	render     renderstore.RenderStore
	assetPlain renderstore.RenderStore
	asset      renderstore.RenderStore
}

// Resolver returns the right render/asset store for a repo based on its
// assigned storage destination (nil = local). It rebuilds its per-destination
// map on demand when destinations change — no process restart.
type Resolver struct {
	st             *store.Store
	localRenderDir string
	localAssetDir  string
	assetKey       []byte

	mu    sync.RWMutex
	local storeSet
	byID  map[string]storeSet // destination id → store-set
}

func New(st *store.Store, localRenderDir, localAssetDir string, assetKey []byte) (*Resolver, error) {
	if len(assetKey) != 32 {
		return nil, fmt.Errorf("asset key must be 32 bytes")
	}
	r := &Resolver{
		st:             st,
		localRenderDir: localRenderDir,
		localAssetDir:  localAssetDir,
		assetKey:       assetKey,
	}
	local, err := r.buildSet("local", "", "", "", "", "", false)
	if err != nil {
		return nil, err
	}
	r.local = local
	if err := r.Rebuild(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}

// buildSet constructs a store-set. storeType "local" uses the local dirs;
// otherwise it's an S3 destination with the given config.
func (r *Resolver) buildSet(storeType, endpoint, bucket, accessKey, secretKey, region string, useSSL bool) (storeSet, error) {
	render, err := renderstore.New(storeType, r.localRenderDir, endpoint, bucket, accessKey, secretKey, region, useSSL, "renders/")
	if err != nil {
		return storeSet{}, err
	}
	assetPlain, err := renderstore.New(storeType, r.localAssetDir, endpoint, bucket, accessKey, secretKey, region, useSSL, "assets/")
	if err != nil {
		return storeSet{}, err
	}
	asset, err := renderstore.NewEncryptingStore(assetPlain, r.assetKey)
	if err != nil {
		return storeSet{}, err
	}
	return storeSet{render: render, assetPlain: assetPlain, asset: asset}, nil
}

// Rebuild re-reads all destinations from the DB and rebuilds the per-id map.
func (r *Resolver) Rebuild(ctx context.Context) error {
	dests, err := r.st.ListStorageDestinations(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]storeSet, len(dests))
	for _, d := range dests {
		set, err := r.buildSet("s3", d.S3Endpoint, d.S3Bucket, d.S3AccessKey, d.S3SecretKey, d.S3Region, d.S3UseSSL)
		if err != nil {
			return fmt.Errorf("build store-set for destination %s: %w", d.ID, err)
		}
		next[d.ID] = set
	}
	r.mu.Lock()
	r.byID = next
	r.mu.Unlock()
	return nil
}

func (r *Resolver) setForRepo(ctx context.Context, repoID string) (storeSet, error) {
	repo, err := r.st.GetRepo(ctx, repoID)
	if err != nil {
		return storeSet{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if repo.StorageDestinationID == nil {
		return r.local, nil
	}
	set, ok := r.byID[*repo.StorageDestinationID]
	if !ok {
		// Assigned destination no longer exists in the map — fall back to
		// local rather than fail hard; a Rebuild should reconcile this.
		return r.local, nil
	}
	return set, nil
}

func (r *Resolver) RenderStoreFor(ctx context.Context, repoID string) (renderstore.RenderStore, error) {
	set, err := r.setForRepo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return set.render, nil
}

func (r *Resolver) AssetStoreFor(ctx context.Context, repoID string) (renderstore.RenderStore, error) {
	set, err := r.setForRepo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return set.asset, nil
}

func (r *Resolver) PlainAssetStoreFor(ctx context.Context, repoID string) (renderstore.RenderStore, error) {
	set, err := r.setForRepo(ctx, repoID)
	if err != nil {
		return nil, err
	}
	return set.assetPlain, nil
}

func (r *Resolver) LocalRenderStore() renderstore.RenderStore     { return r.local.render }
func (r *Resolver) LocalAssetStorePlain() renderstore.RenderStore { return r.local.assetPlain }

// DestinationRenderStore returns the render store for a destination id, and
// whether it exists in the current map.
func (r *Resolver) DestinationRenderStore(destID string) (renderstore.RenderStore, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	set, ok := r.byID[destID]
	if !ok {
		return nil, false
	}
	return set.render, true
}

// StoresForDestination returns the render + plain-asset stores for a
// destination id (or the local set when destID is nil). Used by per-repo
// migration to build source/dest pairs. Plain (non-encrypting) asset store is
// returned so asset ciphertext copies verbatim.
func (r *Resolver) StoresForDestination(destID *string) (render, assetPlain renderstore.RenderStore, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if destID == nil {
		return r.local.render, r.local.assetPlain, true
	}
	set, found := r.byID[*destID]
	if !found {
		return nil, nil, false
	}
	return set.render, set.assetPlain, true
}
