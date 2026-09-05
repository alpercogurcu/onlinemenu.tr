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
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// fakeStaleSessionStore is a unit-testable stand-in for dbStaleSessionStore,
// same rationale as fakeStaleStore in reconciliation_test.go: the sweep logic
// is exercised without a database.
type fakeStaleSessionStore struct {
	mu sync.Mutex

	sessions []domain.CashSession
	err      error
	calls    int
}

func (s *fakeStaleSessionStore) ListOpenOlderThan(_ context.Context, _ time.Time) ([]domain.CashSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.sessions, nil
}

var fixedWatchNow = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func newTestStaleSessionWatch(store staleSessionStore, logger *zap.Logger, cfg StaleSessionWatchConfig) *StaleSessionWatch {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &StaleSessionWatch{
		store:  store,
		cfg:    cfg.withDefaults(),
		logger: logger,
		now:    func() time.Time { return fixedWatchNow },
		warned: make(map[uuid.UUID]struct{}),
	}
}

func openSessionAge(age time.Duration) domain.CashSession {
	return domain.CashSession{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		Status:   domain.CashSessionOpened,
		OpenedAt: fixedWatchNow.Add(-age),
	}
}

// TestStaleSessionWatch_RunOnce_WarnsAboutEveryStaleSession is the core
// contract: every session ListOpenOlderThan returns is warned about exactly
// once per sweep, and no write ever happens (ADR-DATA-008 açıkları: no
// automatic close).
func TestStaleSessionWatch_RunOnce_WarnsAboutEveryStaleSession(t *testing.T) {
	sessions := []domain.CashSession{openSessionAge(21 * time.Hour), openSessionAge(30 * time.Hour)}
	store := &fakeStaleSessionStore{sessions: sessions}
	core, observed := observer.New(zapcore.DebugLevel)
	w := newTestStaleSessionWatch(store, zap.New(core), StaleSessionWatchConfig{MaxAge: 20 * time.Hour})

	stats, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Scanned)
	assert.Equal(t, 2, stats.Warned)

	logs := observed.FilterMessage("payment: cash session open past max age").All()
	require.Len(t, logs, 2, "one Warn log entry per stale session")
	for _, entry := range logs {
		assert.Equal(t, zapcore.WarnLevel, entry.Level)
	}
}

// TestStaleSessionWatch_RunOnce_EmptySweepIsANoOp guards the zero-rows path.
func TestStaleSessionWatch_RunOnce_EmptySweepIsANoOp(t *testing.T) {
	store := &fakeStaleSessionStore{}
	w := newTestStaleSessionWatch(store, nil, StaleSessionWatchConfig{})

	stats, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Zero(t, stats.Scanned)
	assert.Zero(t, stats.Warned)
}

// TestStaleSessionWatch_RunOnce_ListFailure_ReturnsError proves a store error
// surfaces rather than being swallowed.
func TestStaleSessionWatch_RunOnce_ListFailure_ReturnsError(t *testing.T) {
	store := &fakeStaleSessionStore{err: errors.New("rls denied")}
	w := newTestStaleSessionWatch(store, nil, StaleSessionWatchConfig{})

	stats, err := w.RunOnce(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list open cash sessions")
	assert.Zero(t, stats.Scanned)
}

// TestStaleSessionWatch_WarnsOncePerSession: a drawer stuck open for days
// must not re-log on every tick — with a 15m default interval an unbounded
// warn would flood the log for a session stuck open for a week.
func TestStaleSessionWatch_WarnsOncePerSession(t *testing.T) {
	sessions := []domain.CashSession{openSessionAge(21 * time.Hour)}
	store := &fakeStaleSessionStore{sessions: sessions}
	w := newTestStaleSessionWatch(store, nil, StaleSessionWatchConfig{MaxAge: 20 * time.Hour})

	first, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, first.Warned)

	for range 3 {
		again, err := w.RunOnce(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 1, again.Scanned, "the session is still stale")
		assert.Zero(t, again.Warned, "an already-warned session must not warn again")
	}
}

// TestStaleSessionWatch_ResolvedSessionWarnsAgainIfItRecurs proves the warn
// memory is pruned once a session leaves the stale set (it closed), so a
// genuinely different session opened later and left stuck is still reported.
func TestStaleSessionWatch_ResolvedSessionWarnsAgainIfItRecurs(t *testing.T) {
	s := openSessionAge(21 * time.Hour)
	store := &fakeStaleSessionStore{sessions: []domain.CashSession{s}}
	w := newTestStaleSessionWatch(store, nil, StaleSessionWatchConfig{MaxAge: 20 * time.Hour})

	first, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, first.Warned)

	// The session closes and leaves the stale set.
	store.mu.Lock()
	store.sessions = nil
	store.mu.Unlock()
	_, err = w.RunOnce(context.Background())
	require.NoError(t, err)

	// A new session (same id reused here only for test brevity) goes stale.
	store.mu.Lock()
	store.sessions = []domain.CashSession{s}
	store.mu.Unlock()
	again, err := w.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, again.Warned, "a recurrence must be reported, not suppressed forever")
}

func TestStaleSessionWatchConfig_Defaults(t *testing.T) {
	cfg := StaleSessionWatchConfig{}.withDefaults()
	assert.Equal(t, defaultStaleSessionInterval, cfg.Interval)
	assert.Equal(t, defaultStaleSessionMaxAge, cfg.MaxAge)
}

// TestStaleSessionWatch_RunExitsOnContextCancel is the goroutine-leak guard —
// TestMain's goleak.VerifyTestMain-adjacent check trips over any Run left
// running.
func TestStaleSessionWatch_RunExitsOnContextCancel(t *testing.T) {
	store := &fakeStaleSessionStore{}
	w := newTestStaleSessionWatch(store, nil, StaleSessionWatchConfig{Interval: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
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
