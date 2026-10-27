// ABOUTME: Tests on lola that run go-pwr under btmon and check, from the trace,
// ABOUTME: that the only writes it makes to the controller are the allowed ones.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestBtmonPrerequisites fails, never skips, when btmon or the privilege to
// capture is missing, once GO_PWR_TEST_MAC says this is a hardware run.
func TestBtmonPrerequisites(t *testing.T) {
	if os.Getenv("GO_PWR_TEST_MAC") == "" {
		t.Fatal("GO_PWR_TEST_MAC not set: the btmon tests need the controller, and fail rather than skip")
	}

	if err := btmonPrerequisites(os.Getenv("PATH"), os.Geteuid(), effectiveCaps(t)); err != nil {
		t.Fatal(err)
	}

	// The check itself fails when either is missing.
	if err := btmonPrerequisites("/nonexistent", 0, 0); err == nil {
		t.Error("no error with btmon missing from PATH")
	}
	if err := btmonPrerequisites(os.Getenv("PATH"), 1000, 0); err == nil {
		t.Error("no error for a non-root process with no capabilities")
	}
	if err := btmonPrerequisites(os.Getenv("PATH"), 1000, capNetRaw); err != nil {
		t.Errorf("error for a process with CAP_NET_RAW: %v", err)
	}
}

// capNetRaw is the bit of CAP_NET_RAW in a capability set. btmon opens a raw
// HCI monitor socket, which needs it (or root).
const capNetRaw = 1 << 13

// effectiveCaps reads this process's effective capability set.
func effectiveCaps(t *testing.T) uint64 {
	t.Helper()
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if hex, ok := strings.CutPrefix(line, "CapEff:"); ok {
			caps, err := strconv.ParseUint(strings.TrimSpace(hex), 16, 64)
			if err != nil {
				t.Fatalf("CapEff %q: %v", hex, err)
			}
			return caps
		}
	}
	t.Fatal("no CapEff line in /proc/self/status")
	return 0
}

// btmonPrerequisites says what is missing to capture with btmon: the program
// in a directory of pathEnv, and root or CAP_NET_RAW.
func btmonPrerequisites(pathEnv string, euid int, capEff uint64) error {
	found := false
	for _, dir := range filepath.SplitList(pathEnv) {
		if info, err := os.Stat(filepath.Join(dir, "btmon")); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			found = true
			break
		}
	}
	if !found {
		return errors.New("btmon is not installed (BlueZ) or not on PATH")
	}
	if euid != 0 && capEff&capNetRaw == 0 {
		return errors.New("btmon needs root or CAP_NET_RAW: run the test as `sudo -n env GO_PWR_TEST_MAC=… ./gopwr.test …`")
	}
	return nil
}
