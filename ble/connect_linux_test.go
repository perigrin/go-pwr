// ABOUTME: Tests connecting to and disconnecting from the controller through
// ABOUTME: BlueZ on lola, with GO_PWR_TEST_MAC set. Nothing here writes to it.

package ble

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/status"
	"tinygo.org/x/bluetooth"
)

// linkUp reports BlueZ's own view of whether this machine holds a link to
// mac. A device BlueZ has no object for has no link.
func linkUp(t *testing.T, adapter *Adapter, mac string) bool {
	t.Helper()
	path := devicePath(adapter, mac)
	value, err := adapter.conn.Object("org.bluez", path).GetProperty("org.bluez.Device1.Connected")
	if err != nil {
		if dbusErrorName(err) == "org.freedesktop.DBus.Error.UnknownObject" {
			return false
		}
		t.Fatalf("read Connected of %s: %v", mac, err)
	}
	on, _ := value.Value().(bool)
	return on
}

// waitLinkDown waits up to wait for BlueZ to report no link to mac.
func waitLinkDown(t *testing.T, adapter *Adapter, mac string, wait time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		if !linkUp(t, adapter, mac) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ensureLinkDownOnCleanup makes sure each test ends with the controller
// disconnected, even when it fails. It disconnects through bluetoothctl so
// it does not depend on the code under test.
func ensureLinkDownOnCleanup(t *testing.T, mac string) {
	t.Helper()
	t.Cleanup(func() { sh(t, "bluetoothctl disconnect "+mac+"; true") })
}

// scanFor finds the controller, since the library connects only to a device
// BlueZ already has an object for.
func scanFor(t *testing.T, adapter *Adapter, mac string) {
	t.Helper()
	if _, err := adapter.Scan(context.Background(), mac); err != nil {
		t.Fatalf("Scan(%s) before connecting: %v", mac, err)
	}
}

func TestConnectAndClose(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	conn, err := adapter.Connect(ctx, mac)
	t.Logf("Connect(%s) = %v after %v", mac, err, time.Since(started).Round(time.Millisecond))
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if !linkUp(t, adapter, mac) {
		t.Error("no link after Connect, want one")
	}

	started = time.Now()
	err = conn.Close()
	t.Logf("Close() = %v after %v", err, time.Since(started).Round(time.Millisecond))
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if linkUp(t, adapter, mac) {
		t.Error("link still up right after Close, want it down")
	}
}

// settledGoroutines waits up to 3 seconds for the goroutine count to fall to
// want or below, and returns the count it ended on.
func settledGoroutines(want int) int {
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > want && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}

// goroutineBaseline lets the goroutines of a finished scan settle, then
// returns how many are running.
func goroutineBaseline() int {
	time.Sleep(time.Second)
	return settledGoroutines(0)
}

func TestConnectAbandonedAtTimeout(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)
	baseline := goroutineBaseline()

	// A connect takes seconds on lola, so 300 ms ends it part way.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	conn, err := adapter.Connect(ctx, mac)
	took := time.Since(started)
	t.Logf("Connect(%s) with a 300ms deadline = %v after %v", mac, err, took.Round(time.Millisecond))

	if conn != nil {
		conn.Close()
		t.Fatal("Connect() made a link inside 300 ms, want the deadline to cut it short")
	}
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrRetryable) {
		t.Errorf("Connect() error = %v, want DeadlineExceeded and ErrRetryable", err)
	}
	if linkUp(t, adapter, mac) {
		t.Error("link up after the abandoned connect returned, want it down")
	}
	if got := settledGoroutines(baseline); got > baseline {
		t.Errorf("%d goroutines after the abandoned connect, want %d or fewer", got, baseline)
	}
	// BlueZ goes on connecting on its own unless it is told to stop, and
	// connects take seconds, so wait long enough for a late link to appear.
	time.Sleep(lateLinkWait)
	if linkUp(t, adapter, mac) {
		t.Errorf("link came up %v after the abandoned connect, want BlueZ's attempt cancelled", lateLinkWait)
	}
}

// lateLinkWait is longer than the slowest connect measured on lola.
const lateLinkWait = 8 * time.Second

