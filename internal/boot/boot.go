// ABOUTME: Reports the kernel's boot identifier and seconds since boot, so a
// ABOUTME: consumer can tell how old a reading is on a machine with no RTC.

// Package boot reports the current boot's identifier and the time since
// boot on the boottime clock. A reading's age is the current uptime (the
// first number in /proc/uptime) minus its uptime, and it only counts if its
// boot identifier matches the current boot (decision 0001, Staleness).
package boot

import "errors"

// ErrUnsupportedPlatform is returned on an operating system with no boot
// identifier or boottime clock.
var ErrUnsupportedPlatform = errors.New("unsupported platform")
