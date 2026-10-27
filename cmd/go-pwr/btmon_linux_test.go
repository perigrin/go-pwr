// ABOUTME: Tests on lola that run go-pwr under btmon and check, from the trace,
// ABOUTME: that the only writes it makes to the controller are the allowed ones.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/internal/btmon"
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

// forgetController clears BlueZ's GATT cache for the controller with
// `bluetoothctl remove`, then scans (`--timeout` ends each scan itself) until BlueZ sees it again, so the next
// connection's trace holds service discovery and the handle map does not
// depend on a cache.
func forgetController(t *testing.T, mac string) {
	t.Helper()
	sh(t, "bluetoothctl remove "+mac)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		sh(t, "bluetoothctl --timeout 8 scan on")
		if strings.Contains(sh(t, "bluetoothctl devices"), mac) {
			return
		}
	}
	t.Fatalf("BlueZ did not see %s again within 90 s of removing it", mac)
}

// btmonCapture is a btmon process recording the adapter's HCI traffic.
type btmonCapture struct {
	t      *testing.T
	cmd    *exec.Cmd
	file   string
	exited chan struct{}
}

// startBtmon starts btmon recording to a file, and returns once it has begun.
// The test kills it on the way out whatever happens.
func startBtmon(t *testing.T) *btmonCapture {
	t.Helper()
	c := &btmonCapture{t: t, file: filepath.Join(t.TempDir(), "capture.btsnoop")}
	c.cmd = commandInGroup("btmon", "-w", c.file)
	c.cmd.Stdout, c.cmd.Stderr = &syncBuffer{}, &syncBuffer{}
	if err := c.cmd.Start(); err != nil {
		t.Fatalf("start btmon: %v", err)
	}
	c.exited = make(chan struct{})
	go func() { c.cmd.Wait(); close(c.exited) }()
	t.Cleanup(func() {
		killGroup(c.cmd)
		<-c.exited
	})
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if info, err := os.Stat(c.file); err == nil && info.Size() > 0 {
			time.Sleep(500 * time.Millisecond)
			return c
		}
	}
	t.Fatal("btmon wrote nothing in 10 s")
	return nil
}

// stop ends the capture and returns btmon's text decoding of it.
func (c *btmonCapture) stop() string {
	c.t.Helper()
	time.Sleep(time.Second) // let btmon drain the last packets
	if err := c.cmd.Process.Signal(syscall.SIGINT); err != nil {
		c.t.Fatalf("stop btmon: %v", err)
	}
	select {
	case <-c.exited:
	case <-time.After(10 * time.Second):
		c.t.Fatal("btmon still running 10 s after SIGINT")
	}
	out, err := exec.Command("btmon", "-r", c.file).Output()
	if err != nil {
		c.t.Fatalf("btmon -r: %v", err)
	}
	return string(out)
}

// checkTrace runs the checker on a capture and logs the writes it saw.
func checkTrace(t *testing.T, trace, mac string) (*btmon.Report, error) {
	t.Helper()
	report, err := btmon.Check(strings.NewReader(trace), mac)
	counts := map[string]int{}
	for _, w := range report.Writes {
		counts[fmt.Sprintf("opcode %#02x handle %#04x data % x", w.Opcode, w.Handle, w.Data)]++
	}
	var lines []string
	for line, n := range counts {
		lines = append(lines, fmt.Sprintf("%3d x %s", n, line))
	}
	sort.Strings(lines)
	t.Logf("%d connections to %s, handle map %+v, writes on them:\n%s", report.Connections, mac, report.Map, strings.Join(lines, "\n"))
	return report, err
}

// capturedRun records the controller's connections while run goes, after
// clearing BlueZ's GATT cache, and returns the checker's verdict.
func capturedRun(t *testing.T, mac string, run func()) (*btmon.Report, error) {
	t.Helper()
	forgetController(t, mac)
	c := startBtmon(t)
	run()
	return checkTrace(t, c.stop(), mac)
}

