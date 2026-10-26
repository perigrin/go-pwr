// ABOUTME: End-to-end tests of --poll on lola, run as root, for startup
// ABOUTME: failures and for failures in the middle of a run that it survives.

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pollThroughFailure starts --poll, lets one reading arrive, then breaks
// lola with breakIt. It checks that go-pwr writes errors on stderr and keeps
// running, with no more readings, then repairs lola with fix and checks that
// readings resume in the same process, which then exits 0 on SIGTERM.
func pollThroughFailure(t *testing.T, mac string, breakIt, fix func(*testing.T)) {
	t.Helper()
	restoreOnCleanup(t)
	r := startGoPwr(t, "--mac", mac, "--poll=8s", "--timeout=10s", "--format", "json")
	if !r.waitFor(60*time.Second, func() bool { return r.readings() >= 1 }) {
		t.Fatalf("no reading in 60 s before the failure; stderr %q", r.stderr)
	}

	breakIt(t)
	errorsBefore := r.errorLines()
	if !r.waitFor(90*time.Second, func() bool { return r.errorLines() > errorsBefore }) {
		t.Fatalf("no error line on stderr 90 s after the failure; running: %v", r.running())
	}
	if !r.running() {
		t.Fatalf("go-pwr exited on a failed reading\nstderr:\n%s", r.stderr)
	}
	// Go on failing for a while: it keeps going and prints no reading.
	time.Sleep(12 * time.Second)
	if !r.running() {
		t.Fatalf("go-pwr exited while readings were failing\nstderr:\n%s", r.stderr)
	}
	during := r.readings()

	fix(t)
	if !r.waitFor(120*time.Second, func() bool { return r.readings() > during+1 }) {
		t.Fatalf("no reading in 120 s after the repair (%d before it, %d now); running: %v; stderr:\n%s", during, r.readings(), r.running(), r.stderr)
	}
	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	wantOnlyJSONObjects(t, got.stdout)
	for _, line := range strings.Split(strings.TrimSuffix(got.stderr, "\n"), "\n") {
		if !strings.HasPrefix(line, "error: ") && !strings.HasPrefix(line, "warning: ") {
			t.Errorf("stderr line %q is not an error or warning line", line)
		}
	}
	requireLinkDown(t, mac)
}

func TestPollContinuesAfterFailedReading(t *testing.T) {
	mac := requireRoot(t)
	pollThroughFailure(t, mac,
		func(t *testing.T) { sh(t, "rfkill block bluetooth") },
		func(t *testing.T) {
			sh(t, "rfkill unblock bluetooth")
			if !waitPowered(t) {
				t.Fatal("adapter not powered 30 s after unblocking it")
			}
		})
}

func TestPollContinuesAfterBlueZRestart(t *testing.T) {
	mac := requireRoot(t)
	pollThroughFailure(t, mac,
		stopBlueZ,
		func(t *testing.T) {
			sh(t, "systemctl unmask --runtime bluetooth.service; systemctl start bluetooth")
			if !waitPowered(t) {
				t.Fatal("adapter not powered 30 s after restarting BlueZ")
			}
		})
}

