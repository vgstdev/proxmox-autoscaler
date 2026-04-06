// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package monitor

import "time"

// ResourceKind identifies which resource a state refers to.
type ResourceKind string

const (
	ResourceCPU    ResourceKind = "cpu"
	ResourceMemory ResourceKind = "memory"
)

// boostPhase represents whether a resource is currently boosted.
type boostPhase int

const (
	phaseNormal boostPhase = iota
	phaseBoosted
)

// ResourceState tracks the autoscaling state for a single resource of a container.
type ResourceState struct {
	Phase             boostPhase
	SaturatedCount    int       // consecutive saturated polls
	DownscaleCount    int       // consecutive polls below the downscale threshold while boosted
	OriginalValue     float64   // value before boost
	BoostedValue      float64   // value after boost
	BoostFactor       float64   // factor applied (1.5 or 1.25)
	BoostedAt         time.Time // when the boost was applied
	UsageHistory      []float64 // rolling history of usage fractions (most recent last)
	PreBoostAvg       float64   // average usage before saturation kicked in
	warnedUnlimited   bool      // whether we already warned about cpulimit=0
	downscaleDeferred bool      // whether we've already logged that a revert was deferred
}

// ContainerState holds the per-resource states for a container.
type ContainerState struct {
	VMID int
	Name string
	CPU  ResourceState
	Mem  ResourceState
}

// addHistory appends v to the usage history, capping at maxLen.
func (rs *ResourceState) addHistory(v float64, maxLen int) {
	rs.UsageHistory = append(rs.UsageHistory, v)
	if len(rs.UsageHistory) > maxLen {
		rs.UsageHistory = rs.UsageHistory[len(rs.UsageHistory)-maxLen:]
	}
}

// computePreBoostAvg calculates the average of history excluding the last
// consecutiveSaturated entries. Returns 0.5 if not enough data.
func (rs *ResourceState) computePreBoostAvg(consecutiveSaturated int) float64 {
	usable := len(rs.UsageHistory) - consecutiveSaturated
	if usable <= 0 {
		return 0.5
	}
	slice := rs.UsageHistory[:usable]
	sum := 0.0
	for _, v := range slice {
		sum += v
	}
	return sum / float64(len(slice))
}

// observeBoostedUsage tracks whether usage has stayed low enough to allow a downscale.
// The threshold is strict: usage must be lower than the configured value.
func (rs *ResourceState) observeBoostedUsage(usage, threshold float64) {
	if usage < threshold {
		rs.DownscaleCount++
		return
	}

	rs.DownscaleCount = 0
	rs.downscaleDeferred = false
}

// canDownscale reports whether the boost has lasted long enough and usage has
// remained below the downscale threshold for the required number of polls.
func (rs *ResourceState) canDownscale(now time.Time, minBoostDuration time.Duration, requiredSamples int) bool {
	if now.Sub(rs.BoostedAt) < minBoostDuration {
		return false
	}
	return rs.DownscaleCount >= requiredSamples
}

// clearBoostState resets transient boost tracking while keeping usage history.
func (rs *ResourceState) clearBoostState() {
	rs.Phase = phaseNormal
	rs.SaturatedCount = 0
	rs.DownscaleCount = 0
	rs.BoostedValue = 0
	rs.BoostFactor = 0
	rs.BoostedAt = time.Time{}
	rs.PreBoostAvg = 0
	rs.downscaleDeferred = false
}
