// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a Proxmox REST API client.
type Client struct {
	httpClient  *http.Client
	baseURL     string
	node        string
	authHeader  string
}

// NewClient creates a new Proxmox API client.
func NewClient(host, node, tokenID, tokenSecret string, insecureTLS bool) *Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecureTLS, //nolint:gosec // configurable per deployment
		},
	}
	return &Client{
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		baseURL:    strings.TrimRight(host, "/") + "/api2/json",
		node:       node,
		authHeader: fmt.Sprintf("PVEAPIToken=%s=%s", tokenID, tokenSecret),
	}
}

// ListLXC returns all LXC containers on the node (running only, not excluded by tag).
func (c *Client) ListLXC(ctx context.Context, excludeTag string) ([]LXCEntry, error) {
	endpoint := fmt.Sprintf("/nodes/%s/lxc", c.node)
	var entries []LXCEntry
	if err := c.get(ctx, endpoint, &entries); err != nil {
		return nil, fmt.Errorf("list lxc: %w", err)
	}

	var filtered []LXCEntry
	for _, e := range entries {
		if e.Status != "running" || e.Type != "lxc" {
			continue
		}
		if excludeTag != "" && hasTag(e.Tags, excludeTag) {
			filtered = append(filtered, LXCEntry{VMID: e.VMID, Tags: e.Tags, Name: e.Name, Status: "excluded"})
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

// ListAllLXC returns all LXC containers without any filtering (used for capacity checks).
func (c *Client) ListAllLXC(ctx context.Context) ([]LXCEntry, error) {
	endpoint := fmt.Sprintf("/nodes/%s/lxc", c.node)
	var entries []LXCEntry
	if err := c.get(ctx, endpoint, &entries); err != nil {
		return nil, fmt.Errorf("list all lxc: %w", err)
	}
	return entries, nil
}

// GetContainerStatus fetches the current runtime status of a container.
func (c *Client) GetContainerStatus(ctx context.Context, vmid int) (*ContainerStatus, error) {
	endpoint := fmt.Sprintf("/nodes/%s/lxc/%d/status/current", c.node, vmid)
	var status ContainerStatus
	if err := c.get(ctx, endpoint, &status); err != nil {
		return nil, fmt.Errorf("get container status vmid=%d: %w", vmid, err)
	}
	return &status, nil
}

// GetContainerConfig fetches the configuration of a container.
func (c *Client) GetContainerConfig(ctx context.Context, vmid int) (*ContainerConfig, error) {
	endpoint := fmt.Sprintf("/nodes/%s/lxc/%d/config", c.node, vmid)
	var cfg ContainerConfig
	if err := c.get(ctx, endpoint, &cfg); err != nil {
		return nil, fmt.Errorf("get container config vmid=%d: %w", vmid, err)
	}
	return &cfg, nil
}

// UpdateContainerConfig sends a partial configuration update.
func (c *Client) UpdateContainerConfig(ctx context.Context, vmid int, req ConfigUpdateRequest) error {
	endpoint := fmt.Sprintf("/nodes/%s/lxc/%d/config", c.node, vmid)

	fields := url.Values{}
	if req.Cores != nil {
		fields.Set("cores", fmt.Sprintf("%d", *req.Cores))
	}
	if req.CPULimit != nil {
		fields.Set("cpulimit", fmt.Sprintf("%.2f", *req.CPULimit))
	}
	if req.Memory != nil {
		fields.Set("memory", fmt.Sprintf("%d", *req.Memory))
	}

	if len(fields) == 0 {
		return nil
	}

	body := strings.NewReader(fields.Encode())
	fullURL := c.baseURL + endpoint

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, fullURL, body)
	if err != nil {
		return fmt.Errorf("build request PUT %s: %w", endpoint, err)
	}
	httpReq.Header.Set("Authorization", c.authHeader)
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT %s returned %d: %s", endpoint, resp.StatusCode, string(respBody))
	}
	return nil
}

// GetNodeStatus fetches the current node-level statistics.
func (c *Client) GetNodeStatus(ctx context.Context) (*NodeStatus, error) {
	endpoint := fmt.Sprintf("/nodes/%s/status", c.node)
	var status NodeStatus
	if err := c.get(ctx, endpoint, &status); err != nil {
		return nil, fmt.Errorf("get node status: %w", err)
	}
	return &status, nil
}

// get performs a GET request and JSON-decodes the `data` field of the response.
func (c *Client) get(ctx context.Context, endpoint string, out interface{}) error {
	fullURL := c.baseURL + endpoint

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("build request GET %s: %w", endpoint, err)
	}
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s returned %d: %s", endpoint, resp.StatusCode, string(body))
	}

	wrapped := &apiResponse[json.RawMessage]{}
	if err := json.NewDecoder(resp.Body).Decode(wrapped); err != nil {
		return fmt.Errorf("decode response from %s: %w", endpoint, err)
	}

	if err := json.Unmarshal(wrapped.Data, out); err != nil {
		return fmt.Errorf("unmarshal data from %s: %w", endpoint, err)
	}
	return nil
}

// hasTag checks whether tagList (semicolon-separated) contains target as an exact entry.
func hasTag(tagList, target string) bool {
	if tagList == "" || target == "" {
		return false
	}
	for _, t := range strings.Split(tagList, ";") {
		if strings.TrimSpace(t) == target {
			return true
		}
	}
	return false
}
