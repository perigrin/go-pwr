// ABOUTME: Connects to the controller and disconnects from it on Linux, with
// ABOUTME: a connect that a context can abandon and a bounded, graceful close.

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

const (
	deviceInterface   = "org.bluez.Device1"
	connectedProperty = "org.bluez.Device1.Connected"
	unknownObject     = "org.freedesktop.DBus.Error.UnknownObject"
)

// pollInterval is how often the Connected property is read while waiting for
// it to change.
const pollInterval = 50 * time.Millisecond

// Conn is a link to the controller, held until Close.
type Conn struct {
	adapter *Adapter
	object  dbus.BusObject
	// closing is closed by Close, to end a request that is waiting.
	closing chan struct{}

	mu     sync.Mutex
	closed bool

	// requests serialises RequestStatus, and guards link.
	requests sync.Mutex
	link     *statusLink

	// onDrop, when set, hears of each notification that was not used, in
	// place of the log. beforeWrite, when set, runs after the checks and
	// before the closing check that precedes the write; afterWrite, when set,
	// runs right after the write returns. Tests set them before the first
	// request.
	onDrop      func(error)
	beforeWrite func()
	afterWrite  func()
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
func (a *Adapter) Connect(ctx context.Context, mac string) (*Conn, error) {
	if _, err := bluetooth.ParseMAC(mac); err != nil {
		return nil, fmt.Errorf("connect: invalid address %q: %w", mac, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, retryableError(fmt.Errorf("connect %s: %w", mac, err))
	}
	object := a.conn.Object("org.bluez", devicePath(a, mac))
	if err := a.connect(ctx, object); err != nil {
		if ctx.Err() != nil {
			return nil, a.abandoned(object, mac, ctx.Err())
		}
		if checkErr := a.checkContext(ctx); checkErr != nil {
			if ctx.Err() != nil {
				return nil, a.abandoned(object, mac, ctx.Err())
			}
			return nil, checkErr
		}
		if dbusErrorName(err) == unknownObject {
			return nil, retryableError(classify(nil, ErrNotFound, "connect "+mac+": BlueZ has no such device, scan for it first", err))
		}
		return nil, retryableError(describe("connect "+mac, err))
	}
	return &Conn{adapter: a, object: object, closing: make(chan struct{})}, nil
}

// connect connects unless the link is already up.
func (a *Adapter) connect(ctx context.Context, object dbus.BusObject) error {
	up, err := connected(ctx, object)
	if err != nil || up {
		return err
	}
	return object.CallWithContext(ctx, deviceInterface+".Connect", 0).Err
}

// abandoned drops a connect that ctx cut short, whichever way BlueZ was
// getting on with it, and returns the error for it: the context's error,
// joined with the cleanup's own error if BlueZ would not disconnect. The
// cleanup runs on a context of its own, since ctx is done.
func (a *Adapter) abandoned(object dbus.BusObject, mac string, ctxErr error) error {
	err := fmt.Errorf("connect %s: %w", mac, ctxErr)
	if cleanup := disconnect(object); cleanup != nil {
		err = errors.Join(err, fmt.Errorf("abandon connect: %w", cleanup))
	}
	return retryableError(err)
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

// Close ends the link: it turns notifications off if a status request
// turned them on, asks BlueZ to disconnect, then waits up to closeBound for the link to be down. A link that is already down is not an
// error, and Close a second time returns nil without doing anything.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	close(c.closing)
	// A request in flight sees closing and returns; wait for it, then turn
	// notifications off before the link goes down.
	c.requests.Lock()
	link := c.link
	c.link = nil
	c.requests.Unlock()
	var linkErr error
	if link != nil {
		if err := link.close(); err != nil {
			linkErr = fmt.Errorf("close status link: %w", err)
		}
	}
	if err := disconnect(c.object); err != nil {
		return retryableError(errors.Join(linkErr, fmt.Errorf("disconnect %s: %w", c.object.Path(), err)))
	}
	if linkErr != nil {
		return retryableError(linkErr)
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
