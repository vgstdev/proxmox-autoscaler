// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package notifier

// Notifier is the interface implemented by all notification backends.
type Notifier interface {
	SendBoost(p BoostParams)
	SendRevert(p RevertParams)
}

// MultiNotifier fans out notifications to all registered backends.
type MultiNotifier struct {
	backends []Notifier
}

// NewMulti creates a MultiNotifier from the provided backends.
func NewMulti(backends ...Notifier) *MultiNotifier {
	return &MultiNotifier{backends: backends}
}

func (m *MultiNotifier) SendBoost(p BoostParams) {
	for _, b := range m.backends {
		b.SendBoost(p)
	}
}

func (m *MultiNotifier) SendRevert(p RevertParams) {
	for _, b := range m.backends {
		b.SendRevert(p)
	}
}
