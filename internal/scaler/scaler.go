// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package scaler

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	"proxmox-autoscaler/internal/config"
	"proxmox-autoscaler/internal/proxmox"
)

// Scaler handles capacity checks and Proxmox config updates.
type Scaler struct {
	client *proxmox.Client
	cfg    *config.Config
	logger *slog.Logger
}

// New creates a new Scaler.
func New(client *proxmox.Client, cfg *config.Config, logger *slog.Logger) *Scaler {
	return &Scaler{client: client, cfg: cfg, logger: logger}
}

// ComputeBoost calculates the boosted value for a resource, checking host capacity.
// kind must be "cpu" or "memory".
// Returns the boosted value and the factor used, or an error if no boost fits.
//
// CPU hard capacity (cores sum vs physical cores) is NOT checked because Proxmox
// uses the CFS scheduler and allows overprovisioning by design. However, if the
// host itself is saturated above host_cpu_max_threshold, the boost is skipped —
// there is no spare CPU time to redistribute.
func (s *Scaler) ComputeBoost(_ context.Context, vmid int, kind string, currentValue float64, nodeStatus *proxmox.NodeStatus) (float64, float64, error) {
	// Check host-level saturation regardless of resource type.
	if kind == "cpu" {
		if nodeStatus.CPU >= s.cfg.Scaling.HostCPUMaxThreshold {
			return 0, 0, fmt.Errorf("host CPU saturated (%.0f%% >= threshold %.0f%%) — no spare CPU time to redistribute",
				nodeStatus.CPU*100, s.cfg.Scaling.HostCPUMaxThreshold*100)
		}
		// CPU overprovisioning is normal in Proxmox — no hard core count check needed.
		candidate := applyFactor(kind, s.cfg.Scaling.CPUResource, currentValue, s.cfg.Scaling.PrimaryBoostFactor)
		return candidate, s.cfg.Scaling.PrimaryBoostFactor, nil
	}

	hostMaxBytes := nodeStatus.MaxMemBytes()
	if hostMaxBytes <= 0 {
		return 0, 0, fmt.Errorf("host memory total unavailable")
	}

	hostMemUsed := nodeStatus.Memory.Used / (1024 * 1024)
	hostMax := hostMaxBytes / (1024 * 1024)
	hostMemFraction := nodeStatus.Memory.Used / hostMaxBytes
	if hostMemFraction >= s.cfg.Scaling.HostMemoryMaxThreshold {
		return 0, 0, fmt.Errorf("host memory saturated (%.0f%% >= threshold %.0f%%) — host_used=%.0fMB host_limit=%.0fMB host_max=%.0fMB",
			hostMemFraction*100, s.cfg.Scaling.HostMemoryMaxThreshold*100, hostMemUsed, hostMax*s.cfg.Scaling.HostMemoryMaxThreshold, hostMax)
	}

	// Overcommit is normal in Proxmox, so base capacity on real host usage.
	// The boost delta is treated conservatively as potential extra demand.
	hostLimit := hostMax * s.cfg.Scaling.HostMemoryMaxThreshold
	headroom := hostLimit - hostMemUsed

	s.logger.Debug("capacity check summary",
		"vmid", vmid,
		"host_used_mb", fmt.Sprintf("%.0f", hostMemUsed),
		"host_limit_mb", fmt.Sprintf("%.0f", hostLimit),
		"host_max_mb", fmt.Sprintf("%.0f", hostMax),
		"current_mb", fmt.Sprintf("%.0f", currentValue),
		"headroom_mb", fmt.Sprintf("%.0f", headroom),
	)

	for _, factor := range []float64{s.cfg.Scaling.PrimaryBoostFactor, s.cfg.Scaling.FallbackBoostFactor} {
		candidate := applyFactor(kind, s.cfg.Scaling.CPUResource, currentValue, factor)
		delta := candidate - currentValue
		s.logger.Debug("capacity check: testing factor",
			"vmid", vmid,
			"factor", factor,
			"candidate_mb", fmt.Sprintf("%.0f", candidate),
			"delta_mb", fmt.Sprintf("%.0f", delta),
			"headroom_mb", fmt.Sprintf("%.0f", headroom),
			"fits", delta <= headroom+0.01,
		)
		if delta <= headroom+0.01 {
			return candidate, factor, nil
		}
	}

	return 0, 0, fmt.Errorf("no boost factor fits within real host memory headroom below threshold (host_used=%.0fMB, host_limit=%.0fMB, host_max=%.0fMB, current=%.0fMB, headroom=%.0fMB)",
		hostMemUsed, hostLimit, hostMax, currentValue, headroom)
}

// ApplyBoost sends the boosted value to Proxmox.
func (s *Scaler) ApplyBoost(ctx context.Context, vmid int, kind string, boostedValue float64) error {
	return s.applyValue(ctx, vmid, kind, boostedValue)
}

// RevertBoost sends the original (pre-boost) value to Proxmox.
func (s *Scaler) RevertBoost(ctx context.Context, vmid int, kind string, originalValue float64) error {
	return s.applyValue(ctx, vmid, kind, originalValue)
}

// applyValue sends a resource update to Proxmox.
func (s *Scaler) applyValue(ctx context.Context, vmid int, kind string, value float64) error {
	req := proxmox.ConfigUpdateRequest{}

	if kind == "cpu" {
		if s.cfg.Scaling.CPUResource == "cores" {
			cores := int(math.Ceil(value))
			req.Cores = &cores
		} else {
			rounded := math.Round(value*100) / 100
			req.CPULimit = &rounded
		}
	} else {
		mem := int(math.Ceil(value))
		req.Memory = &mem
	}

	return s.client.UpdateContainerConfig(ctx, vmid, req)
}

// applyFactor computes the boosted value for a resource.
func applyFactor(kind string, cpuResource string, current, factor float64) float64 {
	raw := current * factor
	if kind == "cpu" {
		if cpuResource == "cores" {
			return math.Ceil(raw)
		}
		// cpulimit: round to 2 decimal places
		return math.Round(raw*100) / 100
	}
	// memory: round up to nearest MB
	return math.Ceil(raw)
}
