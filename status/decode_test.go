// ABOUTME: Tests decoding a controller reply into a Reading, using the real
// ABOUTME: capture in testdata and altered copies of it for error cases.

package status

import (
	"os"
	"strings"
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

// alteredReply returns the real capture with field index replaced by value.
// Altered captures exercise error paths; they never stand in for a real
// reading being accepted.
func alteredReply(t *testing.T, index int, value string) string {
	t.Helper()
	fields := strings.Split(capturedReply(t), ";")
	fields[index] = value
	return strings.Join(fields, ";")
}

// gatingFields are the three fields whose values can fail a reading.
var gatingFields = []struct {
	name  string
	index int
}{
	{"charge current", indexChargeMA},
	{"battery voltage", indexBatteryMV},
	{"PV voltage", indexSolarMV},
}

func TestGatingBounds(t *testing.T) {
	cases := []struct {
		field string
		index int
		value string
	}{
		{"charge current", indexChargeMA, "37501"},
		{"charge current", indexChargeMA, "99999"},
		{"battery voltage", indexBatteryMV, "35001"},
		{"battery voltage", indexBatteryMV, "99999"},
		{"PV voltage", indexSolarMV, "35001"},
		{"PV voltage", indexSolarMV, "99999"},
	}
	for _, c := range cases {
		reply := alteredReply(t, c.index, c.value)
		_, err := Decode(reply)
		if err == nil {
			t.Errorf("%s %s: Decode succeeded, want a bounds error", c.field, c.value)
			continue
		}
		if !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s %s: error %q does not name the field", c.field, c.value, err)
		}
	}
}
