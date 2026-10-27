// ABOUTME: Tests on lola that a reading never sends its status request on a link
// ABOUTME: it did not make: one left up by another client, or by a killed run.

package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// linkUpWithin waits up to limit for BlueZ to say the link to the controller
// is up, and says whether it did.
func linkUpWithin(mac string, limit time.Duration) bool {
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if linkUp(mac) {
			return true
		}
	}
	return linkUp(mac)
}

func TestLeftoverLinkIsDroppedBeforeTheRequest(t *testing.T) {
	mac := requireBtmon(t)
	t.Cleanup(func() { sh(t, "bluetoothctl disconnect "+mac) })
	report, err := capturedRun(t, mac, func(*btmonCapture) {
		// The link of another client, which BlueZ keeps up as it does for a
		// killed go-pwr: it is the first link in the trace.
		sh(t, "bluetoothctl connect "+mac)
		if !linkUpWithin(mac, 20*time.Second) {
			t.Fatal("bluetoothctl connect did not bring the link up, so there is no leftover link")
		}
		if r, _ := runGoPwr(t, "--mac", mac, "--format", "json"); r.status != 0 {
			t.Fatalf("status %d, want 0", r.status)
		}
	})
	if err != nil {
		t.Error(err)
	}
	if report.Connections != 2 {
		t.Errorf("%d connections to the controller, want 2: the leftover one and go-pwr's own", report.Connections)
	}
	if err := report.FirstRequestOnNewLink(); err != nil {
		t.Error(err)
	}
	requireLinkDown(t, mac)
}

// linkEvent is a change BlueZ announced on the controller's D-Bus objects.
type linkEvent struct {
	at   time.Time
	what string // "up", "down", or "reply" for a notification of the status reply
}

// linkWatch records the controller's Connected changes and the notifications
// of its reply, from a D-Bus connection of its own, so a test can see
// the order they came in.
type linkWatch struct {
	mu     sync.Mutex
	events []linkEvent
}

func watchLinkEvents(t *testing.T, mac string) *linkWatch {
	t.Helper()
	bus, err := dbus.ConnectSystemBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close() })
	path := dbus.ObjectPath("/org/bluez/hci0/dev_" + strings.ReplaceAll(strings.ToUpper(mac), ":", "_"))
	if err := bus.AddMatchSignal(
		dbus.WithMatchPathNamespace(path),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
	); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 256)
	bus.Signal(signals)
	w := &linkWatch{}
	go func() {
		for s := range signals {
			if len(s.Body) < 2 {
				continue
			}
			iface, _ := s.Body[0].(string)
			changed, _ := s.Body[1].(map[string]dbus.Variant)
			switch {
			case iface == "org.bluez.Device1" && changed["Connected"].Value() == true:
				w.add("up")
			case iface == "org.bluez.Device1" && changed["Connected"].Value() == false:
				w.add("down")
			case iface == "org.bluez.GattCharacteristic1":
				if _, ok := changed["Value"]; ok {
					w.add("reply")
				}
			}
		}
	}()
	return w
}

func (w *linkWatch) add(what string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, linkEvent{time.Now(), what})
}

// since is the events from index n on.
func (w *linkWatch) since(n int) []linkEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]linkEvent(nil), w.events[n:]...)
}

func (w *linkWatch) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.events)
}

// whatOf lists what the events were, in order.
func whatOf(events []linkEvent) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.what)
	}
	return out
}

func TestLeftoverLinkFromKilledRunIsDropped(t *testing.T) {
	mac := requireHardware(t)
	leaveLinkDown(t, mac)
	watch := watchLinkEvents(t, mac)

	// Kill a one-shot run on the first notification of its reply, which is
	// just after its request was written. The run may have finished by the
	// time the signal arrives, which is logged and tried again.
	killedRunning := false
	for try := 1; try <= 3 && !killedRunning; try++ {
		resetLink(t, mac)
		mark := watch.count()
		killed := nextRun(t, mac)
		if !killed.waitFor(45*time.Second, func() bool {
			for _, e := range watch.since(mark) {
				if e.what == "reply" {
					return true
				}
			}
			return false
		}) {
			t.Fatalf("no notification of a reply in 45 s; stderr %q", killed.stderr)
		}
		killedRunning = sigkillIfRunning(t, killed)
		t.Logf("try %d: killed a running go-pwr: %v; events %v", try, killedRunning, whatOf(watch.since(mark)))
	}
	if !killedRunning {
		t.Fatal("no try killed a running go-pwr on its reply, so nothing was left to drop")
	}
	if !linkUp(mac) {
		t.Fatal("the link is down after the SIGKILL, so the next run meets no leftover link")
	}

	mark := watch.count()
	began := time.Now()
	r := readsOnce(t, mac)
	events := whatOf(watch.since(mark))
	t.Logf("events during the next run: %v", events)

	// The leftover link goes down first, then the run's own comes up, and
	// only then does a reply arrive: the reading was not made on the leftover.
	want := []string{"down", "up", "reply"}
	if len(events) < len(want) || strings.Join(events[:3], " ") != strings.Join(want, " ") {
		t.Errorf("events %v, want them to begin %v", events, want)
	}
	replies := 0
	for _, e := range events {
		if e == "reply" {
			replies++
		}
	}
	t.Logf("%d notifications of the reply between the run's link coming up and going down", replies)
	var got struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("stdout %q: %v", r.stdout, err)
	}
	wantFramedWhole(t, got.Raw)
	readings := parseReadings(t, r.stdout)
	if len(readings) != 1 || readings[0].at.Before(began.Truncate(time.Second)) {
		t.Errorf("readings %+v, want one made at or after the second the run began (%v)", readings, began)
	}
	requireLinkDown(t, mac)
}
