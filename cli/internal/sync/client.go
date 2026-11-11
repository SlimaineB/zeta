package sync

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

type ClientOptions struct {
	URL            string
	Cookie         string
	Workspace      string
	Artifacts      string
	Ignore         []string
	ArtifactIgnore []string
}

type Session struct {
	conn      Transport
	workspace *Syncer
	artifacts *Syncer

	readyOnce   sync.Once
	flushedOnce sync.Once
	helloOnce   sync.Once
	ready       chan struct{}
	flushed     chan struct{}
	hello       chan struct{}
	errs        chan error
	cancel      context.CancelFunc
	closeOnce   sync.Once
}

func StartClient(ctx context.Context, opts ClientOptions) (*Session, error) {
	conn, err := Dial(ctx, opts.URL, opts.Cookie)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.Artifacts, 0o755); err != nil {
		_ = conn.Close()
		return nil, err
	}
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{
		conn:      conn,
		workspace: NewSyncer(opts.Workspace, ScopeWorkspace, opts.Ignore, conn),
		artifacts: NewSyncer(opts.Artifacts, ScopeArtifacts, opts.ArtifactIgnore, conn),
		ready:     make(chan struct{}),
		flushed:   make(chan struct{}),
		hello:     make(chan struct{}),
		errs:      make(chan error, 1),
		cancel:    cancel,
	}
	go s.receiveLoop()
	if err := s.conn.Send(Message{Type: "hello", Version: 2}); err != nil {
		cancel()
		_ = conn.Close()
		return nil, err
	}
	helloCtx, cancelHello := context.WithTimeout(ctx, 20*time.Second)
	select {
	case <-helloCtx.Done():
		cancelHello()
		cancel()
		_ = conn.Close()
		if err := parent.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("agent de synchronisation indisponible ou obsolète; recréez l'environnement")
	case err := <-s.errs:
		cancelHello()
		cancel()
		_ = conn.Close()
		return nil, err
	case <-s.hello:
		cancelHello()
	}
	s.workspace.Chunked = true
	s.artifacts.Chunked = true
	if err := s.workspace.SendInitial(); err != nil {
		cancel()
		_ = conn.Close()
		return nil, err
	}
	go s.tick(ctx)
	return s, nil
}

func (s *Session) receiveLoop() {
	for {
		msg, err := s.conn.Receive()
		if err != nil {
			s.fail(err)
			return
		}
		switch msg.Type {
		case "hello":
			if msg.Version >= 2 {
				s.helloOnce.Do(func() { close(s.hello) })
				_ = s.conn.Send(Message{Type: "hello", Version: 2})
			}
		case "file", "file-chunk", "dir", "symlink", "delete":
			switch msg.Scope {
			case ScopeWorkspace:
				if err := s.workspace.Apply(msg); err != nil {
					s.fail(err)
					return
				}
			case ScopeArtifacts:
				if err := s.artifacts.Apply(msg); err != nil {
					s.fail(err)
					return
				}
			}
		case "ready":
			s.readyOnce.Do(func() { close(s.ready) })
		case "flushed":
			s.flushedOnce.Do(func() { close(s.flushed) })
		case "error":
			fmt.Fprintf(os.Stderr, "sync: agent: %s\n", msg.Error)
			s.fail(fmt.Errorf("agent: %s", msg.Error))
			return
		case "bye":
			return
		}
	}
}

func (s *Session) tick(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.workspace.SendChanges(); err != nil {
				s.fail(err)
				return
			}
		}
	}
}

func (s *Session) fail(err error) {
	select {
	case s.errs <- err:
	default:
	}
}

func (s *Session) WaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ready:
		return nil
	case err := <-s.errs:
		return err
	}
}

func (s *Session) Flush(ctx context.Context) error {
	if err := s.conn.Send(Message{Type: "flush"}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.flushed:
		return nil
	case err := <-s.errs:
		return err
	}
}

func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		_ = s.conn.Send(Message{Type: "bye"})
		s.cancel()
		_ = s.workspace.Close()
		_ = s.artifacts.Close()
		err = s.conn.Close()
	})
	return err
}
