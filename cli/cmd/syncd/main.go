package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	opts := sync.AgentOptions{URL: url, Token: token, Workspace: workspace, Artifacts: artifacts, Env: envMode}
	delay := time.Second
	for {
		started := time.Now()
		err := sync.RunAgent(ctx, opts)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > 30*time.Second {
			delay = time.Second
		}
		log.Printf("sync: %v; reconnexion dans %s", err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 10*time.Second {
			delay *= 2
			if delay > 10*time.Second {
				delay = 10 * time.Second
			}
		}
	}
}
