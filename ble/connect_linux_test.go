// ABOUTME: Tests connecting to and disconnecting from the controller through
// ABOUTME: BlueZ on lola, with GO_PWR_TEST_MAC set. Nothing here writes to it.

package ble

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
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
