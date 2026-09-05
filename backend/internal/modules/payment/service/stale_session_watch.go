package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/db"
)

const (
	defaultStaleSessionInterval = 15 * time.Minute
	// A branch's cash session left 'opened' this long past its opened_at is
	// almost certainly a forgotten drawer, not a genuinely long shift — 20h
	// covers even a double shift with margin. ADR-DATA-008 açıkları mandates a
	// warning here, explicitly NOT an automatic close: only the cashier or
	// manager who actually knows what is in the till may close it.
	defaultStaleSessionMaxAge = 20 * time.Hour
)

// StaleSessionWatchConfig tunes the sweep. Zero values fall back to the
// defaults above.
type StaleSessionWatchConfig struct {
	Interval time.Duration
	MaxAge   time.Duration
}

func (c StaleSessionWatchConfig) withDefaults() StaleSessionWatchConfig {
	if c.Interval <= 0 {
		c.Interval = defaultStaleSessionInterval
	}
	if c.MaxAge <= 0 {
		c.MaxAge = defaultStaleSessionMaxAge
	}
	return c
}

// staleSessionStore is the read surface the watch needs, declared here so the
// sweep logic is unit-testable without a database — same pattern as
// staleSubmissionStore in reconciliation.go.
type staleSessionStore interface {
	ListOpenOlderThan(ctx context.Context, before time.Time) ([]domain.CashSession, error)
}

// StaleSessionWatchStats reports one sweep's outcome.
type StaleSessionWatchStats struct {
	Scanned int
	Warned  int
}

// StaleSessionWatch surfaces branch cash sessions left 'opened' long past a
// normal shift (ADR-DATA-008 açıkları). It never closes anything: the whole
// point of a cash session is that only a human who actually counted the
// drawer may close it, so this is a warning-only safety net, exactly the
// fiscal Reconciler's StaleAfter behaviour applied to a different table.
type StaleSessionWatch struct {
	store  staleSessionStore
	cfg    StaleSessionWatchConfig
	logger *zap.Logger
	now    func() time.Time

	// warned remembers which sessions were already reported so a drawer left
	// open for days does not emit a warning on every tick. Entries drop once
	// the session leaves the stale set (closed, or simply not returned
	// anymore), so a genuine recurrence is reported again.
	mu     sync.Mutex
	warned map[uuid.UUID]struct{}
}

// StaleSessionWatchParams groups fx-injected dependencies.
type StaleSessionWatchParams struct {
	fx.In

	DB       *db.Pool
	Sessions *repo.CashSessionRepo
	Logger   *zap.Logger
	Config   StaleSessionWatchConfig `optional:"true"`
}

func NewStaleSessionWatch(p StaleSessionWatchParams) *StaleSessionWatch {
	return &StaleSessionWatch{
		store:  &dbStaleSessionStore{db: p.DB, repo: p.Sessions},
		cfg:    p.Config.withDefaults(),
		logger: p.Logger,
		now:    func() time.Time { return time.Now().UTC() },
		warned: make(map[uuid.UUID]struct{}),
	}
}

// Run sweeps until ctx is cancelled. A failed sweep is logged, never fatal:
// the same rows are still there on the next tick.
func (w *StaleSessionWatch) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.RunOnce(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				w.logger.Error("payment: stale cash session watch sweep failed", zap.Error(err))
			}
		}
	}
}

// RunOnce performs a single sweep. Exported so tests can drive it
// deterministically.
func (w *StaleSessionWatch) RunOnce(ctx context.Context) (StaleSessionWatchStats, error) {
	cutoff := w.now().Add(-w.cfg.MaxAge)
	stale, err := w.store.ListOpenOlderThan(ctx, cutoff)
	if err != nil {
		return StaleSessionWatchStats{}, fmt.Errorf("payment/stale_session_watch: list open cash sessions: %w", err)
	}

	stats := StaleSessionWatchStats{Scanned: len(stale)}
	seen := make(map[uuid.UUID]struct{}, len(stale))

	for _, s := range stale {
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		seen[s.ID] = struct{}{}
		if !w.markWarned(s.ID) {
			continue // already reported; do not re-log every tick
		}
		stats.Warned++
		w.logger.Warn("payment: cash session open past max age",
			zap.Stringer("session_id", s.ID),
			zap.Stringer("branch_id", s.BranchID),
			zap.Time("opened_at", s.OpenedAt),
		)
	}

	w.pruneWarned(seen)
	return stats, nil
}

// markWarned reports whether this is the first time the session is warned
// about. Concurrent RunOnce callers may race, so the map is guarded.
func (w *StaleSessionWatch) markWarned(id uuid.UUID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.warned[id]; ok {
		return false
	}
	w.warned[id] = struct{}{}
	return true
}

// pruneWarned drops sessions that left the stale set (closed, most likely),
// bounding the map and letting a session that somehow goes stale again be
// reported again.
func (w *StaleSessionWatch) pruneWarned(seen map[uuid.UUID]struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.warned {
		if _, ok := seen[id]; !ok {
			delete(w.warned, id)
		}
	}
}

// dbStaleSessionStore is the production staleSessionStore backed by Postgres.
type dbStaleSessionStore struct {
	db   *db.Pool
	repo *repo.CashSessionRepo
}

// ListOpenOlderThan reads across tenants, which the per-tenant RLS policy
// forbids; WithAllTenantsReadTx is the platform's named cross-tenant door
// (migration/000009's cash_sessions_all_tenants_select policy must grant it —
// same dependency shape as the fiscal reconciler's
// fiscal_submissions_all_tenants_select).
func (s *dbStaleSessionStore) ListOpenOlderThan(ctx context.Context, before time.Time) ([]domain.CashSession, error) {
	var out []domain.CashSession
	err := s.db.WithAllTenantsReadTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.repo.ListOpenOlderThan(ctx, tx, before)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
