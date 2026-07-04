// Package outbox implements the transactional outbox dispatcher (ADR-DATA-001).
// The dispatcher polls outbox tables and publishes undelivered events to NATS JetStream.
// It uses a BYPASSRLS database role (app_migrator) to read across all tenants without
// setting per-tenant RLS context. Faz 2 replaces this with Debezium reading from WAL.
package outbox

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/eventbus"
)

// msgPublisher is the subset of eventbus.Bus used by the dispatcher.
// Defined here so tests can substitute a fake without importing the full Bus.
type msgPublisher interface {
	PublishMsg(ctx context.Context, msg *nats.Msg) error
}

// Config holds dispatcher configuration. DSN must point to a role with BYPASSRLS.
// If DSN is empty, the dispatcher starts in no-op mode (events accumulate in DB).
type Config struct {
	// DSN is the database connection string for a BYPASSRLS role (e.g. app_migrator).
	DSN string
	// PollInterval is the fallback polling interval when no LISTEN notification arrives.
	PollInterval time.Duration
	// BatchSize is the maximum number of events fetched per poll cycle.
	BatchSize int
	// MaxRetries is the retry limit before marking an event as dead.
	MaxRetries int
	// PublishTimeout bounds each individual NATS publish call. Publishing happens
	// outside the claim transaction, so a slow/unavailable NATS server must not
	// hold a claimed row (or a DB connection) indefinitely. Defaults to 5s.
	PublishTimeout time.Duration
	// StaleClaimAfter is how long a row may stay claimed without being resolved
	// (processed or retried) before another dispatcher instance may reclaim it.
	// This bounds recovery time after a dispatcher crash between claim and
	// result-apply. Defaults to 5 minutes.
	StaleClaimAfter time.Duration
}

// tableSpec describes one outbox table and its NATS subject namespace.
type tableSpec struct {
	table  string
	module string // used to construct NATS subject: "<module>.<eventType>.v1"
}

var tables = []tableSpec{
	{table: "pos_outbox", module: "pos"},
	{table: "payment_outbox", module: "payment"},
	{table: "billing_outbox", module: "billing"},
}

// outboxRow is a row fetched from any outbox table.
type outboxRow struct {
	eventID       uuid.UUID
	tenantID      uuid.UUID
	aggregateType string
	aggregateID   string
	eventType     string
	payload       []byte
	retryCount    int
}

// Dispatcher polls outbox tables and publishes events to NATS JetStream.
type Dispatcher struct {
	pool            *pgxpool.Pool
	pub             msgPublisher
	cfg             Config
	logger          *zap.Logger
	cancel          context.CancelFunc
	done            chan struct{}
	publishTimeout  time.Duration
	staleClaimAfter time.Duration
}

// Params groups fx-injected dependencies for the Dispatcher.
type Params struct {
	fx.In

	LC     fx.Lifecycle
	Config Config
	Bus    *eventbus.Bus
	Logger *zap.Logger
}

// Register wires the Dispatcher into the fx lifecycle.
// This is an fx.Invoke target — the Dispatcher is not returned as a dependency.
func Register(p Params) error {
	if p.Config.DSN == "" {
		p.Logger.Warn("outbox: OUTBOX_MIGRATOR_DSN not set — dispatcher disabled; events will accumulate in DB")
		return nil
	}

	poolCfg, err := pgxpool.ParseConfig(p.Config.DSN)
	if err != nil {
		return fmt.Errorf("outbox: parse dispatcher dsn: %w", err)
	}
	poolCfg.MaxConns = 4
	poolCfg.MinConns = 1
	// SimpleProtocol for pgBouncer transaction-mode compatibility.
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return fmt.Errorf("outbox: create dispatcher pool: %w", err)
	}

	publishTimeout := p.Config.PublishTimeout
	if publishTimeout <= 0 {
		publishTimeout = 5 * time.Second
	}
	staleClaimAfter := p.Config.StaleClaimAfter
	if staleClaimAfter <= 0 {
		staleClaimAfter = 5 * time.Minute
	}

	d := &Dispatcher{
		pool:            pool,
		pub:             p.Bus,
		cfg:             p.Config,
		logger:          p.Logger,
		done:            make(chan struct{}),
		publishTimeout:  publishTimeout,
		staleClaimAfter: staleClaimAfter,
	}

	p.LC.Append(fx.Hook{
		OnStart: func(startCtx context.Context) error {
			if err := pool.Ping(startCtx); err != nil {
				pool.Close()
				return fmt.Errorf("outbox: ping dispatcher pool: %w", err)
			}
			runCtx, cancel := context.WithCancel(context.Background())
			d.cancel = cancel
			go d.run(runCtx)
			p.Logger.Info("outbox dispatcher started",
				zap.Duration("poll_interval", p.Config.PollInterval),
				zap.Int("batch_size", p.Config.BatchSize),
				zap.Duration("publish_timeout", publishTimeout),
				zap.Duration("stale_claim_after", staleClaimAfter),
			)
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			if d.cancel != nil {
				d.cancel()
			}
			// Wait for run() to observe cancellation and exit before closing the
			// pool, otherwise an in-flight query would fail against a closed pool
			// and the run() goroutine would leak past OnStop's return.
			select {
			case <-d.done:
			case <-stopCtx.Done():
				p.Logger.Warn("outbox: dispatcher stop deadline exceeded before loop exit")
			}
			pool.Close()
			p.Logger.Info("outbox dispatcher stopped")
			return nil
		},
	})

	return nil
}

