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
// scans so each starts from a stopped adapter. It is a measurement, and a miss
// is an accepted outcome of the window, so it runs only when
// GO_PWR_MEASURE_SCAN is set, and logs misses rather than failing on them.
func TestScanTimeToFindController(t *testing.T) {
	if os.Getenv("GO_PWR_MEASURE_SCAN") == "" {
		t.Skip("measurement: set GO_PWR_MEASURE_SCAN=1 to run it")
	}
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	var times []time.Duration
	for i := range 20 {
		started := time.Now()
		found, err := adapter.Scan(context.Background(), mac)
		took := time.Since(started)
		if err != nil {
			t.Logf("scan %d: missed: %v after %v", i, err, took)
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

// scanSlack is how far a scan's length may differ from the window, or from a
// cancel, before a test calls it early or late.
const scanSlack = time.Second

func TestScanFindsControllerByMac(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")

	started := time.Now()
	found, err := adapter.Scan(context.Background(), mac)
	took := time.Since(started)
	t.Logf("Scan(%s) = %+v, %v after %v (window %v)", mac, found, err, took.Round(time.Millisecond), scanWindow)

	if err != nil {
		t.Fatalf("Scan() error = %v, want the controller", err)
	}
	if len(found) != 1 || !strings.EqualFold(found[0].Address, mac) {
		t.Fatalf("Scan() = %+v, want only %s", found, mac)
	}
	if took >= scanWindow-scanSlack {
		t.Errorf("Scan() took %v of a %v window, want it to stop at the match", took, scanWindow)
	}
	if !waitNotDiscovering(t, adapter) {
		t.Error("adapter still discovering 2s after the match, want discovery stopped")
	}
}

func TestScanListsControllersWithoutMac(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")

	started := time.Now()
	found, err := adapter.Scan(context.Background(), "")
	took := time.Since(started)
	t.Logf("Scan() = %+v, %v after %v (window %v)", found, err, took.Round(time.Millisecond), scanWindow)

	if err != nil {
		t.Fatalf("Scan() error = %v, want the controllers heard", err)
	}
	if took < scanWindow-scanSlack {
		t.Errorf("Scan() without an address took %v of a %v window, want the whole window", took, scanWindow)
	}
	seen := false
	for _, c := range found {
		if strings.EqualFold(c.Address, mac) {
			seen = true
		}
		if c.Address != strings.ToUpper(c.Address) {
			t.Errorf("Address %q is not upper case", c.Address)
		}
		if !c.Connected && c.RSSI >= 0 {
			t.Errorf("%s: RSSI %d dBm and not connected, want a negative signal strength", c.Address, c.RSSI)
		}
	}
	if !seen {
		t.Errorf("Scan() = %+v, want it to include %s", found, mac)
	}
	if !waitNotDiscovering(t, adapter) {
		t.Error("adapter still discovering 2s after the window, want discovery stopped")
	}
}

// otherMac returns mac with its last octet changed, a valid address that
// is not the test controller's.
func otherMac(t *testing.T, mac string) string {
	t.Helper()
	if len(mac) != 17 {
		t.Fatalf("GO_PWR_TEST_MAC = %q, want an address like C3:00:00:58:51:40", mac)
	}
	last := mac[15:]
	if strings.EqualFold(last, "41") {
		return mac[:15] + "42"
	}
	return mac[:15] + "41"
}

func TestScanAbsentMacNotFound(t *testing.T) {
	adapter := openAdapter(t)
	absent := otherMac(t, os.Getenv("GO_PWR_TEST_MAC"))

	started := time.Now()
	found, err := adapter.Scan(context.Background(), absent)
	took := time.Since(started)
	t.Logf("Scan(%s) = %+v, %v after %v (window %v)", absent, found, err, took.Round(time.Millisecond), scanWindow)

	if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrRetryable) {
		t.Errorf("Scan() error = %v, want ErrNotFound and ErrRetryable", err)
	}
	if len(found) != 0 {
		t.Errorf("Scan() = %+v for an absent address, want nothing", found)
	}
	if took < scanWindow-scanSlack {
		t.Errorf("Scan() took %v, want the whole %v window before giving up", took, scanWindow)
	}
	if !waitNotDiscovering(t, adapter) {
		t.Error("adapter still discovering 2s after the window, want discovery stopped")
	}
}

func TestScanMacCaseInsensitive(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	lower := strings.ToLower(mac)
	if lower == mac {
		t.Fatalf("GO_PWR_TEST_MAC = %q has no upper-case letters to lower; use an address with a letter in it", mac)
	}

	found, err := adapter.Scan(context.Background(), lower)
	t.Logf("Scan(%s) = %+v, %v", lower, found, err)
	if err != nil {
		t.Fatalf("Scan() error = %v, want the controller", err)
	}
	if len(found) != 1 || found[0].Address != mac {
		t.Errorf("Scan() = %+v, want %s in upper case", found, mac)
	}
}

func TestScanCancelledStopsDiscovery(t *testing.T) {
	adapter := openAdapter(t)
	absent := otherMac(t, os.Getenv("GO_PWR_TEST_MAC"))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(2*time.Second, cancel)
	started := time.Now()
	found, err := adapter.Scan(ctx, absent)
	took := time.Since(started)
	t.Logf("Scan() = %+v, %v after %v, cancelled at 2s", found, err, took.Round(time.Millisecond))

	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrRetryable) {
		t.Errorf("Scan() error = %v, want context.Canceled and ErrRetryable", err)
	}
	if took > 2*time.Second+scanSlack {
		t.Errorf("Scan() took %v, want it back within %v of the cancel", took, scanSlack)
	}
	if !waitNotDiscovering(t, adapter) {
		t.Error("adapter still discovering 2s after the cancel, want discovery stopped")
	}
}

// TestScanAdapterBlockedMidScan blocks the adapter with rfkill two seconds into
// a scan for an absent address, which would otherwise run the whole window.
// It needs root and restores lola on cleanup.
func TestScanAdapterBlockedMidScan(t *testing.T) {
	requireRoot(t)
	restoreOnCleanup(t)
	adapter := openAdapter(t)
	absent := otherMac(t, os.Getenv("GO_PWR_TEST_MAC"))

	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		time.Sleep(2 * time.Second)
		sh(t, "rfkill block bluetooth")
	}()
	started := time.Now()
	found, err := adapter.Scan(context.Background(), absent)
	took := time.Since(started)
	<-blocked
	t.Logf("Scan() = %+v, %v after %v, blocked at 2s (window %v)", found, err, took.Round(time.Millisecond), scanWindow)

	if err == nil {
		t.Fatalf("Scan() = %+v, nil with the adapter blocked, want an error", found)
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("Scan() error = %v, want the adapter's loss reported, not ErrNotFound", err)
	}
	if !errors.Is(err, ErrRetryable) {
		t.Errorf("Scan() error = %v, want ErrRetryable", err)
	}
	if took >= scanWindow {
		t.Errorf("Scan() took %v, want an error before the %v window ends", took, scanWindow)
	}
}

