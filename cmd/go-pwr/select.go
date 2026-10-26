// ABOUTME: Chooses the controller to read from a scan's results: one is read,
// ABOUTME: none is an error, and several are listed for the person to pick.

package main

import (
	"fmt"
	"strings"

	"github.com/perigrin/go-pwr/ble"
)

// selectController decides what to do with a scan's results. Exactly one
// controller is the one to read. None gives an error that matches
// ble.ErrNotFound, and several give an *ambiguousError, which lists them all.
func selectController(found []ble.Controller) (ble.Controller, error) {
	switch len(found) {
	case 0:
		return ble.Controller{}, fmt.Errorf("scan: %w", ble.ErrNotFound)
	case 1:
		return found[0], nil
	}
	return ble.Controller{}, &ambiguousError{found: found}
}

// ambiguousError is the error for a scan that heard more than one
// controller. Its message is one line that lists every address with its
// signal strength, or "connected" when BlueZ has no signal strength to give.
type ambiguousError struct {
	found []ble.Controller
}

func (e *ambiguousError) Error() string {
	listed := make([]string, len(e.found))
	for i, c := range e.found {
		listed[i] = c.Address + " (" + heard(c) + ")"
	}
	return fmt.Sprintf("%d controllers in range, choose one with --mac: %s", len(e.found), strings.Join(listed, ", "))
}

// heard describes how well a controller was heard. A connected device has
// no signal strength, and a zero RSSI means none was seen, so neither is
// shown as 0 dBm.
func heard(c ble.Controller) string {
	switch {
	case c.Connected:
		return "connected"
	case c.RSSI == 0:
		return "signal unknown"
	}
	return fmt.Sprintf("%d dBm", c.RSSI)
}
