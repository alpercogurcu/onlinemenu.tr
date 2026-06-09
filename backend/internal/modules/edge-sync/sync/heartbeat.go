// Package sync implements the edge server's connectivity state machine and sync engine.
package sync

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/edge-sync/domain"
)

// HeartbeatConfig controls the cloud health-check ping.
type HeartbeatConfig struct {
	// CloudHealthURL is the cloud /healthz endpoint. Empty = heartbeat disabled.
	CloudHealthURL string
	// Interval between pings. Default: 30s.
	Interval time.Duration
	// DegradedAfter is the round-trip latency threshold. Above this → DEGRADED. Default: 500ms.
	DegradedAfter time.Duration
	// OfflineAfter is the number of consecutive failures before transitioning to OFFLINE. Default: 3.
	OfflineAfter int
}

// StateNotifier is called on every ConnState transition with the previous and next state.
type StateNotifier func(prev, next domain.ConnState)

// Heartbeat monitors cloud connectivity via periodic HTTP pings and drives the
// ONLINE → DEGRADED → OFFLINE → SYNCING state machine.
type Heartbeat struct {
	cfg      HeartbeatConfig
	client   *http.Client
	log      *zap.Logger
	notify   StateNotifier
	state    domain.ConnState
	failures int
}

// NewHeartbeat constructs a Heartbeat.
// notify may be nil if the caller doesn't need transition events.
func NewHeartbeat(cfg HeartbeatConfig, log *zap.Logger, notify StateNotifier) *Heartbeat {
	return &Heartbeat{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
		log:    log,
		notify: notify,
		state:  domain.ConnStateOffline,
	}
}

// Run starts the ping loop. Blocks until ctx is cancelled.
func (h *Heartbeat) Run(ctx context.Context) {
	interval := h.cfg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.ping(ctx)
		}
	}
}

// State returns the current connectivity state (safe to call from any goroutine
// as long as there is only one Heartbeat.Run goroutine writing it).
func (h *Heartbeat) State() domain.ConnState {
	return h.state
}

func (h *Heartbeat) ping(ctx context.Context) {
	if h.cfg.CloudHealthURL == "" {
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.CloudHealthURL, nil)
	if err != nil {
		h.recordFailure()
		return
	}

	start := time.Now()
	resp, err := h.client.Do(req)
	elapsed := time.Since(start)

	var next domain.ConnState
	if err != nil || resp.StatusCode >= 500 {
		h.failures++
		threshold := h.cfg.OfflineAfter
		if threshold <= 0 {
			threshold = 3
		}
		if h.failures >= threshold {
			next = domain.ConnStateOffline
		} else {
			next = domain.ConnStateDegraded
		}
	} else {
		resp.Body.Close()
		h.failures = 0
		degraded := h.cfg.DegradedAfter
		if degraded <= 0 {
			degraded = 500 * time.Millisecond
		}
		switch {
		case elapsed > degraded:
			next = domain.ConnStateDegraded
		case h.state == domain.ConnStateOffline || h.state == domain.ConnStateSyncing:
			// Just reconnected — enter SYNCING so engine can flush backlog.
			next = domain.ConnStateSyncing
		default:
			next = domain.ConnStateOnline
		}
	}

	h.transition(next)
}

func (h *Heartbeat) recordFailure() {
	h.failures++
	threshold := h.cfg.OfflineAfter
	if threshold <= 0 {
		threshold = 3
	}
	if h.failures >= threshold {
		h.transition(domain.ConnStateOffline)
	} else {
		h.transition(domain.ConnStateDegraded)
	}
}

func (h *Heartbeat) transition(next domain.ConnState) {
	if next == h.state {
		return
	}
	h.log.Info("edge: connectivity transition",
		zap.String("from", h.state.String()),
		zap.String("to", next.String()),
	)
	prev := h.state
	h.state = next
	if h.notify != nil {
		h.notify(prev, next)
	}
}
