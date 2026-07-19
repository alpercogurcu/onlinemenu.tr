package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/payment/domain"
	"onlinemenu.tr/internal/modules/payment/repo"
)

// Goroutine-leak coverage: TestRun_ExitsOnContextCancel asserts Run() returns
// on cancellation. A goleak.VerifyNone here would trip over the testcontainers
// goroutines that the external service_test TestMain keeps alive for the whole
// binary, so the exit assertion is the guard.

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type markRetryCall struct {
	submissionID uuid.UUID
	retryCount   int
	nextRetryAt  time.Time
	lastError    string
}

type fakeQueue struct {
	mu sync.Mutex

	batch     []repo.FiscalSubmission
	claimErr  error
	claimCall int

	submitted    []uuid.UUID
	markSubErr   error
	retries      []markRetryCall
	markRetryErr error
}

func (q *fakeQueue) ClaimPending(_ context.Context, _ int, _ time.Duration) ([]repo.FiscalSubmission, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.claimCall++
	if q.claimErr != nil {
		return nil, q.claimErr
	}
	batch := q.batch
	q.batch = nil // one claim per batch, like SKIP LOCKED
	return batch, nil
}

func (q *fakeQueue) MarkSubmitted(_ context.Context, sub repo.FiscalSubmission) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.markSubErr != nil {
		return false, q.markSubErr
	}
	q.submitted = append(q.submitted, sub.ID)
	return true, nil
}

func (q *fakeQueue) MarkRetry(_ context.Context, sub repo.FiscalSubmission, retryCount int, nextRetryAt time.Time, lastError string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.markRetryErr != nil {
		return q.markRetryErr
	}
	q.retries = append(q.retries, markRetryCall{sub.ID, retryCount, nextRetryAt, lastError})
	return nil
}

type fakeAdapter struct {
	mu sync.Mutex

	result   *domain.FiscalResult
	err      error
	calls    int
	lastSale domain.FiscalSale
	caps     domain.FiscalCapabilities
}

func (a *fakeAdapter) SubmitSale(_ context.Context, sale domain.FiscalSale) (*domain.FiscalResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	a.lastSale = sale
	if a.err != nil {
		return nil, a.err
	}
	if a.result == nil {
		return nil, nil
	}
	clone := *a.result
	return &clone, nil
}

func (a *fakeAdapter) VoidSale(_ context.Context, _ domain.FiscalSubmissionRef) (*domain.FiscalResult, error) {
	return nil, nil
}

func (a *fakeAdapter) Capabilities() domain.FiscalCapabilities { return a.caps }

type fakeSink struct {
	mu      sync.Mutex
	results []domain.FiscalResult
	err     error
}

func (s *fakeSink) OnFiscalResult(_ context.Context, res domain.FiscalResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.results = append(s.results, res)
	return nil
}

func newTestWorker(q submissionQueue, a domain.FiscalDeviceAdapter, s domain.FiscalResultSink, cfg SubmissionWorkerConfig) *SubmissionWorker {
	return &SubmissionWorker{
		queue:   q,
		adapter: a,
		sink:    s,
		cfg:     cfg.withDefaults(),
		logger:  zap.NewNop(),
	}
}

func newSubmission(t *testing.T, retryCount int) repo.FiscalSubmission {
	t.Helper()
	subID := uuid.New()
	tenantID := uuid.New()
	branchID := uuid.New()
	paymentID := uuid.New()

	payload, err := json.Marshal(domain.FiscalSale{
		SubmissionID: subID,
		TenantID:     tenantID,
		BranchID:     branchID,
		PaymentID:    paymentID,
		Currency:     "TRY",
		TotalMinor:   4200,
		Lines: []domain.FiscalLine{
			{Name: "Satis", UnitPriceMinor: 4200, QuantityMilli: 1000, Unit: "C62"},
		},
	})
	require.NoError(t, err)

	return repo.FiscalSubmission{
		ID:          subID,
		TenantID:    tenantID,
		BranchID:    branchID,
		PaymentID:   paymentID,
		AdapterType: "mock",
		Status:      domain.FiscalSubmissionPending,
		SalePayload: payload,
		RetryCount:  retryCount,
	}
}

