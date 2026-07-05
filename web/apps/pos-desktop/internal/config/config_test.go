package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string // empty means no config.json written
		envOverride string
		wantBaseURL string
		wantErr     bool
	}{
		{
			name:        "no config file uses built-in default",
			wantBaseURL: "http://localhost:8080",
		},
		{
			name:        "config.json overrides default",
			fileContent: `{"api_base_url":"https://station.example.com"}`,
			wantBaseURL: "https://station.example.com",
		},
		{
			name:        "env var overrides config.json",
			fileContent: `{"api_base_url":"https://station.example.com"}`,
			envOverride: "https://env-override.example.com",
			wantBaseURL: "https://env-override.example.com",
		},
		{
			name:        "malformed config.json is an error, not a silent default",
			fileContent: `{not json`,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.fileContent != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.fileContent), 0o600); err != nil {
					t.Fatalf("write config.json: %v", err)
				}
			}
			if tt.envOverride != "" {
				t.Setenv("POS_API_BASE_URL", tt.envOverride)
			}

			cfg, err := Load(dir)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load: want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.APIBaseURL != tt.wantBaseURL {
				t.Fatalf("APIBaseURL = %q, want %q", cfg.APIBaseURL, tt.wantBaseURL)
			}
		})
	}
}

func TestLoad_KeycloakDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KeycloakURL != "http://localhost:8090" {
		t.Fatalf("KeycloakURL = %q, want http://localhost:8090", cfg.KeycloakURL)
	}
	if cfg.KeycloakRealm != "onlinemenu" {
		t.Fatalf("KeycloakRealm = %q, want onlinemenu", cfg.KeycloakRealm)
	}
	if !cfg.EnableDevLogin {
		t.Fatal("EnableDevLogin default = false, want true (dev default)")
	}
}

func TestLoad_KeycloakConfigJSONOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	content := `{"keycloak_url":"https://kc.example.com","keycloak_realm":"onlinemenu-staging","enable_dev_login":false}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KeycloakURL != "https://kc.example.com" {
		t.Fatalf("KeycloakURL = %q, want https://kc.example.com", cfg.KeycloakURL)
	}
	if cfg.KeycloakRealm != "onlinemenu-staging" {
		t.Fatalf("KeycloakRealm = %q, want onlinemenu-staging", cfg.KeycloakRealm)
	}
	if cfg.EnableDevLogin {
		t.Fatal("EnableDevLogin = true, want false (explicit in config.json)")
	}
}

func TestLoad_EnableDevLoginAbsentFromConfigJSONKeepsDefaultTrue(t *testing.T) {
	dir := t.TempDir()
	// enable_dev_login intentionally omitted — must not be interpreted as
	// an explicit `false` (Config's bool zero value would make that
	// ambiguous without fileConfig's *bool).
	content := `{"api_base_url":"https://station.example.com"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.EnableDevLogin {
		t.Fatal("EnableDevLogin = false, want true (field absent from config.json, default preserved)")
	}
}

func TestLoad_KeycloakEnvVarsOverrideConfigJSON(t *testing.T) {
	dir := t.TempDir()
	content := `{"keycloak_url":"https://kc.example.com","keycloak_realm":"onlinemenu-staging","enable_dev_login":true}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv("POS_KEYCLOAK_URL", "https://kc-env.example.com")
	t.Setenv("POS_KEYCLOAK_REALM", "onlinemenu-env")
	t.Setenv("POS_ENABLE_DEV_LOGIN", "false")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.KeycloakURL != "https://kc-env.example.com" {
		t.Fatalf("KeycloakURL = %q, want env override", cfg.KeycloakURL)
	}
	if cfg.KeycloakRealm != "onlinemenu-env" {
		t.Fatalf("KeycloakRealm = %q, want env override", cfg.KeycloakRealm)
	}
	if cfg.EnableDevLogin {
		t.Fatal("EnableDevLogin = true, want false (POS_ENABLE_DEV_LOGIN=false)")
	}
}
