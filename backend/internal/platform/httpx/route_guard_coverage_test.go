package httpx_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Route-guard coverage (docs/lessons-from-b2b.md item 1, OPA bullet:
// "Her modülün route kaydında authz middleware varlığını assert eden bir test
// (route tablosunu iterate eden smoke test) ekle").
//
// Every module already HAS such a test: internal/modules/<mod>/http/
// authz_smoke_test.go walks its own chi router with chi.Walk and requires 403
// on every route. That is the strong form and it is real enforcement — for the
// modules that have it.
//
// What nothing enforced until now is the meta-property: that a route
// REGISTRAR cannot exist without one. b2b's Casbin failure was not a wrong
// rule, it was a middleware bound to zero routes — i.e. a registrar nobody
// audited. A new module (or, more likely, a second handler bolted onto an
// existing module, which is exactly how payment ended up with three
// registrars) inherits no coverage automatically.
//
// So this test enumerates every `func (…) Register…Routes(` in every module's
// transport layer and requires each one to be classified below. Adding a
// registrar without touching this file fails the build with the symbol named.
// ---------------------------------------------------------------------------

// guardKind says how a registrar's routes are proven to be guarded.
type guardKind string

const (
	// guardAuthzWalk: a chi.Walk test in the same package asserts 403 for a
	// roleless principal on every route this registrar mounts.
	guardAuthzWalk guardKind = "authz-walk"
	// guardPublicWalk: the routes are deliberately outside the staff auth
	// chain (anonymous QR diners have no principal). A chi.Walk test asserts
	// the *public* guard chain instead — guest session required.
	guardPublicWalk guardKind = "public-walk"
	// guardSecretPath: a vendor inbound webhook. There is no principal to
	// authorize; the route only exists when a shared secret is configured and
	// the secret is compared in constant time, answering 404 otherwise.
	guardSecretPath guardKind = "secret-path"
	// guardInlineEngine: the registrar takes the auth.Engine directly and
	// checks the permission inside the handler (WebSocket upgrade, where
	// middleware cannot answer 403 after the handshake). Proven by explicit
	// forbidden-case tests rather than a route walk.
	guardInlineEngine guardKind = "inline-engine"
)

// routeGuard describes one registrar.
type routeGuard struct {
	kind guardKind
	// proof is the test file (relative to internal/modules/) that holds the
	// assertion. It must exist and must mention the registrar's method name.
	proof string
	// why explains a non-authz-walk classification.
	why string
}

// routeRegistrars is the exhaustive, reviewed list. Key format:
// "<module>/<pkg>/<file>.go:<Receiver>.<Method>".
var routeRegistrars = map[string]routeGuard{
	"billing/http/handler.go:HandlerWithCache.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "billing/http/authz_smoke_test.go",
	},
	"catalog/http/handler.go:Handler.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "catalog/http/authz_smoke_test.go",
	},
	"hr-core/http/handler.go:Handler.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "hr-core/http/authz_smoke_test.go",
	},
	"identity/http/routes.go:Handler.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "identity/http/authz_smoke_test.go",
	},
	"inventory/http/handler.go:Handler.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "inventory/http/authz_smoke_test.go",
	},
	"party/http/handler.go:Handler.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "party/http/authz_smoke_test.go",
	},
	"payment/http/handler.go:HandlerWithCache.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "payment/http/authz_smoke_test.go",
	},
	"payment/http/fiscal_handler.go:FiscalHandler.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "payment/http/authz_smoke_test.go",
	},
	"pos/http/handler.go:HandlerWithCache.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "pos/http/authz_smoke_test.go",
	},
	"storefront/http/admin_handler.go:AdminHandler.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "storefront/http/authz_smoke_test.go",
	},
	"tenant/http/routes.go:Handler.RegisterRoutes": {
		kind: guardAuthzWalk, proof: "tenant/http/authz_smoke_test.go",
	},

	"storefront/http/public_handler.go:PublicHandlerWithCache.RegisterPublicRoutes": {
		kind:  guardPublicWalk,
		proof: "storefront/http/public_guard_smoke_test.go",
		why: "ADR-ARCH-006: anonymous QR diners have no Keycloak identity, so cmd/api's " +
			"auth chain skips /api/public/v1/*. The guard chain (IP rate limit -> guest " +
			"session cookie -> per-session rate limit -> idempotency) IS the boundary and " +
			"the walk test asserts every route bar /sessions refuses a session-less caller.",
	},

	"payment/http/webhook_handler.go:TokenXWebhookHandler.RegisterRoutes": {
		kind:  guardSecretPath,
		proof: "payment/http/webhook_guard_test.go",
		why: "ADR-FISCAL-002 vendor inbound edge: the ÖKC cloud has no principal. The route " +
			"is only mounted when a secret is configured, the secret path segment is " +
			"compared with subtle.ConstantTimeCompare, a mismatch answers 404 (never 401, " +
			"which would confirm the path), and the only DB access is the narrow " +
			"fiscalRoutingStore.GetRouting lookup that recovers the tenant before any write.",
	},

	"pos/ws/handler.go:Hub.RegisterRoutes": {
		kind:  guardInlineEngine,
		proof: "pos/ws/hub_test.go",
		why: "Kitchen display WebSocket: the permission decision must happen before the " +
			"upgrade handshake, so the Hub takes auth.Engine and denies inline. Proven by " +
			"TestKitchenWS_NoPermission_Forbidden / _ForeignBranch_Forbidden rather than " +
			"by a route walk (a walked GET would not upgrade).",
	},
}

