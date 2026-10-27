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
	// runs right after the write returns; onNotified, when set, runs on each
	// notification the status link takes, to count them. The status link
	// copies onDrop and onNotified when it is opened, which ConnectWithRetry
	// does before it returns, so tests set them through connectWithRetry's
	// configure hook, and set the others before the first request.
	onDrop      func(error)
	beforeWrite func()
	afterWrite  func()
	onNotified  func()
}

// devicePath is the object path BlueZ gives the device with the address mac.
func devicePath(a *Adapter, mac string) dbus.ObjectPath {
	return dbus.ObjectPath(string(a.object.Path()) + "/dev_" + strings.ReplaceAll(strings.ToUpper(mac), ":", "_"))
}

// Connect opens a link to the controller at mac, which the caller found with
// Scan first, because BlueZ connects only to a device it already has an
// object for. A link that is already up is reused, which Connect leaves to
// its caller to rule out: a reading goes through ConnectWithRetry, which
// drops such a link first (see dropLeftover).
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

// dropLeftover disconnects the controller if its link is up before this run
// has made one, and returns once BlueZ reports the link down. A link that is
// up then is not this run's: a killed go-pwr left it (BlueZ keeps it up for
// as long as anyone has measured), or another client such as bluetoothctl
// made it, and the controller may still hold bytes of an old reply to send on
// it. A reading must never send its request on it, so it is dropped and the
// reading connects afresh.
//
// The wait is bounded by ctx. A link that does not go down, because BlueZ
// refuses or the time runs out, is an error that matches ErrRetryable: the
// reading fails rather than read over a link it did not make. A link that
// another client makes again after the drop is not noticed: Connect finds it
// up and reuses it, a window that protocol.md records. A device BlueZ has no
// object for is not an error here; Connect reports it.
func (a *Adapter) dropLeftover(ctx context.Context, mac string) error {
	object := a.conn.Object("org.bluez", devicePath(a, mac))
	up, err := connected(ctx, object)
	if isGone(err) {
		return nil
	}
	if err != nil {
		return retryableError(describe("check for a link left up to "+mac, err))
	}
	if !up {
		return nil
	}
	if err := disconnectWithin(ctx, object); err != nil {
		return retryableError(describe("drop the link left up to "+mac, err))
	}
	return nil
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
// joined with the cleanup's own error, marked ErrCleanupFailed, if BlueZ
// would not disconnect: the link may still be up. The cleanup runs on a
// context of its own, since ctx is done.
func (a *Adapter) abandoned(object dbus.BusObject, mac string, ctxErr error) error {
	err := fmt.Errorf("connect %s: %w", mac, ctxErr)
	if cleanup := disconnect(object); cleanup != nil {
		err = errors.Join(err, fmt.Errorf("abandon connect: %w: %w", ErrCleanupFailed, cleanup))
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
	if err := disconnectWithin(ctx, object); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("link still up after %v", closeBound)
		}
		return err
	}
	return nil
}

// disconnectWithin asks BlueZ to disconnect the device and waits until ctx
// ends for it to report the link down. A device that is not connected, or
// that BlueZ no longer knows, counts as down. When ctx ends first, the error
// wraps ctx's.
func disconnectWithin(ctx context.Context, object dbus.BusObject) error {
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
			return fmt.Errorf("link still up when the time ran out: %w", ctx.Err())
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

// isNotConnected reports whether err says BlueZ found the device not
// connected.
func isNotConnected(err error) bool {
	return dbusErrorName(err) == "org.bluez.Error.NotConnected"
}

// linkLost turns an error from opening the status link into the error of a
// lost link, closed with closeLink, or returns nil if the link is not lost. A
// link is lost when up says it is down, whichever step of opening gave the
// error, and not only when the error is the one that BlueZ gives for it. That
// error (isNotConnected) counts too, since the Connected property can lag a
// link that is going down: on lola, a run killed while it was closing left a
// link that the next run found up and that failed with NotConnected when its
// status link was opened. An error with the link still up, or one with no
// answer on the link, is not a loss: it is left for the request, which opens
// the link again and reports it. A lost link is closed first, so the next
// attempt meets a link that is down and makes a new one; the error is
// retryable, and joined with the close's own error if that failed.
func linkLost(mac string, openErr error, up func() (bool, error), closeLink func() error) error {
	if openErr == nil {
		return nil
	}
	if !isNotConnected(openErr) {
		if isUp, err := up(); err != nil || isUp {
			return nil
		}
	}
	err := fmt.Errorf("connect %s: the link was lost while opening it: %w", mac, openErr)
	if closeErr := closeLink(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close the lost link: %w", closeErr))
	}
	return retryableError(err)
}

// ready opens the status link of a Conn that Connect returned, so a link
// that an earlier run left behind and that is going down is found here, where
// ConnectWithRetry can connect again, and not in the request. Only the loss
// of the link is reported from here, whichever step of opening found it (see
// linkLost): any other error from opening the status link is left for
// RequestStatus, which opens it again and reports it as it always has.
func (c *Conn) ready(ctx context.Context, mac string) (*Conn, error) {
	up := func() (bool, error) {
		// The check runs after ctx may be done, so it has a bound of its own.
		checkCtx, cancel := context.WithTimeout(context.Background(), linkCheckBound)
		defer cancel()
		up, err := connected(checkCtx, c.object)
		if isGone(err) {
			return false, nil
		}
		return up, err
	}
	if err := linkLost(mac, c.openStatusLink(ctx), up, c.Close); err != nil {
		return nil, err
	}
	return c, nil
}