// run is the main dispatcher loop. It polls all outbox tables on a ticker.
func (d *Dispatcher) run(ctx context.Context) {
	defer close(d.done)

	interval := d.cfg.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, t := range tables {
				if err := d.dispatchTable(ctx, t); err != nil {
					d.logger.Error("outbox: dispatch cycle failed",
						zap.String("table", t.table),
						zap.Error(err),
					)
				}
			}
		}
	}
}

// dispatchTable claims a batch from one outbox table, publishes each event
// outside any transaction, then persists the outcome in a short results tx.
//
// Splitting claim / publish / apply-results is deliberate (see ADR-DATA-001
// discussion in the sprint report): publishing while holding a row lock and a
// pooled connection meant a slow or unavailable NATS server could exhaust the
// dispatcher's small connection pool (MaxConns=4) and stall the dispatcher
// entirely. Publishing is now bounded by PublishTimeout and never blocks a
// database connection.
func (d *Dispatcher) dispatchTable(ctx context.Context, t tableSpec) error {
	batch, err := d.claimBatch(ctx, t.table)
	if err != nil {
		return fmt.Errorf("outbox: claim batch for %s: %w", t.table, err)
	}
	if len(batch) == 0 {
		return nil
	}

	var (
		successIDs []uuid.UUID
		failures   []failureResult
	)

	for _, r := range batch {
		subject := toSubject(t.module, r.eventType)
		pubErr := d.publishWithTimeout(ctx, r.eventID, subject, r.payload)
		if pubErr == nil {
			successIDs = append(successIDs, r.eventID)
		} else {
			d.logger.Warn("outbox: publish failed",
				zap.String("table", t.table),
				zap.Stringer("event_id", r.eventID),
				zap.String("subject", subject),
				zap.Error(pubErr),
			)
			failures = append(failures, failureResult{row: r, err: pubErr})
		}
	}

	maxRetries := d.cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 10
	}

	if err := d.applyResults(ctx, t.table, successIDs, failures, maxRetries); err != nil {
		return fmt.Errorf("outbox: apply results for %s: %w", t.table, err)
	}

	if len(successIDs) > 0 {
		d.logger.Info("outbox: dispatched",
			zap.String("table", t.table),
			zap.Int("count", len(successIDs)),
		)
	}

	return nil
}

// claimBatch atomically selects and marks up to BatchSize eligible rows as
// claimed, in one short transaction, and returns them for publishing.
//
// Eligible rows are: unprocessed, not dead, past their retry backoff, and
// either never claimed or claimed longer than StaleClaimAfter ago (recovers
// rows left claimed by a dispatcher that crashed after claiming but before
// applying results — at-least-once delivery relies on this reclaim plus
// JetStream's Nats-Msg-Id dedup and consumers' ON CONFLICT DO NOTHING).
func (d *Dispatcher) claimBatch(ctx context.Context, table string) ([]outboxRow, error) {
	// Truncated to whole seconds for the INTERVAL literal below: guard against
	// a sub-second StaleClaimAfter (e.g. in a misconfigured test) rounding down
	// to 0, which would make every claimed-but-unresolved row reclaimable
	// instantly instead of after a meaningful crash-recovery window.
	staleSeconds := int(d.staleClaimAfter.Seconds())
	if staleSeconds < 1 {
		staleSeconds = 1
	}

	query := fmt.Sprintf(`
		UPDATE %s
		SET claimed_at = NOW()
		WHERE event_id IN (
			SELECT event_id
			FROM %s
			WHERE processed_at IS NULL
			  AND is_dead = FALSE
			  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
			  AND (claimed_at IS NULL OR claimed_at <= NOW() - INTERVAL '%d seconds')
			ORDER BY aggregate_id, event_id
			FOR UPDATE SKIP LOCKED
			LIMIT %d
		)
		RETURNING event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload, retry_count
	`, table, table, staleSeconds, d.cfg.BatchSize)

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("claim query: %w", err)
	}

	var batch []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(
			&r.eventID, &r.tenantID, &r.aggregateType, &r.aggregateID,
			&r.eventType, &r.payload, &r.retryCount,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan claimed row: %w", err)
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("claim rows err: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim tx: %w", err)
	}

	return batch, nil
}

