// ABOUTME: Tests the Bluetooth layer on operating systems other than Linux,
// ABOUTME: where every operation reports that the platform is unsupported.

//go:build !linux

package ble

import (
	"context"
	"errors"
	"testing"
)

func TestOpenUnsupportedPlatform(t *testing.T) {
	_, err := Open()
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Open() error = %v, want ErrUnsupportedPlatform", err)
	}
}

func TestScanUnsupportedPlatform(t *testing.T) {
	_, err := (&Adapter{}).Scan(context.Background(), "")
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Scan() error = %v, want ErrUnsupportedPlatform", err)
	}
}
