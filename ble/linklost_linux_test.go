// ABOUTME: Tests how an error from turning on notifications is told to mean
// ABOUTME: that a link found up went down, so the connect is tried again.

package ble

import (
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestLinkLostClassification(t *testing.T) {
	notConnected := dbus.Error{Name: "org.bluez.Error.NotConnected", Body: []any{"Not Connected"}}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"BlueZ says not connected", notConnected, true},
		{"as wrapped by the status link", fmt.Errorf("turn on notifications of /org/bluez/hci0/dev_C3/service000c/char000d: %w", notConnected), true},
		{"another BlueZ error", dbus.Error{Name: "org.bluez.Error.Failed"}, false},
		{"not a D-Bus error", fmt.Errorf("not connected"), false},
		{"no error", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLinkLost(tc.err); got != tc.want {
				t.Errorf("isLinkLost(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
