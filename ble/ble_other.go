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

// OpenContext returns ErrUnsupportedPlatform.
func OpenContext(ctx context.Context) (*Adapter, error) {
	return nil, ErrUnsupportedPlatform
}

// Scan returns ErrUnsupportedPlatform.
func (a *Adapter) Scan(ctx context.Context, mac string) ([]Controller, error) {
	return nil, ErrUnsupportedPlatform
}

// Conn is a link to the controller. On this platform none can be made.
type Conn struct{}

// Connect returns ErrUnsupportedPlatform.
func (a *Adapter) Connect(ctx context.Context, mac string) (*Conn, error) {
	return nil, ErrUnsupportedPlatform
}

// Close returns ErrUnsupportedPlatform.
func (c *Conn) Close() error {
	return ErrUnsupportedPlatform
}

// RequestStatus returns ErrUnsupportedPlatform.
func (c *Conn) RequestStatus(ctx context.Context) (string, error) {
	return "", ErrUnsupportedPlatform
}
