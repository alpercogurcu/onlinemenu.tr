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
