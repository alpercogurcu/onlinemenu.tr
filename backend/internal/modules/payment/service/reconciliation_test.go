package service

import (
	"context"
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

type fakeStaleStore struct {
	mu sync.Mutex

	rows  []repo.FiscalSubmission
	err   error
	calls int
}

func (s *fakeStaleStore) ListStaleSubmitted(_ context.Context, _ int, _ time.Duration) ([]repo.FiscalSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

// fixedNow anchors the sweep's clock so age thresholds are exact.
var fixedNow = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

func newTestReconciler(store staleSubmissionStore, sink domain.FiscalResultSink, cfg ReconcilerConfig) *Reconciler {
	return &Reconciler{
		store:  store,
		sink:   sink,
		cfg:    cfg.withDefaults(),
		logger: zap.NewNop(),
		now:    func() time.Time { return fixedNow },
		warned: make(map[uuid.UUID]struct{}),
	}
}

func staleSubmission(age time.Duration) repo.FiscalSubmission {
	submittedAt := fixedNow.Add(-age)
	return repo.FiscalSubmission{
		ID:          uuid.New(),
		TenantID:    uuid.New(),
		BranchID:    uuid.New(),
		PaymentID:   uuid.New(),
		AdapterType: "beko_x30tr_cloud",
		Status:      domain.FiscalSubmissionSubmitted,
		SubmittedAt: &submittedAt,
	}
}

// TestReconciler_RunOnce is the core safety property: a late fiscal result is
// only *warned* about. A payment may not be failed merely because a webhook was
// slow or lost — the device may well have printed the receipt. Even past the
// vendor's basket TTL the sweep stays read-only unless AutoExpire is explicitly
// enabled.
func TestReconciler_RunOnce(t *testing.T) {
	tests := []struct {
		name        string
		ages        []time.Duration
		staleAfter  time.Duration
		expireAfter time.Duration
		autoExpire  bool
		wantScanned int
		wantWarned  int
		wantExpired int
	}{
		{
			name:        "an overdue submission is warned about, never failed",
			ages:        []time.Duration{30 * time.Minute},
			expireAfter: 14 * 24 * time.Hour,
			wantScanned: 1,
			wantWarned:  1,
			wantExpired: 0,
		},
		{
			name:        "past the vendor TTL, the default config still only warns",
			ages:        []time.Duration{20 * 24 * time.Hour},
			expireAfter: 14 * 24 * time.Hour,
			autoExpire:  false,
			wantScanned: 1,
			wantWarned:  1,
			wantExpired: 0,
		},
		{
			name:        "a submission past the vendor basket TTL expires when AutoExpire is on",
			ages:        []time.Duration{15 * 24 * time.Hour},
			expireAfter: 14 * 24 * time.Hour,
			autoExpire:  true,
			wantScanned: 1,
			wantExpired: 1,
		},
		{
			name:        "exactly at the TTL boundary expires",
			ages:        []time.Duration{14 * 24 * time.Hour},
			expireAfter: 14 * 24 * time.Hour,
			autoExpire:  true,
			wantScanned: 1,
			wantExpired: 1,
		},
		{
			name:        "one nanosecond short of the TTL only warns",
			ages:        []time.Duration{14*24*time.Hour - time.Nanosecond},
			expireAfter: 14 * 24 * time.Hour,
			autoExpire:  true,
			wantScanned: 1,
			wantWarned:  1,
		},
		{
			name:        "a mixed batch is classified per row",
			ages:        []time.Duration{time.Hour, 20 * 24 * time.Hour, 2 * time.Hour, 15 * 24 * time.Hour},
			expireAfter: 14 * 24 * time.Hour,
			autoExpire:  true,
			wantScanned: 4,
			wantWarned:  2,
			wantExpired: 2,
		},
		{
			name:        "an empty sweep is a no-op",
			ages:        nil,
			expireAfter: 14 * 24 * time.Hour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]repo.FiscalSubmission, 0, len(tc.ages))
			for _, age := range tc.ages {
				rows = append(rows, staleSubmission(age))
			}

			store := &fakeStaleStore{rows: rows}
			sink := &fakeSink{}
			r := newTestReconciler(store, sink, ReconcilerConfig{
				StaleAfter:  tc.staleAfter,
				ExpireAfter: tc.expireAfter,
				AutoExpire:  tc.autoExpire,
			})

			stats, err := r.RunOnce(context.Background())
			require.NoError(t, err)

			assert.Equal(t, tc.wantScanned, stats.Scanned, "scanned")
			assert.Equal(t, tc.wantWarned, stats.Warned, "warned")
			assert.Equal(t, tc.wantExpired, stats.Expired, "expired")

			// Only expiries touch the sink; warnings must produce no writes.
			assert.Len(t, sink.results, tc.wantExpired, "sink writes must equal expiries")
			for _, res := range sink.results {
				assert.Equal(t, domain.FiscalSubmissionExpired, res.Status)
				assert.NotEmpty(t, res.FailureReason)
			}
		})
	}
}

// TestReconciler_ExpiryCarriesSubmissionIdentity ensures the expired result is
// routed to the right payment, since the sink keys every side effect off it.
func TestReconciler_ExpiryCarriesSubmissionIdentity(t *testing.T) {
	sub := staleSubmission(30 * 24 * time.Hour)
	store := &fakeStaleStore{rows: []repo.FiscalSubmission{sub}}
	sink := &fakeSink{}
	r := newTestReconciler(store, sink, ReconcilerConfig{AutoExpire: true})

	_, err := r.RunOnce(context.Background())
	require.NoError(t, err)

	require.Len(t, sink.results, 1)
	got := sink.results[0]
	assert.Equal(t, sub.ID, got.SubmissionID)
	assert.Equal(t, sub.TenantID, got.TenantID)
	assert.Equal(t, sub.BranchID, got.BranchID)
	assert.Equal(t, sub.PaymentID, got.PaymentID)
	assert.Equal(t, sub.AdapterType, got.DeviceType)
}

