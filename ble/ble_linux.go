// ABOUTME: Bluetooth layer on Linux, where BlueZ is reached over the system
// ABOUTME: D-Bus; opens the adapter and checks that it is usable.

package ble

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

// adapterName is the adapter the bluetooth library hard-codes.
const adapterName = "hci0"

const poweredProperty = "org.bluez.Adapter1.Powered"

// Adapter is the local Bluetooth adapter, opened and known to be powered.
type Adapter struct {
	library *bluetooth.Adapter
	conn    *dbus.Conn
	object  dbus.BusObject
}

// Open enables the adapter and checks that it is powered. It never powers on
// or unblocks the adapter. Its errors match ErrFatalAtStartup and one of the
// error classes in the package documentation.
func Open() (*Adapter, error) {
	return OpenContext(context.Background())
}

// OpenContext is Open, except that ctx ends the wait for BlueZ, whose
// property read takes about 15 s to fail when BlueZ is masked. A loop of
// readings opens the adapter again after a failed one, since an Adapter
// can't recover from the system bus or BlueZ restarting, and bounds each
// reading, this included, with ctx. A dbus-daemon restart is covered because
// the system bus is connected again when its connection is closed.
func OpenContext(ctx context.Context) (*Adapter, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, startupError(fmt.Errorf("connect to the system bus: %w", err))
	}
	object := conn.Object("org.bluez", dbus.ObjectPath("/org/bluez/"+adapterName))
	if err := checkPowered(ctx, object); err != nil {
		return nil, startupError(err)
	}
	library := bluetooth.NewAdapter(adapterName)
	if err := library.Enable(); err != nil {
		// The library reads the adapter itself and may drop the typed D-Bus
		// error, so look again with our own read to classify what went wrong.
		if again := checkPowered(ctx, object); again != nil {
			return nil, startupError(again)
		}
		return nil, startupError(describe("enable adapter "+adapterName, err))
	}
	return &Adapter{library: library, conn: conn, object: object}, nil
}

// Check reports whether the adapter is still there and powered. Its errors
// match ErrRetryable: after Open has succeeded, losing the adapter or BlueZ
// is an ordinary failed reading.
func (a *Adapter) Check() error {
	return a.checkContext(context.Background())
}

// checkContext is Check, except that ctx ends the wait for BlueZ, whose
// property read takes about 15 s to fail when BlueZ is masked.
func (a *Adapter) checkContext(ctx context.Context) error {
	if err := checkPowered(ctx, a.object); err != nil {
		return retryableError(err)
	}
	return nil
}

// checkPowered returns an error classified by its class, without saying when
// it happened, unless the adapter is powered. Errors it cannot classify come
// back unchanged.
func checkPowered(ctx context.Context, object dbus.BusObject) error {
	var powered dbus.Variant
	err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.bluez.Adapter1", "Powered").Store(&powered)
	if err != nil {
		return describe("read adapter "+adapterName+" power state", err)
	}
	if on, _ := powered.Value().(bool); !on {
		return classify(nil, ErrAdapterUnavailable, "adapter "+adapterName+" is powered off or blocked", nil)
	}
	return nil
}

// describe classifies a D-Bus error by its name.
func describe(msg string, err error) error {
	name := dbusErrorName(err)
	switch {
	case name == "org.freedesktop.DBus.Error.UnknownObject":
		return classify(nil, ErrNoAdapter, msg, err)
	case name == "org.freedesktop.DBus.Error.AccessDenied":
		return classify(nil, ErrAccessDenied, msg, err)
	case name == "org.freedesktop.DBus.Error.ServiceUnknown",
		name == "org.freedesktop.DBus.Error.NameHasNoOwner",
		name == "org.freedesktop.DBus.Error.FileExists",
		strings.HasPrefix(name, "org.freedesktop.DBus.Error.Spawn."):
		return classify(nil, ErrBlueZNotRunning, msg, err)
	}
	return unclassified(msg, err)
}

// dbusErrorName returns the D-Bus error name in err's chain, or "".
func dbusErrorName(err error) string {
	var value dbus.Error
	if errors.As(err, &value) {
		return value.Name
	}
	var pointer *dbus.Error
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.Name
	}
	return ""
}

