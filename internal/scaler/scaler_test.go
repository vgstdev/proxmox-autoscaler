package scaler

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"proxmox-autoscaler/internal/config"
	"proxmox-autoscaler/internal/proxmox"
)

func TestComputeBoostMemoryAllowsBoostWhenRealHeadroomExists(t *testing.T) {
	s := newTestScaler()
	node := testNodeStatus(63904, 42000)

	boosted, factor, err := s.ComputeBoost(context.Background(), 109, "memory", 4096, node)
	if err != nil {
		t.Fatalf("expected boost to fit, got error: %v", err)
	}
	if boosted != 6144 {
		t.Fatalf("expected primary memory boost to 6144MB, got %.0f", boosted)
	}
	if factor != 1.5 {
		t.Fatalf("expected primary factor 1.5, got %.2f", factor)
	}
}

func TestComputeBoostMemoryFallsBackWhenPrimaryExceedsRealHeadroom(t *testing.T) {
	s := newTestScaler()
	node := testNodeStatus(20000, 15400)

	boosted, factor, err := s.ComputeBoost(context.Background(), 109, "memory", 6144, node)
	if err != nil {
		t.Fatalf("expected fallback boost to fit, got error: %v", err)
	}
	if boosted != 7680 {
		t.Fatalf("expected fallback memory boost to 7680MB, got %.0f", boosted)
	}
	if factor != 1.25 {
		t.Fatalf("expected fallback factor 1.25, got %.2f", factor)
	}
}

func TestComputeBoostMemoryRejectsWhenHostIsAtThreshold(t *testing.T) {
	s := newTestScaler()
	node := testNodeStatus(63904, 57514)

	_, _, err := s.ComputeBoost(context.Background(), 109, "memory", 4096, node)
	if err == nil {
		t.Fatal("expected host memory saturation error")
	}
	if !strings.Contains(err.Error(), "host memory saturated") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestComputeBoostMemoryRejectsWhenNoFactorFitsRealHeadroom(t *testing.T) {
	s := newTestScaler()
	node := testNodeStatus(20000, 17050)

	_, _, err := s.ComputeBoost(context.Background(), 109, "memory", 4096, node)
	if err == nil {
		t.Fatal("expected insufficient headroom error")
	}
	if !strings.Contains(err.Error(), "real host memory headroom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestScaler() *Scaler {
	return &Scaler{
		cfg: &config.Config{
			Scaling: config.ScalingConfig{
				CPUResource:            "cores",
				PrimaryBoostFactor:     1.5,
				FallbackBoostFactor:    1.25,
				HostCPUMaxThreshold:    0.9,
				HostMemoryMaxThreshold: 0.9,
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func testNodeStatus(totalMB, usedMB float64) *proxmox.NodeStatus {
	node := &proxmox.NodeStatus{}
	node.Memory.Total = totalMB * 1024 * 1024
	node.Memory.Used = usedMB * 1024 * 1024
	return node
}