type failureResult struct {
	row outboxRow
	err error
}

// applyResults marks successful events as processed and increments retry/dead for
// failures, in a short transaction separate from the (already-committed) claim
// and the (already-completed) publish attempts.
func (d *Dispatcher) applyResults(
	ctx context.Context,
	table string,
	successIDs []uuid.UUID,
	failures []failureResult,
	maxRetries int,
) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin results tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Executed as individual statements rather than "= ANY($1)" with a slice
	// parameter: under QueryExecModeSimpleProtocol (required for pgBouncer
	// transaction-mode compatibility) pgx cannot text-encode a []uuid.UUID
	// array parameter ("unable to encode ... unknown type"). Batch sizes here
	// are small (BatchSize per cycle), so per-row statements are not a
	// meaningful cost against the outbox poll interval.
	for _, id := range successIDs {
		_, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE %s SET processed_at = NOW() WHERE event_id = $1`, table,
		), id)
		if err != nil {
			return fmt.Errorf("mark processed %s: %w", id, err)
		}
	}

	for _, f := range failures {
		newRetry := f.row.retryCount + 1
		isDead := newRetry > maxRetries
		next := retryBackoff(newRetry)
		errStr := f.err.Error()

		// claimed_at is cleared so the row is immediately eligible again once
		// next_retry_at elapses, instead of also waiting out StaleClaimAfter.
		_, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s
			SET retry_count   = $2,
			    next_retry_at = $3,
			    last_error    = $4,
			    is_dead       = $5,
			    claimed_at    = NULL
			WHERE event_id = $1
		`, table), f.row.eventID, newRetry, next, errStr, isDead)
		if err != nil {
			return fmt.Errorf("mark retry for %s: %w", f.row.eventID, err)
		}

		if isDead {
			d.logger.Error("outbox: event marked dead",
				zap.String("table", table),
				zap.Stringer("event_id", f.row.eventID),
				zap.Int("retry_count", newRetry),
				zap.Error(f.err),
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit results tx: %w", err)
	}
	return nil
}

// publishWithTimeout bounds one publish call so a slow/unavailable NATS server
// cannot stall a dispatch cycle indefinitely. The timeout is derived from the
// dispatcher's run-loop context so shutdown aborts in-flight publishes cleanly.
func (d *Dispatcher) publishWithTimeout(ctx context.Context, eventID uuid.UUID, subject string, payload []byte) error {
	pubCtx, cancel := context.WithTimeout(ctx, d.publishTimeout)
	defer cancel()
	return d.publish(pubCtx, eventID, subject, payload)
}

// publish sends one event to NATS JetStream with Nats-Msg-Id for deduplication.
// The Nats-Msg-Id header enables JetStream's built-in deduplication window,
// preventing duplicate delivery if the dispatcher restarts before committing,
// or if a claimed-but-unresolved row is reclaimed and republished.
func (d *Dispatcher) publish(ctx context.Context, eventID uuid.UUID, subject string, payload []byte) error {
	return d.pub.PublishMsg(ctx, &nats.Msg{
		Subject: subject,
		Data:    payload,
		Header:  nats.Header{"Nats-Msg-Id": []string{eventID.String()}},
	})
}

// toSubject derives the NATS subject from a module name and event type.
// e.g. ("pos", "check.opened") → "pos.check.opened.v1"
//
//	("payment", "payment.completed") → "payment.completed.v1"
func toSubject(module, eventType string) string {
	// Strip redundant module prefix from event_type when present.
	eventType = strings.TrimPrefix(eventType, module+".")
	return fmt.Sprintf("%s.%s.v1", module, eventType)
}

// retryBackoff returns the next retry time using exponential backoff with jitter.
// Formula: min(60s, 2^retry + rand(0..1000ms))
func retryBackoff(retry int) time.Time {
	exp := math.Pow(2, float64(retry))
	jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
	delay := time.Duration(exp)*time.Second + jitter
	if delay > 60*time.Second {
		delay = 60*time.Second + jitter
	}
	return time.Now().Add(delay)
}
