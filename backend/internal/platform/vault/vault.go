// Package vault wraps the HashiCorp Vault client for secret retrieval.
// Module code must not import vault-client-go directly; use this package.
package vault

import (
	"context"
	"fmt"

	vaultclient "github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config holds Vault connection parameters injected via fx.
type Config struct {
	// Address is the Vault server URL, e.g. "https://vault.internal:8200".
	Address string

	// Token is the initial auth token (AppRole or root for bootstrap).
	// In production this is replaced by dynamic AppRole credentials.
	Token string

	// Namespace is the Vault namespace (Enterprise only; leave empty for OSS).
	Namespace string
}

// Client wraps the Vault SDK to expose only the operations platform modules need.
type Client struct {
	inner  *vaultclient.Client
	logger *zap.Logger
}

// Module registers the Vault client with fx.
var Module = fx.Module("vault",
	fx.Provide(NewClient),
)

// NewClient creates an authenticated Vault client.
func NewClient(lc fx.Lifecycle, cfg Config, logger *zap.Logger) (*Client, error) {
	opts := []vaultclient.ClientOption{
		vaultclient.WithAddress(cfg.Address),
	}

	inner, err := vaultclient.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("vault: create client: %w", err)
	}

	if err := inner.SetToken(cfg.Token); err != nil {
		return nil, fmt.Errorf("vault: set token: %w", err)
	}

	if cfg.Namespace != "" {
		if err := inner.SetNamespace(cfg.Namespace); err != nil {
			return nil, fmt.Errorf("vault: set namespace: %w", err)
		}
	}

	c := &Client{inner: inner, logger: logger}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// Verify connectivity by checking Vault system health.
			_, err := inner.System.ReadHealthStatus(ctx)
			if err != nil {
				return fmt.Errorf("vault: health check: %w", err)
			}
			logger.Info("vault client connected", zap.String("address", cfg.Address))
			return nil
		},
	})

	return c, nil
}

// KVGet retrieves a key-value secret from the given mount and path.
// Returns the secret data map or an error.
func (c *Client) KVGet(ctx context.Context, mount, path string) (map[string]interface{}, error) {
	resp, err := c.inner.Secrets.KvV2Read(ctx, path, vaultclient.WithMountPath(mount))
	if err != nil {
		return nil, fmt.Errorf("vault: kv get %s/%s: %w", mount, path, err)
	}
	if resp.Data.Data == nil {
		return nil, fmt.Errorf("vault: kv get %s/%s: empty data", mount, path)
	}
	return resp.Data.Data, nil
}

// KVPut writes a key-value secret to the given mount and path.
func (c *Client) KVPut(ctx context.Context, mount, path string, data map[string]interface{}) error {
	_, err := c.inner.Secrets.KvV2Write(ctx, path,
		schema.KvV2WriteRequest{Data: data},
		vaultclient.WithMountPath(mount),
	)
	if err != nil {
		return fmt.Errorf("vault: kv put %s/%s: %w", mount, path, err)
	}
	return nil
}

// GetString is a convenience wrapper that reads a single string value from a KV secret.
func (c *Client) GetString(ctx context.Context, mount, path, key string) (string, error) {
	data, err := c.KVGet(ctx, mount, path)
	if err != nil {
		return "", err
	}
	val, ok := data[key]
	if !ok {
		return "", fmt.Errorf("vault: key %q not found at %s/%s", key, mount, path)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("vault: key %q at %s/%s is not a string", key, mount, path)
	}
	return str, nil
}
