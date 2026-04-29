// Command edge is the local branch server that runs on-premises hardware.
// It provides offline-capable POS operations and syncs with the cloud API
// when connectivity is restored (ADR-DATA-004, offline-sync.md).
//
// Phase 1 placeholder — full implementation begins in Phase 2.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "edge: build logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("edge server starting (Phase 1 placeholder)")

	<-ctx.Done()
	logger.Info("edge server stopped")
}
