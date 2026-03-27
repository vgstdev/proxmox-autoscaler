// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package proxmox

// LXCEntry represents a single container returned by GET /nodes/{node}/lxc.
type LXCEntry struct {
	VMID   int     `json:"vmid"`
	Name   string  `json:"name"`
	Status string  `json:"status"`
	Type   string  `json:"type"`
	Tags   string  `json:"tags"`
	CPU    float64 `json:"cpu"`
	Mem    float64 `json:"mem"`
	MaxMem float64 `json:"maxmem"`
}

// ContainerStatus represents the current runtime status from
// GET /nodes/{node}/lxc/{vmid}/status/current.
type ContainerStatus struct {
	VMID    int     `json:"vmid"`
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	CPU     float64 `json:"cpu"`  // fraction of total host CPU (0..1 per host CPU)
	CPUs    float64 `json:"cpus"` // number of CPUs visible to container
	Mem     float64 `json:"mem"`
	MaxMem  float64 `json:"maxmem"`
}

// ContainerConfig represents the configuration from
// GET /nodes/{node}/lxc/{vmid}/config.
type ContainerConfig struct {
	Cores    int     `json:"cores"`
	CPULimit float64 `json:"cpulimit"`
	Memory   int     `json:"memory"` // MB
}

// NodeStatus represents the node-level stats from
// GET /nodes/{node}/status.
type NodeStatus struct {
	MaxCPU int     `json:"maxcpu"`
	MaxMem float64 `json:"maxmem"`
	CPU    float64 `json:"cpu"`
	Mem    float64 `json:"mem"`
}

// ConfigUpdateRequest holds the fields to update via PUT /nodes/{node}/lxc/{vmid}/config.
// Only non-zero fields will be sent.
type ConfigUpdateRequest struct {
	Cores    *int
	CPULimit *float64
	Memory   *int // MB
}

// apiResponse is the generic envelope returned by the Proxmox API.
type apiResponse[T any] struct {
	Data T `json:"data"`
}
