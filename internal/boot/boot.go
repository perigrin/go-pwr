// ABOUTME: Reports the kernel's boot identifier and seconds since boot, so a
// ABOUTME: consumer can tell how old a reading is on a machine with no RTC.

// Package boot reports the current boot's identifier and the time since
// boot on the boottime clock. A reading's age is the current uptime (the
// first number in /proc/uptime) minus its uptime, and it only counts if its
// boot identifier matches the current boot (decision 0001, Staleness).
package boot

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrUnsupportedPlatform is returned on an operating system with no boot
// identifier or boottime clock.
var ErrUnsupportedPlatform = errors.New("unsupported platform")

// parseBootID returns the boot identifier in the contents of the kernel's
// boot_id file: a UUID followed by a newline.
func parseBootID(s string) (string, error) {
	id := strings.TrimSuffix(s, "\n")
	if !bootIDPattern.MatchString(id) {
		return "", fmt.Errorf("boot id %q is not a UUID", s)
	}
	return id, nil
}

// bootIDPattern matches the lowercase UUID the kernel writes to boot_id.
var bootIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