func TestReconciler_ListFailure_ReturnsError(t *testing.T) {
	store := &fakeStaleStore{err: errors.New("rls denied")}
	r := newTestReconciler(store, &fakeSink{}, ReconcilerConfig{})

	stats, err := r.RunOnce(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list stale submissions")
	assert.Zero(t, stats.Scanned)
}

// TestReconciler_SinkFailureDoesNotStopTheSweep: one payment that cannot be
// expired must not hide the rest of the backlog.
func TestReconciler_SinkFailureDoesNotStopTheSweep(t *testing.T) {
	rows := []repo.FiscalSubmission{
		staleSubmission(20 * 24 * time.Hour),
		staleSubmission(20 * 24 * time.Hour),
	}
	store := &fakeStaleStore{rows: rows}
	sink := &fakeSink{err: errors.New("db down")}
	r := newTestReconciler(store, sink, ReconcilerConfig{AutoExpire: true})

	stats, err := r.RunOnce(context.Background())
	require.NoError(t, err, "a per-row failure must not fail the sweep")
	assert.Equal(t, 2, stats.Scanned)
	assert.Zero(t, stats.Expired, "neither row could be expired")
}

// TestReconciler_NilSubmittedAtIsSkipped guards the defensive branch: the query
// filters these out, but a nil timestamp must never panic the sweep.
func TestReconciler_NilSubmittedAtIsSkipped(t *testing.T) {
	sub := staleSubmission(20 * 24 * time.Hour)
	sub.SubmittedAt = nil
	store := &fakeStaleStore{rows: []repo.FiscalSubmission{sub}}
	sink := &fakeSink{}
	r := newTestReconciler(store, sink, ReconcilerConfig{AutoExpire: true})

	stats, err := r.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Scanned)
	assert.Zero(t, stats.Warned)
	assert.Zero(t, stats.Expired)
	assert.Empty(t, sink.results)
}

func TestReconcilerConfig_Defaults(t *testing.T) {
	cfg := ReconcilerConfig{}.withDefaults()
	assert.Equal(t, defaultReconcileInterval, cfg.Interval)
	assert.Equal(t, defaultReconcileBatchSize, cfg.BatchSize)
	assert.Equal(t, defaultStaleAfter, cfg.StaleAfter)
	assert.Equal(t, defaultExpireAfter, cfg.ExpireAfter)
	assert.Less(t, cfg.StaleAfter, cfg.ExpireAfter,
		"operators must be warned long before a payment could be auto-failed")
	assert.False(t, cfg.AutoExpire,
		"the sweep must never fail payments by default: a lost webhook does not mean "+
			"the device failed to print (ADR-FISCAL-002 mandates TTL + warning, not a write)")
}

// TestReconciler_RunExitsOnContextCancel is the goroutine-leak guard.
func TestReconciler_RunExitsOnContextCancel(t *testing.T) {
	store := &fakeStaleStore{}
	r := newTestReconciler(store, &fakeSink{}, ReconcilerConfig{Interval: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()

	assert.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.calls > 0
	}, time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

// TestReconciler_WarnsOncePerSubmission: a row stuck for days must not re-log on
// every tick. With a 1m interval an unbounded warn would emit ~20k lines per
// stuck submission over the vendor's two-week basket lifetime.
func TestReconciler_WarnsOncePerSubmission(t *testing.T) {
	rows := []repo.FiscalSubmission{staleSubmission(2 * time.Hour), staleSubmission(3 * time.Hour)}
	store := &fakeStaleStore{rows: rows}
	r := newTestReconciler(store, &fakeSink{}, ReconcilerConfig{})

	first, err := r.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, first.Warned, "both rows are reported the first time")

	for range 5 {
		again, err := r.RunOnce(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 2, again.Scanned, "the rows are still stale")
		assert.Zero(t, again.Warned, "an already-reported row must not warn again")
	}
}

// TestReconciler_ResolvedSubmissionWarnsAgainIfItRecurs proves the warn memory is
// pruned: once a row leaves the stale set it is forgotten, so a genuine
// recurrence is reported rather than silently swallowed.
func TestReconciler_ResolvedSubmissionWarnsAgainIfItRecurs(t *testing.T) {
	sub := staleSubmission(2 * time.Hour)
	store := &fakeStaleStore{rows: []repo.FiscalSubmission{sub}}
	r := newTestReconciler(store, &fakeSink{}, ReconcilerConfig{})

	first, err := r.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, first.Warned)

	// The row resolves (webhook finally arrived) and leaves the stale set.
	store.mu.Lock()
	store.rows = nil
	store.mu.Unlock()
	_, err = r.RunOnce(context.Background())
	require.NoError(t, err)

	// It goes stale again later.
	store.mu.Lock()
	store.rows = []repo.FiscalSubmission{sub}
	store.mu.Unlock()
	again, err := r.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, again.Warned, "a recurrence must be reported, not suppressed forever")
}

// TestReconciler_ExpiredRowIsForgotten: after an expiry the row leaves the stale
// set, so no warn memory may linger for it.
func TestReconciler_ExpiredRowIsForgotten(t *testing.T) {
	sub := staleSubmission(20 * 24 * time.Hour)
	store := &fakeStaleStore{rows: []repo.FiscalSubmission{sub}}
	r := newTestReconciler(store, &fakeSink{}, ReconcilerConfig{AutoExpire: true})

	stats, err := r.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, stats.Expired)

	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Empty(t, r.warned, "an expired row must not retain warn state")
}