// ---------------------------------------------------------------------------
// Dispatch routing
// ---------------------------------------------------------------------------

func TestSubmissionWorker_Process(t *testing.T) {
	errDevice := errors.New("device unreachable")

	tests := []struct {
		name         string
		retryCount   int
		corruptSale  bool
		adapterRes   *domain.FiscalResult
		adapterErr   error
		maxRetries   int
		wantAdapter  int
		wantSubmit   int
		wantSink     int
		wantRetries  int
		wantSinkStat domain.FiscalSubmissionStatus
		wantRetryNum int
		wantBackoff  time.Duration
	}{
		{
			name:         "synchronous completion routes straight to the sink",
			adapterRes:   &domain.FiscalResult{Status: domain.FiscalSubmissionCompleted, ReceiptNo: "MOCK-1"},
			wantAdapter:  1,
			wantSink:     1,
			wantSinkStat: domain.FiscalSubmissionCompleted,
		},
		{
			name:        "nil result parks the row in submitted",
			adapterRes:  nil,
			wantAdapter: 1,
			wantSubmit:  1,
		},
		{
			name:         "first transient error schedules a 1s retry",
			adapterErr:   errDevice,
			retryCount:   0,
			wantAdapter:  1,
			wantRetries:  1,
			wantRetryNum: 1,
			wantBackoff:  1 * time.Second,
		},
		{
			name:         "third transient error schedules a 4s retry",
			adapterErr:   errDevice,
			retryCount:   2,
			wantAdapter:  1,
			wantRetries:  1,
			wantRetryNum: 3,
			wantBackoff:  4 * time.Second,
		},
		{
			name:         "exhausted retry budget fails the payment through the sink",
			adapterErr:   errDevice,
			retryCount:   5,
			maxRetries:   5,
			wantAdapter:  1,
			wantSink:     1,
			wantSinkStat: domain.FiscalSubmissionFailed,
		},
		{
			name:         "corrupt payload fails immediately without touching the device",
			corruptSale:  true,
			wantAdapter:  0,
			wantSink:     1,
			wantSinkStat: domain.FiscalSubmissionFailed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sub := newSubmission(t, tc.retryCount)
			if tc.corruptSale {
				sub.SalePayload = []byte("{not json")
			}

			q := &fakeQueue{}
			a := &fakeAdapter{result: tc.adapterRes, err: tc.adapterErr}
			s := &fakeSink{}
			w := newTestWorker(q, a, s, SubmissionWorkerConfig{MaxRetries: tc.maxRetries})

			before := time.Now().UTC()
			require.NoError(t, w.process(context.Background(), sub))

			assert.Equal(t, tc.wantAdapter, a.calls, "adapter call count")
			assert.Len(t, q.submitted, tc.wantSubmit, "MarkSubmitted count")
			assert.Len(t, s.results, tc.wantSink, "sink call count")
			assert.Len(t, q.retries, tc.wantRetries, "MarkRetry count")

			if tc.wantSink > 0 {
				got := s.results[0]
				assert.Equal(t, tc.wantSinkStat, got.Status)
				// Identity always comes from our row, never from the adapter.
				assert.Equal(t, sub.ID, got.SubmissionID)
				assert.Equal(t, sub.TenantID, got.TenantID)
				assert.Equal(t, sub.BranchID, got.BranchID)
				assert.Equal(t, sub.PaymentID, got.PaymentID)
			}
			if tc.wantRetries > 0 {
				got := q.retries[0]
				assert.Equal(t, sub.ID, got.submissionID)
				assert.Equal(t, tc.wantRetryNum, got.retryCount)
				assert.NotEmpty(t, got.lastError, "last_error must record why the submit failed")
				assert.WithinDuration(t, before.Add(tc.wantBackoff), got.nextRetryAt, 2*time.Second)
			}
		})
	}
}