func TestBtmonWritesOnlyStatusRequest(t *testing.T) {
	mac := requireBtmon(t)

	t.Run("one-shot", func(t *testing.T) {
		report, err := capturedRun(t, mac, func() {
			if r, _ := runGoPwr(t, "--mac", mac, "--format", "json"); r.status != 0 {
				t.Fatalf("status %d, want 0", r.status)
			}
		})
		if err != nil {
			t.Error(err)
		}
		if report.StatusRequests != 1 {
			t.Errorf("%d status requests in the trace, want 1", report.StatusRequests)
		}
	})

	for _, mode := range []string{"--poll=8s", "--follow=8s"} {
		t.Run(mode, func(t *testing.T) {
			report, err := capturedRun(t, mac, func() {
				r := startGoPwr(t, "--mac", mac, mode, "--format", "json")
				if !r.waitFor(90*time.Second, func() bool { return r.readings() >= 3 }) {
					t.Fatalf("fewer than 3 readings in 90 s; stderr %q", r.stderr)
				}
				if got := r.stop(syscall.SIGTERM); got.status != 0 {
					t.Errorf("status %d, want 0", got.status)
				}
			})
			if err != nil {
				t.Error(err)
			}
			if report.StatusRequests < 3 {
				t.Errorf("%d status requests in the trace, want at least 3", report.StatusRequests)
			}
		})
	}
}

// requireBtmon is the start of every btmon test: GO_PWR_TEST_MAC is set and
// btmon can capture, or the test fails.
func requireBtmon(t *testing.T) string {
	t.Helper()
	mac := os.Getenv("GO_PWR_TEST_MAC")
	if mac == "" {
		t.Fatal("GO_PWR_TEST_MAC not set: the btmon tests need the controller, and fail rather than skip")
	}
	if err := btmonPrerequisites(os.Getenv("PATH"), os.Geteuid(), effectiveCaps(t)); err != nil {
		t.Fatal(err)
	}
	return mac
}

// wantNoBadWrites fails the test if the checker found a write that is not
// allowed. A trace with no connection or no handle map is accepted only when
// it holds no writes at all, which is what a reading that never got as far as
// connecting leaves.
func wantNoBadWrites(t *testing.T, report *btmon.Report, err error) {
	t.Helper()
	switch {
	case err == nil:
	case (errors.Is(err, btmon.ErrNoConnection) || errors.Is(err, btmon.ErrNoHandleMap)) && len(report.Writes) == 0:
		t.Logf("the reading ended before it wrote anything: %v", err)
	default:
		t.Error(err)
	}
}

func TestBtmonWritesOnlyStatusRequestOnFailures(t *testing.T) {
	mac := requireBtmon(t)

	// A reading takes at least 2.8 s on lola, so 1 s ends in the scan and 3 s
	// usually in the connect or the request.
	t.Run("timeout too short", func(t *testing.T) {
		report, err := capturedRun(t, mac, func() {
			for _, timeout := range []string{"1s", "3s"} {
				runGoPwr(t, "--mac", mac, "--format", "json", "--timeout", timeout)
			}
		})
		wantNoBadWrites(t, report, err)
	})

	// With the GATT cache just cleared a reading takes about 12 s, so 6 s
	// lands after the connect and in the discovery or the request.
	t.Run("SIGINT mid-reading", func(t *testing.T) {
		report, err := capturedRun(t, mac, func() {
			interrupt(t, syscall.SIGINT, 6*time.Second, "--mac", mac, "--format", "json")
		})
		wantNoBadWrites(t, report, err)
		if report.Connections < 1 {
			t.Errorf("no connection to the controller before the signal, so it did not land mid-reading")
		}
	})

	t.Run("dropped --follow link", func(t *testing.T) {
		t.Cleanup(func() { sh(t, "bluetoothctl disconnect "+mac) })
		report, err := capturedRun(t, mac, func() {
			r := startGoPwr(t, "--mac", mac, "--follow=5s", "--format", "json")
			if !r.waitFor(60*time.Second, func() bool { return r.readings() >= 2 }) {
				t.Fatalf("fewer than 2 readings in 60 s; stderr %q", r.stderr)
			}
			before := r.readings()
			if out := sh(t, "bluetoothctl disconnect "+mac); !strings.Contains(out, "Disconnection successful") {
				t.Fatalf("bluetoothctl disconnect %s: %s", mac, out)
			}
			if !r.waitFor(15*time.Second, func() bool { return r.errorLines() >= 1 }) {
				t.Fatalf("no error line on stderr 15 s after the drop; running: %v", r.running())
			}
			if !r.waitFor(45*time.Second, func() bool { return r.readings() > before }) {
				t.Fatalf("no reading within 45 s of the drop; stderr:\n%s", r.stderr)
			}
			if got := r.stop(syscall.SIGTERM); got.status != 0 {
				t.Errorf("status %d, want 0", got.status)
			}
		})
		// This one must have connected, dropped and connected again, or it
		// proves nothing about the link coming back.
		if err != nil {
			t.Error(err)
		}
		if report.Connections < 2 || report.StatusRequests < 3 {
			t.Errorf("%d connections and %d status requests, want at least 2 and 3 across the drop", report.Connections, report.StatusRequests)
		}
	})
}
