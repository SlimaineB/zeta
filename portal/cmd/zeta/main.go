package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zeta/portal/internal/auth"
	"zeta/portal/internal/builds"
	"zeta/portal/internal/config"
	"zeta/portal/internal/httpapi"
	"zeta/portal/internal/kube"
	synchub "zeta/portal/internal/sync"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	authService, err := auth.New(ctx, cfg.Auth)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	registry, err := builds.LoadProfiles(cfg.ProfilesFile)
	if err != nil {
		log.Fatalf("profils: %v", err)
	}
	repo, err := kube.NewRepository(cfg.Namespace, cfg.KubeConfig, cfg.MasterURL, cfg.StorageClass, cfg.VolumeQuotaBytes, cfg.VolumeMaxBytes)
	if err != nil {
		log.Fatalf("kubernetes: %v", err)
	}
	if err := repo.EnsurePVCs(ctx); err != nil {
		log.Printf("warning: PVCs not initialized: %v", err)
	}
	buildService := builds.NewService(repo, registry)
	api := httpapi.New(cfg, buildService, authService, synchub.NewHub())
	go api.RunIdleReaper(ctx, cfg.EnvIdleTimeout)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("zeta portal listening on %s (namespace=%s, oidc=%v, dev=%v)", cfg.Addr, cfg.Namespace, authService.IsOIDC(), authService.IsDev())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
