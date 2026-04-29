// Package identity manages platform users and their branch-level role assignments.
// All persistence goes through platform/db.WithTenantTx; direct pool access is forbidden.
package identity

import (
	"go.uber.org/fx"
)

// Module is the fx module definition for the identity domain.
// It wires internal providers and exposes public.UserReader to the fx container.
var Module = fx.Module("identity",
	fx.Provide(newService),
)

// serviceParams groups the dependencies injected into the identity service.
type serviceParams struct {
	fx.In
}

// serviceResult groups the values the identity module contributes to the container.
type serviceResult struct {
	fx.Out
}

func newService(p serviceParams) (serviceResult, error) {
	return serviceResult{}, nil
}
