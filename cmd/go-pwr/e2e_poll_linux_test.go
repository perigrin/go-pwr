// ABOUTME: End-to-end tests of --poll on lola: the cross-built go-pwr runs
// ABOUTME: against the real controller and its output is read while it runs.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/ble"
)

// syncBuffer is a buffer that a test can read while go-pwr is still writing
// to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// commandInGroup makes a command that runs in a process group of its own,
// so killGroup can kill what it starts too.
func commandInGroup(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// killGroup kills the command and everything in its process group.
func killGroup(cmd *exec.Cmd) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// liveRun is a go-pwr process that is still running.
type liveRun struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdout *syncBuffer
	stderr *syncBuffer
	exited chan error
	began  time.Time
}

// startGoPwr starts the cross-built go-pwr and keeps it running. The test
// kills it on the way out if it is still there.
func startGoPwr(t *testing.T, args ...string) *liveRun {
	t.Helper()
	r := &liveRun{t: t, stdout: &syncBuffer{}, stderr: &syncBuffer{}, exited: make(chan error, 1)}
	r.cmd = exec.Command(crossBinary(t), args...)
	r.cmd.Stdout, r.cmd.Stderr = r.stdout, r.stderr
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.began = time.Now()
	go func() { r.exited <- r.cmd.Wait() }()
	t.Cleanup(func() {
		select {
		case err := <-r.exited:
			r.exited <- err
		default:
			r.cmd.Process.Kill()
			<-r.exited
			t.Errorf("go-pwr %s was still running at the end of the test and was killed", joinArgs(args))
		}
	})
	return r
}

// running reports whether the process has not exited.
func (r *liveRun) running() bool {
	select {
	case err := <-r.exited:
		r.exited <- err
		return false
	default:
		return true
	}
}

// waitFor polls cond every 200 ms until it holds, the process exits or limit
// passes, and says whether cond held.
func (r *liveRun) waitFor(limit time.Duration, cond func() bool) bool {
	r.t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		if !r.running() {
			return cond()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return cond()
}

// readings is how many complete JSON lines are on stdout so far.
func (r *liveRun) readings() int {
	return strings.Count(r.stdout.String(), "\n")
}

// errorLines is how many lines are on stderr so far.
func (r *liveRun) errorLines() int {
	return strings.Count(r.stderr.String(), "\n")
}

// finish waits up to limit for the process to exit by itself, and returns
// its result. It kills the process and fails the test if it doesn't.
func (r *liveRun) finish(limit time.Duration) result {
	r.t.Helper()
	var err error
	select {
	case err = <-r.exited:
		r.exited <- err
	case <-time.After(limit):
		r.cmd.Process.Kill()
		r.t.Fatalf("go-pwr still running %v after it started\nstdout:\n%sstderr:\n%s", limit, r.stdout, r.stderr)
	}
	status := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		status = exit.ExitCode()
	case err != nil:
		r.t.Fatal(err)
	}
	r.t.Logf("go-pwr exited %d after %v\nstdout:\n%sstderr:\n%s", status, time.Since(r.began).Round(time.Millisecond), r.stdout, r.stderr)
	return result{status, r.stdout.String(), r.stderr.String()}
}

// stop sends sig and returns the result once go-pwr has exited, which is
// within 45 s or the test fails.
func (r *liveRun) stop(sig syscall.Signal) result {
	r.t.Helper()
	if err := r.cmd.Process.Signal(sig); err != nil {
		r.t.Fatal(err)
	}
	return r.finish(45 * time.Second)
}

// polled is one reading's identity and timing, from a JSON line.
type polled struct {
	at     time.Time
	uptime float64
	bootID string
}

// parseReadings parses every line of stdout as a JSON reading.
func parseReadings(t *testing.T, stdout string) []polled {
	t.Helper()
	var out []polled
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		var got struct {
			Time   string  `json:"time"`
			Uptime float64 `json:"uptime_s"`
			BootID string  `json:"boot_id"`
		}
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Errorf("stdout line %q is not a JSON reading: %v", line, err)
			continue
		}
		at, err := time.Parse(time.RFC3339, got.Time)
		if err != nil {
			t.Errorf("time %q: %v", got.Time, err)
		}
		out = append(out, polled{at, got.Uptime, got.BootID})
	}
	return out
}

