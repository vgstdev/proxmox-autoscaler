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
func (s *Scaler) ComputeBoost(ctx context.Context, vmid int, kind string, currentValue float64) (float64, float64, error) {
	nodeStatus, err := s.client.GetNodeStatus(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("get node status: %w", err)
	}

	allLXC, err := s.client.ListAllLXC(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list all lxc: %w", err)
	}

	// Sum total allocations across all containers.
	var totalAlloc float64
	for _, entry := range allLXC {
		cfg, err := s.client.GetContainerConfig(ctx, entry.VMID)
		if err != nil {
			continue
		}
		if kind == "cpu" {
			if s.cfg.Scaling.CPUResource == "cores" {
				totalAlloc += float64(cfg.Cores)
			} else {
				totalAlloc += cfg.CPULimit
			}
		} else {
			totalAlloc += float64(cfg.Memory)
		}
	}

	// Available host capacity for this resource.
	var hostMax float64
	if kind == "cpu" {
		hostMax = float64(nodeStatus.MaxCPU())
	} else {
		// MaxMemBytes is in bytes; convert to MB.
		hostMax = nodeStatus.MaxMemBytes() / (1024 * 1024)
	}

	// Headroom = hostMax - totalAlloc + currentValue (current container's contribution)
	headroom := hostMax - totalAlloc + currentValue

	for _, factor := range []float64{s.cfg.Scaling.PrimaryBoostFactor, s.cfg.Scaling.FallbackBoostFactor} {
		candidate := applyFactor(kind, s.cfg.Scaling.CPUResource, currentValue, factor)
		if candidate-currentValue <= headroom+0.01 {
			return candidate, factor, nil
		}
	}

	return 0, 0, fmt.Errorf("no boost factor fits within host capacity (headroom=%.2f, primary_factor=%.2f, fallback_factor=%.2f)",
		headroom, s.cfg.Scaling.PrimaryBoostFactor, s.cfg.Scaling.FallbackBoostFactor)
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
