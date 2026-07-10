package tokenx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/domain"
)

var errNoTerminal = errors.New("branch has no paired terminal")

// newTestAdapter wires the adapter through its public ClientOption seam, the
// same way fx does in production.
func newTestAdapter(t *testing.T, srv *testServer, mode BasketMode, terminals TerminalResolver) *Adapter {
	t.Helper()
	cfg := testConfig(srv)
	cfg.BasketMode = mode
	adapter, err := New(cfg, staticSections(3, 1000), terminals, WithHTTPClient(srv.Client()))
	require.NoError(t, err)
	return adapter
}

func decodeBasket(t *testing.T, body []byte) Basket {
	t.Helper()
	var b Basket
	require.NoError(t, json.Unmarshal(body, &b))
	return b
}

func TestAdapterSubmitSaleInstantMode(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	adapter := newTestAdapter(t, srv, BasketModeInstant,
		staticTerminal(TerminalRef{Serial: "AV0000000658", VendorBranchRef: "BR-1"}))

	sale := testSale()
	res, err := adapter.SubmitSale(context.Background(), sale)
	require.NoError(t, err)
	assert.Nil(t, res, "cloud registration is asynchronous; the result arrives by webhook")

	req := srv.lastRequest(t)
	assert.Equal(t, "/v1/instant-basket", req.Path)
	assert.Equal(t, "AV0000000658", req.Header.Get("terminal-id"))
	assert.Empty(t, req.Header.Get("branch-id"), "instant basket must not carry branch-id")

	basket := decodeBasket(t, req.Body)
	assert.Equal(t, sale.SubmissionID.String(), basket.BasketID)
	assert.False(t, basket.IsVoid)
	assert.Equal(t, "Masa 5", basket.Title)
	require.Len(t, basket.Items, 1)
	assert.Equal(t, int64(15000), basket.Items[0].Price)
	assert.Equal(t, 3, basket.Items[0].SectionNo)
	require.Len(t, basket.PaymentItems, 1)
	assert.Equal(t, paymentTypeCash, basket.PaymentItems[0].Type)
}

func TestAdapterSubmitSaleListMode(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	adapter := newTestAdapter(t, srv, BasketModeList,
		staticTerminal(TerminalRef{Serial: "AV0000000658", VendorBranchRef: "BR-42"}))

	res, err := adapter.SubmitSale(context.Background(), testSale())
	require.NoError(t, err)
	assert.Nil(t, res)

	req := srv.lastRequest(t)
	assert.Equal(t, "/v1/basket", req.Path)
	assert.Equal(t, "BR-42", req.Header.Get("branch-id"), "list mode routes by the per-basket vendor branch ref")
	assert.Empty(t, req.Header.Get("terminal-id"))
}

func TestAdapterTerminalModeOverridesConfigDefault(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	// Config default is instant, but this terminal is pinned to list mode.
	adapter := newTestAdapter(t, srv, BasketModeInstant,
		staticTerminal(TerminalRef{Serial: "T1", VendorBranchRef: "BR-7", Mode: BasketModeList}))

	_, err := adapter.SubmitSale(context.Background(), testSale())
	require.NoError(t, err)

	req := srv.lastRequest(t)
	assert.Equal(t, "/v1/basket", req.Path)
	assert.Equal(t, "BR-7", req.Header.Get("branch-id"))
}

func TestAdapterSubmitSaleFailsBeforeCallOnMappingErrors(t *testing.T) {
	t.Parallel()

	t.Run("unresolvable terminal", func(t *testing.T) {
		t.Parallel()
		srv := newTestServer(t, serverOpts{})
		terminals := terminalsFunc(func(context.Context, uuid.UUID, uuid.UUID) (TerminalRef, error) {
			return TerminalRef{}, errNoTerminal
		})
		adapter := newTestAdapter(t, srv, BasketModeInstant, terminals)

		_, err := adapter.SubmitSale(context.Background(), testSale())
		require.ErrorIs(t, err, errNoTerminal)
		assert.Empty(t, srv.recorded(), "nothing may reach the device before the mapping is sound")
	})

	t.Run("tax rate mismatch", func(t *testing.T) {
		t.Parallel()
		srv := newTestServer(t, serverOpts{})
		adapter, err := New(testConfig(srv), staticSections(3, 2000),
			staticTerminal(TerminalRef{Serial: "T1"}), WithHTTPClient(srv.Client()))
		require.NoError(t, err)

		_, err = adapter.SubmitSale(context.Background(), testSale())
		require.ErrorIs(t, err, ErrTaxMismatch)
		assert.Empty(t, srv.recorded(), "a tax mismatch must never be registered")
	})
}

