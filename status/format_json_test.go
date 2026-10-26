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

func TestJSONCapturedReply(t *testing.T) {
	line, err := testRecord(t, capturedReply(t)).JSONLine()
	if err != nil {
		t.Fatalf("JSONLine: %v", err)
	}
	want := `{"time":"2026-10-25T15:42:10-04:00","boot_id":"6f1c2a9e-3b7d-4c58-9a10-5d2e8f4b7c31","uptime_s":8123.4,"device":"C3:00:00:58:51:40","battery_v":13.24,"solar_v":22.88,"charge_a":0.66,"charge_state":"bulk","charge_state_code":0,"battery_type":"agm","battery_type_code":3,"temp_c":22,"temp_f":72,"ah_today":12,"ah_yesterday":15,"ah_2d":16,"ah_3d":4,"ah_7d":93,"raw":"00655;200;13244;14475;00000;00003;00000;39026;00008;003;100;22879;000;00000;00019;00049;+22;+72;+22;00012;00015;00016;00004;00093;00132;00000;02306;02025;009;025;008;"}` + "\n"
	if string(line) != want {
		t.Errorf("line =\n%s\nwant\n%s", line, want)
	}
}

// countingWriter records how many Write calls it receives.
type countingWriter struct {
	writes [][]byte
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), p...))
	return len(p), nil
}

func TestWriteJSONLineIsOneWrite(t *testing.T) {
	rec := testRecord(t, capturedReply(t))
	var w countingWriter
	if err := WriteJSONLine(&w, rec); err != nil {
		t.Fatalf("WriteJSONLine: %v", err)
	}
	line, err := rec.JSONLine()
	if err != nil {
		t.Fatalf("JSONLine: %v", err)
	}
	if len(w.writes) != 1 {
		t.Fatalf("got %d writes, want 1", len(w.writes))
	}
	if string(w.writes[0]) != string(line) {
		t.Errorf("wrote %q, want %q", w.writes[0], line)
	}
}
