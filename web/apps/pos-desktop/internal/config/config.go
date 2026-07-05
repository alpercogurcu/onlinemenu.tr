// Package config resolves runtime configuration for the POS desktop client.
//
// The desktop app is a standalone binary distributed to POS stations, not a
// module wired through uber-go/fx like the backend. Configuration therefore
// comes from a local config.json (next to the executable) with environment
// variable overrides for local development, instead of fx.Provide.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config groups the POS desktop client's runtime settings.
type Config struct {
	// APIBaseURL is the base URL of the onlinemenu.tr backend API this
	// station talks to (e.g. http://localhost:8080 in dev).
	APIBaseURL string `json:"api_base_url"`

	// KeycloakURL is the base URL of the onlinemenu Keycloak realm's
	// server (e.g. http://localhost:8090 in dev — see
	// deploy/keycloak/README.md). Used by internal/keycloakauth for the
	// "pos-desktop" client's loopback PKCE login (Sprint-6 Wave 3).
	KeycloakURL string `json:"keycloak_url"`

	// KeycloakRealm is the realm name ("onlinemenu" — ADR-AUTH-002 single
	// realm strategy).
	KeycloakRealm string `json:"keycloak_realm"`

	// EnableDevLogin gates the POST /dev/login shortcut (see
	// apiclient.Client.Login) next to the real Keycloak login. Mirrors
	// admin's NEXT_PUBLIC_ENABLE_DEV_LOGIN: defaults to true so local dev
	// keeps working without extra setup; production/staging deployments
	// must set POS_ENABLE_DEV_LOGIN=false.
	EnableDevLogin bool `json:"enable_dev_login"`
}

// defaultConfig is used when no config.json is present and no environment
// override is set. It targets the local dev stack started via
// `task compose:up` + `task backend:dev`.
func defaultConfig() Config {
	return Config{
		APIBaseURL:     "http://localhost:8080",
		KeycloakURL:    "http://localhost:8090",
		KeycloakRealm:  "onlinemenu",
		EnableDevLogin: true,
	}
}

// fileConfig mirrors Config for config.json decoding, except
// EnableDevLogin is a pointer so Load can tell "field absent from the
// file" (leave the default true) apart from "field explicitly false in the
// file" (Config's zero value for bool would make that ambiguous).
type fileConfig struct {
	APIBaseURL     string `json:"api_base_url"`
	KeycloakURL    string `json:"keycloak_url"`
	KeycloakRealm  string `json:"keycloak_realm"`
	EnableDevLogin *bool  `json:"enable_dev_login"`
}

// Load resolves the effective configuration in this precedence order, per
// field:
//  1. POS_* environment variable (dev convenience, overrides file)
//  2. config.json placed in configDir
//  3. built-in default
//
// Load never fails on a missing config.json — a missing file is expected on
// first run. It only errors when a config.json exists but is malformed,
// since silently ignoring a broken config could point a station at the
// wrong backend.
func Load(configDir string) (Config, error) {
	cfg := defaultConfig()

	path := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var fileCfg fileConfig
		if err := json.Unmarshal(data, &fileCfg); err != nil {
			return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
		}
		if fileCfg.APIBaseURL != "" {
			cfg.APIBaseURL = fileCfg.APIBaseURL
		}
		if fileCfg.KeycloakURL != "" {
			cfg.KeycloakURL = fileCfg.KeycloakURL
		}
		if fileCfg.KeycloakRealm != "" {
			cfg.KeycloakRealm = fileCfg.KeycloakRealm
		}
		if fileCfg.EnableDevLogin != nil {
			cfg.EnableDevLogin = *fileCfg.EnableDevLogin
		}
	} else if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}

	if v := os.Getenv("POS_API_BASE_URL"); v != "" {
		cfg.APIBaseURL = v
	}
	if v := os.Getenv("POS_KEYCLOAK_URL"); v != "" {
		cfg.KeycloakURL = v
	}
	if v := os.Getenv("POS_KEYCLOAK_REALM"); v != "" {
		cfg.KeycloakRealm = v
	}
	if v, ok := os.LookupEnv("POS_ENABLE_DEV_LOGIN"); ok {
		cfg.EnableDevLogin = v != "false"
	}

	return cfg, nil
}
