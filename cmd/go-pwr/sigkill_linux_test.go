// ABOUTME: End-to-end tests on lola of runs that follow a go-pwr killed with
// ABOUTME: SIGKILL, which cannot disconnect and may leave the link up.

package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// sigkill kills a running go-pwr outright, with no chance to disconnect, and
// waits for it to be gone.
func sigkill(t *testing.T, r *liveRun) {
	t.Helper()
	if got := r.stop(syscall.SIGKILL); got.status != -1 {
		t.Fatalf("status %d after SIGKILL, want -1 (killed by a signal)", got.status)
	}
}

// sigkillIfRunning kills go-pwr outright as sigkill does, unless it has
// already exited, and says whether it killed a running process. A process
// that exited by itself before the signal is not a failure of the test, only
// a kill that did not happen.
func sigkillIfRunning(t *testing.T, r *liveRun) bool {
	t.Helper()
	err := r.cmd.Process.Signal(syscall.SIGKILL)
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	// A process that has exited but not yet been reaped still takes the
	// signal without error, and then finishes with its own status 0: that is
	// the same kill that did not happen.
	switch got := r.finish(45 * time.Second); got.status {
	case -1:
		return true
	case 0:
		t.Log("go-pwr exited by itself just before the SIGKILL")
		return false
	default:
		t.Fatalf("status %d after SIGKILL, want -1 (killed by a signal)", got.status)
	}
	return true
}

// watchLink logs whether the link to the controller is up once a second for
// span, starting now. It returns how long the link stayed up from the start
// (0 if it was down at once), and whether it was still up at the end.
func watchLink(t *testing.T, mac string, span time.Duration) (stayedUp time.Duration, upAtEnd bool) {
	t.Helper()
	start := time.Now()
	dropped := false
	for time.Since(start) < span {
		up := linkUp(mac)
		if !up && !dropped {
			dropped = true
			stayedUp = time.Since(start)
		}
		t.Logf("%5.1f s after the kill: link up=%v", time.Since(start).Seconds(), up)
		upAtEnd = up
		time.Sleep(time.Second)
	}
	if !dropped {
		stayedUp = span
	}
	return stayedUp, upAtEnd
}

// nextRun starts the go-pwr run that follows a killed one, which every test
// here is about.
func nextRun(t *testing.T, mac string, args ...string) *liveRun {
	t.Helper()
	return startGoPwr(t, append([]string{"--mac", mac, "--format", "json"}, args...)...)
}

// leaveLinkDown disconnects the controller when the test ends, whatever a
// killed run left up, and fails the test if the link is not down after that.
func leaveLinkDown(t *testing.T, mac string) {
	t.Helper()
	t.Cleanup(func() {
		sh(t, "bluetoothctl disconnect "+mac)
		requireLinkDown(t, mac)
	})
}

// resetLink disconnects the controller and requires the link to be down, so
// a test or subtest starts with no link left by the one before it.
func resetLink(t *testing.T, mac string) {
	t.Helper()
	sh(t, "bluetoothctl disconnect "+mac)
	requireLinkDown(t, mac)
}

// readsOnce runs a one-shot JSON reading and checks that it succeeds with
// one reading on stdout and nothing wrong on stderr.
func readsOnce(t *testing.T, mac string) result {
	t.Helper()
	r := nextRun(t, mac).finish(60 * time.Second)
	if r.status != 0 {
		t.Fatalf("status %d, want 0", r.status)
	}
	wantOnlyJSONObjects(t, r.stdout)
	if n := strings.Count(r.stdout, "\n"); n != 1 {
		t.Errorf("%d readings on stdout, want 1: %q", n, r.stdout)
	}
	if r.stderr != "" {
		t.Errorf("stderr %q, want nothing", r.stderr)
	}
	return r
}

