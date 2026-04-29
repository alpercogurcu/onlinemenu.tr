// Package tenant manages tenant lifecycle, branch configuration, and module enablement.
// All persistence goes through platform/db.WithTenantTx; direct pool access is forbidden.
package tenant

import (
	"go.uber.org/fx"
)

// Module is the fx module definition for the tenant domain.
var Module = fx.Module("tenant",
	fx.Provide(newService),
)

// serviceParams groups the dependencies injected into the tenant service.
type serviceParams struct {
	fx.In
}

// serviceResult groups the values the tenant module contributes to the container.
type serviceResult struct {
	fx.Out
}

func newService(p serviceParams) (serviceResult, error) {
	return serviceResult{}, nil
}
