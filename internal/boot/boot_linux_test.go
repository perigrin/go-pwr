// ABOUTME: Tests boot information against the running Linux kernel; built
// ABOUTME: on the Mac with go test -c and run on lola.

//go:build linux

package boot

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func readProcUptime(t *testing.T) float64 {
	t.Helper()
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		t.Fatalf("read /proc/uptime: %v", err)
	}
	v, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	if err != nil {
		t.Fatalf("parse /proc/uptime %q: %v", b, err)
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
