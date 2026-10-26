// ABOUTME: Tests the status request against the real controller through BlueZ
// ABOUTME: on lola, with GO_PWR_TEST_MAC set; the only write is the 0x20 request.

package ble

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/status"
)

// wholeFramedReply is the length of a reply as the controller sends it: a
// leading space, the 166-byte body and CRLF.
const wholeFramedReply = 169

// connectController scans for the controller and connects to it, and closes
// the link when the test ends, even if the test fails. It leaves the
// controller disconnected, with notifications off, however the test ends.
func connectController(t *testing.T) *Conn {
	t.Helper()
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := adapter.Connect(ctx, mac)
	if err != nil {
		t.Fatalf("Connect(%s) error = %v", mac, err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return conn
}

// requireWholeReading checks that reply is a whole framed reply that decodes,
// passes the gating bounds and carries no warning.
func requireWholeReading(t *testing.T, reply string) status.Reading {
	t.Helper()
	if len(reply) != wholeFramedReply || reply[0] != ' ' || !strings.HasSuffix(reply, "\r\n") {
		t.Fatalf("reply = %q (%d bytes), want %d bytes framed by a space and CRLF", reply, len(reply), wholeFramedReply)
	}
	reading, err := status.Decode(reply)
	if err != nil {
		t.Fatalf("Decode(%q) error = %v", reply, err)
	}
	if reading.Raw != reply {
		t.Errorf("Raw = %q, want the whole reply %q", reading.Raw, reply)
	}
	for _, w := range reading.Warnings {
		t.Errorf("reading carries a warning: %v", w)
	}
	for _, e := range reading.FieldErrors {
		t.Errorf("reading carries a field error: %v", e)
	}
	return reading
}

func TestRequestStatusReturnsWholeReply(t *testing.T) {
	conn := connectController(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	reply, err := conn.RequestStatus(ctx)
	t.Logf("RequestStatus() = %q, %v after %v", reply, err, time.Since(started).Round(time.Millisecond))
	if err != nil {
		t.Fatalf("RequestStatus() error = %v", err)
	}
	reading := requireWholeReading(t, reply)
	t.Logf("battery %d mV, solar %d mV, charge %d mA", reading.BatteryMV, reading.SolarMV, reading.ChargeMA)
}
