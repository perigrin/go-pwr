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

func TestClassifiedErrors(t *testing.T) {
	cause := errors.New("underlying")
	err := fmt.Errorf("open: %w", classify(ErrFatalAtStartup, ErrNoAdapter, "no adapter", cause))
	for _, want := range []error{ErrNoAdapter, ErrFatalAtStartup, cause} {
		if !errors.Is(err, want) {
			t.Errorf("errors.Is(%v, %v) = false, want true", err, want)
		}
	}
	for _, other := range []error{ErrAccessDenied, ErrBlueZNotRunning, ErrAdapterUnavailable, ErrRetryable} {
		if errors.Is(err, other) {
			t.Errorf("errors.Is(%v, %v) = true, want false", err, other)
		}
	}
	if got, want := err.Error(), "open: no adapter: underlying"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
