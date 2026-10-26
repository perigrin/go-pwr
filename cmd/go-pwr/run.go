// ABOUTME: Runs go-pwr's one-shot mode: opens the Bluetooth adapter, reads the
// ABOUTME: controller once and prints the reading on stdout.

package main

import (
	"fmt"
	"io"

	"github.com/perigrin/go-pwr/ble"
	"github.com/perigrin/go-pwr/status"
)

// run reads the controller and returns the exit status: 0 only if a reading
// was printed, 1 otherwise.
func run(cfg config, stdout, stderr io.Writer) int {
	if _, err := ble.Open(); err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	fmt.Fprintln(stderr, "go-pwr: reading is not implemented")
	return 1
}
