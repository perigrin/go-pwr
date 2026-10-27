// ABOUTME: Platform-neutral API of the Bluetooth layer that talks to a
// ABOUTME: Go Power! GP-PWM-30-SB charge controller through BlueZ.

// Package ble reads status from a Go Power! GP-PWM-30-SB charge controller
// over Bluetooth LE.
//
// The controller's FFF2 characteristic feeds a serial command parser, and
// the Go Power! app sends reset, force-charge and settings commands through
// it one character per write. So the only value this package ever writes to
// FFF2 is the single byte 0x20, the status request, and its only other
// writes are the FFF1 notification descriptor (CCCD) on and off. The API
// exposes a status-request operation, never a function that accepts bytes
// to write.
//
// On an operating system other than Linux every operation returns
// ErrUnsupportedPlatform.
//
// # API
//
// A reading goes through these operations, in this order:
//
//	adapter, err := ble.Open()
//	found, err := adapter.Scan(ctx, mac)   // []Controller; mac may be empty
//	conn, err := adapter.Connect(ctx, mac) // *Conn
//	reply, err := conn.RequestStatus(ctx)  // the whole reply, as a string
//	err = conn.Close()
//
// RequestStatus is the only operation that writes to the controller, and
// it takes no bytes. There is no function that does. The request is the
// constant statusRequest, one byte, 0x20, passed as []byte{statusRequest} at
// the one GATT write call site, and TestSingleStatusWriteCallSite and
// TestNoExportedByteWriter guard both.
//
// Before every write, RequestStatus checks that its target is the
// characteristic with UUID 0000fff2-0000-1000-8000-00805f9b34fb under the
// service 0000fff0-0000-1000-8000-00805f9b34fb of the connected device. Its
// writes are 0x20 to FFF2 and the FFF1 notification descriptor on and off,
// which BlueZ writes for StartNotify and StopNotify. The first request turns
// notifications on, and Close turns them off.
//
// The request and the notifications share a D-Bus connection of their own
// with a sequential signal handler. The write reply's sequence number then
// separates the notifications of this reply from late ones of an earlier
// reply, and the reply is assembled in that order (see the status package).
// A request that a context abandoned leaves its reply still arriving, so the
// next request first waits for it to finish or to time out.
//
// No settle delay follows Connect; see "Settle delay" in
// docs/reference/protocol.md.
//
// # Errors
//
// Errors from Open and Adapter.Check say when they happened and, when they
// can, which class they belong to. Tell them apart with errors.Is, never by
// message text. The class, when there is one, is one of ErrNoAdapter,
// ErrAccessDenied, ErrBlueZNotRunning and ErrAdapterUnavailable (powered
// off or blocked). The classes Open returns
// also match ErrFatalAtStartup: the program exits 1 before its first
// reading. The same conditions after Open has succeeded match ErrRetryable
// instead: they are an ordinary failed reading, retried on the next poll.
// An error that fits no class, such as an unreachable system bus, matches
// only ErrFatalAtStartup or ErrRetryable. The underlying D-Bus error stays
// reachable with errors.As.
//
// On Linux, Open classifies by D-Bus error name, as observed on lola (see
// docs/reference/protocol.md): org.freedesktop.DBus.Error.UnknownObject is
// ErrNoAdapter; org.freedesktop.DBus.Error.AccessDenied is ErrAccessDenied;
// org.freedesktop.DBus.Error.FileExists, which D-Bus returns when
// bluetooth.service is masked and stopped, is ErrBlueZNotRunning (as are
// ServiceUnknown, NameHasNoOwner and the Spawn errors, which were not
// seen); and an adapter that is on the bus with Powered false is
// ErrAdapterUnavailable. Open never powers on or unblocks the adapter.
//
// # Connecting
//
// Connect makes its link with a D-Bus call that ctx can cancel, not the
// library's Connect, which waits for the link with no way to stop it. When
// ctx ends, Connect tells BlueZ to disconnect, which also cancels a connect
// still in progress, waits for the link to be down and returns the context's
// error. Close does the same, waiting up to 5 s for BlueZ to report the link
// down.
//
// A reading never sends its request on a link it did not make. A link that
// is up before a reading connects was left by a run that was killed, which
// BlueZ keeps up, or made by another client such as bluetoothctl, and the
// controller may still have an old reply to send on it, which the next
// reading could take for its own. So ConnectWithRetry, which every reading
// connects through, first asks BlueZ to disconnect the controller if its link
// is up, waits until the Connected property is false (within ctx, so inside
// the reading's --timeout) and then connects afresh. If the link will not go
// down, or comes back, because another client such as the phone app holds it,
// the reading fails with an error that matches ErrRetryable. Connect alone
// reuses a link that is up and is left to callers that want that. A Follower
// keeps the link it made itself across readings, and drops only a link that
// was up before its first connect, or one it did not make after a failed
// reading.
//
// Service discovery is not watched. The library's DiscoverServices polls
// ServicesResolved every 10 ms for at most 10 s and can't be cancelled, but
// BlueZ resolves the services during the connect, so on lola the poll ends
// within about a second (0.3 to 1 s over 40 runs). The breakdown of scan,
// connect and discovery times is in docs/reference/protocol.md, "Time budget
// on lola".
//
// # Opening again
//
// An Adapter keeps the D-Bus object it was opened with, so it can't recover
// from BlueZ or the system bus restarting. A loop of readings, such as
// --poll, opens a new one with ReopenContext after each failed reading, which
// reconnects a closed system bus. Its errors match ErrRetryable, where
// OpenContext's match ErrFatalAtStartup.
//
// The reading's context bounds the calls this package makes itself: the
// power-state read in OpenContext and ReopenContext, the same read after a
// failed scan, the Connected reads, the connect and the request. It does not
// bound the calls inside the bluetooth library: Enable's property read, and
// the property read, discovery filter and object listing that a scan makes.
// Those return when BlueZ answers or fails, so a BlueZ that never answers
// could hold one. The poll tests on lola, with BlueZ masked and stopped or its
// adapter gone, saw none outlast a reading's --timeout.
//
// The bluetooth library hard-codes the adapter name hci0. If an adapter
// reset brought the adapter back as hci1, every retry would fail until the
// program restarts. That is unlikely, because Linux reuses the lowest free
// index.
//
// # Holding a connection
//
// A Follower, from Adapter.Follow, holds one connection across readings, for
// --follow, where Connect and Close serve one reading. Its Read connects when
// it has no link, and otherwise reads on the held one. It connects with
// ConnectWithRetry, as a reading of --poll does, since the local controller
// aborts some connects, most often right after a drop.
//
// A Follower prevents a splice by reconnecting after any failed or cut-off
// reading: a tail of an earlier reply can exist only on a held connection, so
// a reading that fails, or that its context cuts off, closes the connection
// and the next Read makes a new one, with a D-Bus connection, status link and
// notification subscription of its own. Notifications of the old reply go to
// the old D-Bus connection, and the new one bounds its read at the write's
// reply sequence number, so a late notification cannot be taken for the new
// reply. That holds even if the old link were still up, which is why the
// guarantee rests on the new D-Bus connection and not on the link being new.
// A reconnect drops such a link first (see "Connecting"), so it is a new link
// unless the drop fails, and then the reading fails. A Follower does not
// drain the old reply up to the D-Bus sequence boundary instead, because
// reconnecting is simpler and stronger, and costs one scan and connect per
// failed reading.
//
// A reply left behind by a run that was killed outright is another way for a
// tail to arrive after a request's write. A read throws away the bytes
// that arrive before its reply has started, up to the space that starts
// every reply (see status.Assembler), and says how many bytes that was only
// if the read then fails. That discard cannot tell a whole stale reply from
// the current one; what keeps one out is that the reading's link is its own
// (see "Connecting").
//
// Close ends a Follower: a Read after it returns ErrNotConnected and makes no
// connection. Close can't interrupt a Read in flight; it waits for it, so
// cancel the Read's context first to end it sooner.
//
// BlueZ announces a drop from the other end only through the Connected
// property of the device. Read reads it before it uses a held connection, and
// watches it while the reply arrives, so a drop ends the reading at once with
// an error that matches ErrLinkDropped, not an incomplete reply after the
// whole timeout. The dead connection is closed, which ends its subscription
// with its D-Bus connection, and the next Read subscribes afresh, so no
// subscription is ever made twice on a characteristic and the notifications
// per reply do not grow.
package ble

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// statusRequest is the one value this package ever writes to the controller's
// FFF2 characteristic: a single space, which asks for a status reply. It is a
// constant, so no code can change it, and it is passed as []byte{statusRequest}
// at the one GATT write call site. TestSingleStatusWriteCallSite holds that,
// and that the name is declared and used nowhere else.
const statusRequest byte = 0x20

