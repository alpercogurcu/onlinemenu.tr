package edm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const sessionTTL = 18 * time.Minute

// sessionManager handles EDM SOAP session caching in Redis.
// Sessions are stored per (tenantID, endpoint) to avoid cross-tenant collisions.
type sessionManager struct {
	c     *client
	redis *redis.Client
	creds func(tenantID uuid.UUID) (username, password string, err error)
}

// getOrCreate returns a valid session ID, logging in if none is cached.
func (m *sessionManager) getOrCreate(ctx context.Context, tenantID uuid.UUID) (string, error) {
	key := m.sessionKey(tenantID)
	cached, err := m.redis.Get(ctx, key).Result()
	if err == nil && cached != "" {
		return cached, nil
	}
	if !errors.Is(err, redis.Nil) && err != nil {
		// Redis unavailable — still try to log in without caching.
		return m.login(ctx, tenantID, "")
	}
	return m.login(ctx, tenantID, "")
}

// invalidate deletes a session from Redis (called when session-expired is detected).
func (m *sessionManager) invalidate(ctx context.Context, tenantID uuid.UUID) {
	m.redis.Del(ctx, m.sessionKey(tenantID)) //nolint:errcheck
}

func (m *sessionManager) login(ctx context.Context, tenantID uuid.UUID, _ string) (string, error) {
	username, password, err := m.creds(tenantID)
	if err != nil {
		return "", fmt.Errorf("edm/session: resolve credentials: %w", err)
	}

	bodyXML := fmt.Sprintf(`<LoginRequest xmlns="http://tempuri.org/">%s`+
		`<USER_NAME xmlns="">%s</USER_NAME>`+
		`<PASSWORD xmlns="">%s</PASSWORD>`+
		`</LoginRequest>`,
		requestHeader("0"), escapeXML(username), escapeXML(password))

	respBody, err := m.c.call(actionLogin, bodyXML)
	if err != nil {
		return "", fmt.Errorf("edm/session: login: %w", err)
	}

	sessionID := extractXMLValue(string(respBody), "SESSION_ID")
	if sessionID == "" {
		return "", fmt.Errorf("%w: empty SESSION_ID", ErrEDMConnection)
	}

	key := m.sessionKey(tenantID)
	if setErr := m.redis.Set(ctx, key, sessionID, sessionTTL).Err(); setErr != nil {
		// Non-fatal: proceed with the session but it won't be cached.
	}

	return sessionID, nil
}

// sessionKey generates a Redis key unique per tenant and endpoint suffix.
func (m *sessionManager) sessionKey(tenantID uuid.UUID) string {
	suffix := "invoice"
	if strings.Contains(m.c.endpoint, "Irsaliye") {
		suffix = "despatch"
	}
	return fmt.Sprintf("edm:session:%s:%s", suffix, tenantID)
}