// followUntilReading starts a --follow run on a 5 s interval and waits for
// its first reading, then a little longer so the link is between readings.
func followUntilReading(t *testing.T, mac string) *liveRun {
	t.Helper()
	r := startGoPwr(t, "--mac", mac, "--follow=5s", "--format", "json")
	if !r.waitFor(45*time.Second, func() bool { return r.readings() >= 1 }) {
		t.Fatalf("no reading in 45 s; stderr %q", r.stderr)
	}
	return r
}

func TestSIGKILLThenNextRunReads(t *testing.T) {
	mac := requireHardware(t)
	// Whatever the test finds, it leaves the link down.
	leaveLinkDown(t, mac)
	r := followUntilReading(t, mac)
	time.Sleep(2 * time.Second)
	if !linkUp(mac) {
		t.Fatal("link is not up between readings, so there is nothing for a SIGKILL to leave behind")
	}
	sigkill(t, r)

	stayedUp, upAtEnd := watchLink(t, mac, 30*time.Second)
	t.Logf("link after SIGKILL: stayed up %.1f s of 30 s, up at the end: %v", stayedUp.Seconds(), upAtEnd)

	readsOnce(t, mac)
	requireLinkDown(t, mac)
}

// linkUpSignal reports on the returned channel the moment BlueZ says the
// link to the controller is up, so a test can act on a timescale of
// milliseconds, which polling bluetoothctl (about 0.5 s a call on lola) can't.
func linkUpSignal(t *testing.T, mac string) <-chan time.Time {
	t.Helper()
	bus, err := dbus.ConnectSystemBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close() })
	path := dbus.ObjectPath("/org/bluez/hci0/dev_" + strings.ReplaceAll(strings.ToUpper(mac), ":", "_"))
	if err := bus.AddMatchSignal(
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
	); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 64)
	bus.Signal(signals)
	up := make(chan time.Time, 1)
	go func() {
		for s := range signals {
			if len(s.Body) < 2 {
				continue
			}
			changed, _ := s.Body[1].(map[string]dbus.Variant)
			if v, ok := changed["Connected"]; ok && v.Value() == true {
				select {
				case up <- time.Now():
				default:
				}
			}
		}
	}()
	return up
}

func TestSIGKILLMidReplyThenPollReads(t *testing.T) {
	mac := requireHardware(t)
	leaveLinkDown(t, mac)
	// The request goes out some 0.4 to 1.1 s after the link is up
	// (protocol.md, Settle delay) and its reply is whole within 0.2 s, so
	// these offsets from the link coming up land before, in and after the
	// reply. Which of them is "in" can't be seen from outside, so each kills
	// a one-shot run and each must leave the next run able to read.
	// A one-shot run may already have exited by the later offsets, which is
	// logged and skips that kill; at least one offset must kill a running one.
	killedRunning := 0
	defer func() {
		if killedRunning == 0 {
			t.Error("no offset killed a running process, so nothing here tested a SIGKILL")
		}
	}()
	for _, offset := range []time.Duration{600 * time.Millisecond, 900 * time.Millisecond, 1200 * time.Millisecond} {
		t.Run(offset.String(), func(t *testing.T) {
			resetLink(t, mac)
			up := linkUpSignal(t, mac)
			killed := startGoPwr(t, "--mac", mac, "--format", "json")
			select {
			case <-up:
			case <-time.After(30 * time.Second):
				t.Fatalf("the link did not come up in 30 s; stderr %q", killed.stderr)
			}
			time.Sleep(offset)
			if sigkillIfRunning(t, killed) {
				killedRunning++
				t.Logf("killed %v after the link came up; the killed run had printed %d readings", offset, killed.readings())
			} else {
				t.Logf("the one-shot run had already exited %v after the link came up: kill skipped at this offset", offset)
			}

			r := nextRun(t, mac, "--poll=5s")
			if !r.waitFor(60*time.Second, func() bool { return r.readings() >= 1 }) {
				t.Fatalf("no reading in 60 s after the SIGKILL; stderr %q", r.stderr)
			}
			got := r.stop(syscall.SIGTERM)
			if got.status != 0 {
				t.Errorf("status %d, want 0", got.status)
			}
			wantOnlyJSONObjects(t, got.stdout)
			if n := failedReadings(got.stderr); n != 0 {
				t.Errorf("%d failed readings, stderr:\n%s", n, got.stderr)
			}
			requireLinkDown(t, mac)
		})
	}
}

