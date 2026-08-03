// Package keycloak wraps the Keycloak Admin REST API for user provisioning
// (ADR-AUTH-003). Module code must not call Keycloak's Admin API directly —
// every write goes through this package's AdminAPI.
package keycloak

// Config holds Keycloak Admin API connection parameters. Populated by
// cmd/api/main.go (the only place os.Getenv is allowed):
//   - BaseURL, Realm, ClientID come from environment (bootstrap values).
//   - ClientSecret is fetched from Vault at startup (a runtime secret, like
//     every other credential platform/vault manages).
//
// A zero-value Config (ClientID == "") is a valid, deliberate "feature
// disabled" state — see NewClient. This lets cmd/api boot in dev/CI without a
// Keycloak admin service account configured, mirroring how
// payment.FiscalConfig defaults to the mock adapter when FISCAL_DEVICE_TYPE
// is unset instead of requiring every TokenX credential unconditionally.
type Config struct {
	// BaseURL is the Keycloak server root, e.g. "https://keycloak.internal:8443"
	// (no trailing slash required).
	BaseURL string
	// Realm is the single realm all tenants share (ADR-AUTH-002), e.g. "onlinemenu".
	Realm string
	// ClientID is the confidential client's client_id for the service account
	// used with the client_credentials grant.
	ClientID string
	// ClientSecret is the service account's client secret, sourced from Vault.
	ClientSecret string
}

// enabled reports whether this Config carries enough to build a working Client.
func (c Config) enabled() bool {
	return c.BaseURL != "" && c.Realm != "" && c.ClientID != "" && c.ClientSecret != ""
}
