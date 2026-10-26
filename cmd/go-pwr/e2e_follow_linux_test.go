// ABOUTME: End-to-end tests of --follow on lola: the cross-built go-pwr holds
// ABOUTME: one connection to the real controller and its output is read live.

package main

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// linkUp reports whether bluetoothctl says the link to the controller is up.
func linkUp(mac string) bool {
	down, out := linkDown(mac)
	return !down && strings.Contains(out, "Connected: yes")
}

func TestFollowHoldsOneConnection(t *testing.T) {
	mac := requireHardware(t)
	r := startGoPwr(t, "--mac", mac, "--follow=5s", "--format", "json")
	if !r.waitFor(45*time.Second, func() bool { return r.readings() >= 1 }) {
		t.Fatalf("no reading in 45 s; stderr %q", r.stderr)
	}
	// Between readings the link stays up: --poll closes it after each one.
	time.Sleep(2 * time.Second)
	if !linkUp(mac) {
		down, out := linkDown(mac)
		t.Errorf("link is not up between readings (down=%v):\n%s", down, out)
	}
	time.Sleep(30 * time.Second)
	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	wantOnlyJSONObjects(t, got.stdout)
	readings := parseReadings(t, got.stdout)
	if len(readings) < 6 {
		t.Fatalf("%d readings in about 35 s, want at least 6", len(readings))
	}
	// The first reading scans and connects, which takes longer than the
	// interval, so the second starts as soon as it is done, start to start,
	// and its gap says nothing. A reading on the held connection takes well
	// under a second, so every gap after that is the interval and not the
	// interval plus a reconnect.
	gaps := uptimeGaps(readings)
	t.Logf("gaps %.1f", gaps)
	for i, gap := range gaps[1:] {
		if gap < 4 || gap > 6 {
			t.Errorf("reading %d came %.1f s after the one before, want 5 s", i+2, gap)
		}
	}
	if n := failedReadings(got.stderr); n != 0 {
		t.Errorf("%d failed readings, stderr:\n%s", n, got.stderr)
	}
	requireLinkDown(t, mac)
}
