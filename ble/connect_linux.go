// ABOUTME: Connects to the controller and disconnects from it on Linux, with
// ABOUTME: a connect that a context can abandon and a bounded, graceful close.

package ble

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

const (
	deviceInterface   = "org.bluez.Device1"
	connectedProperty = "org.bluez.Device1.Connected"
	unknownObject     = "org.freedesktop.DBus.Error.UnknownObject"
)

// closeBound is the longest Close waits for BlueZ to report the link down.
// BlueZ takes under a second to drop a link, so this is generous, and it is
// well inside the time a reading has left after its reply.
const closeBound = 5 * time.Second

// pollInterval is how often the Connected property is read while waiting for
// it to change.
const pollInterval = 50 * time.Millisecond

// Connection is a link to the controller, held until Close.
type Connection struct {
	adapter *Adapter
	object  dbus.BusObject

	mu     sync.Mutex
	closed bool
}

// devicePath is the object path BlueZ gives the device with the address mac.
func devicePath(a *Adapter, mac string) dbus.ObjectPath {
	return dbus.ObjectPath(string(a.object.Path()) + "/dev_" + strings.ReplaceAll(strings.ToUpper(mac), ":", "_"))
}

// Connect opens a link to the controller at mac, which the caller found with
// Scan first, because BlueZ connects only to a device it already has an
// object for. A link that an earlier run left up is reused.
//
// Cancelling ctx, or its deadline passing, abandons the connect: Connect
// asks BlueZ to disconnect the device, waits for the link to be down, and
// returns the context's error. When Connect returns an error, no link is
// held and nothing is left running. The connect is made with a context-aware
// D-Bus call, not the library's Connect, which waits for the link with no way
// to stop it. Every error matches ErrRetryable.
func (a *Adapter) Connect(ctx context.Context, mac string) (*Connection, error) {
	if _, err := bluetooth.ParseMAC(mac); err != nil {
		return nil, fmt.Errorf("connect: invalid address %q: %w", mac, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, retryableError(fmt.Errorf("connect %s: %w", mac, err))
	}
	object := a.conn.Object("org.bluez", devicePath(a, mac))
	if err := a.connect(ctx, object); err != nil {
		if ctx.Err() != nil {
			a.abandon(object)
			return nil, retryableError(fmt.Errorf("connect %s: %w", mac, ctx.Err()))
		}
		if checkErr := a.Check(); checkErr != nil {
			return nil, checkErr
		}
		return nil, retryableError(describe("connect "+mac, err))
	}
	return &Connection{adapter: a, object: object}, nil
}

// connect connects unless the link is already up.
func (a *Adapter) connect(ctx context.Context, object dbus.BusObject) error {
	up, err := connected(ctx, object)
	if err != nil || up {
		return err
	}
	return object.CallWithContext(ctx, deviceInterface+".Connect", 0).Err
}

// abandon drops a connect that ctx cut short, whichever way BlueZ was
// getting on with it. It runs on a context of its own, since ctx is done.
func (a *Adapter) abandon(object dbus.BusObject) {
	_ = disconnect(object)
}

// connected reads the Connected property of a device, which is false for a
// device BlueZ has no object for any more.
func connected(ctx context.Context, object dbus.BusObject) (bool, error) {
	var value dbus.Variant
	err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, deviceInterface, "Connected").Store(&value)
	if err != nil {
		return false, err
	}
	up, _ := value.Value().(bool)
	return up, nil
}

// Close ends the link: it asks BlueZ to disconnect, then waits up to
// closeBound for the link to be down. A link that is already down is not an
// error, and Close a second time returns nil without doing anything.
func (c *Connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if err := disconnect(c.object); err != nil {
		return retryableError(fmt.Errorf("disconnect %s: %w", c.object.Path(), err))
	}
	return nil
}

// disconnect asks BlueZ to disconnect the device and waits up to closeBound
// for it to report the link down. It asks even when no link is up yet, since
// that also cancels a connect BlueZ is still making. A device that is not
// connected, or that BlueZ no longer knows, counts as down.
func disconnect(object dbus.BusObject) error {
	ctx, cancel := context.WithTimeout(context.Background(), closeBound)
	defer cancel()
	if err := object.CallWithContext(ctx, deviceInterface+".Disconnect", 0).Err; err != nil && !isGone(err) {
		return err
	}
	for {
		up, err := connected(ctx, object)
		if err != nil {
			return ignoreGone(err)
		}
		if !up {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("link still up after %v", closeBound)
		case <-time.After(pollInterval):
		}
	}
}

// isGone reports whether err says BlueZ no longer has the device, or the
// device was not connected: either way there is no link to drop.
func isGone(err error) bool {
	switch dbusErrorName(err) {
	case unknownObject, "org.bluez.Error.NotConnected":
		return true
	}
	return false
}

// ignoreGone returns nil for an error that means there is no link.
func ignoreGone(err error) error {
	if isGone(err) {
		return nil
	}
	return err
}
