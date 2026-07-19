package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
	"onlinemenu.tr/internal/platform/db"
)

const (
	defaultWorkerInterval   = 1 * time.Second
	defaultWorkerBatchSize  = 20
	defaultWorkerMaxRetries = 5
	// defaultSubmitTimeout bounds one adapter call; defaultStaleClaimAfter must
	// stay well above it (plus the post-submit write window) because a reclaim
	// re-POSTs the basket and the vendor's duplicate-basketID behaviour is
	// undocumented (ADR-FISCAL-002 open question).
	defaultSubmitTimeout   = 60 * time.Second
	defaultStaleClaimAfter = 10 * time.Minute
	// defaultMaxConcurrency caps how many terminals are driven at once. Kept
	// modest: each slot holds a pgx connection during its bookkeeping writes, and
	// a batch rarely spans more distinct terminals than this.
	defaultMaxConcurrency = 4
	// postSubmitWriteTimeout bounds the bookkeeping writes that run detached
	// from the worker context after the vendor side is already affected.
	postSubmitWriteTimeout = 15 * time.Second
	maxRetryBackoff        = 60 * time.Second
)

// SubmissionWorkerConfig tunes the polling loop. Zero values fall back to the
// defaults above.
type SubmissionWorkerConfig struct {
	Interval        time.Duration
	BatchSize       int
	MaxRetries      int
	SubmitTimeout   time.Duration
	StaleClaimAfter time.Duration
	// MaxConcurrency bounds how many terminals are submitted to in parallel.
	// Ordering within a terminal is never affected by this value.
	MaxConcurrency int
}

func (c SubmissionWorkerConfig) withDefaults() SubmissionWorkerConfig {
	if c.Interval <= 0 {
		c.Interval = defaultWorkerInterval
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultWorkerBatchSize
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = defaultWorkerMaxRetries
	}
	if c.SubmitTimeout <= 0 {
		c.SubmitTimeout = defaultSubmitTimeout
	}
	if c.StaleClaimAfter <= 0 {
		c.StaleClaimAfter = defaultStaleClaimAfter
	}
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = defaultMaxConcurrency
	}
	return c
}

// submissionQueue is the persistence surface the worker needs. Declaring it here
// (rather than depending on *repo.FiscalSubmissionRepo) keeps the worker's
// claim/retry/dispatch logic unit-testable without a database.
type submissionQueue interface {
	ClaimPending(ctx context.Context, batchSize int, staleAfter time.Duration) ([]repo.FiscalSubmission, error)
	MarkSubmitted(ctx context.Context, sub repo.FiscalSubmission) (bool, error)
	MarkRetry(ctx context.Context, sub repo.FiscalSubmission, retryCount int, nextRetryAt time.Time, lastError string) error
}

// SubmissionWorker drains fiscal_submissions: it claims pending rows, hands each
// basket to the device adapter OUTSIDE any transaction (ADR-FISCAL-002), and
// records the outcome in a separate short transaction.
//
// A synchronous adapter (mock, wire) returns a result immediately and the worker
// routes it straight to the sink. An asynchronous adapter (cloud) returns nil;
// the row parks in 'submitted' until the vendor's webhook reaches the sink.
type SubmissionWorker struct {
	queue   submissionQueue
	adapter domain.FiscalDeviceAdapter
	sink    domain.FiscalResultSink
	cfg     SubmissionWorkerConfig
	logger  *zap.Logger
}

// SubmissionWorkerParams groups fx-injected dependencies.
type SubmissionWorkerParams struct {
	fx.In

	DB             *db.Pool
	SubmissionRepo *repo.FiscalSubmissionRepo
	Adapter        domain.FiscalDeviceAdapter
	Sink           domain.FiscalResultSink
	Logger         *zap.Logger
	Config         SubmissionWorkerConfig `optional:"true"`
}

func NewSubmissionWorker(p SubmissionWorkerParams) *SubmissionWorker {
	return &SubmissionWorker{
		queue:   &dbSubmissionQueue{db: p.DB, repo: p.SubmissionRepo},
		adapter: p.Adapter,
		sink:    p.Sink,
		cfg:     p.Config.withDefaults(),
		logger:  p.Logger,
	}
}

// Run polls until ctx is cancelled. It never returns an error: a failed cycle is
// logged and retried on the next tick, since the rows stay claimable.
func (w *SubmissionWorker) Run(ctx context.Context) {
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
				w.logger.Error("payment: fiscal submission cycle failed", zap.Error(err))
			}
		}
	}
}

