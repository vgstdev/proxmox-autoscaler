// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"proxmox-autoscaler/internal/config"
	"proxmox-autoscaler/internal/db"
	"proxmox-autoscaler/internal/notifier"
	"proxmox-autoscaler/internal/proxmox"
	"proxmox-autoscaler/internal/scaler"
)

// Monitor polls Proxmox and drives the autoscaling state machine.
type Monitor struct {
	cfg      *config.Config
	client   *proxmox.Client
	database *db.DB
	scl      *scaler.Scaler
	notif    notifier.Notifier
	logger   *slog.Logger
	hostname string

	mu     sync.RWMutex
	states map[int]*ContainerState

	// track vmids for which we've already warned about unlimited cpulimit
	warnedUnlimited map[int]bool

	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a new Monitor and reconciles any persisted boost states from the DB.
func New(
	cfg *config.Config,
	client *proxmox.Client,
	database *db.DB,
	notif notifier.Notifier,
	logger *slog.Logger,
	hostname string,
) (*Monitor, error) {
	scl := scaler.New(client, cfg, logger)

	m := &Monitor{
		cfg:             cfg,
		client:          client,
		database:        database,
		scl:             scl,
		notif:           notif,
		logger:          logger,
		hostname:        hostname,
		states:          make(map[int]*ContainerState),
		warnedUnlimited: make(map[int]bool),
		done:            make(chan struct{}),
	}

	if err := m.reconcileBootstrapState(); err != nil {
		return nil, fmt.Errorf("reconcile bootstrap state: %w", err)
	}

	return m, nil
}

// Start launches the monitor loop in a background goroutine.
func (m *Monitor) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.loop(ctx)
}

// Stop signals the monitor loop to stop and waits for it to finish.
func (m *Monitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	<-m.done
}

// ActiveStates returns a copy of the current container states (for shutdown revert).
func (m *Monitor) ActiveStates() map[int]*ContainerState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[int]*ContainerState, len(m.states))
	for k, v := range m.states {
		cp := *v
		out[k] = &cp
	}
	return out
}

