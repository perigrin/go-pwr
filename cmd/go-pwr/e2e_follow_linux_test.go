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

func TestFollowReconnectsAfterDrop(t *testing.T) {
	mac := requireHardware(t)
	t.Cleanup(func() { sh(t, "bluetoothctl disconnect "+mac) })
	r := startGoPwr(t, "--mac", mac, "--follow=5s", "--format", "json")
	if !r.waitFor(60*time.Second, func() bool { return r.readings() >= 4 }) {
		t.Fatalf("fewer than 4 readings in 60 s; stderr %q", r.stderr)
	}

	// Cut the link from this end, as the controller going out of range does
	// from the other.
	if out := sh(t, "bluetoothctl disconnect "+mac); !strings.Contains(out, "Disconnection successful") {
		t.Fatalf("bluetoothctl disconnect %s: %s", mac, out)
	}
	before := r.readings()
	if !r.waitFor(15*time.Second, func() bool { return r.errorLines() >= 1 }) {
		t.Fatalf("no error line on stderr 15 s after the drop; running: %v", r.running())
	}
	if !r.running() {
		t.Fatalf("go-pwr exited on a dropped link\nstderr:\n%s", r.stderr)
	}
	if !strings.Contains(r.stderr.String(), "dropped") {
		t.Errorf("stderr %q does not say the link dropped", r.stderr)
	}
	// The link is made again at a following interval, and readings resume.
	if !r.waitFor(60*time.Second, func() bool { return r.readings() >= before+3 }) {
		t.Fatalf("readings did not resume in 60 s after the drop (%d before it, %d now); stderr:\n%s", before, r.readings(), r.stderr)
	}
	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	wantOnlyJSONObjects(t, got.stdout)
	if n := failedReadings(got.stderr); n != 1 {
		t.Errorf("%d failed readings, want exactly the one the drop caused; stderr:\n%s", n, got.stderr)
	}
	requireLinkDown(t, mac)
}
