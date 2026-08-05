package httpx

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"onlinemenu.tr/internal/platform/auth"
)

// RateLimitLevel names the bucket family a limiter writes to (ADR-OPS-003's
// "level"). It is part of the Redis key, so two levels never share a bucket
// even when they happen to derive the same identifier.
type RateLimitLevel string

const (
	// RateLimitLevelPublicIP throttles anonymous callers by source address —
	// the only identity available before a guest session exists.
	RateLimitLevelPublicIP RateLimitLevel = "public_ip"
	// RateLimitLevelGuest throttles an established guest session, so one
	// diner behind a shared restaurant NAT cannot spend the whole table's
	// IP budget.
	RateLimitLevelGuest RateLimitLevel = "guest"
)

const rateLimitKeyPrefix = "ratelimit:"

// RateLimitConfig is one limiter's budget: at most Limit requests in any
// Window-long stretch (sliding, not calendar-aligned).
type RateLimitConfig struct {
	Limit  int
	Window time.Duration
}

// slidingWindowScript is the whole limiter decision, executed atomically.
//
// ADR-OPS-003 rejected the fixed window (spike at the boundary) and chose a
// sliding window log, so the window must NOT appear in the key: expiring
// entries by score inside one immutable key is what makes the window slide.
// A key that carried a rotating window segment would be a fixed window with
// extra steps.
//
// The four commands must not interleave with another request's: a
// check-then-add split across round-trips lets N concurrent callers all read
// count < limit and all insert. Hence EVAL rather than a pipeline.
//
// Returns {allowed, count, reset_ms}: reset_ms is how long until the oldest
// entry leaves the window (when rejected) or a full window (when admitted).
var slidingWindowScript = redis.NewScript(`
local key    = KEYS[1]
local now    = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit  = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
local count = redis.call('ZCARD', key)

if count >= limit then
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    local reset = window
    if oldest[2] then
        reset = tonumber(oldest[2]) + window - now
        if reset < 0 then reset = 0 end
    end
    redis.call('PEXPIRE', key, window)
    return {0, count, reset}
end

redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, window)
return {1, count + 1, window}
`)

// PublicIPRateLimit throttles unauthenticated callers by source address.
//
// It is the outermost guard of the public storefront surface: it runs before
// any session exists, so the source address is the only identifier available.
// The address is whatever chi's RealIP middleware left in RemoteAddr, which
// trusts X-Forwarded-For unconditionally — this level is therefore only as
// honest as the proxy in front of it (ADR-OPS-003 puts the true anti-DDoS
// layer at the edge; this one exists to bound application-level abuse).
func PublicIPRateLimit(cache *redis.Client, cfg RateLimitConfig) func(http.Handler) http.Handler {
	return rateLimit(cache, RateLimitLevelPublicIP, cfg, func(r *http.Request) (string, bool) {
		return clientIP(r), true
	})
}

// GuestRateLimit throttles an established guest session (ADR-ARCH-006 §5).
//
// It must be mounted INSIDE the guest-session guard: without a session in
// context there is nothing to key on, and the request is refused (401) rather
// than silently falling back to a shared bucket, which would let an
// unauthenticated caller drain every diner's budget at once.
func GuestRateLimit(cache *redis.Client, cfg RateLimitConfig) func(http.Handler) http.Handler {
	return rateLimit(cache, RateLimitLevelGuest, cfg, func(r *http.Request) (string, bool) {
		g, ok := auth.GuestFromContext(r.Context())
		if !ok {
			return "", false
		}
		return g.TenantID.String() + ":" + g.SessionID.String(), true
	})
}

// rateLimit builds the middleware shared by every level.
//
// Redis failure is fail-CLOSED (503): the limiter guards an anonymous,
// internet-facing surface, so an outage that silently disabled it would turn
// a cache incident into an open door. The cost is explicit — a Redis outage
// takes the public storefront down with it — and is preferred over an
// unbounded surface.
func rateLimit(
	cache *redis.Client,
	level RateLimitLevel,
	cfg RateLimitConfig,
	identify func(*http.Request) (string, bool),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identifier, ok := identify(r)
			if !ok {
				problemJSON(w, http.StatusUnauthorized, "Unauthorized",
					"Bu uç için oturum gerekli.")
				return
			}

			key := rateLimitKeyPrefix + string(level) + ":" + identifier
			now := time.Now()

			res, err := slidingWindowScript.Run(r.Context(), cache,
				[]string{key},
				now.UnixMilli(),
				cfg.Window.Milliseconds(),
				cfg.Limit,
				uuid.NewString(),
			).Int64Slice()
			if err != nil || len(res) != 3 {
				if r.Context().Err() != nil {
					// Client went away mid-flight; no response is useful.
					return
				}
				w.Header().Set("Retry-After", "5")
				problemJSON(w, http.StatusServiceUnavailable, "Service Unavailable",
					"İstek sayacı şu anda kullanılamıyor, lütfen birazdan tekrar deneyin.")
				return
			}

			allowed, count, resetMS := res[0] == 1, res[1], res[2]
			remaining := int64(cfg.Limit) - count
			if remaining < 0 {
				remaining = 0
			}
			resetSeconds := int64(math.Ceil(float64(resetMS) / 1000))
			if resetSeconds < 1 {
				resetSeconds = 1
			}

			setRateLimitHeaders(w, cfg.Limit, remaining, resetSeconds)

			if !allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(resetSeconds, 10))
				problemJSON(w, http.StatusTooManyRequests, "Rate Limit Exceeded",
					"Çok fazla istek gönderildi, lütfen birazdan tekrar deneyin.")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func setRateLimitHeaders(w http.ResponseWriter, limit int, remaining, resetSeconds int64) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetSeconds, 10))
}

// clientIP returns the host part of RemoteAddr. An unparsable value falls
// back to the raw string (and, failing that, a shared "unknown" bucket) so a
// malformed address is throttled together with every other malformed one
// rather than escaping the limiter entirely.
func clientIP(r *http.Request) string {
	if r.RemoteAddr == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

// problemDetail is the RFC 7807 body ADR-OPS-003 specifies for 429s. The same
// shape is reused for the limiter's other refusals so a public client can
// parse one error format on this surface.
type problemDetail struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func problemJSON(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type:   "https://errors.onlinemenu.tr/" + problemSlug(status),
		Title:  title,
		Status: status,
		Detail: detail,
	})
}

func problemSlug(status int) string {
	switch status {
	case http.StatusTooManyRequests:
		return "rate-limit-exceeded"
	case http.StatusServiceUnavailable:
		return "rate-limit-unavailable"
	case http.StatusUnauthorized:
		return "unauthorized"
	default:
		return "error"
	}
}