func TestAdapterSubmitSalePropagatesAPIError(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{api: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"1100 basket limit"}`))
	}})
	adapter := newTestAdapter(t, srv, BasketModeInstant, staticTerminal(TerminalRef{Serial: "T1"}))

	_, err := adapter.SubmitSale(context.Background(), testSale())
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
}

func TestAdapterVoidSale(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	adapter := newTestAdapter(t, srv, BasketModeInstant, staticTerminal(TerminalRef{Serial: "DEFAULT-T"}))

	ref := domain.FiscalSubmissionRef{
		SubmissionID:   uuid.New(),
		TenantID:       uuid.New(),
		BranchID:       uuid.New(),
		TerminalSerial: "AV0000000999",
	}
	res, err := adapter.VoidSale(context.Background(), ref)
	require.NoError(t, err)
	assert.Nil(t, res, "the void is confirmed by a status 99 webhook")

	req := srv.lastRequest(t)
	assert.Equal(t, "/v1/instant-basket", req.Path)
	assert.Equal(t, "AV0000000999", req.Header.Get("terminal-id"),
		"an explicit terminal serial must win over the resolver default")

	basket := decodeBasket(t, req.Body)
	assert.True(t, basket.IsVoid)
	assert.Equal(t, ref.SubmissionID.String(), basket.BasketID)
}

func TestAdapterVoidSaleFallsBackToResolvedTerminal(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	adapter := newTestAdapter(t, srv, BasketModeInstant, staticTerminal(TerminalRef{Serial: "RESOLVED-T"}))

	_, err := adapter.VoidSale(context.Background(), domain.FiscalSubmissionRef{SubmissionID: uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, "RESOLVED-T", srv.lastRequest(t).Header.Get("terminal-id"))
}

func TestAdapterCapabilities(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	adapter := newTestAdapter(t, srv, BasketModeInstant, staticTerminal(TerminalRef{Serial: "T1"}))

	assert.Equal(t, domain.FiscalCapabilities{
		OnDeviceSplit:   true,
		VoidSale:        true,
		CustomerInfo:    true,
		CurrencyPayment: false,
		OperatorRouting: true,
	}, adapter.Capabilities())
}

func TestAdapterImplementsDomainInterface(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, serverOpts{})
	var adapter domain.FiscalDeviceAdapter = newTestAdapter(t, srv, BasketModeInstant, staticTerminal(TerminalRef{Serial: "T1"}))
	assert.NotNil(t, adapter)
}

func TestNewValidatesConfig(t *testing.T) {
	t.Parallel()

	valid := Config{
		APIURL: "https://api", AuthURL: "https://auth",
		ClientID: "id", ClientSecret: "secret", BasketMode: BasketModeInstant,
	}

	tests := []struct {
		name        string
		mutate      func(*Config)
		nilSections bool
		nilTerminal bool
	}{
		{name: "missing api url", mutate: func(c *Config) { c.APIURL = "" }},
		{name: "missing auth url", mutate: func(c *Config) { c.AuthURL = "" }},
		{name: "missing client id", mutate: func(c *Config) { c.ClientID = "" }},
		{name: "missing client secret", mutate: func(c *Config) { c.ClientSecret = "" }},
		{name: "empty basket mode", mutate: func(c *Config) { c.BasketMode = "" }},
		{name: "unknown basket mode", mutate: func(c *Config) { c.BasketMode = "turbo" }},
		{name: "missing section resolver", mutate: func(*Config) {}, nilSections: true},
		{name: "missing terminal resolver", mutate: func(*Config) {}, nilTerminal: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			tc.mutate(&cfg)

			var sections SectionResolver = staticSections(1, 1000)
			if tc.nilSections {
				sections = nil
			}
			var terminals TerminalResolver = staticTerminal(TerminalRef{})
			if tc.nilTerminal {
				terminals = nil
			}

			_, err := New(cfg, sections, terminals)
			require.ErrorIs(t, err, ErrInvalidConfig)
		})
	}

	t.Run("accepts a complete config", func(t *testing.T) {
		t.Parallel()
		adapter, err := New(valid, staticSections(1, 1000), staticTerminal(TerminalRef{}))
		require.NoError(t, err)
		assert.NotNil(t, adapter.Client())
	})
}

func TestBasketModeValid(t *testing.T) {
	t.Parallel()
	assert.True(t, BasketModeInstant.Valid())
	assert.True(t, BasketModeList.Valid())
	assert.False(t, BasketMode("").Valid())
	assert.False(t, BasketMode("list-mode").Valid())
}
