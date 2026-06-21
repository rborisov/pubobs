package api

import (
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/renderstore"
	"github.com/pubobs/backend/internal/store"
)

// Deps holds all shared dependencies injected into API handlers.
type Deps struct {
	Store         *store.Store
	Cache         *gitcache.Cache
	Auth          *auth.SessionStore
	OIDCProviders []*auth.NamedProvider
	Config        *config.Config
	RenderStore   renderstore.RenderStore
}

// oidcProvider returns the named provider by ID, or nil if not found.
func (d *Deps) oidcProvider(id string) *auth.NamedProvider {
	for _, p := range d.OIDCProviders {
		if p.ID == id {
			return p
		}
	}
	return nil
}
