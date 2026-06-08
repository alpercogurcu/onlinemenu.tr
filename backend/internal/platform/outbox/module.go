package outbox

import "go.uber.org/fx"

// Module registers the outbox dispatcher with fx.
// The dispatcher is an fx.Invoke target: it registers lifecycle hooks but
// does not expose a dependency consumed by other modules.
var Module = fx.Module("outbox",
	fx.Invoke(Register),
)
