package tokenx

import (
	"context"
	"fmt"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// Config holds the Token X Connect Cloud connection settings. Credentials come
// from Vault and are injected through fx; this package never reads os.Getenv.
type Config struct {
	APIURL       string // e.g. https://test-api.devtokeninc.com/dtokc/integration
	AuthURL      string
	ClientID     string
	ClientSecret string
	// BasketMode is the branch default, used when the terminal registry does
	// not pin a mode for the resolved terminal.
	BasketMode BasketMode
	// DefaultTerminalSerial is the fallback terminal for void receipts when the
	// submission reference carries none.
	DefaultTerminalSerial string
}

func (c Config) validate() error {
	switch {
	case c.APIURL == "":
		return fmt.Errorf("%w: APIURL is required", ErrInvalidConfig)
	case c.AuthURL == "":
		return fmt.Errorf("%w: AuthURL is required", ErrInvalidConfig)
	case c.ClientID == "":
		return fmt.Errorf("%w: ClientID is required", ErrInvalidConfig)
	case c.ClientSecret == "":
		return fmt.Errorf("%w: ClientSecret is required", ErrInvalidConfig)
	case !c.BasketMode.Valid():
		return fmt.Errorf("%w: BasketMode %q must be %q or %q", ErrInvalidConfig, c.BasketMode, BasketModeInstant, BasketModeList)
	}
	return nil
}

// Adapter is the Token X Connect Cloud implementation of FiscalDeviceAdapter.
type Adapter struct {
	cfg       Config
	client    *Client
	sections  SectionResolver
	terminals TerminalResolver
}

var _ domain.FiscalDeviceAdapter = (*Adapter)(nil)

// New builds an Adapter. Both resolvers are mandatory: without them the
// adapter would have to guess a device section or a terminal, and either guess
// corrupts a legal receipt.
func New(cfg Config, sections SectionResolver, terminals TerminalResolver, opts ...ClientOption) (*Adapter, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if sections == nil {
		return nil, fmt.Errorf("%w: SectionResolver is required", ErrInvalidConfig)
	}
	if terminals == nil {
		return nil, fmt.Errorf("%w: TerminalResolver is required", ErrInvalidConfig)
	}
	return &Adapter{
		cfg:       cfg,
		client:    NewClient(cfg, opts...),
		sections:  sections,
		terminals: terminals,
	}, nil
}

// SubmitSale delivers the basket and returns (nil, nil): the registration is
// asynchronous and its outcome arrives as a BASKET_COMPLETED webhook, which
// ParseWebhook turns into a FiscalResult for the sink.
func (a *Adapter) SubmitSale(ctx context.Context, sale domain.FiscalSale) (*domain.FiscalResult, error) {
	terminal, err := a.terminals.Resolve(ctx, sale.TenantID, sale.BranchID)
	if err != nil {
		return nil, fmt.Errorf("resolve terminal for branch %s: %w", sale.BranchID, err)
	}
	basket, err := buildBasket(ctx, sale, a.sections, false)
	if err != nil {
		return nil, fmt.Errorf("build basket for submission %s: %w", sale.SubmissionID, err)
	}
	if err := a.send(ctx, terminal, basket); err != nil {
		return nil, fmt.Errorf("submit basket %s: %w", basket.BasketID, err)
	}
	return nil, nil
}

// VoidSale sends an isVoid basket that cancels a completed registration. The
// device confirms it with another BASKET_COMPLETED webhook (status 99), so the
// result is nil here too.
func (a *Adapter) VoidSale(ctx context.Context, ref domain.FiscalSubmissionRef) (*domain.FiscalResult, error) {
	terminal, err := a.terminals.Resolve(ctx, ref.TenantID, ref.BranchID)
	if err != nil {
		return nil, fmt.Errorf("resolve terminal for branch %s: %w", ref.BranchID, err)
	}
	// The caller may pin the exact terminal that printed the original receipt.
	if ref.TerminalSerial != "" {
		terminal.Serial = ref.TerminalSerial
	}
	if err := a.send(ctx, terminal, buildVoidBasket(ref)); err != nil {
		return nil, fmt.Errorf("void basket %s: %w", ref.SubmissionID, err)
	}
	return nil, nil
}

// send routes the basket to the endpoint the terminal's mode requires.
func (a *Adapter) send(ctx context.Context, terminal TerminalRef, basket Basket) error {
	mode := terminal.Mode
	if mode == "" {
		mode = a.cfg.BasketMode
	}
	switch mode {
	case BasketModeInstant:
		serial := terminal.Serial
		if serial == "" {
			serial = a.cfg.DefaultTerminalSerial
		}
		return a.client.AddInstantBasket(ctx, serial, basket)
	case BasketModeList:
		return a.client.AddBasket(ctx, terminal.VendorBranchRef, basket)
	default:
		return fmt.Errorf("%w: unsupported basket mode %q", ErrInvalidConfig, mode)
	}
}

// Capabilities reports what the X30TR can do beyond the mandatory flows. POS
// core features never depend on these (ADR-FISCAL-002 §3).
func (a *Adapter) Capabilities() domain.FiscalCapabilities {
	return domain.FiscalCapabilities{
		OnDeviceSplit: true,
		VoidSale:      true,
		CustomerInfo:  true,
		// Foreign-currency payment items are a post-phase-1 concern.
		CurrencyPayment: false,
		OperatorRouting: true,
	}
}

// AdapterType is the fiscal_submissions.adapter_type value that routes a
// claimed submission back to this driver.
func (a *Adapter) AdapterType() string { return DeviceType }

// Client exposes the REST client for the reconciliation job (open baskets) and
// the section-sync job (fiscal info).
func (a *Adapter) Client() *Client { return a.client }
