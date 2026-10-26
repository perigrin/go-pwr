// ABOUTME: Tests the choice of controller after a scan: none is an error, one
// ABOUTME: is read, and several are listed on stderr with nothing on stdout.

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/perigrin/go-pwr/ble"
	"github.com/perigrin/go-pwr/status"
)

// The scan results are plain values built here, the inputs of a pure
// function, not a stand-in for a device.
var (
	nearby    = ble.Controller{Address: "C3:00:00:58:51:40", RSSI: -62}
	farther   = ble.Controller{Address: "C3:00:00:58:51:41", RSSI: -88}
	linkedNow = ble.Controller{Address: "C3:00:00:58:51:42", Connected: true}
)

func TestSelectControllerFromScan(t *testing.T) {
	t.Run("none is not found", func(t *testing.T) {
		for _, found := range [][]ble.Controller{nil, {}} {
			if _, err := selectController(found); !errors.Is(err, ble.ErrNotFound) {
				t.Errorf("selectController(%v) error = %v, want ErrNotFound", found, err)
			}
		}
	})

	t.Run("one is read", func(t *testing.T) {
		for _, only := range []ble.Controller{nearby, linkedNow} {
			got, err := selectController([]ble.Controller{only})
			if err != nil || got != only {
				t.Errorf("selectController(%v) = %v, %v, want it back and no error", only, got, err)
			}
		}
	})

	t.Run("several are listed with signal or connected", func(t *testing.T) {
		_, err := selectController([]ble.Controller{nearby, linkedNow, farther})
		if err == nil {
			t.Fatal("selectController of three controllers succeeded, want an error")
		}
		line := status.ErrorLine(err)
		if strings.Count(line, "\n") != 1 || !strings.HasPrefix(line, "error: ") {
			t.Errorf("error line %q is not one line", line)
		}
		for _, want := range []string{
			"C3:00:00:58:51:40 (-62 dBm)",
			"C3:00:00:58:51:42 (connected)",
			"C3:00:00:58:51:41 (-88 dBm)",
			"--mac",
		} {
			if !strings.Contains(line, want) {
				t.Errorf("error line %q does not contain %q", line, want)
			}
		}
	})

	t.Run("no signal is never shown as 0 dBm", func(t *testing.T) {
		silent := ble.Controller{Address: "C3:00:00:58:51:43"}
		_, err := selectController([]ble.Controller{nearby, silent})
		if err == nil || strings.Contains(err.Error(), "C3:00:00:58:51:43 (0 dBm)") {
			t.Errorf("error = %v, want one that does not show 0 dBm", err)
		}
	})
}
