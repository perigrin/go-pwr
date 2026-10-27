// ABOUTME: Tests on lola that run go-pwr under btmon and check, from the trace,
// ABOUTME: that the only writes it makes to the controller are the allowed ones.

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// btmonPrerequisites says what is missing to capture with btmon: btmon and
// stdbuf (which keeps btmon's live text from sitting in a pipe buffer) in a
// directory of pathEnv, and root or CAP_NET_RAW.
func btmonPrerequisites(pathEnv string, euid int, capEff uint64) error {
	for _, program := range []string{"btmon", "stdbuf"} {
		found := false
		for _, dir := range filepath.SplitList(pathEnv) {
			if info, err := os.Stat(filepath.Join(dir, program)); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s is not installed (btmon is BlueZ, stdbuf is coreutils) or not on PATH", program)
		}
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
	if err := forgetRefusal(sh(t, "bluetoothctl info "+mac)); err != nil {
		t.Fatal(err)
	}
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
	out    *syncBuffer // btmon's text of the packets so far, which it prints as it records
	exited chan struct{}
}

// startBtmon starts btmon recording to a file, and returns once it has begun.
// The test kills it on the way out whatever happens.
func startBtmon(t *testing.T) *btmonCapture {
	t.Helper()
	c := &btmonCapture{t: t, file: filepath.Join(t.TempDir(), "capture.btsnoop")}
	// stdbuf -oL: btmon prints to a pipe, which glibc block-buffers, so
	// without it the text the tests wait on arrives in chunks, late.
	c.cmd = commandInGroup("stdbuf", "-oL", "btmon", "-w", c.file)
	c.out = &syncBuffer{}
	c.cmd.Stdout, c.cmd.Stderr = c.out, &syncBuffer{}
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

// size is how many bytes btmon has printed so far.
func (c *btmonCapture) size() int {
	c.out.mu.Lock()
	defer c.out.mu.Unlock()
	return c.out.b.Len()
}

// since is what btmon printed from byte offset off on.
func (c *btmonCapture) since(off int) string {
	c.out.mu.Lock()
	defer c.out.mu.Unlock()
	b := c.out.b.Bytes()
	if off > len(b) {
		off = len(b)
	}
	return string(b[off:])
}

// settle waits until btmon has printed nothing for 2 s, so that the lines of
// an earlier run are all behind the offset the next wait starts from. It
// fails the test if btmon is still printing after 30 s.
func (c *btmonCapture) settle() {
	c.t.Helper()
	last, quietSince, deadline := c.size(), time.Now(), time.Now().Add(30*time.Second)
	for time.Since(quietSince) < 2*time.Second {
		if time.Now().After(deadline) {
			c.t.Fatal("btmon was still printing 30 s after the last run")
		}
		time.Sleep(50 * time.Millisecond)
		if n := c.size(); n != last {
			last, quietSince = n, time.Now()
		}
	}
}

// waitFor polls every 25 ms, for up to limit, for marker in what btmon
// printed after offset from, looking only at that new text.
func (c *btmonCapture) waitFor(marker *regexp.Regexp, from int, limit time.Duration) bool {
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if marker.MatchString(c.since(from)) {
			return true
		}
	}
	return false
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
	t.Logf("%d connections to %s, %d writes seen on any connection, handle map %+v, writes on the controller's:\n%s", report.Connections, mac, report.WritesSeen, report.Map, strings.Join(lines, "\n"))
	return report, err
}

// capturedRun records the controller's connections while run goes, after
// clearing BlueZ's GATT cache, and returns the checker's verdict.
func capturedRun(t *testing.T, mac string, run func(c *btmonCapture)) (*btmon.Report, error) {
	t.Helper()
	forgetController(t, mac)
	c := startBtmon(t)
	run(c)
	return checkTrace(t, c.stop(), mac)
}

func TestBtmonWritesOnlyStatusRequest(t *testing.T) {
	mac := requireBtmon(t)

	t.Run("one-shot", func(t *testing.T) {
		report, err := capturedRun(t, mac, func(*btmonCapture) {
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
			report, err := capturedRun(t, mac, func(*btmonCapture) {
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
// no ATT write was sent on any connection, recognised or not, which is what a
// reading that never got as far as connecting leaves.
func wantNoBadWrites(t *testing.T, report *btmon.Report, err error) {
	t.Helper()
	switch {
	case err == nil:
	case (errors.Is(err, btmon.ErrNoConnection) || errors.Is(err, btmon.ErrNoHandleMap)) && report.WritesSeen == 0:
		t.Logf("the reading ended before it wrote anything: %v", err)
	default:
		t.Error(err)
	}
}

// What btmon prints for the writes go-pwr makes: the CCCD set to notifications
// on, and the status request. They are matched whole, ATT line to data, since
// other devices' advertisements print Data[] lines too.
var (
	cccdOnMarker = regexp.MustCompile(`ATT: Write Request \(0x12\) len 4\n(?:[ \t]+(?:Handle|Value Handle):[^\n]*\n)+[ \t]+Data\[2\]: 0100\n`)
	statusMarker = regexp.MustCompile(`ATT: Write Request \(0x12\) len 3\n(?:[ \t]+(?:Handle|Value Handle):[^\n]*\n)+[ \t]+Data\[1\]: 20\n`)
)

const (
	oneShotWarmup = "a full reading first, so the trace holds discovery and the reading after it is not a cold one"
)

// cancelAfter starts go-pwr, signals it with SIGINT as soon as btmon prints
// marker, and checks that it exits.
//
// The signal follows btmon's text, not the radio: btmon must read the packet,
// decode it and print it on lola's ARMv6, so the signal can reach go-pwr
// well after the write, even after the reading is done (exit 0). Which side
// of the next write it falls on is whatever the lag makes it, so each run's
// exit status and the time from start to signal are logged, and the check is
// on what the trace holds afterwards.
func cancelAfter(t *testing.T, c *btmonCapture, mac, what string, marker *regexp.Regexp) {
	t.Helper()
	c.settle()
	from := c.size()
	r := startGoPwr(t, "--mac", mac, "--format", "json")
	if !c.waitFor(marker, from, 60*time.Second) {
		t.Fatalf("btmon showed no %s within 60 s of starting go-pwr; stderr %q", what, r.stderr)
	}
	signalledAfter := time.Since(r.began).Round(time.Millisecond)
	r.cmd.Process.Signal(syscall.SIGINT)
	got := r.finish(45 * time.Second)
	t.Logf("SIGINT %v after go-pwr started, on btmon printing the %s: exit status %d", signalledAfter, what, got.status)
	if got.status > 1 {
		t.Errorf("status %d after SIGINT, want 0 (the reading was done) or 1", got.status)
	}
}

func TestBtmonWritesOnlyStatusRequestOnFailures(t *testing.T) {
	mac := requireBtmon(t)

	// A --timeout budgets the scan, the connect and the reply, and the link is
	// closed after the reply, outside the budget (decision 0001, "--timeout";
	// closeBound in ble/ble.go): a reading that printed 4 s in can exit 8 s
	// in. So whether a given timeout ends the reading before or after the
	// connect varies from run to run, and the test searches for it. A warm
	// reading, which also puts discovery in the trace, gives the time to its
	// reply; timeouts from 95% of that down to 45% in steps are tried, at most
	// six, until two readings are cut off after the connect, which their
	// error says by naming the status request ("status request:" also
	// prefixes a failure to open the link, so the trace's second connection
	// is the check that a connect really happened). 1 s ends in the scan and
	// writes nothing. At least one reading must be cut off after the connect.
	t.Run("timeout too short", func(t *testing.T) {
		cutOffAfterConnect := 0
		report, err := capturedRun(t, mac, func(*btmonCapture) {
			t.Log(oneShotWarmup)
			warm := startGoPwr(t, "--mac", mac, "--format", "json")
			if !warm.waitFor(60*time.Second, func() bool { return warm.readings() >= 1 }) {
				t.Fatalf("no warm-up reading in 60 s; stderr %q", warm.stderr)
			}
			replyAt := time.Since(warm.began)
			if got := warm.finish(45 * time.Second); got.status != 0 {
				t.Fatalf("warm-up status %d, want 0", got.status)
			}
			t.Logf("the warm-up reading printed %v after go-pwr started", replyAt.Round(10*time.Millisecond))

			runGoPwr(t, "--mac", mac, "--format", "json", "--timeout", "1s")
			for _, fraction := range []float64{0.95, 0.85, 0.75, 0.65, 0.55, 0.45} {
				timeout := (time.Duration(float64(replyAt)*fraction) / (100 * time.Millisecond)) * (100 * time.Millisecond)
				r, _ := runGoPwr(t, "--mac", mac, "--format", "json", "--timeout", timeout.String())
				if r.status == 1 && strings.Contains(r.stderr, "status request:") {
					cutOffAfterConnect++
				}
				if cutOffAfterConnect >= 2 {
					break
				}
			}
		})
		wantNoBadWrites(t, report, err)
		if cutOffAfterConnect == 0 || report.Connections < 2 {
			t.Errorf("%d readings were cut off after the connect and the trace has %d connections: no timeout landed after the connect, so none proves anything", cutOffAfterConnect, report.Connections)
		}
	})

	// With the GATT cache just cleared a reading takes about 12 s, so 6 s
	// lands after the connect and in the discovery.
	t.Run("SIGINT mid-discovery", func(t *testing.T) {
		report, err := capturedRun(t, mac, func(*btmonCapture) {
			interrupt(t, syscall.SIGINT, 6*time.Second, "--mac", mac, "--format", "json")
		})
		wantNoBadWrites(t, report, err)
		if report.Connections < 1 {
			t.Errorf("no connection to the controller before the signal, so it did not land mid-reading")
		}
	})

	// SIGINT the moment btmon prints the CCCD write, then the moment it
	// prints the status request: the cancel comes after each of the two
	// writes go-pwr makes, and nothing else may follow it.
	t.Run("SIGINT after the CCCD and after the status request", func(t *testing.T) {
		report, err := capturedRun(t, mac, func(c *btmonCapture) {
			t.Log(oneShotWarmup)
			if r, _ := runGoPwr(t, "--mac", mac, "--format", "json"); r.status != 0 {
				t.Fatalf("status %d, want 0", r.status)
			}
			cancelAfter(t, c, mac, "CCCD write", cccdOnMarker)
			cancelAfter(t, c, mac, "status request", statusMarker)
		})
		if err != nil {
			t.Error(err)
		}
		cccdOn, status := 0, 0
		for _, w := range report.Writes {
			switch {
			case w.Handle == report.Map.FFF1CCCD && string(w.Data) == "\x01\x00":
				cccdOn++
			case w.Handle == report.Map.FFF2Value:
				status++
			}
		}
		// The warm-up and both cancelled runs each turned notifications on;
		// the warm-up and the second cancelled run each sent the request.
		if cccdOn < 3 || status < 2 {
			t.Errorf("%d CCCD-on writes and %d status requests in the trace, want at least 3 and 2", cccdOn, status)
		}
	})

	t.Run("dropped --follow link", func(t *testing.T) {
		t.Cleanup(func() { sh(t, "bluetoothctl disconnect "+mac) })
		report, err := capturedRun(t, mac, func(*btmonCapture) {
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
