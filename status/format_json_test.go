// ABOUTME: Tests formatting a Record as one JSON line, using the real capture
// ABOUTME: in testdata and altered copies of it for the null and escaping cases.

package status

import (
	"encoding/json"
	"testing"
	"time"
)

// testRecord returns a Record for reply with fixed metadata.
func testRecord(t *testing.T, reply string) Record {
	t.Helper()
	r, err := Decode(reply)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return Record{
		Time:    time.Date(2026, 10, 25, 15, 42, 10, 0, time.FixedZone("EDT", -4*60*60)),
		BootID:  "6f1c2a9e-3b7d-4c58-9a10-5d2e8f4b7c31",
		UptimeS: 8123.4,
		Device:  "C3:00:00:58:51:40",
		Reading: r,
	}
}

// jsonValues formats rec and returns each key's encoded value, as text.
func jsonValues(t *testing.T, rec Record) map[string]string {
	t.Helper()
	line, err := rec.JSONLine()
	if err != nil {
		t.Fatalf("JSONLine: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("line %q is not valid JSON: %v", line, err)
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = string(v)
	}
	return out
}

// withVolts returns the captured reading's record with the three gating
// values replaced.
func withGating(t *testing.T, chargeMA, batteryMV, solarMV int) Record {
	t.Helper()
	rec := testRecord(t, capturedReply(t))
	rec.Reading.ChargeMA = chargeMA
	rec.Reading.BatteryMV = batteryMV
	rec.Reading.SolarMV = solarMV
	return rec
}

func TestRoundHalfUp(t *testing.T) {
	cases := []struct {
		mv   int
		want string
	}{
		{13244, "13.24"},
		{22879, "22.88"},
		{655, "0.66"},
		{5, "0.01"},
		{4, "0.00"},
		{1005, "1.01"},
		{99999, "100.00"},
	}
	for _, c := range cases {
		got := jsonValues(t, withGating(t, c.mv, c.mv, c.mv))
		for _, key := range []string{"battery_v", "solar_v", "charge_a"} {
			if got[key] != c.want {
				t.Errorf("%d: %s = %s, want %s", c.mv, key, got[key], c.want)
			}
		}
	}
}
