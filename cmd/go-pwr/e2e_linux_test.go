// ABOUTME: End-to-end tests of one-shot mode on lola: they run the cross-built
// ABOUTME: go-pwr beside this test binary against the real controller.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/ble"
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

// linkDown reports whether bluetoothctl info says the link to the controller
// is down. It fails closed: a bluetoothctl that fails, or output that doesn't
// name the device, is not "down". BlueZ saying it has no such device, or
// Connected: no, is.
func linkDown(mac string) (down bool, out string) {
	raw, err := exec.Command("bluetoothctl", "info", mac).CombinedOutput()
	out = string(raw)
	if !strings.Contains(out, mac) {
		return false, out
	}
	if strings.Contains(out, "not available") {
		return true, out
	}
	return err == nil && strings.Contains(out, "Connected: no"), out
}

// requireLinkDown fails the test unless BlueZ reports the controller not
// connected.
func requireLinkDown(t *testing.T, mac string) {
	t.Helper()
	if down, out := linkDown(mac); !down {
		t.Errorf("link to %s is not reported down after go-pwr exited:\n%s", mac, out)
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

// scanFor scans for the controller with the address mac, or for every
// controller when mac is empty.
func scanFor(t *testing.T, adapter *ble.Adapter, mac string) []ble.Controller {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	found, err := adapter.Scan(ctx, mac)
	if err != nil {
		t.Fatalf("scan for %q: %v", mac, err)
	}
	return found
}

func TestOneShotWithoutMac(t *testing.T) {
	mac := requireHardware(t)
	adapter, err := ble.Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range scanFor(t, adapter, "") {
		if c.Address != mac {
			t.Fatalf("a second GP-PWM-30-SB is in range: %s (the test controller is %s); move it away", c.Address, mac)
		}
	}
	// Without --mac the whole 8 s scan window runs, so the reading can need
	// more than the 20 s default (owner decision 13).
	r, _ := runGoPwr(t, "--timeout=60s")
	if r.status != 0 {
		t.Fatalf("status %d, want 0", r.status)
	}
	if !strings.Contains(r.stdout, mac) {
		t.Errorf("stdout does not show the controller's address %s:\n%s", mac, r.stdout)
	}
	requireLinkDown(t, mac)
}

// interrupt starts go-pwr with args, sends it sig after delay, and returns
// its result once it has exited.
func interrupt(t *testing.T, sig syscall.Signal, delay time.Duration, args ...string) (r result, took time.Duration) {
	t.Helper()
	cmd, stdout, stderr := goPwr(t, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		t.Fatalf("go-pwr exited by itself within %v: %v\nstdout:\n%sstderr:\n%s", delay, err, stdout, stderr)
	case <-time.After(delay):
	}
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	var err error
	select {
	case err = <-exited:
	case <-time.After(45 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("go-pwr still running 45s after %v\nstdout:\n%sstderr:\n%s", sig, stdout, stderr)
	}
	took = time.Since(start)
	status := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		status = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	t.Logf("go-pwr %s, %v after %v: status %d after %v\nstdout:\n%sstderr:\n%s", joinArgs(args), sig, delay, status, took, stdout, stderr)
	return result{status, stdout.String(), stderr.String()}, took
}

// interruptDelays are when a signal arrives: during the scan, and later,
// during the connect or the request. A reading takes at least 2.8 s on lola,
// so neither is after the reading.
var interruptDelays = []time.Duration{500 * time.Millisecond, 2 * time.Second}

func TestOneShotInterruptedExit1(t *testing.T) {
	mac := requireHardware(t)
	for _, delay := range interruptDelays {
		t.Run(delay.String(), func(t *testing.T) {
			r, _ := interrupt(t, syscall.SIGINT, delay, "--mac", mac)
			if r.status != 1 || r.stdout != "" {
				t.Errorf("status %d, stdout %q, want 1 and nothing", r.status, r.stdout)
			}
			if r.stderr != "" {
				t.Errorf("stderr %q, want nothing: a signal is not an error", r.stderr)
			}
			requireLinkDown(t, mac)
		})
	}
}

func TestOneShotWithLeftoverLink(t *testing.T) {
	mac := requireHardware(t)
	adapter, err := ble.Open()
	if err != nil {
		t.Fatal(err)
	}
	scanFor(t, adapter, mac)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	leftover, err := adapter.Connect(ctx, mac)
	if err != nil {
		t.Fatalf("make the leftover link: %v", err)
	}
	t.Cleanup(func() { leftover.Close() })

	r, _ := runGoPwr(t, "--mac", mac, "--format", "json")
	if r.status != 0 || !strings.Contains(r.stdout, `"device":"`+mac+`"`) {
		t.Errorf("status %d, stdout %q, want 0 and a reading", r.status, r.stdout)
	}
	requireLinkDown(t, mac)
}

func TestOneShotAbsentMacExit1(t *testing.T) {
	requireHardware(t)
	const absent = "00:11:22:33:44:55"
	const timeout = 12 * time.Second
	r, took := runGoPwr(t, "--mac", absent, "--timeout", timeout.String())
	if r.status != 1 || r.stdout != "" {
		t.Errorf("status %d, stdout %q, want 1 and nothing", r.status, r.stdout)
	}
	if strings.Count(r.stderr, "\n") != 1 || !strings.Contains(r.stderr, "not found") {
		t.Errorf("stderr %q, want one line saying the controller was not found", r.stderr)
	}
	if took > timeout+3*time.Second {
		t.Errorf("took %v, want within --timeout %v plus 3 s", took, timeout)
	}
}

func TestOneShotTimeoutExit1Disconnects(t *testing.T) {
	mac := requireHardware(t)
	// A reading takes at least 2.8 s on lola, and about 6 s from a cold
	// start. 1 s ends in the scan and 3 s usually in the connect, with a link
	// to drop.
	for _, timeout := range []time.Duration{time.Second, 3 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			r, took := runGoPwr(t, "--mac", mac, "--timeout", timeout.String())
			if r.status == 0 {
				t.Skipf("the reading took less than %v this time", timeout)
			}
			if r.status != 1 || r.stdout != "" {
				t.Errorf("status %d, stdout %q, want 1 and nothing", r.status, r.stdout)
			}
			if strings.Count(r.stderr, "\n") != 1 {
				t.Errorf("stderr %q, want one line", r.stderr)
			}
			// Cleaning up an abandoned connect can take up to 10 s past the deadline.
			if took > timeout+12*time.Second {
				t.Errorf("took %v, want within --timeout plus the 10 s disconnect bound", took)
			}
			requireLinkDown(t, mac)
		})
	}
}

func TestOneShotSIGTERMExit1(t *testing.T) {
	mac := requireHardware(t)
	for _, delay := range interruptDelays {
		t.Run(delay.String(), func(t *testing.T) {
			r, _ := interrupt(t, syscall.SIGTERM, delay, "--mac", mac)
			if r.status != 1 || r.stdout != "" {
				t.Errorf("status %d, stdout %q, want 1 and nothing", r.status, r.stdout)
			}
			if r.stderr != "" {
				t.Errorf("stderr %q, want nothing: a signal is not an error", r.stderr)
			}
			requireLinkDown(t, mac)
		})
	}
}

// wantOnlyJSONObjects fails the test unless every line of stdout is one
// complete JSON object.
func wantOnlyJSONObjects(t *testing.T, stdout string) {
	t.Helper()
	if stdout != "" && !strings.HasSuffix(stdout, "\n") {
		t.Errorf("stdout %q does not end in a newline", stdout)
	}
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		var object map[string]any
		if err := json.Unmarshal([]byte(line), &object); err != nil || object == nil {
			t.Errorf("stdout line %q is not a JSON object: %v", line, err)
		}
	}
}

