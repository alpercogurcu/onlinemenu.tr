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
}

// defaultConfig is used when no config.json is present and no environment
// override is set. It targets the local dev stack started via
// `task compose:up` + `task backend:dev`.
func defaultConfig() Config {
	return Config{
		APIBaseURL: "http://localhost:8080",
	}
}

// Load resolves the effective configuration in this precedence order:
//  1. POS_API_BASE_URL environment variable (dev convenience, overrides file)
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
		var fileCfg Config
		if err := json.Unmarshal(data, &fileCfg); err != nil {
			return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
		}
		if fileCfg.APIBaseURL != "" {
			cfg.APIBaseURL = fileCfg.APIBaseURL
		}
	} else if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}

	if v := os.Getenv("POS_API_BASE_URL"); v != "" {
		cfg.APIBaseURL = v
	}

	return cfg, nil
}
