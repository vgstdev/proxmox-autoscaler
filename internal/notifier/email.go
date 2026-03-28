// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package notifier

import (
	"bytes"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// BoostParams holds parameters for a boost notification email.
type BoostParams struct {
	Hostname      string
	VMID          int
	Name          string
	Resource      string // "cpu" | "memory"
	Original      float64
	Boosted       float64
	BoostFactor   float64
	UsagePct      float64
	ElapsedSecs   int
	CPUResource   string        // "cores" | "cpulimit"
	BoostDuration time.Duration // configured boost duration
}

// RevertParams holds parameters for a revert notification email.
type RevertParams struct {
	Hostname      string
	VMID          int
	Name          string
	Resource      string
	Boosted       float64
	Original      float64
	UsagePct      float64
	CPUResource   string
	BoostDuration time.Duration
}

// EmailNotifier sends email notifications via the system mail binary.
type EmailNotifier struct {
	enabled    bool
	mailBinary string
	to         string
	logger     *slog.Logger
}

// New creates a new EmailNotifier.
func New(enabled bool, mailBinary, to string, logger *slog.Logger) *EmailNotifier {
	return &EmailNotifier{
		enabled:    enabled,
		mailBinary: mailBinary,
		to:         to,
		logger:     logger,
	}
}

// SendBoost sends a boost notification email.
func (n *EmailNotifier) SendBoost(p BoostParams) {
	if !n.enabled {
		return
	}

	subject := fmt.Sprintf("Autoescalado producido en %s => %s(%d)",
		strings.ToUpper(shortHostname(p.Hostname)),
		strings.ToLower(p.Name),
		p.VMID,
	)

	resourceName, unit := resourceLabel(p.Resource, p.CPUResource)
	pctIncrease := int((p.BoostFactor - 1) * 100)

	body := fmt.Sprintf(
		"Contenedor: %d (%s)\nRecurso escalado: %s\nValor original: %s %s\nNuevo valor temporal: %s %s (+%d%%)\nMotivo: uso sostenido al %.0f%% durante %ds\nDuracion del boost: %s\nTimestamp: %s\n",
		p.VMID,
		p.Name,
		resourceName,
		formatValue(p.Resource, p.CPUResource, p.Original),
		unit,
		formatValue(p.Resource, p.CPUResource, p.Boosted),
		unit,
		pctIncrease,
		p.UsagePct,
		p.ElapsedSecs,
		p.BoostDuration.String(),
		time.Now().UTC().Format(time.RFC3339),
	)

	n.send(subject, body)
}

// SendRevert sends a revert notification email.
func (n *EmailNotifier) SendRevert(p RevertParams) {
	if !n.enabled {
		return
	}

	subject := fmt.Sprintf("Autoescalado revertido en %s => %s(%d)",
		strings.ToUpper(shortHostname(p.Hostname)),
		strings.ToLower(p.Name),
		p.VMID,
	)

	resourceName, unit := resourceLabel(p.Resource, p.CPUResource)

	body := fmt.Sprintf(
		"Contenedor: %d (%s)\nRecurso revertido: %s\nValor durante boost: %s %s\nValor restaurado: %s %s\nUso al revertir: %.0f%%\nTimestamp: %s\n",
		p.VMID,
		p.Name,
		resourceName,
		formatValue(p.Resource, p.CPUResource, p.Boosted),
		unit,
		formatValue(p.Resource, p.CPUResource, p.Original),
		unit,
		p.UsagePct,
		time.Now().UTC().Format(time.RFC3339),
	)

	n.send(subject, body)
}

// shortHostname returns the first segment of a hostname (before the first dot).
func shortHostname(h string) string {
	if idx := strings.IndexByte(h, '.'); idx != -1 {
		return h[:idx]
	}
	return h
}

// send executes the mail binary with the given subject and body.
func (n *EmailNotifier) send(subject, body string) {
	cmd := exec.Command(n.mailBinary, "-s", subject, n.to)
	cmd.Stdin = strings.NewReader(body)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		n.logger.Warn("email send failed",
			"error", fmt.Sprintf("%v: %s", err, stderr.String()),
		)
		return
	}

	n.logger.Info("email sent", "to", n.to, "subject", subject)
}

// resourceLabel returns the human-readable resource name and unit for notifications.
func resourceLabel(resource, cpuResource string) (name, unit string) {
	if resource == "cpu" {
		if cpuResource == "cpulimit" {
			return "CPU", "cpulimit"
		}
		return "CPU", "cores"
	}
	return "Memoria", "MB"
}

// formatValue formats a resource value for display.
func formatValue(resource, cpuResource string, value float64) string {
	if resource == "cpu" {
		if cpuResource == "cores" {
			return fmt.Sprintf("%d", int(value))
		}
		return fmt.Sprintf("%.2f", value)
	}
	return fmt.Sprintf("%d", int(value))
}