func TestPollContinuesAfterAdapterUnbound(t *testing.T) {
	mac := requireRoot(t)
	pollThroughFailure(t, mac,
		unbindAdapter,
		func(t *testing.T) {
			sh(t, "test -e /sys/bus/serial/drivers/hci_uart_bcm/serial0-0 || echo serial0-0 | tee /sys/bus/serial/drivers/hci_uart_bcm/bind")
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat("/sys/class/bluetooth/hci0"); err == nil {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if !waitPowered(t) {
				t.Fatal("adapter not powered 30 s after rebinding it")
			}
		})
}

// wantPollStartupError starts --poll and checks the startup-failure
// contract: it exits 1 on its own, within limit, with nothing on stdout and
// one stderr line that names the cause.
func wantPollStartupError(t *testing.T, mac, cause string, limit time.Duration) {
	t.Helper()
	r := startGoPwr(t, "--mac", mac, "--poll=10s", "--format", "json")
	got := r.finish(60 * time.Second)
	wantStartupError(t, got, cause)
	if took := time.Since(r.began); took > limit {
		t.Errorf("took %v to exit, want within %v", took, limit)
	}
}

func TestPollStartupNoAdapterExit1(t *testing.T) {
	mac := requireRoot(t)
	restoreOnCleanup(t)
	unbindAdapter(t)
	wantPollStartupError(t, mac, "no bluetooth adapter", 5*time.Second)
}

func TestPollStartupBlueZNotRunningExit1(t *testing.T) {
	mac := requireRoot(t)
	restoreOnCleanup(t)
	stopBlueZ(t)
	wantPollStartupError(t, mac, "BlueZ not running", 25*time.Second)
}

func TestPollStartupBlockedExit1(t *testing.T) {
	mac := requireRoot(t)
	restoreOnCleanup(t)
	sh(t, "rfkill block bluetooth")
	time.Sleep(time.Second)
	wantPollStartupError(t, mac, "powered off or blocked", 5*time.Second)
}

func TestPollStartupPoweredOffExit1(t *testing.T) {
	mac := requireRoot(t)
	restoreOnCleanup(t)
	sh(t, "bluetoothctl power off")
	wantPollStartupError(t, mac, "powered off or blocked", 5*time.Second)
}

func TestPollStartupAccessDeniedExit1(t *testing.T) {
	mac := requireRoot(t)
	started := time.Now()
	r := runAsNobody(t, "--mac", mac, "--poll=10s", "--format", "json")
	wantStartupError(t, r, "access to BlueZ denied")
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("took %v, want a prompt exit instead of retrying", took)
	}
}

// setWallClock steps lola's wall clock to the given Unix second with date -s.
func setWallClock(t *testing.T, unixSeconds int64) {
	t.Helper()
	out, err := exec.Command("date", "-s", "@"+strconv.FormatInt(unixSeconds, 10)).CombinedOutput()
	if err != nil {
		t.Fatalf("date -s: %v: %s", err, out)
	}
}

// TestPollIntervalSurvivesClockStep steps the wall clock an hour forward and
// back in the middle of a run, with systemd-timesyncd stopped, and always
// restarts timesyncd with NTP on, so the clock is left as it was.
func TestPollIntervalSurvivesClockStep(t *testing.T) {
	mac := requireRoot(t)
	t.Cleanup(func() {
		sh(t, "systemctl start systemd-timesyncd; timedatectl set-ntp true")
	})
	sh(t, "systemctl stop systemd-timesyncd")

	r := startGoPwr(t, "--mac", mac, "--poll=10s", "--format", "json")
	time.Sleep(15 * time.Second)
	stepped := false
	// Runs before the timesyncd cleanup above: if the step back below never
	// ran, an offline lola must not stay an hour ahead.
	t.Cleanup(func() {
		if stepped {
			setWallClock(t, time.Now().Unix()-3600)
		}
	})
	setWallClock(t, time.Now().Unix()+3600)
	stepped = true
	time.Sleep(25 * time.Second)
	setWallClock(t, time.Now().Unix()-3600)
	stepped = false
	time.Sleep(25 * time.Second)

	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	readings := parseReadings(t, got.stdout)
	// The step did happen: some reading's wall-clock time jumped.
	jumped := false
	for i := 1; i < len(readings); i++ {
		if d := readings[i].at.Sub(readings[i-1].at); d > 50*time.Minute || d < -50*time.Minute {
			jumped = true
		}
	}
	if !jumped {
		t.Errorf("no reading's time stepped, so the run did not cross the clock step")
	}
	// uptime_s is continuous across it and the interval still counts in it.
	wantStartToStart(t, readings, 10*time.Second, failedReadings(got.stderr) == 0)
	for i, gap := range uptimeGaps(readings) {
		if gap > 40 {
			t.Errorf("uptime_s jumped %.0f s at reading %d", gap, i+1)
		}
	}
}
