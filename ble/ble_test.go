// ABOUTME: Tests the platform-neutral parts of the Bluetooth layer, which
// ABOUTME: run on every operating system.

package ble

import (
	"errors"
	"fmt"
	"testing"
)

func TestUnsupportedPlatformErrorIdentity(t *testing.T) {
	wrapped := fmt.Errorf("open adapter: %w", ErrUnsupportedPlatform)
	if !errors.Is(wrapped, ErrUnsupportedPlatform) {
		t.Errorf("errors.Is(%v, ErrUnsupportedPlatform) = false, want true", wrapped)
	}
	unrelated := errors.New("unsupported platform")
	if errors.Is(unrelated, ErrUnsupportedPlatform) {
		t.Errorf("errors.Is(%v, ErrUnsupportedPlatform) = true for an unrelated error with the same text", unrelated)
	}
}
