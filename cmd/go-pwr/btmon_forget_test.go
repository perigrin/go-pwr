// ABOUTME: Tests the guard that stops the btmon tests from removing a controller
// ABOUTME: that BlueZ has paired, bonded or trusted, whose keys removal would lose.

package main

import (
	"fmt"
	"strings"
	"testing"
)

// forgetRefusal says why the controller must not be removed from BlueZ, given
// the output of `bluetoothctl info`: removal also deletes the keys of a paired,
// bonded or trusted device. Output that does not show all three as "no" is
// refused too, since silence is not proof; a device BlueZ does not know has
// nothing to lose.
func forgetRefusal(info string) error {
	if strings.Contains(info, "not available") {
		return nil
	}
	for _, field := range []string{"Paired", "Bonded", "Trusted"} {
		if !strings.Contains(info, field+": no") {
			return fmt.Errorf("bluetoothctl info does not show %q as no, so removing the device could lose its keys:\n%s", field, info)
		}
	}
	return nil
}

func TestForgetRefusedForPairedBondedOrTrusted(t *testing.T) {
	const unpaired = "Device C3:00:00:58:51:40 (public)\n\tName: GP-PWM-30-SB\n\tPaired: no\n\tBonded: no\n\tTrusted: no\n\tConnected: no\n"
	for _, tc := range []struct {
		name string
		info string
		want bool // refused
	}{
		{"unpaired", unpaired, false},
		{"not known to BlueZ", "Device C3:00:00:58:51:40 not available\n", false},
		{"paired", "\tPaired: yes\n\tBonded: no\n\tTrusted: no\n", true},
		{"bonded", "\tPaired: no\n\tBonded: yes\n\tTrusted: no\n", true},
		{"trusted", "\tPaired: no\n\tBonded: no\n\tTrusted: yes\n", true},
		{"empty output is not proof of unpaired", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := forgetRefusal(tc.info); (err != nil) != tc.want {
				t.Errorf("forgetRefusal = %v, refused %v, want %v", err, err != nil, tc.want)
			}
		})
	}
}
