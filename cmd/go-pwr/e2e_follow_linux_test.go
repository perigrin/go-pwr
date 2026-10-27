// ABOUTME: End-to-end tests of --follow on lola: the cross-built go-pwr holds
// ABOUTME: one connection to the real controller and its output is read live.

package main

import (
	"os"
	"strconv"
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
			t.Errorf("reading %d came %.1f s after the one before, want 5 s", i+3, gap)
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
	// The link is made again at the next interval, and readings resume. That
	// reading connects and reads within --timeout (20 s by default), so the
	// first one after the error comes within an interval plus that.
	failedAt := time.Now()
	if !r.waitFor(30*time.Second, func() bool { return r.readings() > before }) {
		t.Fatalf("no reading within 30 s of the error (%d before the drop, %d now); stderr:\n%s", before, r.readings(), r.stderr)
	}
	if took := time.Since(failedAt); took > 25*time.Second {
		t.Errorf("the first reading after the error came %v later, want within one 5 s interval and the 20 s timeout", took)
	}
	if !r.waitFor(30*time.Second, func() bool { return r.readings() >= before+3 }) {
		t.Fatalf("readings did not go on after the drop (%d before it, %d now); stderr:\n%s", before, r.readings(), r.stderr)
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

func TestFollowSignalsExit0(t *testing.T) {
	mac := requireHardware(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			r := startGoPwr(t, "--mac", mac, "--follow=10s", "--format", "json")
			if !r.waitFor(45*time.Second, func() bool { return r.readings() >= 1 }) {
				t.Fatalf("no reading in 45 s; stderr %q", r.stderr)
			}
			time.Sleep(2 * time.Second)
			if !linkUp(mac) {
				t.Fatal("link is not up between readings, so there is nothing to disconnect")
			}
			signalled := time.Now()
			got := r.stop(sig)
			if got.status != 0 {
				t.Errorf("status %d, want 0", got.status)
			}
			if took := time.Since(signalled); took > 10*time.Second {
				t.Errorf("took %v to exit between readings, want a prompt exit", took)
			}
			wantOnlyJSONObjects(t, got.stdout)
			if got.stderr != "" {
				t.Errorf("stderr %q, want nothing: a signal is not an error", got.stderr)
			}
			requireLinkDown(t, mac)
		})
	}
}

func TestFollowSignalDuringFirstReading(t *testing.T) {
	mac := requireHardware(t)
	// The first reading scans and connects for 3 s or more, so these land in
	// the scan and in the connect. A reading that ignored the signal would
	// finish and print before go-pwr exited.
	for _, delay := range []time.Duration{500 * time.Millisecond, 2 * time.Second} {
		t.Run(delay.String(), func(t *testing.T) {
			r, took := interrupt(t, syscall.SIGTERM, delay, "--mac", mac, "--follow=10s", "--format", "json")
			if r.status != 0 {
				t.Errorf("status %d, want 0", r.status)
			}
			if r.stdout != "" {
				t.Errorf("stdout %q, want nothing: the reading should have been cut off by the signal", r.stdout)
			}
			if r.stderr != "" {
				t.Errorf("stderr %q, want nothing: a signal is not an error", r.stderr)
			}
			if took > delay+4*time.Second {
				t.Errorf("took %v to exit after a signal at %v, want the reading cut off", took, delay)
			}
			requireLinkDown(t, mac)
		})
	}
}

