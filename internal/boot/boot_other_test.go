// ABOUTME: Tests boot information on operating systems other than Linux,
// ABOUTME: where it is unsupported.

//go:build !linux

package boot

import (
	"errors"
	"testing"
)

func TestBootUnsupportedPlatform(t *testing.T) {
	if _, err := ID(); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Errorf("ID() error = %v, want ErrUnsupportedPlatform", err)
	}
	if _, err := Uptime(); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Errorf("Uptime() error = %v, want ErrUnsupportedPlatform", err)
	}
}