// startupError marks a classified error as fatal at startup.
func startupError(err error) error {
	return markWhen(ErrFatalAtStartup, err)
}

// retryableError marks any error after Open as retryable.
func retryableError(err error) error {
	return markWhen(ErrRetryable, err)
}

// markWhen adds when to a classified error, and wraps one that is not.
func markWhen(when, err error) error {
	var classified *classifiedError
	if errors.As(err, &classified) {
		copied := *classified
		copied.when = when
		return &copied
	}
	return classify(when, nil, "", err)
}

// Scan looks for controllers for the length of the scan window.
//
// With an address in mac, it stops as soon as that address is seen and returns
// that controller alone; the comparison ignores case. If the window ends
// without it, Scan returns ErrNotFound. The caller connects only after a scan,
// because the library can connect only to a device BlueZ already has an object
// for.
//
// With an empty mac, Scan always runs the whole window, never stopping at the
// first match, and returns every controller it heard, or ErrNotFound if it
// heard none.
//
// Cancelling ctx stops the scan and discovery and returns the context's
// error. Every error matches ErrRetryable.
func (a *Adapter) Scan(ctx context.Context, mac string) ([]Controller, error) {
	window, cancel := context.WithTimeout(ctx, scanWindow)
	defer cancel()

	var found []Controller
	matched := false
	guard := newScanGuard(a.library.StopScan)
	callback := func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		address := r.Address.String()
		if mac != "" && !strings.EqualFold(address, mac) {
			return
		}
		if mac == "" && r.LocalName() != controllerName {
			return
		}
		found = record(found, Controller{Address: strings.ToUpper(address), RSSI: int(r.RSSI)})
		if mac != "" {
			matched = true
			guard.Stop()
		}
	}

	ended := make(chan struct{})
	var stopper sync.WaitGroup
	stopper.Add(1)
	go func() {
		defer stopper.Done()
		stopWhenDone(window.Done(), ended, guard, stopRetry)
	}()
	err := recoverScan(func() error { return a.library.Scan(callback) }, guard, a.stopDiscovery)
	close(ended)
	stopper.Wait()

	if ctx.Err() != nil {
		return nil, retryableError(fmt.Errorf("scan: %w", ctx.Err()))
	}
	if err != nil && (errors.Is(err, errScanPanicked) || (!matched && window.Err() == nil)) {
		if checkErr := a.checkContext(ctx); checkErr != nil {
			return nil, checkErr
		}
		return nil, retryableError(fmt.Errorf("scan: %w", err))
	}
	if len(found) == 0 {
		return nil, retryableError(fmt.Errorf("scan for %s: %w", describeTarget(mac), ErrNotFound))
	}
	a.markConnected(ctx, found)
	return found, nil
}

// stopRetry is how often a stop is tried again while the scan has not
// started yet.
const stopRetry = 50 * time.Millisecond

// record adds c to found, or updates the entry with its address.
func record(found []Controller, c Controller) []Controller {
	for i := range found {
		if found[i].Address == c.Address {
			found[i].RSSI = c.RSSI
			return found
		}
	}
	return append(found, c)
}

// describeTarget names what a scan looked for.
func describeTarget(mac string) string {
	if mac == "" {
		return controllerName
	}
	return mac
}

// markConnected sets Connected on each controller this machine already holds
// a link to. The library presents such a device with no signal strength, so
// its RSSI would read 0 dBm.
func (a *Adapter) markConnected(ctx context.Context, found []Controller) {
	for i := range found {
		path := devicePath(a, found[i].Address)
		on, err := connected(ctx, a.conn.Object("org.bluez", path))
		if err != nil {
			continue
		}
		if on {
			found[i].Connected = true
			found[i].RSSI = 0
		}
	}
}

// stopDiscovery asks BlueZ to stop discovering. Not discovering is fine.
// BlueZ tracks discovery per D-Bus client, so this stops the library's
// discovery only because a.object rides the same shared dbus.SystemBus()
// connection the library starts discovery on.
func (a *Adapter) stopDiscovery() error {
	err := a.object.Call("org.bluez.Adapter1.StopDiscovery", 0).Err
	if err != nil && strings.Contains(err.Error(), "No discovery started") {
		return nil
	}
	return err
}