// controllerName is the Bluetooth name the GP-PWM-30-SB advertises.
const controllerName = "GP-PWM-30-SB"

// scanWindow is how long a scan runs when it does not stop at a match. The
// controller advertises about once a second, and on lola the slowest of 20
// scans took 7.1 s to hear it, so 8 s is 0.9 s over the slowest seen. That
// does not bound the tail: a present controller can still be missed, and a
// miss is a retryable failed reading (protocol.md, "What 8 s does not
// cover"). With --mac the scan stops at the match (4.4 s at worst), so the
// scan, the connect (9.8 s at worst) and the reply (3 s) fit the 20 s default
// --timeout. Without --mac the whole window runs, and a one-shot can exceed
// 20 s and fail as a retryable reading. See "Scan window" and "Time budget on
// lola" in docs/reference/protocol.md.
const scanWindow = 8 * time.Second

// closeBound is the longest Close waits for BlueZ to report the link down.
// BlueZ took 2.3 to 4.6 s to drop the link in 40 runs on lola, so 10 s is
// twice the slowest. Close runs after the reply, so the wait does not count
// against the reading's --timeout. See "Time budget on lola" in
// docs/reference/protocol.md.
const closeBound = 10 * time.Second

// Error classes. Each is returned wrapped, so test with errors.Is.
var (
	// ErrNoAdapter means BlueZ has no adapter, or the adapter has vanished.
	ErrNoAdapter = errors.New("no bluetooth adapter")
	// ErrAccessDenied means the system bus policy denies this process
	// access to BlueZ.
	ErrAccessDenied = errors.New("access to BlueZ denied")
	// ErrBlueZNotRunning means nothing is serving BlueZ on the system bus.
	ErrBlueZNotRunning = errors.New("BlueZ not running")
	// ErrAdapterUnavailable means the adapter is powered off or blocked.
	// This package never powers on or unblocks it.
	ErrAdapterUnavailable = errors.New("bluetooth adapter powered off or blocked")
)

