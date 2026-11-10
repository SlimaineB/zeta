//go:build !windows

package sync

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func watchResize(ctx context.Context, sendSize func()) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-winch:
			sendSize()
		}
	}
}
