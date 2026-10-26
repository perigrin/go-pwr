// ABOUTME: Tests decoding a controller reply into a Reading, using the real
// ABOUTME: capture in testdata and altered copies of it for error cases.

package status

import (
	"os"
	"testing"
)

// capturedReply returns the real reply captured on 2026-10-25 while the
// controller was bulk-charging an AGM battery (docs/reference/protocol.md).
func capturedReply(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/reply-2026-10-25-bulk-agm.txt")
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	return string(b)
}

func TestDecodeGatingFieldsCapturedReply(t *testing.T) {
	r, err := Decode(capturedReply(t))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if r.ChargeMA != 655 {
		t.Errorf("ChargeMA = %d, want 655", r.ChargeMA)
	}
	if r.BatteryMV != 13244 {
		t.Errorf("BatteryMV = %d, want 13244", r.BatteryMV)
	}
	if r.SolarMV != 22879 {
		t.Errorf("SolarMV = %d, want 22879", r.SolarMV)
	}
}
