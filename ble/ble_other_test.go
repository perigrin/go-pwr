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

func TestConnectUnsupportedPlatform(t *testing.T) {
	conn, err := (&Adapter{}).Connect(context.Background(), "C3:00:00:58:51:40")
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Connect() error = %v, want ErrUnsupportedPlatform", err)
	}
	if err := conn.Close(); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Close() error = %v, want ErrUnsupportedPlatform", err)
	}
}

func TestFollowUnsupportedPlatform(t *testing.T) {
	f := (&Adapter{}).Follow("C3:00:00:58:51:40")
	if _, err := f.Read(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Read() error = %v, want ErrUnsupportedPlatform", err)
	}
	if err := f.Close(); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Close() error = %v, want ErrUnsupportedPlatform", err)
	}
}
