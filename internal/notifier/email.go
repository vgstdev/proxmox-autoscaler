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

// translations holds all localised strings used in email notifications.
type translations struct {
	SubjectBoost   string // e.g. "Autoscaling triggered on"
	SubjectRevert  string // e.g. "Autoscaling reverted on"
	Container      string
	ScaledResource string
	OriginalValue  string
	NewTempValue   string
	Reason         string
	BoostDuration  string
	RevertResource string
	BoostValue     string
	RestoredValue  string
	UsageAtRevert  string
	Timestamp      string
	Memory         string // resource label for memory
}

var langs = map[string]translations{
	"es": {
		SubjectBoost:   "Autoescalado producido en",
		SubjectRevert:  "Autoescalado revertido en",
		Container:      "Contenedor",
		ScaledResource: "Recurso escalado",
		OriginalValue:  "Valor original",
		NewTempValue:   "Nuevo valor temporal",
		Reason:         "Motivo: uso sostenido al %.0f%% durante %ds",
		BoostDuration:  "Duracion del boost",
		RevertResource: "Recurso revertido",
		BoostValue:     "Valor durante boost",
		RestoredValue:  "Valor restaurado",
		UsageAtRevert:  "Uso al revertir",
		Timestamp:      "Timestamp",
		Memory:         "Memoria",
	},
	"en": {
		SubjectBoost:   "Autoscaling triggered on",
		SubjectRevert:  "Autoscaling reverted on",
		Container:      "Container",
		ScaledResource: "Scaled resource",
		OriginalValue:  "Original value",
		NewTempValue:   "Temporary new value",
		Reason:         "Reason: sustained usage at %.0f%% for %ds",
		BoostDuration:  "Boost duration",
		RevertResource: "Reverted resource",
		BoostValue:     "Value during boost",
		RestoredValue:  "Restored value",
		UsageAtRevert:  "Usage at revert",
		Timestamp:      "Timestamp",
		Memory:         "Memory",
	},
}

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
	lang       translations
	logger     *slog.Logger
}

// New creates a new EmailNotifier. language must be "es" or "en"; defaults to "es".
func New(enabled bool, mailBinary, to, language string, logger *slog.Logger) *EmailNotifier {
	t, ok := langs[language]
	if !ok {
		t = langs["es"]
	}
	return &EmailNotifier{
		enabled:    enabled,
		mailBinary: mailBinary,
		to:         to,
		lang:       t,
		logger:     logger,
	}
}

// SendBoost sends a boost notification email.
func (n *EmailNotifier) SendBoost(p BoostParams) {
	if !n.enabled {
		return
	}

	subject := fmt.Sprintf("%s %s => %s(%d)",
		n.lang.SubjectBoost,
		strings.ToUpper(shortHostname(p.Hostname)),
		strings.ToLower(p.Name),
		p.VMID,
	)

	resourceName, unit := n.resourceLabel(p.Resource, p.CPUResource)
	pctIncrease := int((p.BoostFactor - 1) * 100)
	reason := fmt.Sprintf(n.lang.Reason, p.UsagePct, p.ElapsedSecs)

	body := fmt.Sprintf(
		"%s: %d (%s)\n%s: %s\n%s: %s %s\n%s: %s %s (+%d%%)\n%s\n%s: %s\n%s: %s\n",
		n.lang.Container, p.VMID, p.Name,
		n.lang.ScaledResource, resourceName,
		n.lang.OriginalValue, formatValue(p.Resource, p.CPUResource, p.Original), unit,
		n.lang.NewTempValue, formatValue(p.Resource, p.CPUResource, p.Boosted), unit, pctIncrease,
		reason,
		n.lang.BoostDuration, formatDuration(p.BoostDuration),
		n.lang.Timestamp, time.Now().Format(time.RFC3339),
	)

	n.send(subject, body)
}

// SendRevert sends a revert notification email.
func (n *EmailNotifier) SendRevert(p RevertParams) {
	if !n.enabled {
		return
	}

	subject := fmt.Sprintf("%s %s => %s(%d)",
		n.lang.SubjectRevert,
		strings.ToUpper(shortHostname(p.Hostname)),
		strings.ToLower(p.Name),
		p.VMID,
	)

	resourceName, unit := n.resourceLabel(p.Resource, p.CPUResource)

	body := fmt.Sprintf(
		"%s: %d (%s)\n%s: %s\n%s: %s %s\n%s: %s %s\n%s: %.0f%%\n%s: %s\n",
		n.lang.Container, p.VMID, p.Name,
		n.lang.RevertResource, resourceName,
		n.lang.BoostValue, formatValue(p.Resource, p.CPUResource, p.Boosted), unit,
		n.lang.RestoredValue, formatValue(p.Resource, p.CPUResource, p.Original), unit,
		n.lang.UsageAtRevert, p.UsagePct,
		n.lang.Timestamp, time.Now().Format(time.RFC3339),
	)

	n.send(subject, body)
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

// resourceLabel returns the human-readable resource name and unit.
func (n *EmailNotifier) resourceLabel(resource, cpuResource string) (name, unit string) {
	if resource == "cpu" {
		if cpuResource == "cpulimit" {
			return "CPU", "cpulimit"
		}
		return "CPU", "cores"
	}
	return n.lang.Memory, "MB"
}

// shortHostname returns the first segment of a hostname (before the first dot).
func shortHostname(h string) string {
	if idx := strings.IndexByte(h, '.'); idx != -1 {
		return h[:idx]
	}
	return h
}

// formatDuration formats a duration as a human-readable string without redundant zero units.
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	case m > 0 && s > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", s)
	}
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
