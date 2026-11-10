//go:build windows

package sync

import (
	"context"
	"time"
)

func watchResize(ctx context.Context, sendSize func()) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendSize()
		}
	}
}