// registrarRe matches both method registrars (`func (h *Handler)
// RegisterRoutes(`) and free-function ones (`func RegisterRoutes(`), so a
// module that mounts routes without a receiver cannot slip past the audit.
// Free functions are recorded under the receiver name "func".
var registrarRe = regexp.MustCompile(`func (?:\(\w+ \*?(\w+)\) )?(Register\w*Routes)\(`)

// TestEveryRouteRegistrarHasAGuardProof is the meta-enforcement: no route
// registrar may exist without a reviewed classification and a proof test.
func TestEveryRouteRegistrarHasAGuardProof(t *testing.T) {
	modulesRoot := filepath.Join("..", "..", "modules")
	found := map[string]bool{}

	err := filepath.WalkDir(modulesRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Only transport layers mount routes; a match anywhere else would be a
		// boundary violation go-arch-lint already catches.
		slashed := filepath.ToSlash(path)
		if !strings.Contains(slashed, "/http/") && !strings.Contains(slashed, "/ws/") {
			return nil
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // test-only read of repo source
		if readErr != nil {
			return readErr
		}
		rel := relKey(path)
		for _, m := range registrarRe.FindAllStringSubmatch(string(src), -1) {
			receiver := m[1]
			if receiver == "" {
				receiver = "func"
			}
			found[rel+":"+receiver+"."+m[2]] = true
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, found, "no route registrars discovered — the walk is broken, not the codebase")

	var unclassified []string
	for symbol := range found {
		if _, ok := routeRegistrars[symbol]; !ok {
			unclassified = append(unclassified, symbol)
		}
	}
	sort.Strings(unclassified)
	assert.Emptyf(t, unclassified,
		"these route registrars have no entry in routeRegistrars: %v\n"+
			"Every registrar must be classified and point at the test that proves its routes "+
			"are guarded (docs/lessons-from-b2b.md item 1: b2b's Casbin middleware was wired "+
			"to zero routes). Add the registrar to routeRegistrars together with a chi.Walk "+
			"authz smoke test in its own package.", unclassified)

	// Stale entries are how a registry like this rots into decoration.
	var stale []string
	for symbol := range routeRegistrars {
		if !found[symbol] {
			stale = append(stale, symbol)
		}
	}
	sort.Strings(stale)
	assert.Emptyf(t, stale,
		"routeRegistrars lists registrars that no longer exist: %v — delete the stale entries", stale)
}

// TestRouteGuardProofsExist checks the other half: the named proof test file
// must exist and must actually reference the registrar it claims to walk. A
// registry pointing at a deleted or renamed test is the same failure as no
// test at all, just quieter.
func TestRouteGuardProofsExist(t *testing.T) {
	modulesRoot := filepath.Join("..", "..", "modules")

	symbols := make([]string, 0, len(routeRegistrars))
	for s := range routeRegistrars {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)

	for _, symbol := range symbols {
		g := routeRegistrars[symbol]
		require.NotEmptyf(t, g.proof, "%s has no proof test named", symbol)
		if g.kind != guardAuthzWalk {
			assert.NotEmptyf(t, g.why,
				"%s is classified %q rather than %q, which needs a written justification",
				symbol, g.kind, guardAuthzWalk)
		}

		proofPath := filepath.Join(modulesRoot, filepath.FromSlash(g.proof))
		src, err := os.ReadFile(proofPath) //nolint:gosec // test-only read of repo source
		if !assert.NoErrorf(t, err, "%s points at proof test %s which does not exist", symbol, g.proof) {
			continue
		}

		// The registrar call may live in a shared router-builder helper in a
		// sibling test file of the same package (storefront's newPublicRouter
		// does exactly that), so the search is package-wide; the chi.Walk
		// assertion below still pins it to the named proof file.
		method := symbol[strings.LastIndex(symbol, ".")+1:]
		pkgTests, globErr := filepath.Glob(filepath.Join(filepath.Dir(proofPath), "*_test.go"))
		require.NoError(t, globErr)
		called := false
		for _, f := range pkgTests {
			b, readErr := os.ReadFile(f) //nolint:gosec // test-only read of repo source
			if readErr == nil && strings.Contains(string(b), method+"(") {
				called = true
				break
			}
		}
		assert.Truef(t, called,
			"%s claims to be proven by %s, but no test in that package ever calls %s — the "+
				"proof does not exercise the registrar it is registered against", symbol, g.proof, method)

		if g.kind == guardAuthzWalk || g.kind == guardPublicWalk {
			assert.Truef(t, strings.Contains(string(src), "chi.Walk"),
				"%s is classified %q but %s does not iterate the route table with chi.Walk — "+
					"a hand-picked list of routes cannot notice a newly added one",
				symbol, g.kind, g.proof)
		}
	}
}