// TestScanConnectedNotZeroRSSI holds a link to the test controller with
// bluetoothctl and checks that a scan lists it as connected. BlueZ reports no
// RSSI for a connected device, so the library would show a false 0 dBm. It
// connects to the controller, which the controller allows once at a time:
// close the Go Power! Connect app and stop any go-pwr that owns it first.
func TestScanConnectedNotZeroRSSI(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")

	// bluetoothctl can only connect to a device BlueZ has an object for, so
	// find the controller first, as go-pwr itself does before connecting.
	if _, err := adapter.Scan(context.Background(), mac); err != nil {
		t.Fatalf("Scan(%s) before connecting: %v", mac, err)
	}
	t.Cleanup(func() { sh(t, "bluetoothctl disconnect "+mac) })
	if out := sh(t, "bluetoothctl --timeout 30 connect "+mac); !strings.Contains(out, "Connection successful") {
		t.Fatalf("bluetoothctl connect %s: %s", mac, out)
	}

	found, err := adapter.Scan(context.Background(), "")
	t.Logf("Scan() with a link held = %+v, %v", found, err)
	if err != nil {
		t.Fatalf("Scan() error = %v, want the connected controller", err)
	}
	for _, c := range found {
		if !strings.EqualFold(c.Address, mac) {
			continue
		}
		if !c.Connected {
			t.Errorf("%s listed as not connected (RSSI %d dBm) while a link is held", c.Address, c.RSSI)
		}
		if c.RSSI != 0 {
			t.Errorf("%s connected but carries RSSI %d dBm, want it cleared", c.Address, c.RSSI)
		}
		return
	}
	t.Errorf("Scan() = %+v, want it to include the connected %s", found, mac)
}
