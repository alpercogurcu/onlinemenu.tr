package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	opaCacheTTL    = 60 * time.Second
	opaPolicyQuery = "data.authz"
)

// Decision is the result of an OPA policy evaluation.
// Allow signals whether the action is permitted.
// Scope narrows the data visibility: "tenant", "branch", or "own".
type Decision struct {
	Allow bool
	Scope string
}

// opaInput is the input document sent to OPA for evaluation.
type opaInput struct {
	Action    string    `json:"action"`
	Principal principal `json:"principal"`
}

// principal is the OPA input shape. Roles carries role UUIDs (Principal.RoleIDs),
// not role names — the rego policy matches against the well-known system role IDs
// seeded in identity/000006_seed_system_roles.up.sql. Resolving custom tenant-role
// UUIDs to human-readable names requires identity module internals (PermSet /
// PermissionRepo) and is deferred; see docs note in configs/opa/bundles/authz.rego.
type principal struct {
	Sub       string   `json:"sub"`
	TenantID  string   `json:"tenant_id"`
	BranchIDs []string `json:"branch_ids"`
	Roles     []string `json:"roles"`
}

// EngineConfig holds OPA engine configuration injected via fx.
type EngineConfig struct {
	// BundlePath is the directory containing .rego policy files.
	BundlePath string
}

// Engine wraps the OPA rego query with a Redis-backed decision cache.
type Engine struct {
	query  rego.PreparedEvalQuery
	cache  *redis.Client
	logger *zap.Logger
}

// EngineModule registers the OPA Engine with fx.
var EngineModule = fx.Module("opa",
	fx.Provide(NewEngine),
)

// NewEngine loads the OPA policy bundle from disk and prepares the evaluation query.
func NewEngine(cfg EngineConfig, cache *redis.Client, logger *zap.Logger) (*Engine, error) {
	query, err := rego.New(
		rego.Query(opaPolicyQuery),
		rego.Load([]string{cfg.BundlePath}, nil),
	).PrepareForEval(context.Background())
	if err != nil {
		return nil, fmt.Errorf("opa: prepare eval query: %w", err)
	}

	return &Engine{
		query:  query,
		cache:  cache,
		logger: logger,
	}, nil
}

// Decide evaluates the OPA policy for the given action and principal.
// Results are cached in Redis for opaCacheTTL to reduce evaluation overhead
// on hot paths (ADR-AUTH-001).
func (e *Engine) Decide(ctx context.Context, action string, p Principal) (Decision, error) {
	cacheKey := buildCacheKey(action, p)

	if cached, err := e.getCached(ctx, cacheKey); err == nil {
		return cached, nil
	}

	roleStrs := make([]string, len(p.RoleIDs))
	for i, id := range p.RoleIDs {
		roleStrs[i] = id.String()
	}

	input := opaInput{
		Action: action,
		Principal: principal{
			Sub:       p.PersonID.String(),
			TenantID:  p.TenantID.String(),
			BranchIDs: []string{p.BranchID.String()},
			Roles:     roleStrs,
		},
	}

	results, err := e.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return Decision{}, fmt.Errorf("opa: eval: %w", err)
	}

	if len(results) == 0 || len(results[0].Expressions) == 0 {
		return Decision{Allow: false}, nil
	}

	raw, ok := results[0].Expressions[0].Value.(map[string]interface{})
	if !ok {
		return Decision{Allow: false}, nil
	}

	decision := Decision{}
	if allow, ok := raw["allow"].(bool); ok {
		decision.Allow = allow
	}
	if scope, ok := raw["scope"].(string); ok {
		decision.Scope = scope
	}

	if err := e.setCache(ctx, cacheKey, decision); err != nil {
		e.logger.Warn("opa: cache write failed", zap.Error(err))
	}

	return decision, nil
}

func buildCacheKey(action string, p Principal) string {
	strs := make([]string, len(p.RoleIDs))
	for i, id := range p.RoleIDs {
		strs[i] = id.String()
	}
	sort.Strings(strs)
	h := sha256.Sum256([]byte(fmt.Sprintf("%v", strs)))
	return fmt.Sprintf("opa:%s:%s:%s:%s:%x", p.TenantID, p.BranchID, p.PersonID, action, h[:8])
}

func (e *Engine) getCached(ctx context.Context, key string) (Decision, error) {
	val, err := e.cache.Get(ctx, key).Result()
	if err != nil {
		return Decision{}, err
	}
	var d Decision
	if err := json.Unmarshal([]byte(val), &d); err != nil {
		return Decision{}, fmt.Errorf("opa: unmarshal cached decision: %w", err)
	}
	return d, nil
}

func (e *Engine) setCache(ctx context.Context, key string, d Decision) error {
	data, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("opa: marshal decision: %w", err)
	}
	return e.cache.Set(ctx, key, data, opaCacheTTL).Err()
}
