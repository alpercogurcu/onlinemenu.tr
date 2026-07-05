package ws

// White-box (package ws, not ws_test) so these fast, container-free unit
// tests can construct conn directly. Heavier end-to-end scenarios (real
// NATS + Postgres, full handshake/authz) live in hub_test.go (package
// ws_test).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMain lives in hub_test.go (package ws_test) — a test binary may only
// declare one, regardless of which of the two test packages in this
// directory it's in — and also runs goleak.Find() over this file's tests.

// TestConn_Enqueue_DropsConnectionOnBackpressure proves the hub's
// backpressure policy (requirement #4): once a connection's outbound buffer
// is full, further sends are dropped and the connection is torn down
// (cancel called) instead of blocking the broadcaster.
func TestConn_Enqueue_DropsConnectionOnBackpressure(t *testing.T) {
	var cancelled atomic.Bool
	c := &conn{
		id:       uuid.New(),
		tenantID: uuid.New(),
		branchID: uuid.New(),
		send:     make(chan []byte, sendBufferSize),
		cancel:   func() { cancelled.Store(true) },
		logger:   zap.NewNop(),
	}

	for i := 0; i < sendBufferSize; i++ {
		require.True(t, c.enqueue([]byte("msg")), "buffer should accept up to sendBufferSize messages")
	}
	require.False(t, cancelled.Load(), "must not cancel while the buffer still has room")

	ok := c.enqueue([]byte("overflow"))
	require.False(t, ok, "enqueue must report failure once the buffer is full")
	require.True(t, cancelled.Load(), "a full buffer must tear the connection down instead of blocking the broadcaster")
}

// TestConn_HeartbeatPump_ClosesUnresponsivePeer proves heartbeat timeout
// disconnection: a peer that never answers pings (never reads at all, so it
// cannot even process the ping frame) must be disconnected within
// heartbeatInterval+heartbeatTimeout, not left dangling.
func TestConn_HeartbeatPump_ClosesUnresponsivePeer(t *testing.T) {
	upgraded := make(chan *websocket.Conn, 1)
	// handlerDone, not r.Context().Done(): once a connection is hijacked for
	// the WebSocket upgrade, net/http no longer reliably cancels the
	// request context on client disconnect, so the handler must be told
	// explicitly when to return (registered via t.Cleanup, LIFO, so it
	// unblocks and the handler returns BEFORE srv.Close() is called).
	handlerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsConn, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		upgraded <- wsConn
		<-handlerDone
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(handlerDone) })

	clientCtx, clientCancel := context.WithCancel(context.Background())
	t.Cleanup(clientCancel)
	wsURL := "ws" + srv.URL[len("http"):]
	clientConn, _, err := websocket.Dial(clientCtx, wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { clientConn.CloseNow() })
	// Deliberately never call Read/CloseRead on the client: an unanswered
	// ping is exactly the "unresponsive peer" this test simulates.

	serverConn := <-upgraded
	t.Cleanup(func() { serverConn.CloseNow() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	_ = serverConn.CloseRead(ctx) // mirrors production wiring; see ServeKitchenWS

	c := &conn{
		id:       uuid.New(),
		tenantID: uuid.New(),
		branchID: uuid.New(),
		ws:       serverConn,
		send:     make(chan []byte, sendBufferSize),
		cancel:   cancel,
		logger:   zap.NewNop(),
		timing: timing{
			heartbeatInterval: 30 * time.Millisecond,
			heartbeatTimeout:  50 * time.Millisecond,
			writeTimeout:      time.Second,
		},
	}

	done := make(chan struct{})
	go func() {
		c.heartbeatPump(ctx)
		close(done)
	}()

	select {
	case <-ctx.Done():
		// heartbeatPump called cancel() after a failed ping — success.
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeatPump did not close an unresponsive connection in time")
	}
	<-done
}