func TestFollowSignalDuringHeldReading(t *testing.T) {
	mac := requireHardware(t)
	// With a 1 s interval the next reading starts 0.5 to 0.8 s after a reply
	// arrives and takes 0.2 to 0.5 s on the held connection, so signals at
	// these offsets land in or just before the next reading. Which of the two
	// can't be seen from outside, so every offset must exit promptly, 0, with
	// the link down and nothing on stderr.
	for _, offset := range []time.Duration{700 * time.Millisecond, 850 * time.Millisecond, time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			r := startGoPwr(t, "--mac", mac, "--follow=1s", "--format", "json")
			if !r.waitFor(45*time.Second, func() bool { return r.readings() >= 3 }) {
				t.Fatalf("fewer than 3 readings in 45 s; stderr %q", r.stderr)
			}
			time.Sleep(offset)
			// A reading is stamped with the uptime when its reply arrives. One
			// stamped after the signal was sent finished although it was
			// signalled, so the Read was not cut off.
			signalUptime := uptimeNow(t)
			signalled := time.Now()
			got := r.stop(syscall.SIGTERM)
			if got.status != 0 {
				t.Errorf("status %d, want 0", got.status)
			}
			if took := time.Since(signalled); took > 10*time.Second {
				t.Errorf("took %v to exit, want a prompt exit", took)
			}
			wantOnlyJSONObjects(t, got.stdout)
			for i, rd := range parseReadings(t, got.stdout) {
				if rd.uptime > signalUptime+0.05 {
					t.Errorf("reading %d was received %.2f s after the signal was sent: the Read was not cut off", i+1, rd.uptime-signalUptime)
				}
			}
			if got.stderr != "" {
				t.Errorf("stderr %q, want nothing: a signal is not an error", got.stderr)
			}
			requireLinkDown(t, mac)
		})
	}
}

// uptimeNow is the machine's uptime in seconds, the clock of a reading's
// uptime_s.
func uptimeNow(t *testing.T) float64 {
	t.Helper()
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		t.Fatal(err)
	}
	up, err := strconv.ParseFloat(strings.Fields(string(raw))[0], 64)
	if err != nil {
		t.Fatal(err)
	}
	return up
}

func TestFollowLongReadingNoMakeUp(t *testing.T) {
	mac := requireHardware(t)
	// The first reading scans and connects, which takes more than the 1 s
	// interval: the second starts when it is done, and the intervals it
	// missed are not made up. A reading on the held connection after that
	// takes well under 1 s.
	r := startGoPwr(t, "--mac", mac, "--follow=1s", "--format", "json")
	if !r.waitFor(45*time.Second, func() bool { return r.readings() >= 1 }) {
		t.Fatalf("no reading in 45 s; stderr %q", r.stderr)
	}
	time.Sleep(30 * time.Second)
	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	wantOnlyJSONObjects(t, got.stdout)
	readings := parseReadings(t, got.stdout)
	if len(readings) < 10 {
		t.Fatalf("%d readings in 30 s, want at least 10", len(readings))
	}
	gaps := uptimeGaps(readings)
	t.Logf("gaps %.1f", gaps)
	// A schedule that made up the intervals the first reading overran would
	// follow it with a burst of readings, each as soon as the one before. From
	// the first reply to the last, 1 s intervals allow one reading a second,
	// plus two for the jitter of a reply's latency. The span is taken from the
	// readings' own uptime_s, not from when this test saw the first one.
	span := readings[len(readings)-1].uptime - readings[0].uptime
	if max := int(span) + 2; len(readings)-1 > max {
		t.Errorf("%d readings after the first in %.1f s at a 1 s interval, want at most %d: make-up readings", len(readings)-1, span, max)
	}
	// Reply latency moves single gaps by up to a second, a slow reply
	// followed by a quick one, but no three in a row come in under two
	// seconds unless readings are made up.
	for i := 1; i+2 < len(gaps); i++ {
		if sum := gaps[i] + gaps[i+1] + gaps[i+2]; sum < 2.0 {
			t.Errorf("readings %d to %d came %.1f s after reading %d: overlap or make-up readings", i+2, i+4, sum, i+1)
		}
	}
	rest := gaps[1:]
	sum := 0.0
	for _, gap := range rest {
		sum += gap
	}
	if mean := sum / float64(len(rest)); mean < 0.9 || mean > 1.4 {
		t.Errorf("mean gap %.2f s, want the 1 s interval within 0.4 s", mean)
	}
	requireLinkDown(t, mac)
}
