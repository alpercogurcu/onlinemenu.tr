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
}

// tableSpec describes one outbox table and its NATS subject namespace.
type tableSpec struct {
	table  string
	module string // used to construct NATS subject: "<module>.<eventType>.v1"
}

var tables = []tableSpec{
	{table: "pos_outbox", module: "pos"},
	{table: "payment_outbox", module: "payment"},
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
	pool   *pgxpool.Pool
	pub    msgPublisher
	cfg    Config
	logger *zap.Logger
	cancel context.CancelFunc
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

	d := &Dispatcher{pool: pool, pub: p.Bus, cfg: p.Config, logger: p.Logger}

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
			)
			return nil
		},
		OnStop: func(_ context.Context) error {
			if d.cancel != nil {
				d.cancel()
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

// dispatchTable fetches a batch from one outbox table and publishes each event.
func (d *Dispatcher) dispatchTable(ctx context.Context, t tableSpec) error {
	query := fmt.Sprintf(`
		SELECT event_id, tenant_id, aggregate_type, aggregate_id, event_type, payload, retry_count
		FROM %s
		WHERE processed_at IS NULL
		  AND is_dead = FALSE
		  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY aggregate_id, event_id
		FOR UPDATE SKIP LOCKED
		LIMIT %d
	`, t.table, d.cfg.BatchSize)

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("outbox: begin tx for %s: %w", t.table, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("outbox: query %s: %w", t.table, err)
	}

	var batch []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(
			&r.eventID, &r.tenantID, &r.aggregateType, &r.aggregateID,
			&r.eventType, &r.payload, &r.retryCount,
		); err != nil {
			rows.Close()
			return fmt.Errorf("outbox: scan %s: %w", t.table, err)
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("outbox: rows err %s: %w", t.table, err)
	}

	if len(batch) == 0 {
		_ = tx.Rollback(ctx)
		return nil
	}

	var (
		successIDs []uuid.UUID
		failures   []failureResult
	)

	for _, r := range batch {
		subject := toSubject(t.module, r.eventType)
		pubErr := d.publish(ctx, r.eventID, subject, r.payload)
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

	if err := d.applyResults(ctx, tx, t.table, successIDs, failures, maxRetries); err != nil {
		return fmt.Errorf("outbox: apply results for %s: %w", t.table, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("outbox: commit %s: %w", t.table, err)
	}

	if len(successIDs) > 0 {
		d.logger.Info("outbox: dispatched",
			zap.String("table", t.table),
			zap.Int("count", len(successIDs)),
		)
	}

	return nil
}

type failureResult struct {
	row outboxRow
	err error
}

// applyResults marks successful events as processed and increments retry/dead for failures.
func (d *Dispatcher) applyResults(
	ctx context.Context,
	tx pgx.Tx,
	table string,
	successIDs []uuid.UUID,
	failures []failureResult,
	maxRetries int,
) error {
	if len(successIDs) > 0 {
		_, err := tx.Exec(ctx, fmt.Sprintf(
			`UPDATE %s SET processed_at = NOW() WHERE event_id = ANY($1)`, table,
		), successIDs)
		if err != nil {
			return fmt.Errorf("mark processed: %w", err)
		}
	}

	for _, f := range failures {
		newRetry := f.row.retryCount + 1
		isDead := newRetry > maxRetries
		next := retryBackoff(newRetry)
		errStr := f.err.Error()

		_, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s
			SET retry_count   = $2,
			    next_retry_at = $3,
			    last_error    = $4,
			    is_dead       = $5
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

	return nil
}

// publish sends one event to NATS JetStream with Nats-Msg-Id for deduplication.
// The Nats-Msg-Id header enables JetStream's built-in deduplication window,
// preventing duplicate delivery if the dispatcher restarts before committing.
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
