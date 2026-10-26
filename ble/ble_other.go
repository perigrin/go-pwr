// ABOUTME: Bluetooth layer for operating systems other than Linux, where
// ABOUTME: BlueZ is unavailable and every operation is unsupported.

//go:build !linux

package ble

import "context"

// Adapter is the local Bluetooth adapter. On this platform it can't be
// opened.
type Adapter struct{}

// Open returns ErrUnsupportedPlatform.
func Open() (*Adapter, error) {
	return nil, ErrUnsupportedPlatform
}

// Scan returns ErrUnsupportedPlatform.
func (a *Adapter) Scan(ctx context.Context, mac string) ([]Controller, error) {
	return nil, ErrUnsupportedPlatform
}