// loop is the main polling goroutine.
func (m *Monitor) loop(ctx context.Context) {
	defer close(m.done)

	ticker := time.NewTicker(m.cfg.Monitor.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

// poll performs one complete monitoring cycle.
func (m *Monitor) poll(ctx context.Context) {
	containers, err := m.client.ListLXC(ctx, m.cfg.Scaling.ExcludeTag)
	if err != nil {
		m.logger.Error("Proxmox API error", "endpoint", fmt.Sprintf("/nodes/%s/lxc", m.cfg.Proxmox.Node), "error", err)
		return
	}

	nodeStatus, err := m.client.GetNodeStatus(ctx)
	if err != nil {
		m.logger.Error("Proxmox API error", "endpoint", fmt.Sprintf("/nodes/%s/status", m.cfg.Proxmox.Node), "error", err)
		return
	}

	for _, entry := range containers {
		if entry.Status == "excluded" {
			m.mu.Lock()
			if !m.warnedUnlimited[entry.VMID] {
				m.warnedUnlimited[entry.VMID] = true
				m.logger.Warn("container skipped - excluded by tag",
					"vmid", entry.VMID,
					"tag", m.cfg.Scaling.ExcludeTag,
				)
			}
			m.mu.Unlock()
			continue
		}
		if entry.Status != "running" {
			continue
		}
		m.processContainer(ctx, entry, nodeStatus)
	}

	// Check for expired boosts
	m.checkExpiredBoosts(ctx)
}

// processContainer evaluates saturation for a single container and potentially triggers boosts.
func (m *Monitor) processContainer(ctx context.Context, entry proxmox.LXCEntry, node *proxmox.NodeStatus) {
	status, err := m.client.GetContainerStatus(ctx, entry.VMID)
	if err != nil {
		m.logger.Error("Proxmox API error",
			"endpoint", fmt.Sprintf("/nodes/%s/lxc/%d/status/current", m.cfg.Proxmox.Node, entry.VMID),
			"error", err,
		)
		return
	}

	cfg, err := m.client.GetContainerConfig(ctx, entry.VMID)
	if err != nil {
		m.logger.Error("Proxmox API error",
			"endpoint", fmt.Sprintf("/nodes/%s/lxc/%d/config", m.cfg.Proxmox.Node, entry.VMID),
			"error", err,
		)
		return
	}

	m.mu.Lock()
	state, ok := m.states[entry.VMID]
	if !ok {
		state = &ContainerState{VMID: entry.VMID, Name: entry.Name}
		m.states[entry.VMID] = state
	}
	state.Name = status.Name
	m.mu.Unlock()

	m.evaluateCPU(ctx, state, status, cfg, node)
	m.evaluateMemory(ctx, state, status, cfg, node)
}

// evaluateCPU handles the CPU saturation check and boost trigger.
func (m *Monitor) evaluateCPU(
	ctx context.Context,
	state *ContainerState,
	status *proxmox.ContainerStatus,
	cfg *proxmox.ContainerConfig,
	node *proxmox.NodeStatus,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rs := &state.CPU

	if rs.Phase == phaseBoosted {
		// CPU is already boosted; saturation checks are skipped until it reverts.
		return
	}

	// Determine the allocated CPU value (used for boost amount calculation).
	var allocatedCPU float64
	if m.cfg.Scaling.CPUResource == "cores" {
		allocatedCPU = float64(cfg.Cores)
	} else {
		allocatedCPU = cfg.CPULimit
	}

	// cpulimit=0 means unlimited; skip check.
	if m.cfg.Scaling.CPUResource == "cpulimit" && cfg.CPULimit == 0 {
		if !state.CPU.warnedUnlimited {
			state.CPU.warnedUnlimited = true
			m.logger.Warn("cpulimit=0 (unlimited) on container, skipping CPU saturation check", "vmid", state.VMID)
		}
		return
	}

	// cores=0 means Proxmox is using its default; fall back to status.CPUs.
	if m.cfg.Scaling.CPUResource == "cores" && cfg.Cores == 0 {
		if !state.CPU.warnedUnlimited {
			state.CPU.warnedUnlimited = true
			m.logger.Warn("cores=0 on container config, falling back to status.CPUs for boost calculation", "vmid", state.VMID, "status_cpus", status.CPUs)
		}
		allocatedCPU = status.CPUs
	}

	if allocatedCPU <= 0 {
		m.logger.Warn("could not determine allocated CPU for container, skipping check", "vmid", state.VMID)
		return
	}

	// CPU saturation: status.CPU is the fraction of the container's usable CPUs
	// (status.CPUs) currently in use — directly comparable to the 0–1 threshold.
	cpuUsage := status.CPU

	m.logger.Debug("cpu saturation sample",
		"vmid", state.VMID,
		"status_cpu", status.CPU,
		"status_cpus", status.CPUs,
		"allocated_cpu", allocatedCPU,
		"cpu_usage_fraction", fmt.Sprintf("%.4f", cpuUsage),
		"saturated_count", rs.SaturatedCount,
	)

	rs.addHistory(cpuUsage, m.cfg.Monitor.HistorySamples)

	threshold := m.cfg.Monitor.SaturationThreshold
	if cpuUsage >= threshold {
		rs.SaturatedCount++
	} else {
		rs.SaturatedCount = 0
	}

	if rs.SaturatedCount >= m.cfg.Monitor.ConsecutiveSamples {
		rs.PreBoostAvg = rs.computePreBoostAvg(m.cfg.Monitor.ConsecutiveSamples)
		m.triggerBoost(ctx, state, ResourceCPU, allocatedCPU, cpuUsage)
	}
}

// evaluateMemory handles the memory saturation check and boost trigger.
func (m *Monitor) evaluateMemory(
	ctx context.Context,
	state *ContainerState,
	status *proxmox.ContainerStatus,
	cfg *proxmox.ContainerConfig,
	node *proxmox.NodeStatus,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rs := &state.Mem

	if rs.Phase == phaseBoosted {
		return
	}

	if status.MaxMem <= 0 {
		return
	}

	memUsage := status.Mem / status.MaxMem

	m.logger.Debug("memory saturation sample",
		"vmid", state.VMID,
		"mem_used_bytes", status.Mem,
		"mem_max_bytes", status.MaxMem,
		"mem_usage_fraction", fmt.Sprintf("%.4f", memUsage),
		"saturated_count", rs.SaturatedCount,
	)

	rs.addHistory(memUsage, m.cfg.Monitor.HistorySamples)

	threshold := m.cfg.Monitor.SaturationThreshold
	if memUsage >= threshold {
		rs.SaturatedCount++
	} else {
		rs.SaturatedCount = 0
	}

	if rs.SaturatedCount >= m.cfg.Monitor.ConsecutiveSamples {
		rs.PreBoostAvg = rs.computePreBoostAvg(m.cfg.Monitor.ConsecutiveSamples)
		// Convert current memory allocation to MB for consistent units.
		allocatedMB := float64(cfg.Memory)
		m.triggerBoost(ctx, state, ResourceMemory, allocatedMB, memUsage)
	}
}

// triggerBoost attempts to apply a boost to the given resource.
// Caller must hold m.mu.Lock().
func (m *Monitor) triggerBoost(
	ctx context.Context,
	state *ContainerState,
	kind ResourceKind,
	currentValue float64,
	usageFraction float64,
) {
	boostedValue, factor, err := m.scl.ComputeBoost(ctx, state.VMID, string(kind), currentValue)
	if err != nil {
		m.logger.Warn("boost impossible",
			"vmid", state.VMID,
			"resource", string(kind),
			"reason", err.Error(),
		)
		return
	}

	// Apply the boost via the Proxmox API.
	if err := m.scl.ApplyBoost(ctx, state.VMID, string(kind), boostedValue); err != nil {
		m.logger.Error("Proxmox API error",
			"endpoint", fmt.Sprintf("/nodes/%s/lxc/%d/config", m.cfg.Proxmox.Node, state.VMID),
			"error", err,
		)
		return
	}

	now := time.Now()
	rs := m.resourceState(state, kind)
	rs.Phase = phaseBoosted
	rs.OriginalValue = currentValue
	rs.BoostedValue = boostedValue
	rs.BoostFactor = factor
	rs.BoostedAt = now
	rs.SaturatedCount = 0

	m.logger.Info("boost applied",
		"vmid", state.VMID,
		"resource", string(kind),
		"original_value", currentValue,
		"new_value", boostedValue,
		"factor", factor,
	)

	// Persist to DB.
	rec := db.BoostRecord{
		VMID:          state.VMID,
		ResourceType:  string(kind),
		OriginalValue: currentValue,
		BoostedValue:  boostedValue,
		BoostFactor:   factor,
		BoostedAt:     now,
	}
	if err := m.database.SaveBoost(rec); err != nil {
		m.logger.Error("DB error", "operation", "SaveBoost", "error", err)
	}

	// Add "boosted" tag to container.
	if cfg, err := m.client.GetContainerConfig(ctx, state.VMID); err == nil {
		m.setTag(ctx, state.VMID, cfg.Tags, true)
	}

	// Send notification.
	elapsed := int(float64(m.cfg.Monitor.ConsecutiveSamples) * m.cfg.Monitor.PollInterval.Seconds())
	go m.notif.SendBoost(notifier.BoostParams{
		Hostname:      m.hostname,
		VMID:          state.VMID,
		Name:          state.Name,
		Resource:      string(kind),
		Original:      currentValue,
		Boosted:       boostedValue,
		BoostFactor:   factor,
		UsagePct:      usageFraction * 100,
		ElapsedSecs:   elapsed,
		CPUResource:   m.cfg.Scaling.CPUResource,
		BoostDuration: m.cfg.Monitor.BoostDuration,
	})
}

// checkExpiredBoosts reverts any boosts whose duration has elapsed.
func (m *Monitor) checkExpiredBoosts(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for _, state := range m.states {
		for _, kind := range []ResourceKind{ResourceCPU, ResourceMemory} {
			rs := m.resourceState(state, kind)
			if rs.Phase != phaseBoosted {
				continue
			}
			if now.Sub(rs.BoostedAt) < m.cfg.Monitor.BoostDuration {
				continue
			}
			m.revertBoost(ctx, state, kind, rs)
		}
	}
}

// revertBoost performs the revert of a boost.
// Caller must hold m.mu.Lock().
func (m *Monitor) revertBoost(ctx context.Context, state *ContainerState, kind ResourceKind, rs *ResourceState) {
	// Re-read current config from Proxmox to detect manual changes.
	currentCfg, err := m.client.GetContainerConfig(ctx, state.VMID)
	if err != nil {
		m.logger.Error("Proxmox API error",
			"endpoint", fmt.Sprintf("/nodes/%s/lxc/%d/config", m.cfg.Proxmox.Node, state.VMID),
			"error", err,
		)
		return
	}

	var currentActual float64
	if kind == ResourceCPU {
		if m.cfg.Scaling.CPUResource == "cores" {
			currentActual = float64(currentCfg.Cores)
		} else {
			currentActual = currentCfg.CPULimit
		}
	} else {
		currentActual = float64(currentCfg.Memory)
	}

	const epsilon = 0.01
	if abs(currentActual-rs.BoostedValue) > epsilon {
		// Manual change detected.
		m.logger.Warn("manual change detected - adopting new baseline",
			"vmid", state.VMID,
			"resource", string(kind),
			"expected_boost_value", rs.BoostedValue,
			"actual_value", currentActual,
			"new_baseline", currentActual,
		)
		rs.OriginalValue = currentActual
		rs.Phase = phaseNormal
		rs.SaturatedCount = 0
		if err := m.database.DeleteBoost(state.VMID, string(kind)); err != nil {
			m.logger.Error("DB error", "operation", "DeleteBoost", "error", err)
		}
		return
	}

	// Safe to revert.
	if err := m.scl.RevertBoost(ctx, state.VMID, string(kind), rs.OriginalValue); err != nil {
		m.logger.Error("Proxmox API error",
			"endpoint", fmt.Sprintf("/nodes/%s/lxc/%d/config", m.cfg.Proxmox.Node, state.VMID),
			"error", err,
		)
		return
	}

	// Remove "boosted" tag from container.
	m.setTag(ctx, state.VMID, currentCfg.Tags, false)

	// Determine current usage for the log.
	currentStatus, _ := m.client.GetContainerStatus(ctx, state.VMID)
	nodeStatus, _ := m.client.GetNodeStatus(ctx)

	var currentUsagePct float64
	if currentStatus != nil && nodeStatus != nil {
		if kind == ResourceCPU {
			var alloc float64
			if m.cfg.Scaling.CPUResource == "cores" {
				alloc = float64(currentCfg.Cores)
			} else {
				alloc = currentCfg.CPULimit
			}
			if alloc > 0 {
				currentUsagePct = currentStatus.CPU * 100
			}
		} else {
			if currentStatus.MaxMem > 0 {
				currentUsagePct = currentStatus.Mem / currentStatus.MaxMem * 100
			}
		}
	}

	returnedToNormal := currentUsagePct < rs.PreBoostAvg*1.2*100
	if returnedToNormal {
		m.logger.Info("boost reverted - normal",
			"vmid", state.VMID,
			"resource", string(kind),
			"boosted_value", rs.BoostedValue,
			"original_value", rs.OriginalValue,
			"current_usage_pct", fmt.Sprintf("%.1f", currentUsagePct),
		)
	} else {
		m.logger.Info("boost reverted - still elevated",
			"vmid", state.VMID,
			"resource", string(kind),
			"boosted_value", rs.BoostedValue,
			"original_value", rs.OriginalValue,
			"current_usage_pct", fmt.Sprintf("%.1f", currentUsagePct),
		)
	}

	boostedVal := rs.BoostedValue
	originalVal := rs.OriginalValue
	cpuResource := m.cfg.Scaling.CPUResource

	rs.Phase = phaseNormal
	rs.SaturatedCount = 0
	rs.BoostedValue = 0
	rs.BoostFactor = 0
	rs.BoostedAt = time.Time{}

	if err := m.database.DeleteBoost(state.VMID, string(kind)); err != nil {
		m.logger.Error("DB error", "operation", "DeleteBoost", "error", err)
	}

	go m.notif.SendRevert(notifier.RevertParams{
		Hostname:      m.hostname,
		VMID:          state.VMID,
		Name:          state.Name,
		Resource:      string(kind),
		Boosted:       boostedVal,
		Original:      originalVal,
		UsagePct:      currentUsagePct,
		CPUResource:   cpuResource,
		BoostDuration: m.cfg.Monitor.BoostDuration,
	})
}

// reconcileBootstrapState loads persisted boosts from DB and reconciles with Proxmox.
func (m *Monitor) reconcileBootstrapState() error {
	records, err := m.database.LoadAllBoosts()
	if err != nil {
		m.logger.Error("DB error", "operation", "LoadAllBoosts", "error", err)
		return nil // non-fatal: start fresh
	}

	ctx := context.Background()

	for _, rec := range records {
		cfg, err := m.client.GetContainerConfig(ctx, rec.VMID)
		if err != nil {
			m.logger.Error("Proxmox API error",
				"endpoint", fmt.Sprintf("/nodes/%s/lxc/%d/config", m.cfg.Proxmox.Node, rec.VMID),
				"error", err,
			)
			continue
		}

		var currentVal float64
		if rec.ResourceType == string(ResourceCPU) {
			if m.cfg.Scaling.CPUResource == "cores" {
				currentVal = float64(cfg.Cores)
			} else {
				currentVal = cfg.CPULimit
			}
		} else {
			currentVal = float64(cfg.Memory)
		}

		const epsilon = 0.01

		switch {
		case abs(currentVal-rec.BoostedValue) <= epsilon:
			// Still boosted — resume timer.
			elapsed := time.Since(rec.BoostedAt)
			remaining := m.cfg.Monitor.BoostDuration - elapsed

			m.mu.Lock()
			state := m.getOrCreateState(rec.VMID)
			rs := m.resourceStateByType(state, rec.ResourceType)
			rs.Phase = phaseBoosted
			rs.OriginalValue = rec.OriginalValue
			rs.BoostedValue = rec.BoostedValue
			rs.BoostFactor = rec.BoostFactor
			rs.BoostedAt = rec.BoostedAt
			m.mu.Unlock()

			if remaining <= 0 {
				remaining = 0
			}

			m.logger.Info("boost state resumed from DB on startup",
				"vmid", rec.VMID,
				"resource", rec.ResourceType,
				"original", rec.OriginalValue,
				"boosted", rec.BoostedValue,
				"remaining_seconds", int(remaining.Seconds()),
			)

		case abs(currentVal-rec.OriginalValue) <= epsilon:
			// Boost was reverted externally.
			m.logger.Info("boost state cleared from DB - reverted externally",
				"vmid", rec.VMID,
				"resource", rec.ResourceType,
			)
			if err := m.database.DeleteBoost(rec.VMID, rec.ResourceType); err != nil {
				m.logger.Error("DB error", "operation", "DeleteBoost", "error", err)
			}

		default:
			// Manual change.
			m.logger.Warn("manual change detected - adopting new baseline",
				"vmid", rec.VMID,
				"resource", rec.ResourceType,
				"expected_boost_value", rec.BoostedValue,
				"actual_value", currentVal,
				"new_baseline", currentVal,
			)
			if err := m.database.DeleteBoost(rec.VMID, rec.ResourceType); err != nil {
				m.logger.Error("DB error", "operation", "DeleteBoost", "error", err)
			}
		}
	}
	return nil
}

// RevertAllBoosts reverts all active boosts — called during graceful shutdown.
func (m *Monitor) RevertAllBoosts(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.states {
		for _, kind := range []ResourceKind{ResourceCPU, ResourceMemory} {
			rs := m.resourceState(state, kind)
			if rs.Phase != phaseBoosted {
				continue
			}

			if err := m.scl.RevertBoost(ctx, state.VMID, string(kind), rs.OriginalValue); err != nil {
				m.logger.Error("failed to revert boost on shutdown",
					"vmid", state.VMID,
					"resource", string(kind),
					"error", err,
				)
				continue
			}

			if err := m.database.DeleteBoost(state.VMID, string(kind)); err != nil {
				m.logger.Error("DB error", "operation", "DeleteBoost", "error", err)
			}

			rs.Phase = phaseNormal
		}
	}
}

// resourceState returns the ResourceState pointer for the given kind.
func (m *Monitor) resourceState(state *ContainerState, kind ResourceKind) *ResourceState {
	if kind == ResourceCPU {
		return &state.CPU
	}
	return &state.Mem
}

// resourceStateByType returns the ResourceState pointer by string type name.
func (m *Monitor) resourceStateByType(state *ContainerState, resourceType string) *ResourceState {
	if resourceType == string(ResourceCPU) {
		return &state.CPU
	}
	return &state.Mem
}

// getOrCreateState retrieves or creates a ContainerState for vmid.
// Caller must hold m.mu.Lock().
func (m *Monitor) getOrCreateState(vmid int) *ContainerState {
	state, ok := m.states[vmid]
	if !ok {
		state = &ContainerState{VMID: vmid}
		m.states[vmid] = state
	}
	return state
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

const boostedTag = "boosted"

// setTag adds or removes a tag from the container's tag list and updates Proxmox.
func (m *Monitor) setTag(ctx context.Context, vmid int, currentTags string, add bool) {
	tags := splitTags(currentTags)
	if add {
		for _, t := range tags {
			if t == boostedTag {
				return // already present
			}
		}
		tags = append(tags, boostedTag)
	} else {
		filtered := tags[:0]
		for _, t := range tags {
			if t != boostedTag {
				filtered = append(filtered, t)
			}
		}
		tags = filtered
	}
	newTags := joinTags(tags)
	if err := m.client.UpdateContainerConfig(ctx, vmid, proxmox.ConfigUpdateRequest{Tags: &newTags}); err != nil {
		m.logger.Warn("could not update container tags", "vmid", vmid, "error", err)
	}
}

func splitTags(raw string) []string {
	var out []string
	for _, t := range strings.Split(raw, ";") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func joinTags(tags []string) string {
	return strings.Join(tags, ";")
}
