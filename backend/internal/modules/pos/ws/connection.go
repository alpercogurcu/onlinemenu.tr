package ws

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// sendBufferSize bounds the per-connection outbound queue. A slow KDS
	// client (or a dead TCP peer the OS hasn't noticed yet) must never be
	// allowed to block the hub's broadcast loop — once full, the connection
	// is dropped outright; the client is expected to reconnect and recover
	// state via the snapshot message rather than the hub buffering forever.
	sendBufferSize = 64

	// defaultHeartbeatInterval is how often the server pings an idle
	// connection in production. Overridable per-Hub (see Hub.timing) so
	// tests do not have to wait 30s+ for a heartbeat-timeout scenario.
	defaultHeartbeatInterval = 30 * time.Second

	// defaultHeartbeatTimeout bounds how long a single ping may take to be
	// answered before the connection is considered dead.
	defaultHeartbeatTimeout = 10 * time.Second

	// defaultWriteTimeout bounds a single outbound frame write.
	defaultWriteTimeout = 5 * time.Second
)

// timing groups the connection lifecycle durations a Hub applies to every
// connection it accepts. Kept as a value (not loose constants) so tests can
// shrink heartbeat timing without waiting out production-scale intervals —
// see export_test.go.
type timing struct {
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	writeTimeout      time.Duration
}

func defaultTiming() timing {
	return timing{
		heartbeatInterval: defaultHeartbeatInterval,
		heartbeatTimeout:  defaultHeartbeatTimeout,
		writeTimeout:      defaultWriteTimeout,
	}
}

// conn is one accepted kitchen WebSocket connection, registered in exactly
// one Hub room (tenant+branch).
type conn struct {
	id       uuid.UUID
	tenantID uuid.UUID
	branchID uuid.UUID

	ws     *websocket.Conn
	send   chan []byte
	cancel context.CancelFunc
	logger *zap.Logger
	timing timing
}

func newConn(tenantID, branchID uuid.UUID, wsConn *websocket.Conn, cancel context.CancelFunc, logger *zap.Logger, t timing) *conn {
	return &conn{
		id:       uuid.New(),
		tenantID: tenantID,
		branchID: branchID,
		ws:       wsConn,
		send:     make(chan []byte, sendBufferSize),
		cancel:   cancel,
		logger:   logger,
		timing:   t,
	}
}

// enqueue attempts a non-blocking send of a pre-marshaled message. If the
// connection's outbound buffer is full, the connection is torn down
// (backpressure policy, requirement #4) instead of blocking or dropping
// silently only from the caller's perspective — the caller (Hub.broadcast)
// simply moves on to the next connection in the room.
//
// Returns false if the message could not be queued (buffer full or the
// connection is already shutting down), true otherwise.
func (c *conn) enqueue(data []byte) bool {
	select {
	case c.send <- data:
		return true
	default:
		c.logger.Warn("kitchen ws: send buffer full, dropping connection",
			zap.String("conn_id", c.id.String()),
			zap.String("tenant_id", c.tenantID.String()),
			zap.String("branch_id", c.branchID.String()),
		)
		c.cancel()
		return false
	}
}

// writePump owns all writes to the underlying connection (coder/websocket
// forbids concurrent writers) and exits when ctx is cancelled or a write
// fails.
func (c *conn) writePump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-c.send:
			if !ok {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, c.timing.writeTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, data)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

// heartbeatPump periodically pings the peer. Ping requires a concurrent
// Reader — the caller is expected to have already called ws.CloseRead(ctx)
// so control frames (pong/close) are consumed automatically. A failed or
// timed-out ping tears the connection down via cancel, mirroring a TCP-level
// dead-peer detection that the OS alone would take much longer to surface.
func (c *conn) heartbeatPump(ctx context.Context) {
	ticker := time.NewTicker(c.timing.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, c.timing.heartbeatTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.logger.Info("kitchen ws: heartbeat timeout, closing connection",
					zap.String("conn_id", c.id.String()),
					zap.Error(err),
				)
				c.cancel()
				return
			}
		}
	}
}
