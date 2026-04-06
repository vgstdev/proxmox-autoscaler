package config

import (
	"strings"
	"testing"
)

func TestDefaultConfigIncludesDownscaleDefaults(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Monitor.DownscaleThreshold != 0.8 {
		t.Fatalf("expected default downscale threshold 0.8, got %v", cfg.Monitor.DownscaleThreshold)
	}
	if cfg.Monitor.DownscaleConsecutiveSamples != 6 {
		t.Fatalf("expected default downscale consecutive samples 6, got %d", cfg.Monitor.DownscaleConsecutiveSamples)
	}
}

func TestValidateRejectsInvalidDownscaleThreshold(t *testing.T) {
	cfg := defaultConfig()
	cfg.Proxmox.Host = "https://example.invalid"
	cfg.Proxmox.Node = "pve"
	cfg.Proxmox.TokenID = "root@pam!autoscaler"
	cfg.Proxmox.TokenSecret = "secret"
	cfg.Monitor.DownscaleThreshold = 0.95

	err := cfg.validate()
	if err == nil {
		t.Fatal("expected validation to fail")
	}
	if !strings.Contains(err.Error(), "downscale_threshold must be lower") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidDownscaleConsecutiveSamples(t *testing.T) {
	cfg := defaultConfig()
	cfg.Proxmox.Host = "https://example.invalid"
	cfg.Proxmox.Node = "pve"
	cfg.Proxmox.TokenID = "root@pam!autoscaler"
	cfg.Proxmox.TokenSecret = "secret"
	cfg.Monitor.DownscaleConsecutiveSamples = 0

	err := cfg.validate()
	if err == nil {
		t.Fatal("expected validation to fail")
	}
	if !strings.Contains(err.Error(), "downscale_consecutive_samples must be positive") {
		t.Fatalf("unexpected error: %v", err)
	}
}
