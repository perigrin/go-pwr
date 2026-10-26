// ABOUTME: Bluetooth layer on Linux, where BlueZ is reached over the system
// ABOUTME: D-Bus; opens the adapter and checks that it is usable.

package ble

import (
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

// adapterName is the adapter the bluetooth library hard-codes.
const adapterName = "hci0"

// Adapter is the local Bluetooth adapter, opened and known to be powered.
type Adapter struct {
	library *bluetooth.Adapter
}

// Open enables the adapter and checks that it is powered.
func Open() (*Adapter, error) {
	library := bluetooth.NewAdapter(adapterName)
	if err := library.Enable(); err != nil {
		return nil, fmt.Errorf("enable adapter %s: %w", adapterName, err)
	}
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the system bus: %w", err)
	}
	powered, err := conn.Object("org.bluez", dbus.ObjectPath("/org/bluez/"+adapterName)).GetProperty("org.bluez.Adapter1.Powered")
	if err != nil {
		return nil, fmt.Errorf("read adapter %s power state: %w", adapterName, err)
	}
	if on, _ := powered.Value().(bool); !on {
		return nil, errors.New("adapter " + adapterName + " is not powered")
	}
	return &Adapter{library: library}, nil
}
