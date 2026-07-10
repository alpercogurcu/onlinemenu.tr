package payment

import (
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/db"
)

// supplyExternals provides the dependencies cmd/api/main.go injects into this
// module. ValidateApp only resolves the graph — no constructor runs and no hook
// fires — so the nil pointers are never dereferenced.
func supplyExternals(cfg FiscalConfig) fx.Option {
	return fx.Supply(
		cfg,
		(*db.Pool)(nil),
		(*redis.Client)(nil),
		(*auth.Engine)(nil),
		zap.NewNop(),
		chi.NewMux(),
	)
}

// TestModuleGraphResolves catches a broken fx graph at test time instead of at
// process start. `go build` cannot see it: a constructor whose parameter has no
// provider, or a handler left out of the RegisterRoutes invoke, compiles fine
// and only fails when main() runs.
func TestModuleGraphResolves(t *testing.T) {
	t.Parallel()
	require.NoError(t, fx.ValidateApp(supplyExternals(FiscalConfig{DeviceType: "mock"}), Module))
}

// TestNewFiscalAdapter covers the factory's branches (ADR-FISCAL-002 §4). The
// tokenx branch needs a valid config; an unknown type must fail loudly rather
// than fall back to the mock, which would silently disable fiscal registration
// in production (ADR-FISCAL-001 forbids a 'none' device).
func TestNewFiscalAdapter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		deviceType string
		wantErr    bool
	}{
		{name: "empty defaults to mock", deviceType: ""},
		{name: "explicit mock", deviceType: "mock"},
		{name: "unknown type is rejected", deviceType: "hugin_9000", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			adapter, err := newFiscalAdapter(FiscalConfig{DeviceType: tt.deviceType}, nil, nil)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, adapter)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, adapter)
		})
	}
}
