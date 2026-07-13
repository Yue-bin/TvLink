package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name: "valid configuration",
			content: `listen_addr = ":8080"
tvlink_api_key = "tlk-client"
usage_refresh_interval = "90s"
research_mapping_ttl = "24h"
monitor_refresh_interval = "5s"
request_body_limit = "32MiB"

[[tavily_keys]]
name = "primary-01"
api_key = "tvly-one"
`,
		},
		{
			name: "unknown field is rejected",
			content: `listen_addr = ":8080"
tvlink_api_key = "tlk-client"
unknown = true

[[tavily_keys]]
name = "primary-01"
api_key = "tvly-one"
`,
			wantErr: true,
		},
		{
			name: "missing client key is rejected",
			content: `listen_addr = ":8080"

[[tavily_keys]]
name = "primary-01"
api_key = "tvly-one"
`,
			wantErr: true,
		},
		{
			name: "duplicate key names are rejected",
			content: `listen_addr = ":8080"
tvlink_api_key = "tlk-client"

[[tavily_keys]]
name = "primary"
api_key = "tvly-one"

[[tavily_keys]]
name = "primary"
api_key = "tvly-two"
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tvlink.toml")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write configuration: %v", err)
			}

			got, err := Load(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.ListenAddr != ":8080" {
				t.Errorf("ListenAddr = %q, want %q", got.ListenAddr, ":8080")
			}
			if len(got.TavilyKeys) != 1 {
				t.Errorf("len(TavilyKeys) = %d, want 1", len(got.TavilyKeys))
			}
		})
	}
}
