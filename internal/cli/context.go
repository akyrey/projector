package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// rootContext returns a context that is cancelled when the process receives
// SIGINT or SIGTERM. This allows running commands to be interrupted cleanly.
func rootContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(quit)
		select {
		case <-quit:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx
}
