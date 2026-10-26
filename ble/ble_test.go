// ABOUTME: Tests the platform-neutral parts of the Bluetooth layer, which
// ABOUTME: run on every operating system.

package ble

import (
	"errors"
	"fmt"
	"strings"
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
// to hear the test controller in 20 scans ("Scan window"), the slowest scan
// that stopped at an address and the slowest connect of 40 runs ("Time budget
// on lola").
const (
	defaultTimeout  = 20 * time.Second
	slowestSighting = 7100 * time.Millisecond
	slowestMacScan  = 4440 * time.Millisecond
	slowestConnect  = 9790 * time.Millisecond
	// slowestDiscovery is the slowest service discovery after a connect.
	slowestDiscovery = 970 * time.Millisecond
)

// TestScanWindowWithinTimeout checks the path a machine uses, with --mac: the
// scan stops at the address, so the slowest such scan, the slowest connect and
// the reply limit must fit in the default timeout. A scan without --mac runs
// the whole window, and window + slowest connect + discovery + reply is about
// 21.8 s, which does not fit; a one-shot without --mac can fail as a retryable
// reading, and a person can pass a longer --timeout. perigrin accepted this
// (owner decision 13; protocol.md, "Time budget on lola").
func TestScanWindowWithinTimeout(t *testing.T) {
	if scanWindow <= 0 {
		t.Fatalf("scanWindow = %v, want it positive", scanWindow)
	}
	if scanWindow < slowestSighting {
		t.Errorf("scanWindow = %v, shorter than the slowest sighting measured on lola (%v)", scanWindow, slowestSighting)
	}
	if slowestMacScan > scanWindow {
		t.Errorf("slowest scan with an address %v is longer than the window %v", slowestMacScan, scanWindow)
	}
	total := slowestMacScan + slowestConnect + slowestDiscovery + status.TotalTimeout
	if total > defaultTimeout {
		t.Errorf("scan %v + connect %v + discovery %v + reply %v = %v, want at most the default timeout %v",
			slowestMacScan, slowestConnect, slowestDiscovery, status.TotalTimeout, total, defaultTimeout)
	}
}

// slowestClose is the slowest Close of 40 runs on lola ("Time budget on
// lola" in docs/reference/protocol.md).
const slowestClose = 4610 * time.Millisecond

// TestCloseBoundHasMargin holds the wait for a link to drop to twice the
// slowest disconnect measured, since one night on one controller is no bound.
func TestCloseBoundHasMargin(t *testing.T) {
	if closeBound < 2*slowestClose {
		t.Errorf("closeBound = %v, want at least twice the slowest close measured (%v)", closeBound, slowestClose)
	}
}

func TestErrorTextHasNoTrailingNewline(t *testing.T) {
	// D-Bus error messages end in a newline.
	dbusLike := errors.New("Method \"Get\" with signature \"ss\" doesn't exist\n")
	for name, err := range map[string]error{
		"classified":   classify(ErrRetryable, ErrNoAdapter, "read adapter", dbusLike),
		"unclassified": unclassified("read adapter", dbusLike),
	} {
		if got := err.Error(); got != strings.TrimRight(got, "\r\n") || !strings.Contains(got, "doesn't exist") {
			t.Errorf("%s: Error() = %q, want the message without a trailing newline", name, got)
		}
		if !errors.Is(err, dbusLike) {
			t.Errorf("%s: the cause is no longer reachable with errors.Is", name)
		}
	}
}
