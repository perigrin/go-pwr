// ABOUTME: Boot information on Linux: boot_id from /proc and seconds since
// ABOUTME: boot from CLOCK_BOOTTIME, the clock /proc/uptime counts.

//go:build linux

package boot

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// bootIDPath is the kernel's identifier for the current boot.
const bootIDPath = "/proc/sys/kernel/random/boot_id"

// ID returns the kernel's identifier for the current boot.
func ID() (string, error) {
	b, err := os.ReadFile(bootIDPath)
	if err != nil {
		return "", fmt.Errorf("read boot id: %w", err)
	}
	return parseBootID(string(b))
}

// Uptime returns seconds since boot on CLOCK_BOOTTIME, which counts time
// suspended and is the clock behind the first number in /proc/uptime. It
// is not CLOCK_MONOTONIC, and wall-clock steps don't move it.
func Uptime() (float64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return 0, fmt.Errorf("read boottime clock: %w", err)
	}
	return float64(ts.Sec) + float64(ts.Nsec)/1e9, nil
}