func TestConnectWithLeftoverLink(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)

	if out := sh(t, "bluetoothctl --timeout 30 connect "+mac); !strings.Contains(out, "Connection successful") {
		t.Fatalf("bluetoothctl connect %s: %s", mac, out)
	}
	if !linkUp(t, adapter, mac) {
		t.Fatal("no leftover link to test with")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := adapter.Connect(ctx, mac)
	if err != nil {
		t.Fatalf("Connect() over a leftover link error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if linkUp(t, adapter, mac) {
		t.Error("link still up after Close, want it down")
	}
}

// budget is the --timeout the readings must fit in.
const budget = 20 * time.Second

// replyTime is the longest request-to-last-notification time in the 21
// replies of the lola timing capture (protocol.md, "Notification sizes and
// timing on lola").
const replyTime = 202 * time.Millisecond

// budgetRuns is how many runs TestTimeBudget makes with an address, and how
// many without.
const budgetRuns = 10

// budgetRun is the timing of one scan, connect, discovery and close.
type budgetRun struct {
	scan, connect, discovery, closing time.Duration
}

// total is what a reading spends before it can ask for status.
func (r budgetRun) total() time.Duration {
	return r.scan + r.connect + r.discovery
}

// timeRun makes one scan, connect, service discovery and close against the
// controller. It sends nothing to the controller: discovery only reads what
// BlueZ already resolved. A connect that fails is returned, since the
// controller sometimes refuses one (a failed reading, retried on the next
// poll); a failed scan or discovery stops the test.
func timeRun(t *testing.T, adapter *Adapter, mac string, scanMac string) (run budgetRun, connectErr error) {
	t.Helper()

	started := time.Now()
	found, err := adapter.Scan(context.Background(), scanMac)
	run.scan = time.Since(started)
	if err != nil {
		t.Fatalf("Scan(%q) error = %v after %v", scanMac, err, run.scan)
	}
	heard := false
	for _, c := range found {
		heard = heard || strings.EqualFold(c.Address, mac)
	}
	if !heard {
		t.Fatalf("Scan(%q) = %+v, want it to include %s", scanMac, found, mac)
	}

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	started = time.Now()
	conn, err := adapter.Connect(ctx, mac)
	run.connect = time.Since(started)
	if err != nil {
		return run, fmt.Errorf("after %v: %w", run.connect.Round(time.Millisecond), err)
	}
	defer func() {
		started := time.Now()
		if err := conn.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		run.closing = time.Since(started)
	}()

	// The library's own discovery wait is what a later status request needs
	// to get past, so time it as the library runs it.
	parsed, err := bluetooth.ParseMAC(mac)
	if err != nil {
		t.Fatalf("ParseMAC(%s): %v", mac, err)
	}
	started = time.Now()
	device, err := adapter.library.Connect(bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: parsed}}, bluetooth.ConnectionParams{})
	if err == nil {
		_, err = device.DiscoverServices(nil)
	}
	run.discovery = time.Since(started)
	if err != nil {
		t.Fatalf("discover services error = %v after %v", err, run.discovery)
	}
	return run, nil
}

// TestTimeBudget records how long the steps before a status request take on
// lola, and checks that they and the longest reply wait fit in --timeout.
func TestTimeBudget(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	// Forget the device so the first run discovers services with a cold
	// cache, as the first connection after a restart would; the scan finds it
	// again. The later runs have a warm cache.
	sh(t, "bluetoothctl remove "+mac+"; true")
	failures := 0

	for _, group := range []struct{ name, scanMac string }{
		{"with an address", mac},
		{"without an address", ""},
	} {
		var worst budgetRun
		for i := 0; i < budgetRuns; {
			run, err := timeRun(t, adapter, mac, group.scanMac)
			if err != nil {
				t.Logf("%s: connect failed, run repeated: %v", group.name, err)
				failures++
				if failures > budgetRuns {
					t.Fatalf("%d connects failed, want a controller that connects", failures)
				}
				time.Sleep(time.Second)
				continue
			}
			t.Logf("%s, run %2d: scan %v, connect %v, discovery %v, close %v, total %v",
				group.name, i, run.scan.Round(time.Millisecond), run.connect.Round(time.Millisecond),
				run.discovery.Round(time.Millisecond), run.closing.Round(time.Millisecond), run.total().Round(time.Millisecond))
			if run.total() > worst.total() {
				worst = run
			}
			if run.total()+replyTime >= budget {
				t.Errorf("%s, run %d: %v and a %v reply do not fit in %v", group.name, i, run.total(), replyTime, budget)
			}
			i++
			time.Sleep(time.Second)
		}
		t.Logf("%s: slowest scan+connect+discovery %v, plus the %v longest reply = %v of %v; with the %v reply limit it would be %v",
			group.name, worst.total().Round(time.Millisecond), replyTime, (worst.total() + replyTime).Round(time.Millisecond), budget,
			status.TotalTimeout, (worst.total() + status.TotalTimeout).Round(time.Millisecond))
	}
}

