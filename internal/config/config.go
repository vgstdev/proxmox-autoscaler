// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the complete service configuration.
type Config struct {
	Proxmox       ProxmoxConfig       `yaml:"proxmox"`
	Monitor       MonitorConfig       `yaml:"monitor"`
	Scaling       ScalingConfig       `yaml:"scaling"`
	Notifications NotificationsConfig `yaml:"notifications"`
	Logging       LoggingConfig       `yaml:"logging"`
	Storage       StorageConfig       `yaml:"storage"`
}

// ProxmoxConfig holds Proxmox API connection settings.
type ProxmoxConfig struct {
	Host        string `yaml:"host"`
	Node        string `yaml:"node"`
	TokenID     string `yaml:"token_id"`
	TokenSecret string `yaml:"token_secret"`
	InsecureTLS bool   `yaml:"insecure_tls"`
}

// MonitorConfig holds monitoring behaviour settings.
type MonitorConfig struct {
	PollInterval                time.Duration `yaml:"poll_interval"`
	SaturationThreshold         float64       `yaml:"saturation_threshold"`
	DownscaleThreshold          float64       `yaml:"downscale_threshold"`
	ConsecutiveSamples          int           `yaml:"consecutive_samples"`
	DownscaleConsecutiveSamples int           `yaml:"downscale_consecutive_samples"`
	BoostDuration               time.Duration `yaml:"boost_duration"`
	HistorySamples              int           `yaml:"history_samples"`
}

// ScalingConfig holds resource scaling settings.
type ScalingConfig struct {
	CPUResource            string  `yaml:"cpu_resource"`
	PrimaryBoostFactor     float64 `yaml:"primary_boost_factor"`
	FallbackBoostFactor    float64 `yaml:"fallback_boost_factor"`
	ExcludeTag             string  `yaml:"exclude_tag"`
	HostCPUMaxThreshold    float64 `yaml:"host_cpu_max_threshold"`
	HostMemoryMaxThreshold float64 `yaml:"host_memory_max_threshold"`
}

// NotificationsConfig holds notification settings for all backends.
type NotificationsConfig struct {
	Email EmailNotifConfig `yaml:"email"`
	Slack SlackNotifConfig `yaml:"slack"`
}

// EmailNotifConfig holds email notification settings.
type EmailNotifConfig struct {
	Enabled    bool   `yaml:"enabled"`
	MailBinary string `yaml:"mail_binary"`
	To         string `yaml:"to"`
	Language   string `yaml:"language"` // "es" | "en"
}

// SlackNotifConfig holds Slack Bot API notification settings.
type SlackNotifConfig struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token"`   // Bot token (xoxb-...)
	Channel string `yaml:"channel"` // Channel ID (e.g. C0XXXXXXXXX)
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	File   string `yaml:"file"`
}

// StorageConfig holds persistence settings.
type StorageConfig struct {
	DBPath string `yaml:"db_path"`
}

// Load reads and parses the YAML configuration file at the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// defaultConfig returns a Config pre-populated with sensible defaults.
func defaultConfig() *Config {
	return &Config{
		Monitor: MonitorConfig{
			PollInterval:                5 * time.Second,
			SaturationThreshold:         0.95,
			DownscaleThreshold:          0.8,
			ConsecutiveSamples:          3,
			DownscaleConsecutiveSamples: 6,
			BoostDuration:               2 * time.Minute,
			HistorySamples:              10,
		},
		Scaling: ScalingConfig{
			CPUResource:            "cores",
			PrimaryBoostFactor:     1.5,
			FallbackBoostFactor:    1.25,
			HostCPUMaxThreshold:    0.9,
			HostMemoryMaxThreshold: 0.9,
		},
		Notifications: NotificationsConfig{
			Email: EmailNotifConfig{
				MailBinary: "/usr/bin/mail",
				Language:   "es",
			},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
		Storage: StorageConfig{
			DBPath: "/var/lib/proxmox-autoscaler/state.db",
		},
	}
}

func (c *Config) validate() error {
	if c.Proxmox.Host == "" {
		return fmt.Errorf("proxmox.host is required")
	}
	if c.Proxmox.Node == "" {
		return fmt.Errorf("proxmox.node is required")
	}
	if c.Proxmox.TokenID == "" {
		return fmt.Errorf("proxmox.token_id is required")
	}
	if c.Proxmox.TokenSecret == "" {
		return fmt.Errorf("proxmox.token_secret is required")
	}
	if c.Monitor.PollInterval <= 0 {
		return fmt.Errorf("monitor.poll_interval must be positive")
	}
	if c.Monitor.SaturationThreshold <= 0 || c.Monitor.SaturationThreshold > 1 {
		return fmt.Errorf("monitor.saturation_threshold must be between 0 and 1")
	}
	if c.Monitor.DownscaleThreshold <= 0 || c.Monitor.DownscaleThreshold > 1 {
		return fmt.Errorf("monitor.downscale_threshold must be between 0 and 1")
	}
	if c.Monitor.DownscaleThreshold >= c.Monitor.SaturationThreshold {
		return fmt.Errorf("monitor.downscale_threshold must be lower than monitor.saturation_threshold")
	}
	if c.Monitor.ConsecutiveSamples <= 0 {
		return fmt.Errorf("monitor.consecutive_samples must be positive")
	}
	if c.Monitor.DownscaleConsecutiveSamples <= 0 {
		return fmt.Errorf("monitor.downscale_consecutive_samples must be positive")
	}
	if c.Monitor.BoostDuration <= 0 {
		return fmt.Errorf("monitor.boost_duration must be positive")
	}
	if c.Monitor.HistorySamples <= 0 {
		return fmt.Errorf("monitor.history_samples must be positive")
	}
	if c.Scaling.CPUResource != "cores" && c.Scaling.CPUResource != "cpulimit" {
		return fmt.Errorf("scaling.cpu_resource must be 'cores' or 'cpulimit'")
	}
	if c.Scaling.PrimaryBoostFactor <= 1 {
		return fmt.Errorf("scaling.primary_boost_factor must be > 1")
	}
	if c.Scaling.FallbackBoostFactor <= 1 {
		return fmt.Errorf("scaling.fallback_boost_factor must be > 1")
	}
	if c.Storage.DBPath == "" {
		return fmt.Errorf("storage.db_path is required")
	}
	return nil
}