func TestOneShotStdoutOnlyReadings(t *testing.T) {
	mac := requireHardware(t)

	r, _ := runGoPwr(t, "--mac", mac, "--format", "json")
	if r.status != 0 || strings.Count(r.stdout, "\n") != 1 {
		t.Fatalf("status %d, stdout %q, want 0 and one line", r.status, r.stdout)
	}
	wantOnlyJSONObjects(t, r.stdout)
	for _, line := range strings.Split(r.stderr, "\n") {
		if strings.Contains(r.stdout, line) && line != "" {
			t.Errorf("stderr line %q also reached stdout", line)
		}
	}

	failed, _ := runGoPwr(t, "--mac", mac, "--format", "json", "--timeout=1s")
	if failed.status != 1 || failed.stdout != "" || failed.stderr == "" {
		t.Errorf("failed run: status %d, stdout %q, stderr %q, want 1, nothing, an error on stderr", failed.status, failed.stdout, failed.stderr)
	}
}

// A signal that lands while the adapter is being opened is quiet, like one
// in the scan: the open's error is a bare cancellation. This needs the
// system bus but no controller.
func TestOneShotSignalDuringOpenIsQuiet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ble.OpenContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext on a cancelled context = %v, want an error matching context.Canceled", err)
	}
	var stdout, stderr bytes.Buffer
	code := oneShot(ctx, config{format: "json", mac: "C3:00:00:58:51:40", timeout: time.Minute}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("code %d, stdout %q, stderr %q, want 1 and nothing at all", code, stdout.String(), stderr.String())
	}
}
