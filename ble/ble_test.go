// ABOUTME: Tests the platform-neutral parts of the Bluetooth layer, which
// ABOUTME: run on every operating system.

package ble

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/status"
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

// The numbers the scan window is checked against. defaultTimeout is the
// default --timeout (docs/decisions/0001-go-pwr-design.md). The others are
// from the lola measurements in docs/reference/protocol.md: the slowest scan
// to hear the test controller in 20 scans ("Scan window"), and the slowest
// first-look connect to resolved services ("Notification sizes and timing on
// lola"). The connect issue re-checks the connect time with go-pwr's own.
const (
	defaultTimeout     = 20 * time.Second
	slowestSighting    = 7100 * time.Millisecond
	slowestConnect     = 5900 * time.Millisecond
	spareInsideTimeout = 2 * time.Second
)

func TestScanWindowWithinTimeout(t *testing.T) {
	if scanWindow <= 0 {
		t.Fatalf("scanWindow = %v, want it positive", scanWindow)
	}
	if scanWindow < slowestSighting {
		t.Errorf("scanWindow = %v, shorter than the slowest sighting measured on lola (%v)", scanWindow, slowestSighting)
	}
	total := scanWindow + slowestConnect + status.TotalTimeout
	if total > defaultTimeout-spareInsideTimeout {
		t.Errorf("window %v + connect %v + reply %v = %v, want at most %v (the default timeout %v less %v spare)",
			scanWindow, slowestConnect, status.TotalTimeout, total, defaultTimeout-spareInsideTimeout, defaultTimeout, spareInsideTimeout)
	}
}
