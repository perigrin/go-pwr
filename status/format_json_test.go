// ABOUTME: Tests formatting a Record as one JSON line, using the real capture
// ABOUTME: in testdata and altered copies of it for the null and escaping cases.

package status

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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

// jsonKeyOrder is the key order of decision 0001's json example.
var jsonKeyOrder = []string{
	"time", "boot_id", "uptime_s", "device", "battery_v", "solar_v", "charge_a",
	"charge_state", "charge_state_code", "battery_type", "battery_type_code",
	"temp_c", "temp_f", "ah_today", "ah_yesterday", "ah_2d", "ah_3d", "ah_7d", "raw",
}

// neverNull are the keys that are never null.
var neverNull = []string{
	"time", "boot_id", "uptime_s", "device", "battery_v", "solar_v", "charge_a", "raw",
}

func TestJSONNullKeys(t *testing.T) {
	// Every non-gating field fails to decode.
	reply := capturedReply(t)
	for _, i := range []int{indexBatteryType, indexChargeState, indexTempC, indexTempF,
		indexAhToday, indexAhYesterday, indexAh2d, indexAh3d, indexAh7d} {
		reply = alteredField(reply, i, "x")
	}
	got := jsonValues(t, testRecord(t, reply))
	for _, key := range jsonKeyOrder {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q is missing", key)
		}
	}
	if len(got) != len(jsonKeyOrder) {
		t.Errorf("got %d keys, want %d", len(got), len(jsonKeyOrder))
	}
	for _, key := range neverNull {
		if got[key] == "null" {
			t.Errorf("%s is null", key)
		}
	}
	for _, key := range jsonKeyOrder[7 : len(jsonKeyOrder)-1] {
		if got[key] != "null" {
			t.Errorf("%s = %s, want null", key, got[key])
		}
	}
}

// alteredField returns reply with field index replaced by value.
func alteredField(reply string, index int, value string) string {
	fields := strings.Split(reply, ";")
	fields[index] = value
	return strings.Join(fields, ";")
}

func TestRoundHalfUpNotHalfEven(t *testing.T) {
	cases := []struct {
		milli int
		want  string
	}{
		{13245, "13.25"},
		{125, "0.13"},
		{22875, "22.88"},
	}
	for _, c := range cases {
		got := jsonValues(t, withGating(t, c.milli, c.milli, c.milli))
		for _, key := range []string{"battery_v", "solar_v", "charge_a"} {
			if got[key] != c.want {
				t.Errorf("%d: %s = %s, want %s", c.milli, key, got[key], c.want)
			}
		}
	}
}

func TestRoundNoFloatArtifacts(t *testing.T) {
	twoDecimals := regexp.MustCompile(`^\d+\.\d\d$`)
	for milli := 0; milli <= 99999; milli++ {
		got := jsonValues(t, withGating(t, milli, milli, milli))["battery_v"]
		if !twoDecimals.MatchString(got) {
			t.Fatalf("%d mV encodes as %s, want at most two decimals", milli, got)
		}
		// Half up in integers: the hundredths are (milli+5)/10.
		hundredths := (milli + 5) / 10
		want := fmt.Sprintf("%d.%02d", hundredths/100, hundredths%100)
		if got != want {
			t.Fatalf("%d mV encodes as %s, want %s", milli, got, want)
		}
	}
}

func TestJSONZeroAmpHoursNotNull(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"00000", "0"},
		{"0000A", "null"},
	}
	for _, c := range cases {
		reply := alteredField(capturedReply(t), indexAhToday, c.value)
		got := jsonValues(t, testRecord(t, reply))["ah_today"]
		if got != c.want {
			t.Errorf("amp-hours %q: ah_today = %s, want %s", c.value, got, c.want)
		}
	}
}

func TestJSONNullAndUnknownCodes(t *testing.T) {
	cases := []struct {
		name               string
		index              int
		value              string
		nameKey, codeKey   string
		wantName, wantCode string
	}{
		{"failed charge state", indexChargeState, "x", "charge_state", "charge_state_code", "null", "null"},
		{"failed battery type", indexBatteryType, "x", "battery_type", "battery_type_code", "null", "null"},
		{"unknown charge state", indexChargeState, "008", "charge_state", "charge_state_code", `"unknown"`, "8"},
		{"unknown battery type", indexBatteryType, "008", "battery_type", "battery_type_code", `"unknown"`, "8"},
	}
	for _, c := range cases {
		got := jsonValues(t, testRecord(t, alteredField(capturedReply(t), c.index, c.value)))
		if got[c.nameKey] != c.wantName {
			t.Errorf("%s: %s = %s, want %s", c.name, c.nameKey, got[c.nameKey], c.wantName)
		}
		if got[c.codeKey] != c.wantCode {
			t.Errorf("%s: %s = %s, want %s", c.name, c.codeKey, got[c.codeKey], c.wantCode)
		}
	}
}

func TestJSONRawEscapingSingleLine(t *testing.T) {
	rec := testRecord(t, capturedReply(t))
	rec.Reading.Raw = "a\r\nb\"c\\d\x00\x01\x1b\u2028é\n" + rec.Reading.Raw
	line, err := rec.JSONLine()
	if err != nil {
		t.Fatalf("JSONLine: %v", err)
	}
	if n := bytes.Count(line, []byte("\n")); n != 1 || line[len(line)-1] != '\n' {
		t.Errorf("line has %d newlines, want exactly one, at the end: %q", n, line)
	}
	if bytes.Contains(line, []byte("\r")) {
		t.Errorf("line contains a literal carriage return: %q", line)
	}
	var m struct{ Raw string }
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
	if m.Raw != rec.Reading.Raw {
		t.Errorf("raw = %q, want %q", m.Raw, rec.Reading.Raw)
	}
}

func TestJSONSignedTemps(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"-005", "-5"},
		{"+100", "100"},
		{"-100", "-100"},
		{"+022", "22"},
	}
	for _, c := range cases {
		reply := alteredField(alteredField(capturedReply(t), indexTempC, c.value), indexTempF, c.value)
		got := jsonValues(t, testRecord(t, reply))
		for _, key := range []string{"temp_c", "temp_f"} {
			if got[key] != c.want {
				t.Errorf("temperature %q: %s = %s, want %s", c.value, key, got[key], c.want)
			}
		}
	}
}
