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
package ble

import "errors"

// ErrUnsupportedPlatform is returned by every operation on an operating
// system where the Bluetooth layer has no implementation.
var ErrUnsupportedPlatform = errors.New("unsupported platform")
