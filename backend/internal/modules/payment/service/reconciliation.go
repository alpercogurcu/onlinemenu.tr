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
	defaultReconcileInterval  = 1 * time.Minute
	defaultReconcileBatchSize = 100
	// A submission still 'submitted' after this long means the device never
	// reported back. It is surfaced to operators, not resolved automatically.
	defaultStaleAfter = 15 * time.Minute
	// Vendor baskets live at most two weeks (ADR-FISCAL-002, Token X). Past that
	// the basket provably no longer exists on the vendor side, so the sale can
	// never complete and the submission may be expired.
	defaultExpireAfter = 14 * 24 * time.Hour
)

// ReconcilerConfig tunes the sweep. Zero values fall back to the defaults above.
type ReconcilerConfig struct {
	Interval    time.Duration
	BatchSize   int
	StaleAfter  time.Duration
	ExpireAfter time.Duration
	// AutoExpire lets the sweep fail payments whose submission outlived
	// ExpireAfter. It is off by default and must stay off until an adapter can
	// ask the vendor whether the basket was actually registered: the basket
	// disappearing from the vendor says nothing about whether the device printed
	// a receipt, so expiring on a clock alone can fail a legally registered sale.
	// ADR-FISCAL-002 only mandates "TTL + uyarı" — the warning, not the write.
	AutoExpire bool
}

func (c ReconcilerConfig) withDefaults() ReconcilerConfig {
	if c.Interval <= 0 {
		c.Interval = defaultReconcileInterval
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultReconcileBatchSize
	}
	if c.StaleAfter <= 0 {
		c.StaleAfter = defaultStaleAfter
	}
	if c.ExpireAfter <= 0 {
		c.ExpireAfter = defaultExpireAfter
	}
	return c
}

// staleSubmissionStore is the read surface the reconciler needs, declared here so
// the sweep logic is unit-testable without a database.
type staleSubmissionStore interface {
	ListStaleSubmitted(ctx context.Context, batchSize int, staleAfter time.Duration) ([]repo.FiscalSubmission, error)
}

// ReconcileStats reports one sweep's outcome.
type ReconcileStats struct {
	Scanned int
	Warned  int
	Expired int
}

// Reconciler is the safety net for fiscal results that never arrived: a webhook
// dropped in transit, a device left off the sales screen, a basket the cashier
// walked away from (ADR-FISCAL-002, "Uzlaştırma").
//
// It deliberately does NOT fail a payment just because a result is late. A
// missing webhook does not mean the device failed to print — it may well have
// registered the sale and only the notification was lost. So by default the
// sweep observes and reports; it never writes:
//
//   - StaleAfter  → warn once per submission so operators can inspect the device.
//   - ExpireAfter → warn that the vendor's basket TTL elapsed. The submission is
//     expired (and its payment failed) ONLY when AutoExpire is enabled.
//
// The missing piece is asking the vendor (Token's "Get Open Baskets For
// Terminal") whether the basket was registered. FiscalDeviceAdapter has no query
// method yet; when one lands, call it before expiring and let the vendor's
// answer — not the clock — decide. AutoExpire exists for that day.
type Reconciler struct {
	store  staleSubmissionStore
	sink   domain.FiscalResultSink
	cfg    ReconcilerConfig
	logger *zap.Logger
	now    func() time.Time

	// warned remembers which submissions were already reported so a row stuck for
	// days does not emit a warning on every tick. Entries are dropped once the
	// row leaves the stale set (it resolved), so a recurrence warns again.
	mu     sync.Mutex
	warned map[uuid.UUID]struct{}
}

// ReconcilerParams groups fx-injected dependencies.
type ReconcilerParams struct {
	fx.In

	DB             *db.Pool
	SubmissionRepo *repo.FiscalSubmissionRepo
	Sink           domain.FiscalResultSink
	Logger         *zap.Logger
	Config         ReconcilerConfig `optional:"true"`
}

func NewReconciler(p ReconcilerParams) *Reconciler {
	return &Reconciler{
		store:  &dbStaleSubmissionStore{db: p.DB, repo: p.SubmissionRepo},
		sink:   p.Sink,
		cfg:    p.Config.withDefaults(),
		logger: p.Logger,
		now:    func() time.Time { return time.Now().UTC() },
		warned: make(map[uuid.UUID]struct{}),
	}
}

// Run sweeps until ctx is cancelled. A failed sweep is logged, never fatal: the
// same rows are still there on the next tick.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.RunOnce(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				r.logger.Error("payment: fiscal reconciliation sweep failed", zap.Error(err))
			}
		}
	}
}

