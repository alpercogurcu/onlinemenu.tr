package apiclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

const kitchenSnapshotFrame = `{"type":"snapshot","orders":[` +
	`{"type":"order.placed","order_id":"o1","check_id":"c1","table_label":"Masa 7","source":"online_qr","status":"pending","seq":0,"occurred_at":"2026-09-19T14:32:00Z"},` +
	`{"type":"order.status_changed","order_id":"o2","source":"pos","status":"preparing","seq":0,"occurred_at":"2026-09-19T14:33:00Z"}]}`

func TestParseKitchenFrame(t *testing.T) {
	tests := []struct {
		name    string
		frame   string
		want    []string // "orderID/source/status"
		wantErr bool
	}{
		{"snapshot expands to its orders", kitchenSnapshotFrame, []string{"o1/online_qr/pending", "o2/pos/preparing"}, false},
		{"empty snapshot", `{"type":"snapshot","orders":[]}`, nil, false},
		{"live placed event", `{"type":"order.placed","order_id":"o3","source":"online_qr","status":"pending","seq":42}`, []string{"o3/online_qr/pending"}, false},
		{"live status change", `{"type":"order.status_changed","order_id":"o4","status":"accepted","seq":43}`, []string{"o4//accepted"}, false},
		{"unknown frame type is ignored, not an error", `{"type":"ping"}`, nil, false},
		{"malformed json", `{nope`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseKitchenFrame([]byte(tt.frame))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			var gotKeys []string
			for _, e := range got {
				gotKeys = append(gotKeys, e.OrderID+"/"+e.Source+"/"+e.Status)
			}
			if strings.Join(gotKeys, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("events = %v, want %v", gotKeys, tt.want)
			}
		})
	}
}

func TestParseKitchenFrame_DecodesRoutingFields(t *testing.T) {
	got, err := ParseKitchenFrame([]byte(kitchenSnapshotFrame))
	if err != nil {
		t.Fatal(err)
	}
	e := got[0]
	if e.Type != "order.placed" || e.CheckID == nil || *e.CheckID != "c1" || e.TableLabel != "Masa 7" {
		t.Fatalf("unexpected event: %+v", e)
	}
	if !e.OccurredAt.Equal(time.Date(2026, 9, 19, 14, 32, 0, 0, time.UTC)) {
		t.Fatalf("occurred_at = %v", e.OccurredAt)
	}
}

func newKitchenServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pos/ws/kitchen" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handler(w, r, conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_OpenKitchenStream_AuthenticatesAndReadsFrames(t *testing.T) {
	var gotAuth, gotBranch string
	srv := newKitchenServer(t, func(w http.ResponseWriter, r *http.Request, conn *websocket.Conn) {
		gotAuth = r.Header.Get("Authorization")
		gotBranch = r.URL.Query().Get("branch_id")
		_ = conn.WriteMessage(websocket.TextMessage, []byte(kitchenSnapshotFrame))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"order.placed","order_id":"o3","source":"online_qr","status":"pending"}`))
		time.Sleep(50 * time.Millisecond)
	})

	c := New(srv.URL, &memStore{token: "tok-1", saved: true})
	stream, err := c.OpenKitchenStream(t.Context(), "branch-1")
	if err != nil {
		t.Fatalf("OpenKitchenStream: %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil || len(first) != 2 {
		t.Fatalf("snapshot read: %v %+v", err, first)
	}
	second, err := stream.Next()
	if err != nil || len(second) != 1 || second[0].OrderID != "o3" {
		t.Fatalf("live read: %v %+v", err, second)
	}
	if gotAuth != "Bearer tok-1" || gotBranch != "branch-1" {
		t.Fatalf("handshake auth=%q branch=%q", gotAuth, gotBranch)
	}
}

func TestClient_OpenKitchenStream_NextReturnsErrorWhenServerCloses(t *testing.T) {
	srv := newKitchenServer(t, func(w http.ResponseWriter, r *http.Request, conn *websocket.Conn) {})
	c := New(srv.URL, &memStore{token: "tok", saved: true})
	stream, err := c.OpenKitchenStream(t.Context(), "b")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(); err == nil {
		t.Fatal("Next after server close: want error")
	}
}

func TestClient_OpenKitchenStream_ContextCancelUnblocksNext(t *testing.T) {
	srv := newKitchenServer(t, func(w http.ResponseWriter, r *http.Request, conn *websocket.Conn) {
		time.Sleep(2 * time.Second)
	})
	c := New(srv.URL, &memStore{token: "tok", saved: true})
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := c.OpenKitchenStream(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	done := make(chan error, 1)
	go func() { _, err := stream.Next(); done <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Next: want error after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not unblock on context cancel")
	}
}

func TestClient_OpenKitchenStream_HandshakeErrorsAreAPIErrors(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", status)
		}))
		c := New(srv.URL, &memStore{token: "tok", saved: true})
		_, err := c.OpenKitchenStream(t.Context(), "b")
		srv.Close()

		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Fatalf("status %d: err = %v, want *APIError with that status", status, err)
		}
	}
}

func TestClient_OpenKitchenStream_RecoversTokenOnce(t *testing.T) {
	var attempts atomic.Int32
	// Rejects the stale token with 401 before upgrading.
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.Header.Get("Authorization") != "Bearer fresh" {
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"snapshot","orders":[]}`))
	}))
	defer gate.Close()

	c := New(gate.URL, &memStore{token: "stale", saved: true})
	var recoveries atomic.Int32
	c.SetUnauthorizedRecovery(func(context.Context) (string, error) {
		recoveries.Add(1)
		return "fresh", nil
	})

	stream, err := c.OpenKitchenStream(t.Context(), "b")
	if err != nil {
		t.Fatalf("OpenKitchenStream: %v", err)
	}
	defer stream.Close()
	if recoveries.Load() != 1 || attempts.Load() != 2 {
		t.Fatalf("recoveries=%d attempts=%d, want 1 and 2", recoveries.Load(), attempts.Load())
	}
}

func TestClient_OpenKitchenStream_RejectsEmptyBranch(t *testing.T) {
	c := New("http://127.0.0.1:1", &memStore{token: "tok", saved: true})
	if _, err := c.OpenKitchenStream(t.Context(), ""); err == nil {
		t.Fatal("want error for empty branch id")
	}
}

func TestClient_OpenKitchenStream_IdleTimeoutDetectsSilentPeer(t *testing.T) {
	old := kitchenStreamIdleTimeout
	kitchenStreamIdleTimeout = 150 * time.Millisecond
	defer func() { kitchenStreamIdleTimeout = old }()

	srv := newKitchenServer(t, func(w http.ResponseWriter, r *http.Request, conn *websocket.Conn) {
		time.Sleep(time.Second) // never sends, never pings
	})
	c := New(srv.URL, &memStore{token: "tok", saved: true})
	stream, err := c.OpenKitchenStream(t.Context(), "b")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	start := time.Now()
	if _, err := stream.Next(); err == nil {
		t.Fatal("Next on a silent connection: want idle-timeout error")
	}
	if time.Since(start) > 800*time.Millisecond {
		t.Fatalf("idle detection took %v", time.Since(start))
	}
}