// TestSubmissionWorker_Process_StampsIdentityOverAdapterValues guards the rule
// that an adapter's echoed identifiers are never trusted: a driver that returns
// a stale or empty submission id must not misroute the result to another
// payment.
func TestSubmissionWorker_Process_StampsIdentityOverAdapterValues(t *testing.T) {
	sub := newSubmission(t, 0)
	rogue := uuid.New()

	q := &fakeQueue{}
	a := &fakeAdapter{result: &domain.FiscalResult{
		SubmissionID: rogue,
		TenantID:     rogue,
		PaymentID:    rogue,
		Status:       domain.FiscalSubmissionCompleted,
	}}
	s := &fakeSink{}
	w := newTestWorker(q, a, s, SubmissionWorkerConfig{})

	require.NoError(t, w.process(context.Background(), sub))
	require.Len(t, s.results, 1)
	assert.Equal(t, sub.ID, s.results[0].SubmissionID)
	assert.Equal(t, sub.TenantID, s.results[0].TenantID)
	assert.Equal(t, sub.PaymentID, s.results[0].PaymentID)
}

// ---------------------------------------------------------------------------
// RunOnce
// ---------------------------------------------------------------------------

func TestRunOnce_ClaimFailure_ReturnsError(t *testing.T) {
	q := &fakeQueue{claimErr: errors.New("rls denied")}
	w := newTestWorker(q, &fakeAdapter{}, &fakeSink{}, SubmissionWorkerConfig{})

	n, err := w.RunOnce(context.Background())
	require.Error(t, err)
	assert.Zero(t, n)
	assert.Contains(t, err.Error(), "claim pending")
}

// TestRunOnce_OnePoisonousSubmissionDoesNotStallTheBatch asserts a per-row
// failure is isolated: the remaining claimed submissions still reach the device.
func TestRunOnce_OnePoisonousSubmissionDoesNotStallTheBatch(t *testing.T) {
	good1 := newSubmission(t, 0)
	poison := newSubmission(t, 0)
	poison.SalePayload = []byte("{not json")
	good2 := newSubmission(t, 0)

	q := &fakeQueue{batch: []repo.FiscalSubmission{good1, poison, good2}}
	a := &fakeAdapter{result: &domain.FiscalResult{Status: domain.FiscalSubmissionCompleted}}
	s := &fakeSink{err: errors.New("sink down")} // every row fails at the sink
	w := newTestWorker(q, a, s, SubmissionWorkerConfig{})

	n, err := w.RunOnce(context.Background())
	require.NoError(t, err, "a per-row failure must not fail the cycle")
	assert.Equal(t, 0, n, "all three rows failed at the sink")
	// The poison row never reaches the device; both healthy rows still do —
	// iteration continued past the failure instead of aborting the batch.
	assert.Equal(t, 2, a.calls)
}

// TestRunOnce_HealthyRowsSurviveAPoisonNeighbour isolates the same guarantee
// with a working sink: the corrupt row fails, the healthy ones complete.
//
// Asserted by payment id rather than by slice index: these rows belong to
// different terminals, so they are dispatched concurrently and the order they
// reach the sink in is deliberately not guaranteed.
func TestRunOnce_HealthyRowsSurviveAPoisonNeighbour(t *testing.T) {
	poison := newSubmission(t, 0)
	poison.SalePayload = []byte("{not json")
	good := newSubmission(t, 0)

	q := &fakeQueue{batch: []repo.FiscalSubmission{poison, good}}
	a := &fakeAdapter{result: &domain.FiscalResult{Status: domain.FiscalSubmissionCompleted}}
	s := &fakeSink{}
	w := newTestWorker(q, a, s, SubmissionWorkerConfig{})

	n, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n, "both rows resolved: one failed terminally, one completed")

	byPayment := make(map[uuid.UUID]domain.FiscalSubmissionStatus, len(s.results))
	for _, res := range s.results {
		byPayment[res.PaymentID] = res.Status
	}
	assert.Equal(t, domain.FiscalSubmissionFailed, byPayment[poison.PaymentID])
	assert.Equal(t, domain.FiscalSubmissionCompleted, byPayment[good.PaymentID])
}

func TestRunOnce_EmptyBatch(t *testing.T) {
	q := &fakeQueue{}
	a := &fakeAdapter{}
	w := newTestWorker(q, a, &fakeSink{}, SubmissionWorkerConfig{})

	n, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Zero(t, a.calls)
}

