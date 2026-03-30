// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const slackAPIURL = "https://slack.com/api/chat.postMessage"

// SlackNotifier sends notifications to a Slack channel via the Bot API.
type SlackNotifier struct {
	enabled bool
	token   string
	channel string
	logger  *slog.Logger
}

// NewSlack creates a new SlackNotifier.
func NewSlack(enabled bool, token, channel string, logger *slog.Logger) *SlackNotifier {
	return &SlackNotifier{
		enabled: enabled,
		token:   token,
		channel: channel,
		logger:  logger,
	}
}

// SendBoost sends a boost notification to Slack.
func (s *SlackNotifier) SendBoost(p BoostParams) {
	if !s.enabled {
		return
	}
	host := strings.ToUpper(shortHostname(p.Hostname))
	title := fmt.Sprintf(":rocket: Autoscaling *%s* => %s(%d)", host, strings.ToLower(p.Name), p.VMID)

	var resourceLabel string
	if p.Resource == "cpu" {
		resourceLabel = "CPU (" + p.CPUResource + ")"
	} else {
		resourceLabel = "Memory"
	}

	text := fmt.Sprintf(
		"%s\n*Resource:* %s\n*%s → %s* (+%.0f%%)\n*Usage:* %.1f%%\n*Boost duration:* %s\n*Time:* %s",
		title,
		resourceLabel,
		formatValue(p.Resource, p.CPUResource, p.Original),
		formatValue(p.Resource, p.CPUResource, p.Boosted),
		(p.BoostFactor-1)*100,
		p.UsagePct,
		formatDuration(p.BoostDuration),
		time.Now().Format(time.RFC3339),
	)

	s.post(text)
}

// SendRevert sends a revert notification to Slack.
func (s *SlackNotifier) SendRevert(p RevertParams) {
	if !s.enabled {
		return
	}
	host := strings.ToUpper(shortHostname(p.Hostname))
	title := fmt.Sprintf(":white_check_mark: Autoscaling reverted *%s* => %s(%d)", host, strings.ToLower(p.Name), p.VMID)

	var resourceLabel string
	if p.Resource == "cpu" {
		resourceLabel = "CPU (" + p.CPUResource + ")"
	} else {
		resourceLabel = "Memory"
	}

	text := fmt.Sprintf(
		"%s\n*Resource:* %s\n*%s → %s*\n*Usage at revert:* %.0f%%\n*Time:* %s",
		title,
		resourceLabel,
		formatValue(p.Resource, p.CPUResource, p.Boosted),
		formatValue(p.Resource, p.CPUResource, p.Original),
		p.UsagePct,
		time.Now().Format(time.RFC3339),
	)

	s.post(text)
}

func (s *SlackNotifier) post(text string) {
	payload := map[string]string{
		"channel": s.channel,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		s.logger.Error("slack: failed to marshal payload", "error", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, slackAPIURL, bytes.NewReader(body))
	if err != nil {
		s.logger.Error("slack: failed to build request", "error", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("slack: request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		s.logger.Error("slack: failed to decode response", "error", err)
		return
	}
	if !result.OK {
		s.logger.Error("slack: API error", "error", result.Error)
	}
}