// RunOnce executes a single claim-and-dispatch cycle and reports how many
// submissions it processed. Exported so tests (and a future asynq trigger) can
// drive the loop deterministically.
//
// The claimed batch is sharded by terminal: rows for the same terminal run
// sequentially in claim order (a fiscal device must print receipts in order),
// while distinct terminals run in parallel up to cfg.MaxConcurrency. That is
// what keeps one unreachable terminal from head-of-line blocking every other
// tenant's submissions.
//
// Only the claim itself can fail the whole cycle; a per-submission failure is
// logged and leaves that row for a later attempt, so one poisonous basket cannot
// stall the others — not even the rest of its own terminal's shard.
//
// RunOnce returns only after every shard has drained, so within one process a
// terminal never has two baskets in flight. Two caveats this barrier does NOT
// cover, both pre-existing: a row pushed to MarkRetry clears claimed_at and can
// be overtaken by a newer row for the same terminal on a later cycle, and
// ClaimPending's FOR UPDATE SKIP LOCKED admits several worker processes, which
// the barrier cannot coordinate. Strict end-to-end receipt ordering would need
// a per-terminal lease in the claim query.
func (w *SubmissionWorker) RunOnce(ctx context.Context) (int, error) {
	batch, err := w.queue.ClaimPending(ctx, w.cfg.BatchSize, w.cfg.StaleClaimAfter)
	if err != nil {
		return 0, fmt.Errorf("payment/worker: claim pending: %w", err)
	}
	if len(batch) == 0 {
		return 0, nil
	}

	shards := shardByTerminal(batch)

	limit := w.cfg.MaxConcurrency
	if limit > len(shards) {
		limit = len(shards)
	}

	// A plain errgroup on the caller's context, NOT errgroup.WithContext: a
	// derived cancel would let one shard's error abort submissions to unrelated,
	// healthy terminals. Shards only ever report cancellation.
	var g errgroup.Group
	g.SetLimit(limit)

	var processed atomic.Int64
	for _, shard := range shards {
		g.Go(func() error {
			n, err := w.processShard(ctx, shard)
			processed.Add(int64(n))
			return err
		})
	}
	err = g.Wait()

	return int(processed.Load()), err
}

// processShard drives one terminal's submissions in claim order. A per-row
// failure is logged and the shard continues: every row here is already claimed,
// so skipping one would strand it until the stale-claim window instead of
// letting it take its retry now.
func (w *SubmissionWorker) processShard(ctx context.Context, shard []repo.FiscalSubmission) (int, error) {
	processed := 0
	for _, sub := range shard {
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		if err := w.process(ctx, sub); err != nil {
			w.logger.Error("payment: fiscal submission failed",
				zap.Stringer("submission_id", sub.ID),
				zap.Stringer("payment_id", sub.PaymentID),
				zap.Error(err),
			)
			continue
		}
		processed++
	}
	return processed, nil
}

// terminalShardKey identifies the physical device a submission prints on.
//
// The branch is the whole key, deliberately. Device routing on the submit path
// is branch-scoped: the driver resolves the terminal from (tenant, branch) alone
// — domain.FiscalSale carries no serial at all, and the submission's
// TerminalSerial only ever reaches an adapter through FiscalSubmissionRef on the
// void path. Sharding on the serial would therefore split rows that print on one
// shared device into parallel shards and reorder its receipts, which is why the
// serial is not part of this key.
type terminalShardKey struct {
	tenantID uuid.UUID
	branchID uuid.UUID
}

func shardKeyOf(sub repo.FiscalSubmission) terminalShardKey {
	return terminalShardKey{tenantID: sub.TenantID, branchID: sub.BranchID}
}

// shardByTerminal groups the batch by terminal, preserving the claim order both
// within each shard and across the returned shards (first-seen order), so the
// result is deterministic for a given batch.
func shardByTerminal(batch []repo.FiscalSubmission) [][]repo.FiscalSubmission {
	index := make(map[terminalShardKey]int, len(batch))
	shards := make([][]repo.FiscalSubmission, 0, len(batch))
	for _, sub := range batch {
		key := shardKeyOf(sub)
		if i, ok := index[key]; ok {
			shards[i] = append(shards[i], sub)
			continue
		}
		index[key] = len(shards)
		shards = append(shards, []repo.FiscalSubmission{sub})
	}
	return shards
}

func (w *SubmissionWorker) process(ctx context.Context, sub repo.FiscalSubmission) error {
	var sale domain.FiscalSale
	if err := json.Unmarshal(sub.SalePayload, &sale); err != nil {
		// A corrupt payload will never parse; retrying is pointless. Fail the
		// payment now rather than burning the retry budget on it.
		return w.finalizeFailure(ctx, sub, fmt.Errorf("payment/worker: unmarshal sale payload: %w", err))
	}

	submitCtx, cancelSubmit := context.WithTimeout(ctx, w.cfg.SubmitTimeout)
	res, err := w.adapter.SubmitSale(submitCtx, sale)
	cancelSubmit()
	if err != nil {
		return w.handleSubmitError(ctx, sub, err)
	}

	// From here on the vendor side is already affected. Detach the bookkeeping
	// writes from worker cancellation so a shutdown between SubmitSale and the
	// status write cannot strand the row as 'pending' and trigger a re-POST of
	// a basket the vendor already has.
	writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), postSubmitWriteTimeout)
	defer cancelWrite()

	if res == nil {
		// Accepted; the vendor delivers the outcome asynchronously.
		if _, err := w.queue.MarkSubmitted(writeCtx, sub); err != nil {
			return fmt.Errorf("payment/worker: mark submitted: %w", err)
		}
		return nil
	}

	stampResultIdentity(res, sub)
	if err := w.sink.OnFiscalResult(writeCtx, *res); err != nil {
		return fmt.Errorf("payment/worker: apply fiscal result: %w", err)
	}
	return nil
}

