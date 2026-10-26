// ABOUTME: Tests decoding a controller reply into a Reading, using the real
// ABOUTME: capture in testdata and altered copies of it for error cases.

package status

import (
	"os"
	"strconv"
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
		{"charge current", indexChargeMA, "100000"},
		{"battery voltage", indexBatteryMV, "100000"},
		{"PV voltage", indexSolarMV, "100000"},
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

// wantGatingError checks that Decode fails reply, naming field and quoting
// the reply.
func wantGatingError(t *testing.T, reply, field string) {
	t.Helper()
	_, err := Decode(reply)
	if err == nil {
		t.Errorf("Decode(%q) succeeded, want an error naming %s", reply, field)
		return
	}
	if !strings.Contains(err.Error(), field) {
		t.Errorf("error %q does not name %s", err, field)
	}
	if !strings.Contains(err.Error(), strconv.Quote(reply)) {
		t.Errorf("error %q does not quote the reply", err)
	}
}

func TestGatingFieldNonNumericFails(t *testing.T) {
	for _, f := range gatingFields {
		for _, v := range []string{"", "+", "-", "13 44", " 1344", "0000A", "+13244", "+0", "１２３", "١٢٣"} {
			wantGatingError(t, alteredReply(t, f.index, v), f.name)
		}
	}
}

func TestGatingNegativeValueFails(t *testing.T) {
	for _, f := range gatingFields {
		for _, v := range []string{"-13244", "-0001", "-1", "-0", "-00000"} {
			wantGatingError(t, alteredReply(t, f.index, v), f.name)
		}
	}
}

func TestGatingOverflowFails(t *testing.T) {
	for _, f := range gatingFields {
		for _, v := range []string{"100000", "013244", "000000013244", "+000013244", "99999999999999999999999"} {
			wantGatingError(t, alteredReply(t, f.index, v), f.name)
		}
	}
}

// TestGatingBoundsOffByOne exercises the bounds comparison at each edge.
// The altered captures test the comparison only, not whether real readings
// are accepted.
func TestGatingBoundsOffByOne(t *testing.T) {
	edges := []struct {
		name     string
		index    int
		min, max int
	}{
		{"charge current", indexChargeMA, minChargeMA, maxChargeMA},
		{"battery voltage", indexBatteryMV, minBatteryMV, maxBatteryMV},
		{"PV voltage", indexSolarMV, minSolarMV, maxSolarMV},
	}
	for _, e := range edges {
		for _, v := range []int{e.min, e.max} {
			if _, err := Decode(alteredReply(t, e.index, strconv.Itoa(v))); err != nil {
				t.Errorf("%s at its limit %d: %v", e.name, v, err)
			}
		}
		for _, v := range []int{e.min - 1, e.max + 1} {
			wantGatingError(t, alteredReply(t, e.index, strconv.Itoa(v)), e.name)
		}
	}
}

// TestGatingZeroBatteryVoltage follows docs/reference/protocol.md,
// Plausibility bounds: "A battery field of 00000 is accepted."
func TestGatingZeroBatteryVoltage(t *testing.T) {
	r, err := Decode(alteredReply(t, indexBatteryMV, "00000"))
	if err != nil {
		t.Fatalf("Decode with battery 00000: %v, want it accepted", err)
	}
	if r.BatteryMV != 0 {
		t.Errorf("BatteryMV = %d, want 0", r.BatteryMV)
	}
}

func TestDecodeShortReplyNoPanic(t *testing.T) {
	fields := strings.Split(capturedReply(t), ";")
	for _, reply := range []string{
		"",
		strings.Join(fields[:11], ";"),
		strings.Join(fields[:3], ";"),
		fields[0],
	} {
		if _, err := Decode(reply); err == nil {
			t.Errorf("Decode(%q) succeeded, want an error", reply)
		}
	}
}
