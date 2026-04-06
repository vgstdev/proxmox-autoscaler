package monitor

import (
	"testing"
	"time"
)

func TestObserveBoostedUsageStrictThreshold(t *testing.T) {
	rs := &ResourceState{}

	rs.observeBoostedUsage(0.79, 0.80)
	if rs.DownscaleCount != 1 {
		t.Fatalf("expected downscale count 1, got %d", rs.DownscaleCount)
	}

	rs.observeBoostedUsage(0.80, 0.80)
	if rs.DownscaleCount != 0 {
		t.Fatalf("expected downscale count reset at threshold, got %d", rs.DownscaleCount)
	}

	rs.observeBoostedUsage(0.50, 0.80)
	if rs.DownscaleCount != 1 {
		t.Fatalf("expected downscale count to start again, got %d", rs.DownscaleCount)
	}
}

func TestObserveBoostedUsageResetsDeferredFlagWhenUsageClimbsAgain(t *testing.T) {
	rs := &ResourceState{
		DownscaleCount:    4,
		downscaleDeferred: true,
	}

	rs.observeBoostedUsage(0.81, 0.80)

	if rs.DownscaleCount != 0 {
		t.Fatalf("expected downscale count reset, got %d", rs.DownscaleCount)
	}
	if rs.downscaleDeferred {
		t.Fatal("expected deferred flag reset when usage is no longer safe")
	}
}

func TestObserveBoostedUsageRequiresConsecutiveSamples(t *testing.T) {
	rs := &ResourceState{}

	sequence := []float64{0.79, 0.78, 0.82, 0.79, 0.78, 0.77}
	expectedCounts := []int{1, 2, 0, 1, 2, 3}

	for i, usage := range sequence {
		rs.observeBoostedUsage(usage, 0.80)
		if rs.DownscaleCount != expectedCounts[i] {
			t.Fatalf("step %d: expected downscale count %d, got %d", i, expectedCounts[i], rs.DownscaleCount)
		}
	}
}

func TestCanDownscaleRequiresDurationAndSamples(t *testing.T) {
	now := time.Now()
	rs := &ResourceState{
		BoostedAt:      now.Add(-2 * time.Minute),
		DownscaleCount: 5,
	}

	if rs.canDownscale(now, 2*time.Minute, 6) {
		t.Fatal("expected downscale to stay blocked without enough safe samples")
	}

	rs.DownscaleCount = 6
	if !rs.canDownscale(now, 2*time.Minute, 6) {
		t.Fatal("expected downscale to be allowed after enough safe samples")
	}

	rs.BoostedAt = now.Add(-30 * time.Second)
	if rs.canDownscale(now, 2*time.Minute, 6) {
		t.Fatal("expected downscale to stay blocked before minimum boost duration")
	}
}

func TestCanDownscaleAtExactDurationBoundary(t *testing.T) {
	now := time.Now()
	rs := &ResourceState{
		BoostedAt:      now.Add(-2 * time.Minute),
		DownscaleCount: 6,
	}

	if !rs.canDownscale(now, 2*time.Minute, 6) {
		t.Fatal("expected downscale to be allowed at the exact duration boundary")
	}
}

func TestClearBoostStateKeepsBaselineAndHistory(t *testing.T) {
	now := time.Now()
	rs := &ResourceState{
		Phase:             phaseBoosted,
		SaturatedCount:    3,
		DownscaleCount:    4,
		OriginalValue:     2048,
		BoostedValue:      3072,
		BoostFactor:       1.5,
		BoostedAt:         now,
		UsageHistory:      []float64{0.4, 0.7},
		PreBoostAvg:       0.55,
		downscaleDeferred: true,
	}

	rs.clearBoostState()

	if rs.Phase != phaseNormal {
		t.Fatalf("expected phaseNormal, got %v", rs.Phase)
	}
	if rs.OriginalValue != 2048 {
		t.Fatalf("expected original value to be preserved, got %v", rs.OriginalValue)
	}
	if rs.BoostedValue != 0 || rs.BoostFactor != 0 {
		t.Fatalf("expected boost values cleared, got boosted=%v factor=%v", rs.BoostedValue, rs.BoostFactor)
	}
	if rs.SaturatedCount != 0 || rs.DownscaleCount != 0 {
		t.Fatalf("expected counters cleared, got saturated=%d downscale=%d", rs.SaturatedCount, rs.DownscaleCount)
	}
	if !rs.BoostedAt.IsZero() {
		t.Fatal("expected boosted timestamp cleared")
	}
	if rs.PreBoostAvg != 0 {
		t.Fatalf("expected pre-boost average cleared, got %v", rs.PreBoostAvg)
	}
	if len(rs.UsageHistory) != 2 {
		t.Fatalf("expected history to be preserved, got len=%d", len(rs.UsageHistory))
	}
	if rs.downscaleDeferred {
		t.Fatal("expected deferred flag cleared")
	}
}