func TestConnectAbsentDeviceErrors(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	absent := otherMac(t, mac)
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)
	baseline := goroutineBaseline()

	const deadline = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	started := time.Now()
	conn, err := adapter.Connect(ctx, absent)
	took := time.Since(started)
	t.Logf("Connect(%s) = %v after %v", absent, err, took.Round(time.Millisecond))

	if conn != nil {
		conn.Close()
		t.Fatalf("Connect() to absent %s made a link", absent)
	}
	if err == nil || !errors.Is(err, ErrRetryable) {
		t.Errorf("Connect() error = %v, want an ErrRetryable error", err)
	}
	if took >= deadline {
		t.Errorf("Connect() took %v, want an error before the %v deadline", took, deadline)
	}
	if linkUp(t, adapter, absent) || linkUp(t, adapter, mac) {
		t.Error("a link is up after the failed connect, want none")
	}
	if got := settledGoroutines(baseline); got > baseline {
		t.Errorf("%d goroutines after the failed connect, want %d or fewer", got, baseline)
	}
}

func TestConnectExpiredContext(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)
	baseline := goroutineBaseline()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	started := time.Now()
	conn, err := adapter.Connect(ctx, mac)
	took := time.Since(started)
	t.Logf("Connect(%s) with an expired context = %v after %v", mac, err, took.Round(time.Microsecond))

	if conn != nil {
		conn.Close()
		t.Fatal("Connect() made a link with an expired context")
	}
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrRetryable) {
		t.Errorf("Connect() error = %v, want DeadlineExceeded and ErrRetryable", err)
	}
	if took > 50*time.Millisecond {
		t.Errorf("Connect() took %v, want it to return at once", took)
	}
	// A connect that had started would show as a link within seconds.
	time.Sleep(lateLinkWait)
	if linkUp(t, adapter, mac) {
		t.Error("a link came up after Connect returned, want no connect started")
	}
	if got := settledGoroutines(baseline); got > baseline {
		t.Errorf("%d goroutines after the expired connect, want %d or fewer", got, baseline)
	}
}

func TestConnectDeadlineRace(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)

	// Connects take 1.2 to 9.8 s on lola (protocol.md, "Time budget on
	// lola"), so deadlines from 0.4 to 6 s straddle most of them.
	for deadline := 400 * time.Millisecond; deadline <= 6*time.Second; deadline += 400 * time.Millisecond {
		scanFor(t, adapter, mac)
		baseline := goroutineBaseline()

		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		conn, err := adapter.Connect(ctx, mac)
		cancel()
		switch {
		case err == nil:
			t.Logf("deadline %v: connect won", deadline)
			if err := conn.Close(); err != nil {
				t.Errorf("deadline %v: Close() error = %v", deadline, err)
			}
		case errors.Is(err, context.DeadlineExceeded) && errors.Is(err, ErrRetryable):
			t.Logf("deadline %v: deadline won", deadline)
		default:
			t.Logf("deadline %v: connect failed on its own: %v", deadline, err)
		}
		if !waitLinkDown(t, adapter, mac, 3*time.Second) {
			t.Errorf("deadline %v: link up after Connect and Close, want it down", deadline)
		}
		if got := settledGoroutines(baseline); got > baseline {
			t.Errorf("deadline %v: %d goroutines left, want %d or fewer", deadline, got, baseline)
		}
	}
}

func TestCloseTwice(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := adapter.Connect(ctx, mac)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	started := time.Now()
	if err := conn.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
	if took := time.Since(started); took > 50*time.Millisecond {
		t.Errorf("second Close() took %v, want it to return at once", took)
	}
	if linkUp(t, adapter, mac) {
		t.Error("link up after Close, want it down")
	}
}

func TestCloseAfterRemoteDrop(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := adapter.Connect(ctx, mac)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	sh(t, "bluetoothctl disconnect "+mac)
	if !waitLinkDown(t, adapter, mac, 5*time.Second) {
		t.Fatal("bluetoothctl disconnect left the link up")
	}

	started := time.Now()
	err = conn.Close()
	took := time.Since(started)
	t.Logf("Close() after a remote drop = %v after %v (bound %v)", err, took.Round(time.Millisecond), closeBound)
	if err != nil {
		t.Errorf("Close() error = %v, want nil for a link already down", err)
	}
	if took > closeBound/5 {
		t.Errorf("Close() took %v, want it prompt, well under its %v bound", took, closeBound)
	}
}
