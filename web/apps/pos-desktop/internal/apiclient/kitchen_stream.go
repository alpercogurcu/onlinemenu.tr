package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// kitchenStreamPath mirrors backend/internal/modules/pos/ws/handler.go's
// KitchenWSPath. Keep in sync if the backend route ever moves.
const kitchenStreamPath = "/api/v1/pos/ws/kitchen"

const (
	kitchenHandshakeTimeout = 10 * time.Second

	// kitchenStreamMaxFrame bounds a single inbound frame. A busy branch's
	// snapshot is a few hundred rows (~250 B each); 8 MiB is far above any
	// real snapshot and still stops a broken peer from exhausting memory.
	kitchenStreamMaxFrame = 8 << 20
)

// kitchenStreamIdleTimeout is how long a connection may be completely silent
// before it is declared dead. The backend pings every 30s, so 2.5 intervals
// of silence means the peer is gone even though TCP never reported it (the
// half-open connection a yanked network cable leaves behind). A var, not a
// const, so a test can shrink it.
var kitchenStreamIdleTimeout = 75 * time.Second

// KitchenEvent mirrors backend/internal/modules/pos/ws OrderEvent — one row of
// the snapshot or one live order notification. It carries routing fields only,
// never line items: the ticket content is fetched with GetOrder.
type KitchenEvent struct {
	Type       string    `json:"type"`
	OrderID    string    `json:"order_id"`
	CheckID    *string   `json:"check_id"`
	TableLabel string    `json:"table_label"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	Seq        uint64    `json:"seq"`
	OccurredAt time.Time `json:"occurred_at"`
}

const kitchenTypeSnapshot = "snapshot"

// ParseKitchenFrame decodes one text frame of the kitchen socket. A snapshot
// frame expands to its rows; a live event yields itself. A frame of an unknown
// type yields nothing and no error, so the backend can add message kinds
// without breaking older stations.
func ParseKitchenFrame(data []byte) ([]KitchenEvent, error) {
	var probe struct {
		Type   string         `json:"type"`
		Orders []KitchenEvent `json:"orders"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("apiclient: decode kitchen frame: %w", err)
	}
	switch probe.Type {
	case kitchenTypeSnapshot:
		return probe.Orders, nil
	case "order.placed", "order.status_changed":
		var evt KitchenEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			return nil, fmt.Errorf("apiclient: decode kitchen event: %w", err)
		}
		return []KitchenEvent{evt}, nil
	default:
		return nil, nil
	}
}

// KitchenStream is one open connection to the branch's kitchen WebSocket.
type KitchenStream struct {
	conn *websocket.Conn

	closeOnce sync.Once
	closed    chan struct{}
}

// OpenKitchenStream connects to GET /api/v1/pos/ws/kitchen?branch_id=… with
// the current session token as a Bearer header (the backend accepts no other
// auth on the handshake). A handshake refusal is returned as *APIError so
// callers can tell a permanent 403 from a transient failure exactly as they do
// for REST calls. A 401 triggers the same single-shot CTX recovery do() uses.
//
// The stream is closed when ctx is cancelled or Close is called.
func (c *Client) OpenKitchenStream(ctx context.Context, branchID string) (*KitchenStream, error) {
	if branchID == "" {
		return nil, fmt.Errorf("apiclient: open kitchen stream: branch_id is required")
	}
	wsURL, err := c.kitchenStreamURL(branchID)
	if err != nil {
		return nil, err
	}

	conn, err := c.dialKitchen(ctx, wsURL, false)
	if err != nil {
		return nil, err
	}

	s := &KitchenStream{conn: conn, closed: make(chan struct{})}
	conn.SetReadLimit(kitchenStreamMaxFrame)
	_ = conn.SetReadDeadline(time.Now().Add(kitchenStreamIdleTimeout))
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(kitchenStreamIdleTimeout))
		err := conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
		var netErr net.Error
		if errors.Is(err, websocket.ErrCloseSent) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return nil
		}
		return err
	})

	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.closed:
		}
	}()
	return s, nil
}

func (c *Client) dialKitchen(ctx context.Context, wsURL string, retried bool) (*websocket.Conn, error) {
	header := http.Header{}
	if tok := c.token(); tok != "" {
		header.Set("Authorization", "Bearer "+tok)
	}
	dialer := websocket.Dialer{HandshakeTimeout: kitchenHandshakeTimeout}

	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err == nil {
		return conn, nil
	}
	if resp == nil {
		return nil, fmt.Errorf("apiclient: open kitchen stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized && !retried && c.recoveryEnabled() {
		if _, recErr := c.recoverToken(ctx); recErr == nil {
			return c.dialKitchen(ctx, wsURL, true)
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return nil, fmt.Errorf("apiclient: open kitchen stream: %w", &APIError{StatusCode: resp.StatusCode, Body: string(body)})
}

func (c *Client) kitchenStreamURL(branchID string) (string, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("apiclient: open kitchen stream: parse base url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + kitchenStreamPath
	u.RawQuery = url.Values{"branch_id": {branchID}}.Encode()
	return u.String(), nil
}

// Next blocks until the next frame arrives and returns its events (possibly
// none, for a frame of a kind this client ignores). Any error — server close,
// idle timeout, cancellation — is terminal: the caller reconnects with a fresh
// stream, which starts with a new snapshot.
func (s *KitchenStream) Next() ([]KitchenEvent, error) {
	for {
		msgType, data, err := s.conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("apiclient: read kitchen stream: %w", err)
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(kitchenStreamIdleTimeout))
		if msgType != websocket.TextMessage {
			continue
		}
		return ParseKitchenFrame(data)
	}
}

// Close tears the connection down. Safe to call more than once and from
// another goroutine (it is what unblocks a pending Next).
func (s *KitchenStream) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = s.conn.Close()
	})
}
