package sync_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"onlinemenu.tr/internal/modules/edge-sync/domain"
	edgesync "onlinemenu.tr/internal/modules/edge-sync/sync"
)

func newHB(t *testing.T, url string, notify edgesync.StateNotifier) *edgesync.Heartbeat {
	t.Helper()
	cfg := edgesync.HeartbeatConfig{
		CloudHealthURL: url,
		Interval:       100 * time.Millisecond,
		DegradedAfter:  500 * time.Millisecond,
		OfflineAfter:   2,
	}
	return edgesync.NewHeartbeat(cfg, zaptest.NewLogger(t, zaptest.WrapOptions(zap.Development())), notify)
}

func TestHeartbeat_InitialStateIsOffline(t *testing.T) {
	hb := newHB(t, "", nil)
	if hb.State() != domain.ConnStateOffline {
		t.Fatalf("expected OFFLINE, got %s", hb.State())
	}
}

func TestHeartbeat_TransitionsToOnline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transitions := make([]domain.ConnState, 0)
	hb := newHB(t, server.URL, func(_, next domain.ConnState) {
		transitions = append(transitions, next)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	hb.Run(ctx)

	if len(transitions) == 0 {
		t.Fatal("expected at least one state transition")
	}
	// After successful pings from OFFLINE, should enter SYNCING first.
	if transitions[0] != domain.ConnStateSyncing {
		t.Fatalf("expected first transition to SYNCING, got %s", transitions[0])
	}
}

func TestHeartbeat_TransitionsToOfflineAfterFailures(t *testing.T) {
	// Server returns 500 to simulate cloud failure.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	var lastState domain.ConnState
	hb := newHB(t, server.URL, func(_, next domain.ConnState) {
		lastState = next
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	hb.Run(ctx)

	// After OfflineAfter=2 failures, should be OFFLINE.
	if lastState != domain.ConnStateOffline && lastState != domain.ConnStateDegraded {
		t.Fatalf("expected OFFLINE or DEGRADED after failures, got %s", lastState)
	}
}

func TestHeartbeat_NoURL_StaysOffline(t *testing.T) {
	hb := newHB(t, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	hb.Run(ctx)

	if hb.State() != domain.ConnStateOffline {
		t.Fatalf("expected OFFLINE with no URL, got %s", hb.State())
	}
}
