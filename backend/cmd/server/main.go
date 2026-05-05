package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pubobs/backend/internal/api"
	"github.com/pubobs/backend/internal/auth"
	"github.com/pubobs/backend/internal/config"
	"github.com/pubobs/backend/internal/db"
	"github.com/pubobs/backend/internal/gitcache"
	"github.com/pubobs/backend/internal/jobs"
	"github.com/pubobs/backend/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll(cfg.RepoCacheDir, 0755); err != nil {
		log.Fatalf("create cache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0755); err != nil {
		log.Fatalf("create db dir: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	oidcClient, err := auth.NewOIDCClient(ctx, cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.BaseURL)
	if err != nil {
		log.Fatalf("init OIDC: %v", err)
	}
	providers := []*auth.NamedProvider{
		{ID: "oidc", Name: "Sign in with OIDC", Client: oidcClient},
	}
	if cfg.YandexClientID != "" && cfg.YandexClientSecret != "" {
		yandex := auth.NewYandexClient(cfg.YandexClientID, cfg.YandexClientSecret, cfg.BaseURL)
		providers = append(providers, &auth.NamedProvider{ID: "yandex", Name: "Sign in with Yandex", Client: yandex})
	}

	deps := &api.Deps{
		Store:         store.New(database),
		Cache:         gitcache.NewCache(cfg.RepoCacheDir),
		Auth:          auth.NewSessionStore(),
		OIDCProviders: providers,
		Config:        cfg,
	}

	jobs.StartEvictionJob(ctx, deps.Store, deps.Cache, cfg)

	router := api.BuildRouter(deps)
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)
		cancel()
	}()

	log.Printf("PubObs backend listening on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
