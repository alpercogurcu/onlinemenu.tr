package sync

import (
	"context"
	"time"

	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/edge-sync/domain"
	"onlinemenu.tr/internal/modules/edge-sync/outbox"
)

// Engine coordinates the outbox flush worker and inbox apply worker.
// Phase 1: logs pending event counts and connectivity state.
// Phase 2: replaces stubs with actual NATS publish/subscribe.
type Engine struct {
	outbox *outbox.Writer
	hb     *Heartbeat
	log    *zap.Logger
}

// NewEngine constructs a sync Engine.
func NewEngine(out *outbox.Writer, hb *Heartbeat, log *zap.Logger) *Engine {
	return &Engine{outbox: out, hb: hb, log: log}
}

// Run starts the flush loop. Blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.drainOnShutdown(ctx)
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

func (e *Engine) tick(ctx context.Context) {
	state := e.hb.State()
	if state == domain.ConnStateOffline {
		return
	}

	n, err := e.outbox.PendingCount(ctx)
	if err != nil {
		e.log.Error("edge: outbox count error", zap.Error(err))
		return
	}
	if n == 0 {
		return
	}

	// Phase 2 TODO: publish n events to cloud NATS subject sync.<branch_id>.out.<event_type>.v1
	e.log.Info("edge: outbox flush pending (Phase 2)",
		zap.Int("count", n),
		zap.String("connectivity", state.String()),
	)
}

// drainOnShutdown gives the engine a final chance to report pending events.
func (e *Engine) drainOnShutdown(ctx context.Context) {
	drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n, err := e.outbox.PendingCount(drainCtx) //nolint:contextcheck // caller's ctx is already cancelled (this runs from the ctx.Done() branch of Run); the drain needs its own live deadline to reach the DB.
	if err != nil || n == 0 {
		return
	}
	e.log.Warn("edge: shutdown with unshipped outbox events", zap.Int("pending", n))
}