// When an error happened, which decides what the program does about it.
var (
	// ErrFatalAtStartup marks an error from Open: exit 1 before the first
	// reading.
	ErrFatalAtStartup = errors.New("fatal at startup")
	// ErrRetryable marks an error after Open has succeeded: the reading
	// fails and is retried.
	ErrRetryable = errors.New("retryable")
)

// classifiedError is an error that matches its class and when it happened,
// besides its underlying cause. Either of the first two may be nil.
type classifiedError struct {
	when, class error
	msg         string
	cause       error
}

// classify wraps cause with its class and when it happened.
func classify(when, class error, msg string, cause error) error {
	return &classifiedError{when: when, class: class, msg: msg, cause: cause}
}

func (e *classifiedError) Error() string {
	switch {
	case e.cause == nil:
		return e.msg
	case e.msg == "":
		return trimmed(e.cause).Error()
	}
	return fmt.Sprintf("%s: %v", e.msg, trimmed(e.cause))
}

// unclassified returns msg with cause, for an error that fits no class.
func unclassified(msg string, cause error) error {
	return fmt.Errorf("%s: %w", msg, trimmed(cause))
}

// trimmedError is an error whose text has no trailing newline. D-Bus error
// messages end in one, which would show as an escaped \n in the middle of
// the one-line error go-pwr prints. The error underneath stays reachable.
type trimmedError struct{ err error }

func trimmed(err error) error { return trimmedError{err} }

func (e trimmedError) Error() string { return strings.TrimRight(e.err.Error(), "\r\n") }

func (e trimmedError) Unwrap() error { return e.err }

func (e *classifiedError) Unwrap() []error {
	var errs []error
	for _, err := range []error{e.when, e.class, e.cause} {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Controller is a charge controller that a scan found.
type Controller struct {
	// Address is the controller's Bluetooth address, in upper case, as BlueZ
	// prints it.
	Address string
	// Connected is true when this machine already holds a link to the
	// controller. BlueZ reports no signal strength for a connected device, so
	// RSSI means nothing then.
	Connected bool
	// RSSI is the signal strength in dBm of the last advertisement seen. It
	// is meaningful only when Connected is false.
	RSSI int
}

// ErrNotFound is returned, wrapped, by a scan that heard no controller. With
// an address it means that address did not show up in the whole window. Connect
// returns it for an address BlueZ has no device for, because no scan has found
// it.
var ErrNotFound = errors.New("controller not found")

// ErrCleanupFailed is returned, joined to the error that ended an abandoned
// connect, when BlueZ did not drop the link as asked within closeBound: the
// link may still be up. It is never returned alone.
var ErrCleanupFailed = errors.New("link may still be up")

// ErrNotConnected is returned, wrapped, by RequestStatus on a Conn that was
// never connected, or whose Close has been called. Nothing was written.
var ErrNotConnected = errors.New("not connected")

// ErrLinkDropped is returned, wrapped, by a Follower's Read when the link to
// the controller went down while it was held, so the reading did not fail for
// want of a reply. The next Read connects again.
var ErrLinkDropped = errors.New("link to the controller dropped")

// ErrUnsupportedPlatform is returned by every operation on an operating
// system where the Bluetooth layer has no implementation.
var ErrUnsupportedPlatform = errors.New("unsupported platform")
