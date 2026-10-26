// ABOUTME: Tests connecting to and disconnecting from the controller through
// ABOUTME: BlueZ on lola, with GO_PWR_TEST_MAC set. Nothing here writes to it.

package ble

import (
	"context"
	"os"
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
	t.Cleanup(func() { sh(t, "bluetoothctl disconnect "+mac) })
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
