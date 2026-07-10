package tokenx

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	// ErrNoLines is returned when a sale carries no sale lines. Every ÖKC
	// rejects an empty basket.
	ErrNoLines = errors.New("tokenx: sale has no lines")
	// ErrTaxMismatch is returned when a line's tax rate differs from the tax
	// rate of the device section it maps to. Submitting anyway would produce a
	// legally inconsistent receipt, so the sale is rejected instead.
	ErrTaxMismatch = errors.New("tokenx: line tax rate differs from device section tax rate")
	// ErrUnknownPaymentMethod is returned when a payment method has no Token
	// payment-type code.
	ErrUnknownPaymentMethod = errors.New("tokenx: payment method has no vendor code")
	// ErrUnsupportedCurrency is returned for a non-TRY sale. The basket model
	// carries no currency field, so a foreign-currency sale would silently be
	// registered as TRY on a Turkish fiscal device (Capabilities.CurrencyPayment
	// is false until multi-currency payment items are implemented).
	ErrUnsupportedCurrency = errors.New("tokenx: only TRY sales can be registered")
	// ErrInvalidConfig is returned by New for an incomplete configuration.
	ErrInvalidConfig = errors.New("tokenx: invalid config")
	// ErrUnexpectedOperation is returned when a webhook payload is routed to
	// the wrong parser.
	ErrUnexpectedOperation = errors.New("tokenx: unexpected webhook operation")
	// ErrUnknownStatus is returned for a BASKET_COMPLETED status code outside
	// {0, -1, 99}.
	ErrUnknownStatus = errors.New("tokenx: unknown basket status")
)

// APIError is a non-2xx response from the Token API.
type APIError struct {
	StatusCode int
	Endpoint   string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tokenx: %s returned %d: %s", e.Endpoint, e.StatusCode, e.Body)
}

// Retryable reports whether the request may be retried. Token's coding standard
// mandates a backoff on rate limiting; any other 4xx is a permanent contract
// error and retrying it would only burn quota.
func (e *APIError) Retryable() bool { return e.StatusCode == http.StatusTooManyRequests }
