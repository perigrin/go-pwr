// ABOUTME: Tests the Bluetooth layer's BlueZ adapter handling on Linux, on
// ABOUTME: lola with GO_PWR_TEST_MAC set; tests that change state need root.

package ble

import (
	"os"
	"testing"
)

// requireHardware skips the test unless it runs on a machine configured for
// hardware tests. Nothing in these tests connects to the controller.
func requireHardware(t *testing.T) {
	t.Helper()
	if os.Getenv("GO_PWR_TEST_MAC") == "" {
		t.Skip("GO_PWR_TEST_MAC not set")
	}
}

func TestOpenAdapter(t *testing.T) {
	requireHardware(t)
	adapter, err := Open()
	if err != nil {
		t.Fatalf("Open() error = %v, want nil on a powered adapter", err)
	}
	if adapter == nil {
		t.Fatal("Open() returned a nil Adapter and no error")
	}
}