func TestRunOnce_AsyncAdapter_MarksSubmittedForWholeBatch(t *testing.T) {
	subs := []repo.FiscalSubmission{newSubmission(t, 0), newSubmission(t, 0)}
	q := &fakeQueue{batch: subs}
	a := &fakeAdapter{result: nil} // cloud adapter: result arrives by webhook
	s := &fakeSink{}
	w := newTestWorker(q, a, s, SubmissionWorkerConfig{})

	n, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Len(t, q.submitted, 2)
	assert.Empty(t, s.results, "async adapter must not reach the sink from the worker")
}

// ---------------------------------------------------------------------------
// Terminal sharding & bounded parallelism
// ---------------------------------------------------------------------------

// onDevice repoints a submission at an explicit tenant/branch — the pair that
// actually selects the physical device — so a test controls its shard.
func onDevice(sub repo.FiscalSubmission, tenantID, branchID uuid.UUID) repo.FiscalSubmission {
	sub.TenantID = tenantID
	sub.BranchID = branchID
	return sub
}

func TestShardByTerminal(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	branch1, branch2 := uuid.New(), uuid.New()

	withBranch := func(sub repo.FiscalSubmission, tenantID, branchID uuid.UUID, serial string) repo.FiscalSubmission {
		sub = onDevice(sub, tenantID, branchID)
		sub.TerminalSerial = serial
		return sub
	}

	tests := []struct {
		name  string
		batch func(t *testing.T) []repo.FiscalSubmission
		want  [][]int // expected shards, as indexes into the batch
	}{
		{
			name: "same device collapses into one ordered shard",
			batch: func(t *testing.T) []repo.FiscalSubmission {
				return []repo.FiscalSubmission{
					withBranch(newSubmission(t, 0), tenantA, branch1, "SER-1"),
					withBranch(newSubmission(t, 0), tenantA, branch1, "SER-1"),
					withBranch(newSubmission(t, 0), tenantA, branch1, "SER-1"),
				}
			},
			want: [][]int{{0, 1, 2}},
		},
		{
			name: "distinct branches shard apart, interleaving preserved",
			batch: func(t *testing.T) []repo.FiscalSubmission {
				return []repo.FiscalSubmission{
					withBranch(newSubmission(t, 0), tenantA, branch1, "SER-1"),
					withBranch(newSubmission(t, 0), tenantA, branch2, "SER-2"),
					withBranch(newSubmission(t, 0), tenantA, branch1, "SER-1"),
				}
			},
			want: [][]int{{0, 2}, {1}},
		},
		{
			name: "the same branch id in another tenant is a different device",
			batch: func(t *testing.T) []repo.FiscalSubmission {
				return []repo.FiscalSubmission{
					withBranch(newSubmission(t, 0), tenantA, branch1, "SER-1"),
					withBranch(newSubmission(t, 0), tenantB, branch1, "SER-1"),
				}
			},
			want: [][]int{{0}, {1}},
		},
		{
			// The load-bearing case: the submit path routes by branch alone
			// (domain.FiscalSale has no serial), so mixed serials within a branch
			// all print on one device and must share a single ordered shard.
			// Splitting them would reorder that device's receipts.
			name: "mixed serials in one branch stay in a single ordered shard",
			batch: func(t *testing.T) []repo.FiscalSubmission {
				return []repo.FiscalSubmission{
					withBranch(newSubmission(t, 0), tenantA, branch1, "SER-1"),
					withBranch(newSubmission(t, 0), tenantA, branch1, ""),
					withBranch(newSubmission(t, 0), tenantA, branch1, "SER-2"),
				}
			},
			want: [][]int{{0, 1, 2}},
		},
		{
			name: "empty serials shard by branch",
			batch: func(t *testing.T) []repo.FiscalSubmission {
				return []repo.FiscalSubmission{
					withBranch(newSubmission(t, 0), tenantA, branch1, ""),
					withBranch(newSubmission(t, 0), tenantA, branch1, ""),
					withBranch(newSubmission(t, 0), tenantA, branch2, ""),
				}
			},
			want: [][]int{{0, 1}, {2}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			batch := tc.batch(t)
			shards := shardByTerminal(batch)

			got := make([][]int, 0, len(shards))
			for _, shard := range shards {
				ids := make([]int, 0, len(shard))
				for _, sub := range shard {
					for i, orig := range batch {
						if orig.ID == sub.ID {
							ids = append(ids, i)
							break
						}
					}
				}
				got = append(got, ids)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// blockingAdapter records dispatch order and in-flight concurrency, and can hold
// a chosen submission hostage. Unlike fakeAdapter it does not hold its mutex
// across the call, so it can actually observe parallelism.
type blockingAdapter struct {
	mu       sync.Mutex
	started  []uuid.UUID
	finished []uuid.UUID
	inFlight int
	maxSeen  int

	release  chan struct{} // closed to free the blocked submissions
	blockID  uuid.UUID     // submission held until release is closed
	blockAll bool          // hold every submission instead of just blockID
}

func (a *blockingAdapter) SubmitSale(ctx context.Context, sale domain.FiscalSale) (*domain.FiscalResult, error) {
	a.mu.Lock()
	a.started = append(a.started, sale.SubmissionID)
	a.inFlight++
	if a.inFlight > a.maxSeen {
		a.maxSeen = a.inFlight
	}
	blocked := a.blockAll || sale.SubmissionID == a.blockID
	a.mu.Unlock()

	if blocked {
		select {
		case <-a.release:
		case <-ctx.Done():
		}
	}

	a.mu.Lock()
	a.inFlight--
	a.finished = append(a.finished, sale.SubmissionID)
	a.mu.Unlock()

	return &domain.FiscalResult{Status: domain.FiscalSubmissionCompleted}, nil
}

func (a *blockingAdapter) VoidSale(context.Context, domain.FiscalSubmissionRef) (*domain.FiscalResult, error) {
	return nil, nil
}

func (a *blockingAdapter) Capabilities() domain.FiscalCapabilities {
	return domain.FiscalCapabilities{}
}

func (a *blockingAdapter) snapshot() (started, finished []uuid.UUID, maxSeen int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]uuid.UUID(nil), a.started...), append([]uuid.UUID(nil), a.finished...), a.maxSeen
}

// TestRunOnce_SameTerminalStaysSequentialAndOrdered is the receipt-order
// guarantee: a fiscal device must print in claim order, so one device's rows
// are never overlapped or reordered no matter how much headroom the pool has.
// The differing serials are deliberate — they must not buy any parallelism,
// because the submit path routes on the branch alone.
func TestRunOnce_SameTerminalStaysSequentialAndOrdered(t *testing.T) {
	tenantID, branchID := uuid.New(), uuid.New()
	subs := []repo.FiscalSubmission{
		onDevice(newSubmission(t, 0), tenantID, branchID),
		onDevice(newSubmission(t, 0), tenantID, branchID),
		onDevice(newSubmission(t, 0), tenantID, branchID),
	}
	subs[1].TerminalSerial = "SER-OTHER"

	q := &fakeQueue{batch: subs}
	a := &blockingAdapter{}
	w := newTestWorker(q, a, &fakeSink{}, SubmissionWorkerConfig{MaxConcurrency: 8})

	n, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	started, _, maxSeen := a.snapshot()
	assert.Equal(t, 1, maxSeen, "one terminal must never have two baskets in flight")
	want := []uuid.UUID{subs[0].ID, subs[1].ID, subs[2].ID}
	assert.Equal(t, want, started, "receipts must be submitted in claim order")
}

// TestRunOnce_SlowTerminalDoesNotBlockOthers is the O3 regression guard: before
// sharding, a single hung terminal delayed every other tenant's submissions.
func TestRunOnce_SlowTerminalDoesNotBlockOthers(t *testing.T) {
	tenantID := uuid.New()
	slow := onDevice(newSubmission(t, 0), tenantID, uuid.New())
	fast1 := onDevice(newSubmission(t, 0), tenantID, uuid.New())
	fast2 := onDevice(newSubmission(t, 0), tenantID, uuid.New())

	q := &fakeQueue{batch: []repo.FiscalSubmission{slow, fast1, fast2}}
	a := &blockingAdapter{release: make(chan struct{}), blockID: slow.ID}
	w := newTestWorker(q, a, &fakeSink{}, SubmissionWorkerConfig{MaxConcurrency: 4})

	done := make(chan int, 1)
	go func() {
		n, err := w.RunOnce(context.Background())
		assert.NoError(t, err)
		done <- n
	}()

	// The healthy terminals must finish while the slow one is still hostage.
	require.Eventually(t, func() bool {
		_, finished, _ := a.snapshot()
		return len(finished) == 2
	}, 2*time.Second, 5*time.Millisecond, "healthy terminals blocked behind the slow one")

	select {
	case <-done:
		t.Fatal("RunOnce returned before the slow terminal was released")
	default:
	}

	close(a.release)
	select {
	case n := <-done:
		assert.Equal(t, 3, n)
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnce did not drain after the slow terminal was released")
	}
}

// TestRunOnce_HonoursMaxConcurrency keeps the pool bounded: an unbounded
// fan-out would exhaust the pgx pool during the post-submit writes.
func TestRunOnce_HonoursMaxConcurrency(t *testing.T) {
	tenantID := uuid.New()
	const terminals = 8
	batch := make([]repo.FiscalSubmission, 0, terminals)
	for range terminals {
		batch = append(batch, onDevice(newSubmission(t, 0), tenantID, uuid.New()))
	}

	q := &fakeQueue{batch: batch}
	// Every submission blocks, so all admitted slots pile up at once and maxSeen
	// reflects the true ceiling rather than a scheduling accident.
	a := &blockingAdapter{release: make(chan struct{}), blockAll: true}
	w := newTestWorker(q, a, &fakeSink{}, SubmissionWorkerConfig{MaxConcurrency: 3})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := w.RunOnce(context.Background())
		assert.NoError(t, err)
	}()

	require.Eventually(t, func() bool {
		_, _, maxSeen := a.snapshot()
		return maxSeen == 3
	}, 2*time.Second, 5*time.Millisecond, "pool never reached its configured ceiling")

	close(a.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnce did not drain")
	}

	_, _, maxSeen := a.snapshot()
	assert.LessOrEqual(t, maxSeen, 3, "pool exceeded MaxConcurrency")
}

// TestRunOnce_CancellationStopsDispatch ensures a shutdown mid-batch surfaces
// the cancellation instead of driving the remaining rows at a dead context.
func TestRunOnce_CancellationStopsDispatch(t *testing.T) {
	tenantID, branchID := uuid.New(), uuid.New()
	batch := []repo.FiscalSubmission{
		onDevice(newSubmission(t, 0), tenantID, branchID),
		onDevice(newSubmission(t, 0), tenantID, branchID),
	}

	q := &fakeQueue{batch: batch}
	a := &blockingAdapter{}
	w := newTestWorker(q, a, &fakeSink{}, SubmissionWorkerConfig{MaxConcurrency: 4})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := w.RunOnce(ctx)
	require.ErrorIs(t, err, context.Canceled)

	started, _, _ := a.snapshot()
	assert.Empty(t, started, "no basket may be dispatched on a cancelled context")
}

func TestSubmissionWorkerConfig_MaxConcurrencyDefault(t *testing.T) {
	assert.Equal(t, defaultMaxConcurrency, SubmissionWorkerConfig{}.withDefaults().MaxConcurrency)
	assert.Equal(t, defaultMaxConcurrency, SubmissionWorkerConfig{MaxConcurrency: -1}.withDefaults().MaxConcurrency)
	assert.Equal(t, 2, SubmissionWorkerConfig{MaxConcurrency: 2}.withDefaults().MaxConcurrency)
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// TestRun_ExitsOnContextCancel is the goroutine-leak guard: Run must observe
// cancellation and return (TestMain runs goleak.VerifyTestMain).
func TestRun_ExitsOnContextCancel(t *testing.T) {
	q := &fakeQueue{}
	w := newTestWorker(q, &fakeAdapter{}, &fakeSink{}, SubmissionWorkerConfig{Interval: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	// Let at least one tick fire before cancelling.
	assert.Eventually(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.claimCall > 0
	}, time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

// ---------------------------------------------------------------------------
// Backoff
// ---------------------------------------------------------------------------

func TestRetryBackoff(t *testing.T) {
	tests := []struct {
		retry int
		want  time.Duration
	}{
		{retry: 0, want: 1 * time.Second}, // clamped to the first attempt
		{retry: 1, want: 1 * time.Second},
		{retry: 2, want: 2 * time.Second},
		{retry: 3, want: 4 * time.Second},
		{retry: 4, want: 8 * time.Second},
		{retry: 5, want: 16 * time.Second},
		{retry: 7, want: 60 * time.Second}, // capped
		{retry: 64, want: 60 * time.Second},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, retryBackoff(tc.retry), "retry=%d", tc.retry)
	}
}

// ---------------------------------------------------------------------------
// VoidSale guards (no database needed: both checks precede any query)
// ---------------------------------------------------------------------------

// TestVoidSale_UnsupportedCapability_Rejected ensures we never issue a void to a
// driver that cannot honour it (Hugin/Ingenico may not) — the capability flag is
// checked before any database work.
func TestVoidSale_UnsupportedCapability_Rejected(t *testing.T) {
	svc := &PaymentService{
		fiscal: &fakeAdapter{caps: domain.FiscalCapabilities{VoidSale: false}},
		logger: zap.NewNop(),
	}

	err := svc.VoidSale(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support voiding")
}

// ---------------------------------------------------------------------------
// Adapter type resolution
// ---------------------------------------------------------------------------

type namedAdapter struct{ fakeAdapter }

func (*namedAdapter) AdapterType() string { return "beko_x30tr_cloud" }

func TestAdapterTypeOf(t *testing.T) {
	tests := []struct {
		name    string
		adapter domain.FiscalDeviceAdapter
		want    string
	}{
		{"self-reporting driver wins", &namedAdapter{}, "beko_x30tr_cloud"},
		{"mock is recognised structurally", domain.MockFiscalAdapter{}, "mock"},
		{"an unknown driver is never mislabelled as mock", &fakeAdapter{}, "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, adapterTypeOf(tc.adapter))
		})
	}
}

// ---------------------------------------------------------------------------
// buildFiscalSale
// ---------------------------------------------------------------------------

func TestBuildFiscalSale_SynthesizesSingleLineWhenBasketOmitted(t *testing.T) {
	subID, paymentID := uuid.New(), uuid.New()
	req := RegisterSaleRequest{
		TenantID:    uuid.New(),
		BranchID:    uuid.New(),
		Method:      domain.PaymentMethodCash,
		AmountTotal: 4200,
		Currency:    "TRY",
	}

	sale := buildFiscalSale(subID, domain.Payment{ID: paymentID}, req)

	assert.Equal(t, subID, sale.SubmissionID)
	assert.Equal(t, paymentID, sale.PaymentID)
	assert.Equal(t, int64(4200), sale.TotalMinor)
	require.Len(t, sale.Lines, 1)
	assert.Equal(t, "Satis", sale.Lines[0].Name)
	assert.Equal(t, int64(4200), sale.Lines[0].UnitPriceMinor)
	assert.Equal(t, int64(1000), sale.Lines[0].QuantityMilli, "1 adet = 1000 milli")
	assert.Equal(t, 0, sale.Lines[0].TaxRatePermyriad)
	assert.Equal(t, uuid.Nil, sale.Lines[0].CategoryID)

	require.Len(t, sale.Payments, 1, "the payment plan mirrors the chosen method")
	assert.Equal(t, domain.PaymentMethodCash, sale.Payments[0].Method)
	assert.Equal(t, int64(4200), sale.Payments[0].AmountMinor)
}

func TestBuildFiscalSale_PreservesCallerBasket(t *testing.T) {
	categoryID := uuid.New()
	req := RegisterSaleRequest{
		Method:      domain.PaymentMethodTerminal,
		AmountTotal: 4200,
		Lines: []domain.FiscalLine{
			{Name: "Lahmacun", UnitPriceMinor: 2100, QuantityMilli: 2000, TaxRatePermyriad: 1000, CategoryID: categoryID, Unit: "C62"},
		},
		Meta: domain.FiscalMeta{TableLabel: "Masa 5", WaiterName: "Ayse", CheckNumber: 12},
	}

	sale := buildFiscalSale(uuid.New(), domain.Payment{ID: uuid.New()}, req)

	require.Len(t, sale.Lines, 1)
	assert.Equal(t, "Lahmacun", sale.Lines[0].Name)
	assert.Equal(t, categoryID, sale.Lines[0].CategoryID)
	assert.Equal(t, "Masa 5", sale.Meta.TableLabel)
	assert.Equal(t, 12, sale.Meta.CheckNumber)
}