// handleSubmitError schedules a retry, or gives up once the budget is spent.
func (w *SubmissionWorker) handleSubmitError(ctx context.Context, sub repo.FiscalSubmission, submitErr error) error {
	// A cancelled context is shutdown, not a device fault; leave the row claimed
	// so the stale-claim window returns it instead of consuming a retry.
	if ctx.Err() != nil {
		return fmt.Errorf("payment/worker: submit aborted: %w", submitErr)
	}

	retryCount := sub.RetryCount + 1
	if retryCount > w.cfg.MaxRetries {
		return w.finalizeFailure(ctx, sub, submitErr)
	}

	nextRetryAt := time.Now().UTC().Add(retryBackoff(retryCount))
	if err := w.queue.MarkRetry(ctx, sub, retryCount, nextRetryAt, submitErr.Error()); err != nil {
		// The row left 'pending' while we were submitting: a webhook for an
		// earlier attempt (submit timed out but the basket got through) already
		// drove it to a terminal state. That resolution wins; don't alarm.
		if errors.Is(err, repo.ErrNotFound) {
			w.logger.Info("payment: fiscal submission resolved elsewhere during retry",
				zap.Stringer("submission_id", sub.ID))
			return nil
		}
		return fmt.Errorf("payment/worker: mark retry: %w", err)
	}
	w.logger.Warn("payment: fiscal submit failed, retry scheduled",
		zap.Stringer("submission_id", sub.ID),
		zap.Int("retry_count", retryCount),
		zap.Time("next_retry_at", nextRetryAt),
		zap.Error(submitErr),
	)
	return nil
}

// finalizeFailure drives the submission and its payment to a failed terminal
// state through the sink, so the transition stays idempotent and single-sourced.
func (w *SubmissionWorker) finalizeFailure(ctx context.Context, sub repo.FiscalSubmission, cause error) error {
	res := domain.FiscalResult{
		SubmissionID:  sub.ID,
		TenantID:      sub.TenantID,
		BranchID:      sub.BranchID,
		PaymentID:     sub.PaymentID,
		Status:        domain.FiscalSubmissionFailed,
		DeviceType:    sub.AdapterType,
		FailureReason: cause.Error(),
		CompletedAt:   time.Now().UTC(),
	}
	if err := w.sink.OnFiscalResult(ctx, res); err != nil {
		return fmt.Errorf("payment/worker: finalize failure: %w", errors.Join(cause, err))
	}
	w.logger.Error("payment: fiscal submission exhausted retries",
		zap.Stringer("submission_id", sub.ID),
		zap.Stringer("payment_id", sub.PaymentID),
		zap.Error(cause),
	)
	return nil
}

// retryBackoff returns 1s, 2s, 4s, … capped at maxRetryBackoff. No jitter: rows
// are claimed with FOR UPDATE SKIP LOCKED, so concurrent workers never contend
// on the same submission and a synchronized wake-up costs nothing.
func retryBackoff(retryCount int) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	if retryCount > 16 { // guard the shift below
		return maxRetryBackoff
	}
	delay := time.Duration(1<<(retryCount-1)) * time.Second
	if delay > maxRetryBackoff {
		return maxRetryBackoff
	}
	return delay
}

// dbSubmissionQueue is the production submissionQueue backed by Postgres.
type dbSubmissionQueue struct {
	db   *db.Pool
	repo *repo.FiscalSubmissionRepo
}

// ClaimPending scans every tenant's submissions, which the per-tenant RLS policy
// forbids. WithAllTenantsTx is the platform's single named, explicit cross-tenant
// door (app.tenant_scope = 'all_tenants'); the fiscal_submissions policy must
// grant it. This is a background system actor, not a request-scoped one.
func (q *dbSubmissionQueue) ClaimPending(ctx context.Context, batchSize int, staleAfter time.Duration) ([]repo.FiscalSubmission, error) {
	var batch []repo.FiscalSubmission
	err := q.db.WithAllTenantsTx(ctx, func(tx pgx.Tx) error {
		var err error
		batch, err = q.repo.ClaimPending(ctx, tx, batchSize, staleAfter)
		return err
	})
	if err != nil {
		return nil, err
	}
	return batch, nil
}

func (q *dbSubmissionQueue) MarkSubmitted(ctx context.Context, sub repo.FiscalSubmission) (bool, error) {
	var transitioned bool
	err := q.db.WithTenantTx(ctx, sub.TenantID, func(tx pgx.Tx) error {
		var err error
		transitioned, err = q.repo.MarkSubmitted(ctx, tx, sub.ID)
		return err
	})
	return transitioned, err
}

func (q *dbSubmissionQueue) MarkRetry(ctx context.Context, sub repo.FiscalSubmission, retryCount int, nextRetryAt time.Time, lastError string) error {
	return q.db.WithTenantTx(ctx, sub.TenantID, func(tx pgx.Tx) error {
		return q.repo.MarkRetry(ctx, tx, sub.ID, retryCount, nextRetryAt, lastError)
	})
}
