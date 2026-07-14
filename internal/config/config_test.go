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

func TestLoadGrouping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tvlink.toml")
	content := `listen_addr = ":8080"
tvlink_api_key = "tlk-client"
key_group_size = 3
group_usage_limit = 600
group_rotation_timezone = "Asia/Shanghai"

[[tavily_keys]]
name = "primary-01"
api_key = "tvly-one"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}

	settings, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settings.GroupingEnabled() {
		t.Fatal("GroupingEnabled() = false, want true")
	}
	if settings.KeyGroupSize != 3 {
		t.Errorf("KeyGroupSize = %d, want 3", settings.KeyGroupSize)
	}
	if settings.GroupUsageLimit != 600 {
		t.Errorf("GroupUsageLimit = %v, want 600", settings.GroupUsageLimit)
	}
	if settings.GroupRotationTimezone != "Asia/Shanghai" {
		t.Errorf("GroupRotationTimezone = %q, want Asia/Shanghai", settings.GroupRotationTimezone)
	}
}

func TestLoadRejectsInvalidGrouping(t *testing.T) {
	tests := []string{
		"key_group_size = 0\ngroup_usage_limit = 600\ngroup_rotation_timezone = \"Asia/Shanghai\"",
		"key_group_size = 3\ngroup_usage_limit = 0\ngroup_rotation_timezone = \"Asia/Shanghai\"",
		"key_group_size = 3\ngroup_usage_limit = 600\ngroup_rotation_timezone = \"Mars/Olympus\"",
		"group_rotation_timezone = \"Asia/Shanghai\"",
	}
	for _, grouping := range tests {
		path := filepath.Join(t.TempDir(), "tvlink.toml")
		content := "listen_addr = \":8080\"\ntvlink_api_key = \"tlk-client\"\n" + grouping + "\n\n[[tavily_keys]]\nname = \"primary-01\"\napi_key = \"tvly-one\"\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write configuration: %v", err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("Load() error = nil for grouping %q", grouping)
		}
	}
}