// RunOnce performs a single sweep. Exported so tests and an asynq periodic task
// can drive it deterministically (ADR-ARCH-002 routes periodic sweeps to asynq;
// wiring lives outside this package).
func (r *Reconciler) RunOnce(ctx context.Context) (ReconcileStats, error) {
	stale, err := r.store.ListStaleSubmitted(ctx, r.cfg.BatchSize, r.cfg.StaleAfter)
	if err != nil {
		return ReconcileStats{}, fmt.Errorf("payment/reconciler: list stale submissions: %w", err)
	}

	stats := ReconcileStats{Scanned: len(stale)}
	seen := make(map[uuid.UUID]struct{}, len(stale))

	for _, sub := range stale {
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		if sub.SubmittedAt == nil {
			continue // defensive: the query filters these out
		}
		seen[sub.ID] = struct{}{}
		age := r.now().Sub(*sub.SubmittedAt)
		pastTTL := age >= r.cfg.ExpireAfter

		if pastTTL && r.cfg.AutoExpire {
			if err := r.expire(ctx, sub, age); err != nil {
				// One unresolvable row must not stop the sweep.
				r.logger.Error("payment: expire stale submission",
					zap.Stringer("submission_id", sub.ID),
					zap.Error(err),
				)
				continue
			}
			delete(seen, sub.ID) // resolved; a recurrence should warn afresh
			r.forget(sub.ID)
			stats.Expired++
			continue
		}

		if !r.markWarned(sub.ID) {
			continue // already reported; do not re-log every tick
		}
		stats.Warned++
		r.logger.Warn("payment: fiscal result overdue, device may need attention",
			zap.Stringer("submission_id", sub.ID),
			zap.Stringer("payment_id", sub.PaymentID),
			zap.Stringer("tenant_id", sub.TenantID),
			zap.String("terminal_serial", sub.TerminalSerial),
			zap.String("adapter_type", sub.AdapterType),
			zap.Duration("age", age),
			zap.Bool("past_vendor_ttl", pastTTL),
		)
	}

	r.pruneWarned(seen)
	return stats, nil
}

// markWarned reports whether this is the first time the submission is warned
// about. Concurrent RunOnce callers (asynq) may race, so the map is guarded.
func (r *Reconciler) markWarned(id uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.warned[id]; ok {
		return false
	}
	r.warned[id] = struct{}{}
	return true
}

func (r *Reconciler) forget(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.warned, id)
}

// pruneWarned drops submissions that left the stale set, bounding the map and
// letting a row that goes stale again be reported again. Rows beyond BatchSize
// are absent from seen and so are forgotten early; they simply re-warn once the
// backlog drains.
func (r *Reconciler) pruneWarned(seen map[uuid.UUID]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.warned {
		if _, ok := seen[id]; !ok {
			delete(r.warned, id)
		}
	}
}

// expire drives a submission whose vendor basket has outlived its TTL to the
// expired terminal state through the sink, so the transition stays idempotent
// and the payment is failed exactly once.
func (r *Reconciler) expire(ctx context.Context, sub repo.FiscalSubmission, age time.Duration) error {
	r.logger.Error("payment: fiscal submission expired past vendor basket TTL",
		zap.Stringer("submission_id", sub.ID),
		zap.Stringer("payment_id", sub.PaymentID),
		zap.Duration("age", age),
	)
	return r.sink.OnFiscalResult(ctx, domain.FiscalResult{
		SubmissionID:  sub.ID,
		TenantID:      sub.TenantID,
		BranchID:      sub.BranchID,
		PaymentID:     sub.PaymentID,
		Status:        domain.FiscalSubmissionExpired,
		DeviceType:    sub.AdapterType,
		FailureReason: fmt.Sprintf("no fiscal result within %s; vendor basket TTL elapsed", age.Round(time.Hour)),
		CompletedAt:   r.now(),
	})
}

// dbStaleSubmissionStore is the production staleSubmissionStore backed by Postgres.
type dbStaleSubmissionStore struct {
	db   *db.Pool
	repo *repo.FiscalSubmissionRepo
}

// ListStaleSubmitted reads across tenants, which the per-tenant RLS policy
// forbids; WithAllTenantsReadTx is the platform's named cross-tenant door. The
// fiscal_submissions all-tenants policy must grant it (same dependency as the
// submission worker's claim).
func (s *dbStaleSubmissionStore) ListStaleSubmitted(ctx context.Context, batchSize int, staleAfter time.Duration) ([]repo.FiscalSubmission, error) {
	var out []repo.FiscalSubmission
	err := s.db.WithAllTenantsReadTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.repo.ListStaleSubmitted(ctx, tx, batchSize, staleAfter)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
