// ABOUTME: End-to-end tests of one-shot mode on lola: they run the cross-built
// ABOUTME: go-pwr beside this test binary against the real controller.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireHardware skips the test unless GO_PWR_TEST_MAC names the controller,
// and returns that address.
func requireHardware(t *testing.T) string {
	t.Helper()
	mac := os.Getenv("GO_PWR_TEST_MAC")
	if mac == "" {
		t.Skip("GO_PWR_TEST_MAC not set")
	}
	return mac
}

// crossBinary returns the go-pwr binary copied to lola beside this test
// binary. The tests run it, not a build of their own, because lola has no Go
// toolchain.
func crossBinary(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(self), "go-pwr")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("go-pwr is not beside the test binary: %v (copy both to lola:go-pwr-test/)", err)
	}
	return path
}

// sh runs a shell script and returns its combined output.
func sh(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Logf("sh -c %q: %v: %s", script, err, out)
	}
	return string(out)
}

// goPwr starts the cross-built go-pwr with args.
func goPwr(t *testing.T, args ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(crossBinary(t), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	return cmd, &stdout, &stderr
}

// runGoPwr runs the cross-built go-pwr to the end, and says how long it took.
func runGoPwr(t *testing.T, args ...string) (r result, took time.Duration) {
	t.Helper()
	cmd, stdout, stderr := goPwr(t, args...)
	start := time.Now()
	err := cmd.Run()
	took = time.Since(start)
	status := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		status = exit.ExitCode()
	case err != nil:
		t.Fatalf("running go-pwr %q: %v", args, err)
	}
	t.Logf("go-pwr %s: status %d after %v\nstdout:\n%sstderr:\n%s", joinArgs(args), status, took, stdout, stderr)
	return result{status, stdout.String(), stderr.String()}, took
}

// requireLinkDown fails the test unless BlueZ reports the controller not
// connected.
func requireLinkDown(t *testing.T, mac string) {
	t.Helper()
	out := sh(t, "bluetoothctl info "+mac)
	if strings.Contains(out, "Connected: yes") {
		t.Errorf("link to %s is still up after go-pwr exited:\n%s", mac, out)
	}
}

// jsonKeys are the keys of a JSON reading, and the ones that are never null.
var (
	jsonKeys = []string{
		"time", "boot_id", "uptime_s", "device", "battery_v", "solar_v", "charge_a",
		"charge_state", "charge_state_code", "battery_type", "battery_type_code",
		"temp_c", "temp_f", "ah_today", "ah_yesterday", "ah_2d", "ah_3d", "ah_7d", "raw",
	}
	neverNullKeys = []string{
		"time", "boot_id", "uptime_s", "device", "battery_v", "solar_v", "charge_a", "raw",
	}
)

func TestOneShotText(t *testing.T) {
	mac := requireHardware(t)
	r, _ := runGoPwr(t, "--mac", mac)
	if r.status != 0 {
		t.Fatalf("status %d, want 0", r.status)
	}
	for _, label := range []string{"time", "device", "battery", "solar", "charge", "temperature", "amp-hours"} {
		if !strings.Contains("\n"+r.stdout, "\n"+label+" ") {
			t.Errorf("stdout has no %q line:\n%s", label, r.stdout)
		}
	}
	if !strings.Contains(r.stdout, mac) {
		t.Errorf("stdout does not show the device %s:\n%s", mac, r.stdout)
	}
	if !strings.HasSuffix(r.stdout, "\n") || strings.Count(r.stdout, "\n") != 7 {
		t.Errorf("stdout is not one 7-line block ending in a newline: %q", r.stdout)
	}
}

func TestOneShotJSON(t *testing.T) {
	mac := requireHardware(t)
	r, _ := runGoPwr(t, "--mac", mac, "--format", "json")
	if r.status != 0 {
		t.Fatalf("status %d, want 0", r.status)
	}
	if strings.Count(r.stdout, "\n") != 1 || !strings.HasSuffix(r.stdout, "\n") {
		t.Fatalf("stdout is not one newline-terminated line: %q", r.stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("stdout is not a JSON object: %v: %q", err, r.stdout)
	}
	for _, key := range jsonKeys {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q missing", key)
		}
	}
	if len(got) != len(jsonKeys) {
		t.Errorf("%d keys, want %d", len(got), len(jsonKeys))
	}
	for _, key := range neverNullKeys {
		if got[key] == nil {
			t.Errorf("key %q is null", key)
		}
	}
	if got["device"] != mac {
		t.Errorf("device = %v, want %s", got["device"], mac)
	}
}

func TestOneShotDisconnects(t *testing.T) {
	mac := requireHardware(t)
	r, _ := runGoPwr(t, "--mac", mac)
	if r.status != 0 {
		t.Fatalf("status %d, want 0", r.status)
	}
	requireLinkDown(t, mac)
}