// uptimeGaps are the seconds between consecutive readings by uptime_s, which
// a wall-clock step doesn't move.
func uptimeGaps(readings []polled) []float64 {
	var gaps []float64
	for i := 1; i < len(readings); i++ {
		gaps = append(gaps, readings[i].uptime-readings[i-1].uptime)
	}
	return gaps
}

// wantStartToStart checks that readings made with --poll=interval start every
// interval, start to start. A reading arrives some 3 to 8 s after it starts,
// so one gap varies by several seconds either side of the interval, but the
// jitter cancels over the whole run: the mean gap is the interval, where
// loops that wait the interval after each reading, end to start, would have
// a mean of the interval plus the time a reading takes. A run with a failed
// reading has a longer gap that is not the loop's fault, so it only checks
// that readings never come closer than the shortest possible reading.
func wantStartToStart(t *testing.T, readings []polled, interval time.Duration, clean bool) {
	t.Helper()
	if len(readings) < 5 {
		t.Fatalf("%d readings, want at least 5", len(readings))
	}
	for i, gap := range uptimeGaps(readings) {
		if gap < 2.5 {
			t.Errorf("reading %d came %.1f s after the one before: readings overlap", i+1, gap)
		}
		if readings[i+1].bootID != readings[0].bootID {
			t.Errorf("boot_id changed at reading %d", i+1)
		}
	}
	mean := (readings[len(readings)-1].uptime - readings[0].uptime) / float64(len(readings)-1)
	t.Logf("mean gap between %d readings: %.2f s (gaps %.1f)", len(readings), mean, uptimeGaps(readings))
	if !clean {
		t.Logf("a reading failed in this run, so the mean gap is not checked")
		return
	}
	if want := interval.Seconds(); mean < want-1.5 || mean > want+1.5 {
		t.Errorf("mean gap %.2f s, want %.0f s within 1.5 s: the interval is not start to start", mean, want)
	}
}

func TestPollIntervalStartToStart(t *testing.T) {
	mac := requireHardware(t)
	r := startGoPwr(t, "--mac", mac, "--poll=10s", "--format", "json")
	time.Sleep(65 * time.Second)
	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	wantOnlyJSONObjects(t, got.stdout)
	wantStartToStart(t, parseReadings(t, got.stdout), 10*time.Second, failedReadings(got.stderr) == 0)
	requireLinkDown(t, mac)
}

func TestPollDisconnectsBetweenReadings(t *testing.T) {
	mac := requireHardware(t)
	r := startGoPwr(t, "--mac", mac, "--poll=15s", "--format", "json")
	for want := 1; want <= 3; want++ {
		if !r.waitFor(45*time.Second, func() bool { return r.readings() >= want }) {
			t.Fatalf("reading %d did not arrive in 45 s; stderr %q", want, r.stderr)
		}
		// The reading is printed before the link is closed, which takes a few
		// seconds, and the next reading starts 15 s after this one did.
		time.Sleep(8 * time.Second)
		if down, out := linkDown(mac); !down {
			t.Errorf("link is up between reading %d and the next:\n%s", want, out)
		}
	}
	r.stop(syscall.SIGTERM)
	requireLinkDown(t, mac)
}

