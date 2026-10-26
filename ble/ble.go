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
// # Errors
//
// Errors from Open say which class they belong to and when they happened.
// Tell them apart with errors.Is, never by message text. The class is one
// of ErrNoAdapter, ErrAccessDenied, ErrBlueZNotRunning and
// ErrAdapterUnavailable (powered off or blocked). The classes Open returns
// also match ErrFatalAtStartup: the program exits 1 before its first
// reading. The same conditions after Open has succeeded match ErrRetryable
// instead: they are an ordinary failed reading, retried on the next poll.
// The underlying D-Bus error stays reachable with errors.As.
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
// The bluetooth library hard-codes the adapter name hci0. If an adapter
// reset brought the adapter back as hci1, every retry would fail until the
// program restarts. That is unlikely, because Linux reuses the lowest free
// index.
package ble

import (
	"errors"
	"fmt"
)

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
		return e.cause.Error()
	}
	return fmt.Sprintf("%s: %v", e.msg, e.cause)
}

func (e *classifiedError) Unwrap() []error {
	var errs []error
	for _, err := range []error{e.when, e.class, e.cause} {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// ErrUnsupportedPlatform is returned by every operation on an operating
// system where the Bluetooth layer has no implementation.
var ErrUnsupportedPlatform = errors.New("unsupported platform")
