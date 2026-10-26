// ABOUTME: Tests boot information against the running Linux kernel; built
// ABOUTME: on the Mac with go test -c and run on lola.

//go:build linux

package boot

import (
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func readProcUptime(t *testing.T) float64 {
	t.Helper()
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		t.Fatalf("read /proc/uptime: %v", err)
	}
	v, err := parseUptime(string(b))
	if err != nil {
		t.Fatalf("parse /proc/uptime: %v", err)
	}
	return v
}

func TestBootIDMatchesKernel(t *testing.T) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatalf("read boot_id: %v", err)
	}
	id, err := ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if want := strings.TrimSpace(string(b)); id != want {
		t.Errorf("ID() = %q, want %q", id, want)
	}
}

func TestUptimeMatchesProcUptime(t *testing.T) {
	before := readProcUptime(t)
	got, err := Uptime()
	if err != nil {
		t.Fatalf("Uptime: %v", err)
	}
	after := readProcUptime(t)
	// /proc/uptime has 10 ms resolution, so allow 1 s either side.
	if got < before-1 || got > after+1 {
		t.Errorf("Uptime() = %.3f, want within 1 s of /proc/uptime %.2f..%.2f", got, before, after)
	}
	if math.IsNaN(got) {
		t.Errorf("Uptime() is NaN")
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

// TestUptimeIgnoresWallClockStep needs root. It stops systemd-timesyncd,
// steps the wall clock an hour forward and back, and always restarts
// timesyncd with NTP on, so the clock is left as it was.
func TestUptimeIgnoresWallClockStep(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: run with sudo -n")
	}
	t.Cleanup(func() {
		for _, args := range [][]string{
			{"systemctl", "start", "systemd-timesyncd"},
			{"timedatectl", "set-ntp", "true"},
		} {
			if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
				t.Errorf("restore %v: %v: %s", args, err, out)
			}
		}
	})
	if out, err := exec.Command("systemctl", "stop", "systemd-timesyncd").CombinedOutput(); err != nil {
		t.Fatalf("stop systemd-timesyncd: %v: %s", err, out)
	}
	idBefore, err := ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	upBefore, err := Uptime()
	if err != nil {
		t.Fatalf("Uptime: %v", err)
	}
	start := time.Now()

	setWallClock(t, time.Now().Unix()+3600)
	steppedBack := false
	// Registered after the timesyncd cleanup, so it runs first: if the
	// step back below never ran, an offline lola must not stay an hour
	// ahead.
	t.Cleanup(func() {
		if !steppedBack {
			setWallClock(t, time.Now().Unix()-3600)
		}
	})
	upForward, err := Uptime()
	if err != nil {
		t.Fatalf("Uptime: %v", err)
	}
	setWallClock(t, time.Now().Unix()-3600)
	steppedBack = true
	upBack, err := Uptime()
	if err != nil {
		t.Fatalf("Uptime: %v", err)
	}
	elapsed := time.Since(start).Seconds() // monotonic, unaffected by the steps

	if upForward < upBefore || upForward-upBefore > elapsed+1 {
		t.Errorf("uptime jumped with the forward step: %.3f -> %.3f over %.3f s", upBefore, upForward, elapsed)
	}
	if upBack < upForward || upBack-upBefore > elapsed+1 {
		t.Errorf("uptime jumped with the backward step: %.3f -> %.3f over %.3f s", upForward, upBack, elapsed)
	}
	idAfter, err := ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if idAfter != idBefore {
		t.Errorf("boot id changed across a wall-clock step: %q -> %q", idBefore, idAfter)
	}
}

func TestBootIDNoTrailingNewline(t *testing.T) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatalf("read boot_id: %v", err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("kernel boot_id %q no longer ends in a newline; this test needs rethinking", b)
	}
	id, err := ID()
	if err != nil {
		t.Fatalf("ID: %v", err)
	}
	if id != strings.TrimSpace(id) || strings.ContainsAny(id, "\n\r\t ") {
		t.Errorf("ID() = %q has whitespace", id)
	}
}

func TestUptimeNeverDecreases(t *testing.T) {
	prev, err := Uptime()
	if err != nil {
		t.Fatalf("Uptime: %v", err)
	}
	first := prev
	for i := 0; i < 1000; i++ {
		v, err := Uptime()
		if err != nil {
			t.Fatalf("Uptime: %v", err)
		}
		if v < prev {
			t.Fatalf("Uptime decreased: %.9f -> %.9f", prev, v)
		}
		prev = v
	}
	time.Sleep(10 * time.Millisecond)
	last, err := Uptime()
	if err != nil {
		t.Fatalf("Uptime: %v", err)
	}
	if d := last - first; d <= 0 || d >= 1 {
		t.Errorf("uptime advanced %.9f s over a short loop and 10 ms sleep, want a sub-second step", d)
	}
	if last == math.Trunc(last) && first == math.Trunc(first) {
		t.Errorf("uptime %.9f and %.9f are whole seconds; want sub-second resolution", first, last)
	}
}