func TestSIGKILLThenFollowReads(t *testing.T) {
	mac := requireHardware(t)
	leaveLinkDown(t, mac)
	killed := followUntilReading(t, mac)
	time.Sleep(2 * time.Second)
	sigkill(t, killed)

	// The new run meets whatever link the killed one left. A second reading
	// on the same run shows the held connection works, not only the first.
	r := nextRun(t, mac, "--follow=5s")
	if !r.waitFor(60*time.Second, func() bool { return r.readings() >= 2 }) {
		t.Fatalf("fewer than 2 readings in 60 s after the SIGKILL; stderr %q", r.stderr)
	}
	got := r.stop(syscall.SIGTERM)
	if got.status != 0 {
		t.Errorf("status %d, want 0", got.status)
	}
	wantOnlyJSONObjects(t, got.stdout)
	if n := failedReadings(got.stderr); n != 0 {
		t.Errorf("%d failed readings, stderr:\n%s", n, got.stderr)
	}
	requireLinkDown(t, mac)
}

func TestSIGKILLTwiceThenReads(t *testing.T) {
	mac := requireHardware(t)
	leaveLinkDown(t, mac)
	sigkill(t, followUntilReading(t, mac))

	// The second run starts over whatever the first left, and is killed too.
	second := followUntilReading(t, mac)
	time.Sleep(2 * time.Second)
	sigkill(t, second)

	readsOnce(t, mac)
	requireLinkDown(t, mac)
}

// wantFramedWhole fails the test unless raw is a whole framed reply: a
// leading space, a body of 32 fields ending in a trailing ';', and "\r\n".
func wantFramedWhole(t *testing.T, raw string) {
	t.Helper()
	if len(raw) != 169 || !strings.HasPrefix(raw, " ") || !strings.HasSuffix(raw, "\r\n") {
		t.Errorf("raw %q is not a whole framed reply of 169 bytes", raw)
		return
	}
	body := raw[1 : len(raw)-2]
	if n := len(strings.Split(body, ";")); n != 32 || !strings.HasSuffix(body, ";") {
		t.Errorf("raw body has %d fields, want 32 ending in ';': %q", n, body)
	}
}

func TestSIGKILLNextReadingUnspliced(t *testing.T) {
	mac := requireHardware(t)
	leaveLinkDown(t, mac)
	// With a 1 s interval the next reading starts 0.5 to 0.8 s after a reply
	// and takes 0.2 to 0.5 s on the held connection, so these offsets land
	// the kill in or just before one (see TestFollowSignalDuringHeldReading).
	for _, offset := range []time.Duration{700 * time.Millisecond, 850 * time.Millisecond, time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			resetLink(t, mac)
			killed := startGoPwr(t, "--mac", mac, "--follow=1s", "--format", "json")
			if !killed.waitFor(45*time.Second, func() bool { return killed.readings() >= 3 }) {
				t.Fatalf("fewer than 3 readings in 45 s; stderr %q", killed.stderr)
			}
			time.Sleep(offset)
			sigkill(t, killed)

			r := readsOnce(t, mac)
			var got struct {
				Raw string `json:"raw"`
			}
			if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
				t.Fatalf("stdout %q: %v", r.stdout, err)
			}
			wantFramedWhole(t, got.Raw)
			if strings.Contains(r.stderr, "warning") {
				t.Errorf("stderr %q has a warning", r.stderr)
			}
			requireLinkDown(t, mac)
		})
	}
}