func TestPollSignalsExit0(t *testing.T) {
	mac := requireHardware(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			r := startGoPwr(t, "--mac", mac, "--poll=10s", "--format", "json")
			if !r.waitFor(45*time.Second, func() bool { return r.readings() >= 1 }) {
				t.Fatalf("no reading in 45 s; stderr %q", r.stderr)
			}
			// Wait for the link to close, so the signal lands between readings.
			time.Sleep(6 * time.Second)
			signalled := time.Now()
			got := r.stop(sig)
			if got.status != 0 {
				t.Errorf("status %d, want 0", got.status)
			}
			if took := time.Since(signalled); took > 3*time.Second {
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

func TestPollSignalMidReading(t *testing.T) {
	mac := requireHardware(t)
	// A reading takes at least 2.8 s on lola, so these land in the scan and,
	// usually, in the connect.
	for _, delay := range []time.Duration{500 * time.Millisecond, 2 * time.Second} {
		t.Run(delay.String(), func(t *testing.T) {
			r, _ := interrupt(t, syscall.SIGTERM, delay, "--mac", mac, "--poll=10s", "--format", "json")
			if r.status != 0 {
				t.Errorf("status %d, want 0", r.status)
			}
			if r.stdout != "" {
				wantOnlyJSONObjects(t, r.stdout)
			}
			if r.stderr != "" {
				t.Errorf("stderr %q, want nothing: a signal is not an error", r.stderr)
			}
			requireLinkDown(t, mac)
		})
	}
}

func TestPollLongReadingNoMakeUp(t *testing.T) {
	mac := requireHardware(t)
	// A reading takes more than the 1 s interval: each starts when the one
	// before has finished, and none is made up.
	r := startGoPwr(t, "--mac", mac, "--poll=1s", "--format", "json")
	time.Sleep(25 * time.Second)
	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	wantOnlyJSONObjects(t, got.stdout)
	readings := parseReadings(t, got.stdout)
	if len(readings) < 3 {
		t.Fatalf("%d readings in 25 s, want at least 3", len(readings))
	}
	// A reading takes at least 2.8 s from its start to its reply, so with no
	// overlap and no burst, the readings come at least that far apart.
	for i, gap := range uptimeGaps(readings) {
		if gap < 2.5 {
			t.Errorf("reading %d came %.1f s after the one before: overlap or a burst of make-up readings", i+1, gap)
		}
	}
	if max := 25.0 / 2.8; float64(len(readings)) > max+1 {
		t.Errorf("%d readings in 25 s, more than one every 2.8 s", len(readings))
	}
	requireLinkDown(t, mac)
}

// textBlocks matches whole text readings of seven lines, one blank line
// between them and none at either end.
var textBlocks = regexp.MustCompile(`^([^\n]+\n){7}(\n([^\n]+\n){7})+$`)

// timeLines matches the first line of a text reading.
var timeLines = regexp.MustCompile(`(?m)^time `)

func TestPollTextBlankLineBetweenReadings(t *testing.T) {
	mac := requireHardware(t)
	r := startGoPwr(t, "--mac", mac, "--poll=8s")
	if !r.waitFor(90*time.Second, func() bool { return len(timeLines.FindAllString(r.stdout.String(), -1)) >= 2 }) {
		t.Fatalf("fewer than two readings in 90 s; stdout %q stderr %q", r.stdout, r.stderr)
	}
	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	// Stopping may have cut a third reading short, but never printed half of one.
	if !textBlocks.MatchString(got.stdout) {
		t.Errorf("stdout %q is not whole 7-line readings with exactly one blank line between", got.stdout)
	}
	requireLinkDown(t, mac)
}

func TestPollWithLeftoverLink(t *testing.T) {
	mac := requireHardware(t)
	adapter, err := ble.Open()
	if err != nil {
		t.Fatal(err)
	}
	// bluetoothctl can only connect to a device BlueZ has an object for, so
	// scan for the controller first, as go-pwr itself does.
	scanFor(t, adapter, mac)
	t.Cleanup(func() {
		sh(t, "bluetoothctl disconnect "+mac)
		requireLinkDown(t, mac)
	})
	if out := sh(t, "bluetoothctl --timeout 30 connect "+mac); !strings.Contains(out, "Connection successful") {
		t.Fatalf("bluetoothctl connect %s: %s", mac, out)
	}

	r := startGoPwr(t, "--mac", mac, "--poll=15s", "--format", "json")
	for want := 1; want <= 2; want++ {
		if !r.waitFor(45*time.Second, func() bool { return r.readings() >= want }) {
			t.Fatalf("reading %d did not arrive in 45 s; stderr %q", want, r.stderr)
		}
		time.Sleep(8 * time.Second)
		if down, out := linkDown(mac); !down {
			t.Errorf("link is up between reading %d and the next:\n%s", want, out)
		}
	}
	r.stop(syscall.SIGTERM)
	requireLinkDown(t, mac)
}

// killGroup must kill the whole process tree: runuser starts go-pwr as a
// child, and killing runuser alone leaves go-pwr running as nobody.
func TestKillGroupKillsTheChildToo(t *testing.T) {
	cmd := commandInGroup("sh", "-c", "sleep 300 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var child int
	if _, err := fmt.Fscan(out, &child); err != nil {
		t.Fatalf("read the child's pid: %v", err)
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })

	killGroup(cmd)
	cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(child, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("child %d is still running after killGroup", child)
}
