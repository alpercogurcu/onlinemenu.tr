// Package integration provides shared infrastructure for external adapter integrations.
// Domain modules (billing, delivery) own their adapter interfaces and implementations;
// this package supplies the common scaffolding: ProviderConfig, AdapterFactory, and Registry.
package integration

import "github.com/google/uuid"

// ProviderConfig carries the resolved configuration for one external provider binding.
// Sensitive credentials are fetched from Vault at runtime; only the Vault path is stored here.
type ProviderConfig struct {
	Provider        string // e.g. "edm", "parasut", "yemeksepeti"
	TenantID        uuid.UUID
	BranchID        *uuid.UUID     // nil = tenant-wide default
	Config          map[string]any // non-sensitive configuration from JSONB
	VaultSecretPath string         // Vault path for API credentials
	Environment     string         // "test" | "production"
	IsActive        bool
}

// Credentials holds the secrets fetched from Vault for a provider config.
type Credentials map[string]string

// AdapterFactory is a constructor function that builds a provider adapter T
// from a resolved ProviderConfig and its Vault credentials.
type AdapterFactory[T any] func(cfg ProviderConfig, creds Credentials) (T, error)

// Registry holds registered factories and builds adapters on demand.
type Registry[T any] struct {
	factories map[string]AdapterFactory[T]
}

// NewRegistry returns an empty Registry for adapter type T.
func NewRegistry[T any]() *Registry[T] {
	return &Registry[T]{factories: make(map[string]AdapterFactory[T])}
}

// Register stores a factory for the given provider name.
func (r *Registry[T]) Register(provider string, f AdapterFactory[T]) {
	r.factories[provider] = f
}

// Build looks up the factory for cfg.Provider and invokes it with the provided credentials.
func (r *Registry[T]) Build(cfg ProviderConfig, creds Credentials) (T, error) {
	f, ok := r.factories[cfg.Provider]
	if !ok {
		var zero T
		return zero, &ErrUnknownProvider{Provider: cfg.Provider}
	}
	return f(cfg, creds)
}

// ErrUnknownProvider is returned when no factory is registered for the requested provider.
type ErrUnknownProvider struct {
	Provider string
}

func (e *ErrUnknownProvider) Error() string {
	return "integration: unknown provider: " + e.Provider
}
