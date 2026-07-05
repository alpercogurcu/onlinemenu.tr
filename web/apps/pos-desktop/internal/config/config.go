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
	"strconv"
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

	// PrinterAddr is the receipt printer's "host:port" address (e.g.
	// "192.168.1.50:9100" — ESC/POS raw/direct printing on the printer's
	// standard TCP 9100 port). Empty (the default) means no real printer is
	// configured for this station: app.go falls back to hardware.MockPrinter,
	// preserving the pre-existing no-hardware-required dev behavior.
	PrinterAddr string `json:"printer_addr"`

	// PrinterWidth is the printer's paper width in character columns — 32
	// or 48 are the two widths internal/receipt supports. Any other value
	// (including 0, config.json's zero value) falls back to 48.
	PrinterWidth int `json:"printer_width"`

	// BusinessName is the tenant's trade name, printed in the receipt
	// header (internal/receipt.Config.BusinessName). Empty falls back to a
	// generic label rather than a blank header line.
	BusinessName string `json:"business_name"`

	// BranchName is the branch/şube name, printed under BusinessName when
	// set. Empty omits that line entirely (not every tenant is
	// multi-branch).
	BranchName string `json:"branch_name"`
}

// defaultPrinterWidth is used whenever PrinterWidth is absent from
// config.json/environment, or set to a value internal/receipt doesn't
// support.
const defaultPrinterWidth = 48

// defaultConfig is used when no config.json is present and no environment
// override is set. It targets the local dev stack started via
// `task compose:up` + `task backend:dev`.
func defaultConfig() Config {
	return Config{
		APIBaseURL:     "http://localhost:8080",
		KeycloakURL:    "http://localhost:8090",
		KeycloakRealm:  "onlinemenu",
		EnableDevLogin: true,
		PrinterAddr:    "",
		PrinterWidth:   defaultPrinterWidth,
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
	PrinterAddr    string `json:"printer_addr"`
	PrinterWidth   int    `json:"printer_width"`
	BusinessName   string `json:"business_name"`
	BranchName     string `json:"branch_name"`
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
		if fileCfg.PrinterAddr != "" {
			cfg.PrinterAddr = fileCfg.PrinterAddr
		}
		if fileCfg.PrinterWidth != 0 {
			cfg.PrinterWidth = fileCfg.PrinterWidth
		}
		if fileCfg.BusinessName != "" {
			cfg.BusinessName = fileCfg.BusinessName
		}
		if fileCfg.BranchName != "" {
			cfg.BranchName = fileCfg.BranchName
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
	if v := os.Getenv("POS_PRINTER_ADDR"); v != "" {
		cfg.PrinterAddr = v
	}
	if v := os.Getenv("POS_PRINTER_WIDTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PrinterWidth = n
		}
	}
	if v := os.Getenv("POS_BUSINESS_NAME"); v != "" {
		cfg.BusinessName = v
	}
	if v := os.Getenv("POS_BRANCH_NAME"); v != "" {
		cfg.BranchName = v
	}

	if cfg.PrinterWidth != 32 && cfg.PrinterWidth != 48 {
		cfg.PrinterWidth = defaultPrinterWidth
	}

	return cfg, nil
}
