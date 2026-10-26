// ABOUTME: Tests the Bluetooth layer on operating systems other than Linux,
// ABOUTME: where every operation reports that the platform is unsupported.

//go:build !linux

package ble

import (
	"errors"
	"testing"
)

func TestOpenUnsupportedPlatform(t *testing.T) {
	_, err := Open()
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Open() error = %v, want ErrUnsupportedPlatform", err)
	}
}
