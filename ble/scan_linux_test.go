// ABOUTME: Tests scanning for controllers through BlueZ on lola, with
// ABOUTME: GO_PWR_TEST_MAC set. Scans never connect to or write to the controller.

package ble

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// openAdapter opens the adapter for a hardware test.
func openAdapter(t *testing.T) *Adapter {
	t.Helper()
	requireHardware(t)
	adapter, err := Open()
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return adapter
}

// discovering reports BlueZ's own view of whether the adapter is discovering.
func discovering(t *testing.T, adapter *Adapter) bool {
	t.Helper()
	value, err := adapter.object.GetProperty("org.bluez.Adapter1.Discovering")
	if err != nil {
		t.Fatalf("read Discovering: %v", err)
	}
	on, _ := value.Value().(bool)
	return on
}

// waitNotDiscovering waits up to 2 seconds for BlueZ to report that discovery
// has stopped, since it applies a stop asynchronously.
func waitNotDiscovering(t *testing.T, adapter *Adapter) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !discovering(t, adapter) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// emitDeviceWithoutUUIDs sends an InterfacesAdded signal on the system bus for
// a made-up device whose org.bluez.Device1 properties have an Address but no
// UUIDs, every 200 ms until stop closes. The bluetooth library's match rule
// filters by interface only, so its scan receives it and panics in
// makeScanResult. A hand-built signal is allowed here because the real
// condition can't be produced reliably (owner decision 11); it is never used
// to prove a reading is accepted.
func emitDeviceWithoutUUIDs(t *testing.T, stop <-chan struct{}) (done <-chan struct{}) {
	t.Helper()
	conn, err := dbus.SystemBusPrivate()
	if err != nil {
		t.Fatalf("connect to the system bus: %v", err)
	}
	if err := conn.Auth(nil); err != nil {
		t.Fatalf("authenticate to the system bus: %v", err)
	}
	if err := conn.Hello(); err != nil {
		t.Fatalf("say hello to the system bus: %v", err)
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer conn.Close()
		properties := map[string]map[string]dbus.Variant{
			"org.bluez.Device1": {"Address": dbus.MakeVariant("AA:BB:CC:DD:EE:FF")},
		}
		for {
			err := conn.Emit("/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF",
				"org.freedesktop.DBus.ObjectManager.InterfacesAdded",
				dbus.ObjectPath("/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF"), properties)
			if err != nil {
				t.Errorf("emit InterfacesAdded: %v", err)
				return
			}
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()
	return finished
}

func TestScanRecoversLibraryUUIDsPanic(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")

	stop := make(chan struct{})
	finished := emitDeviceWithoutUUIDs(t, stop)
	started := time.Now()
	found, err := adapter.Scan(context.Background(), mac)
	close(stop)
	<-finished
	t.Logf("Scan() with the injected signal = %v, %v after %v", found, err, time.Since(started).Round(time.Millisecond))

	if err == nil {
		t.Fatalf("Scan() = %v, nil with a signal that panics the library, want an error", found)
	}
	if !errors.Is(err, ErrRetryable) {
		t.Errorf("Scan() error = %v, want ErrRetryable", err)
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("Scan() error = %v, want it to say the scan panicked", err)
	}
	if !waitNotDiscovering(t, adapter) {
		t.Error("adapter still discovering 2s after the panic, want discovery stopped")
	}

	found, err = adapter.Scan(context.Background(), mac)
	if err != nil {
		t.Fatalf("Scan() after the panic = %v, want the controller", err)
	}
	if len(found) != 1 || !strings.EqualFold(found[0].Address, mac) {
		t.Errorf("Scan() after the panic = %+v, want %s", found, mac)
	}
}

// TestScanTimeToFindController scans for the test controller 20 times and logs
// how long each took to hear it. The scan window is chosen from these numbers
// (docs/reference/protocol.md, "Scan window"). It sleeps a second between
// scans so each starts from a stopped adapter.
func TestScanTimeToFindController(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	var times []time.Duration
	for i := range 20 {
		started := time.Now()
		found, err := adapter.Scan(context.Background(), mac)
		took := time.Since(started)
		if err != nil {
			t.Errorf("scan %d: %v after %v", i, err, took)
			continue
		}
		t.Logf("scan %2d: %s found after %v, RSSI %d dBm", i, found[0].Address, took.Round(time.Millisecond), found[0].RSSI)
		times = append(times, took)
		time.Sleep(time.Second)
	}
	if len(times) == 0 {
		t.Fatal("no scan found the controller")
	}
}
