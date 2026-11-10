package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"zeta/cli/internal/sync"
)

func main() {
	url := os.Getenv("ZETA_SYNC_URL")
	token := os.Getenv("ZETA_SYNC_TOKEN")
	workspace := os.Getenv("ZETA_WORKSPACE")
	artifacts := os.Getenv("ZETA_ARTIFACTS")
	if url == "" || workspace == "" {
		log.Fatal("ZETA_SYNC_URL and ZETA_WORKSPACE are required")
	}
	envMode := os.Getenv("ZETA_SYNC_ENV") == "1"
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := sync.RunAgent(ctx, sync.AgentOptions{URL: url, Token: token, Workspace: workspace, Artifacts: artifacts, Env: envMode}); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
