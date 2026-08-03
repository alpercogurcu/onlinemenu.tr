package keycloak

import "go.uber.org/fx"

// Module registers the Keycloak Admin API client with fx, binding the
// concrete *Client to the AdminAPI interface consumers depend on (mirrors
// identity's fx.Annotate(..., fx.As(new(pub.PersonReader))) pattern). Neither
// constructor errors — see NewClient's doc comment — so this module never
// blocks app startup even when Config is unconfigured (dev/CI).
var Module = fx.Module("keycloak",
	fx.Provide(
		fx.Annotate(newClientForFx, fx.As(new(AdminAPI))),
	),
)

// newClientForFx adapts NewClient's variadic ClientOption signature (used
// directly by tests) to the fixed-arity shape fx.Provide requires.
func newClientForFx(cfg Config) *Client {
	return NewClient(cfg)
}
