package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"zeta/cli/internal/contexttar"
)

type AgentOptions struct {
	URL       string
	Token     string
	Workspace string
	Artifacts string
	Ignore    []string
	Env       bool
}

func RunAgent(ctx context.Context, opts AgentOptions) error {
	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	conn, err := DialAuth(dialCtx, opts.URL, "", opts.Token)
	cancelDial()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Send(Message{Type: "hello", Version: 2}); err != nil {
		return err
	}

	baseExcludes := contexttar.DefaultExcludes()
	if opts.Env {
		baseExcludes = contexttar.EnvExcludes()
	}
	workspaceIgnore := append(baseExcludes, opts.Ignore...)
	workspace := NewSyncer(opts.Workspace, ScopeWorkspace, workspaceIgnore, conn)
	defer workspace.Close()
	var artifacts *Syncer
	if opts.Artifacts != "" {
		artifacts = NewSyncer(opts.Artifacts, ScopeArtifacts, nil, conn)
		defer artifacts.Close()
	}

	msgs := make(chan Message)
	errs := make(chan error, 1)
	go func() {
		for {
			msg, err := conn.Receive()
			if err != nil {
				errs <- err
				return
			}
			msgs <- msg
		}
	}()

	ready := false
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		case msg := <-msgs:
			switch msg.Type {
			case "hello":
				if msg.Version >= 2 {
					workspace.Chunked = true
					if artifacts != nil {
						artifacts.Chunked = true
					}
				}
			case "file", "file-chunk", "dir", "symlink", "delete":
				if msg.Scope == ScopeWorkspace {
					if err := workspace.Apply(msg); err != nil {
						_ = conn.Send(Message{Type: "error", Error: err.Error()})
					}
				}
			case "sync-done":
				if msg.Scope != ScopeWorkspace {
					continue
				}
				if err := workspace.SetStateFromDisk(); err != nil {
					return err
				}
				marker := filepath.Join(opts.Workspace, ".zeta-ready")
				if err := os.WriteFile(marker, []byte("ready\n"), 0o644); err != nil {
					return fmt.Errorf("marqueur ready: %w", err)
				}
				ready = true
				if err := conn.Send(Message{Type: "ready"}); err != nil {
					return err
				}
			case "flush":
				if ready {
					if err := workspace.SendChanges(); err != nil {
						return err
					}
					if artifacts != nil {
						if err := artifacts.SendChanges(); err != nil {
							return err
						}
					}
				}
				if err := conn.Send(Message{Type: "flushed"}); err != nil {
					return err
				}
			case "bye":
				return nil
			}
		case <-ticker.C:
			if !ready {
				continue
			}
			if err := workspace.SendChanges(); err != nil {
				return err
			}
			if artifacts != nil {
				if err := artifacts.SendChanges(); err != nil {
					return err
				}
			}
		}
	}
}
