// Package config loads and validates TvLink configuration.
package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	defaultListenAddr             = ":8080"
	defaultUsageRefreshInterval   = 90 * time.Second
	defaultResearchMappingTTL     = 24 * time.Hour
	defaultMonitorRefreshInterval = 5 * time.Second
	defaultRequestBodyLimit       = ByteSize(32 << 20)
)

// Config contains the process-wide TvLink settings.
type Config struct {
	ListenAddr             string        `toml:"listen_addr"`
	TvLinkAPIKey           string        `toml:"tvlink_api_key"`
	TavilyKeys             []TavilyKey   `toml:"tavily_keys"`
	UsageRefreshInterval   time.Duration `toml:"usage_refresh_interval"`
	ResearchMappingTTL     time.Duration `toml:"research_mapping_ttl"`
	MonitorRefreshInterval time.Duration `toml:"monitor_refresh_interval"`
	RequestBodyLimit       ByteSize      `toml:"request_body_limit"`
	KeyGroupSize           int           `toml:"key_group_size"`
	GroupUsageLimit        float64       `toml:"group_usage_limit"`
	GroupRotationTimezone  string        `toml:"group_rotation_timezone"`
}

// TavilyKey identifies a single upstream Tavily credential.
type TavilyKey struct {
	Name   string `toml:"name"`
	APIKey string `toml:"api_key"`
}

// ByteSize stores a byte count parsed from a TOML size string such as "32MiB".
type ByteSize int64

// UnmarshalText parses binary and decimal byte-size suffixes.
func (s *ByteSize) UnmarshalText(text []byte) error {
	value := strings.ToUpper(strings.TrimSpace(string(text)))
	for _, unit := range []struct {
		suffix     string
		multiplier int64
	}{
		{suffix: "GIB", multiplier: 1 << 30},
		{suffix: "MIB", multiplier: 1 << 20},
		{suffix: "KIB", multiplier: 1 << 10},
		{suffix: "GB", multiplier: 1_000_000_000},
		{suffix: "MB", multiplier: 1_000_000},
		{suffix: "KB", multiplier: 1_000},
		{suffix: "B", multiplier: 1},
	} {
		if number, ok := strings.CutSuffix(value, unit.suffix); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(number), 10, 64)
			if err != nil {
				return fmt.Errorf("parse byte size %q: %w", text, err)
			}
			if parsed <= 0 {
				return fmt.Errorf("byte size %q must be positive", text)
			}
			*s = ByteSize(parsed * unit.multiplier)
			return nil
		}
	}
	return fmt.Errorf("unsupported byte size %q", text)
}

// Load reads one TOML configuration file and validates every setting.
func Load(path string) (Config, error) {
	config := Config{
		ListenAddr:             defaultListenAddr,
		UsageRefreshInterval:   defaultUsageRefreshInterval,
		ResearchMappingTTL:     defaultResearchMappingTTL,
		MonitorRefreshInterval: defaultMonitorRefreshInterval,
		RequestBodyLimit:       defaultRequestBodyLimit,
	}

	metadata, err := toml.DecodeFile(path, &config)
	if err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("unknown configuration fields: %s", undecoded)
	}
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// GroupingEnabled reports whether any key-group setting was configured.
func (c Config) GroupingEnabled() bool {
	return c.KeyGroupSize != 0 || c.GroupUsageLimit != 0 || strings.TrimSpace(c.GroupRotationTimezone) != ""
}

func (c Config) validate() error {
	if strings.TrimSpace(c.ListenAddr) == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if strings.TrimSpace(c.TvLinkAPIKey) == "" {
		return fmt.Errorf("tvlink_api_key is required")
	}
	if c.UsageRefreshInterval < defaultUsageRefreshInterval {
		return fmt.Errorf("usage_refresh_interval must be at least %s", defaultUsageRefreshInterval)
	}
	if c.ResearchMappingTTL <= 0 {
		return fmt.Errorf("research_mapping_ttl must be positive")
	}
	if c.MonitorRefreshInterval <= 0 {
		return fmt.Errorf("monitor_refresh_interval must be positive")
	}
	if c.RequestBodyLimit <= 0 {
		return fmt.Errorf("request_body_limit must be positive")
	}
	if c.GroupingEnabled() {
		if c.KeyGroupSize <= 0 {
			return fmt.Errorf("key_group_size must be positive when grouping is enabled")
		}
		if c.GroupUsageLimit <= 0 || math.IsNaN(c.GroupUsageLimit) || math.IsInf(c.GroupUsageLimit, 0) {
			return fmt.Errorf("group_usage_limit must be a finite positive number when grouping is enabled")
		}
		if strings.TrimSpace(c.GroupRotationTimezone) == "" {
			return fmt.Errorf("group_rotation_timezone is required when grouping is enabled")
		}
		if _, err := time.LoadLocation(c.GroupRotationTimezone); err != nil {
			return fmt.Errorf("load group_rotation_timezone %q: %w", c.GroupRotationTimezone, err)
		}
	}
	if len(c.TavilyKeys) == 0 {
		return fmt.Errorf("at least one tavily_keys entry is required")
	}

	names := make(map[string]struct{}, len(c.TavilyKeys))
	for _, key := range c.TavilyKeys {
		if strings.TrimSpace(key.Name) == "" {
			return fmt.Errorf("tavily key name is required")
		}
		if strings.TrimSpace(key.APIKey) == "" {
			return fmt.Errorf("tavily key %q api_key is required", key.Name)
		}
		if _, exists := names[key.Name]; exists {
			return fmt.Errorf("duplicate tavily key name %q", key.Name)
		}
		names[key.Name] = struct{}{}
	}
	return nil
}
