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
// Note: Proxmox nests CPU count inside cpuinfo and memory inside memory.
type NodeStatus struct {
	CPU     float64 `json:"cpu"`
	CPUInfo struct {
		CPUs int `json:"cpus"`
	} `json:"cpuinfo"`
	Memory struct {
		Total float64 `json:"total"` // bytes
		Used  float64 `json:"used"`  // bytes
	} `json:"memory"`
}

// MaxCPU returns the total number of logical CPUs on the node.
func (n *NodeStatus) MaxCPU() int { return n.CPUInfo.CPUs }

// MaxMemBytes returns the total memory of the node in bytes.
func (n *NodeStatus) MaxMemBytes() float64 { return n.Memory.Total }

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
