// ABOUTME: Bluetooth layer on Linux, where BlueZ is reached over the system
// ABOUTME: D-Bus; opens the adapter and checks that it is usable.

package ble

import (
	"errors"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

// adapterName is the adapter the bluetooth library hard-codes.
const adapterName = "hci0"

const poweredProperty = "org.bluez.Adapter1.Powered"

// Adapter is the local Bluetooth adapter, opened and known to be powered.
type Adapter struct {
	library *bluetooth.Adapter
	object  dbus.BusObject
}

// Open enables the adapter and checks that it is powered. It never powers on
// or unblocks the adapter. Its errors match ErrFatalAtStartup and one of the
// error classes in the package documentation.
func Open() (*Adapter, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, classify(ErrFatalAtStartup, ErrBlueZNotRunning, "connect to the system bus", err)
	}
	object := conn.Object("org.bluez", dbus.ObjectPath("/org/bluez/"+adapterName))
	if err := checkPowered(object); err != nil {
		return nil, startupError(err)
	}
	library := bluetooth.NewAdapter(adapterName)
	if err := library.Enable(); err != nil {
		return nil, startupError(fmt.Errorf("enable adapter %s: %w", adapterName, err))
	}
	return &Adapter{library: library, object: object}, nil
}

// Check reports whether the adapter is still there and powered. Its errors
// match ErrRetryable: after Open has succeeded, losing the adapter or BlueZ
// is an ordinary failed reading.
func (a *Adapter) Check() error {
	if err := checkPowered(a.object); err != nil {
		return retryableError(err)
	}
	return nil
}

// checkPowered returns an error classified by its class, without saying when
// it happened, unless the adapter is powered. Errors it cannot classify come
// back unchanged.
func checkPowered(object dbus.BusObject) error {
	powered, err := object.GetProperty(poweredProperty)
	if err != nil {
		return describe(err)
	}
	if on, _ := powered.Value().(bool); !on {
		return classify(nil, ErrAdapterUnavailable, "adapter "+adapterName+" is powered off or blocked", nil)
	}
	return nil
}

// describe classifies a D-Bus error by its name.
func describe(err error) error {
	name := dbusErrorName(err)
	msg := "read adapter " + adapterName + " power state"
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
	return fmt.Errorf("%s: %w", msg, err)
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
