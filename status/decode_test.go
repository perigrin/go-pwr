// ABOUTME: Tests decoding a controller reply into a Reading, using the real
// ABOUTME: capture in testdata and altered copies of it for error cases.

package status

import (
	"errors"
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

// wantGatingError checks that Decode fails reply with an error naming
// field and carrying the reply, which ErrorLine quotes once. The error's
// own message leaves the reply out.
func wantGatingError(t *testing.T, reply, field string) {
	t.Helper()
	_, err := Decode(reply)
	if err == nil {
		t.Errorf("Decode(%q) succeeded, want an error naming %s", reply, field)
		return
	}
	var re *ReplyError
	if !errors.As(err, &re) || re.Reply != reply {
		t.Errorf("error %q does not carry the reply %q", err, reply)
	}
	if !strings.Contains(err.Error(), field) {
		t.Errorf("error %q does not name %s", err, field)
	}
	line := ErrorLine(err)
	if n := strings.Count(line, strconv.Quote(reply)); n != 1 {
		t.Errorf("line %q quotes the reply %d times, want once", line, n)
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

// wantInt checks that a nullable decoded value is present and equals want.
func wantInt(t *testing.T, name string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = null, want %d", name, want)
	} else if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

func TestDecodeCapturedReply(t *testing.T) {
	reply := capturedReply(t)
	r, err := Decode(reply)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	wantInt(t, "BatteryType", r.BatteryType, 3)
	wantInt(t, "ChargeState", r.ChargeState, 0)
	wantInt(t, "TempC", r.TempC, 22)
	wantInt(t, "TempF", r.TempF, 72)
	wantInt(t, "AhToday", r.AhToday, 12)
	wantInt(t, "AhYesterday", r.AhYesterday, 15)
	wantInt(t, "Ah2d", r.Ah2d, 16)
	wantInt(t, "Ah3d", r.Ah3d, 4)
	wantInt(t, "Ah7d", r.Ah7d, 93)
	if r.Raw != reply {
		t.Errorf("Raw = %q, want the reply untouched", r.Raw)
	}
	if len(r.FieldErrors) != 0 {
		t.Errorf("FieldErrors = %v, want none", r.FieldErrors)
	}
}

// fieldsByIndex returns the decoded nullable field for each non-gating
// index, so a test can check exactly which ones are null.
func fieldsByIndex(r Reading) map[int]*int {
	return map[int]*int{
		indexBatteryType: r.BatteryType,
		indexChargeState: r.ChargeState,
		indexTempC:       r.TempC,
		indexTempF:       r.TempF,
		indexAhToday:     r.AhToday,
		indexAhYesterday: r.AhYesterday,
		indexAh2d:        r.Ah2d,
		indexAh3d:        r.Ah3d,
		indexAh7d:        r.Ah7d,
	}
}

// wantOnlyNull checks that exactly the fields at nullIndexes are null, each
// with its own FieldError, and that the gating values are the capture's.
func wantOnlyNull(t *testing.T, reply string, nullIndexes ...int) Reading {
	t.Helper()
	r, err := Decode(reply)
	if err != nil {
		t.Fatalf("Decode(%q): %v, want a reading", reply, err)
	}
	isNull := map[int]bool{}
	for _, i := range nullIndexes {
		isNull[i] = true
	}
	for i, v := range fieldsByIndex(r) {
		if isNull[i] && v != nil {
			t.Errorf("field %d = %d, want null", i, *v)
		}
		if !isNull[i] && v == nil {
			t.Errorf("field %d is null, want a value", i)
		}
	}
	if len(r.FieldErrors) != len(nullIndexes) {
		t.Errorf("FieldErrors = %v, want %d", r.FieldErrors, len(nullIndexes))
	}
	for _, e := range r.FieldErrors {
		var fe *FieldError
		if !errors.As(e, &fe) || !isNull[fe.Index] {
			t.Errorf("field error %v is not for a null field", e)
		}
	}
	if r.ChargeMA != 655 || r.BatteryMV != 13244 || r.SolarMV != 22879 {
		t.Errorf("gating values changed: %d mA, %d mV, %d mV", r.ChargeMA, r.BatteryMV, r.SolarMV)
	}
	if r.Raw != reply {
		t.Errorf("Raw = %q, want %q", r.Raw, reply)
	}
	return r
}

func TestDecodeNullForFailedFields(t *testing.T) {
	for _, i := range []int{indexTempC, indexTempF, indexAhToday, indexAh7d, indexBatteryType, indexChargeState} {
		wantOnlyNull(t, alteredReply(t, i, "x"), i)
	}
}

func TestDecodeHexAmpHoursIsNull(t *testing.T) {
	reply := alteredReply(t, indexAhToday, "0000A")
	wantOnlyNull(t, reply, indexAhToday)
	if !strings.Contains(reply, ";0000A;") {
		t.Fatalf("altered reply %q lost 0000A", reply)
	}
}

func TestDecodeLeadingZerosAreDecimal(t *testing.T) {
	for _, c := range []struct {
		index int
		value string
		want  int
	}{
		{indexBatteryType, "008", 8},
		{indexChargeState, "009", 9},
		{indexAhToday, "00008", 8},
		{indexAh7d, "00009", 9},
	} {
		r := wantOnlyNull(t, alteredReply(t, c.index, c.value))
		wantInt(t, c.value, fieldsByIndex(r)[c.index], c.want)
	}
}

func TestDecodeSignedAndThreeDigitTemps(t *testing.T) {
	for _, c := range []struct {
		c, f         string
		wantC, wantF int
	}{
		{"-05", "+23", -5, 23},
		{"-40", "-40", -40, -40},
		{"+38", "+100", 38, 100},
	} {
		reply := alteredReply(t, indexTempC, c.c)
		fields := strings.Split(reply, ";")
		fields[indexTempF] = c.f
		r := wantOnlyNull(t, strings.Join(fields, ";"))
		wantInt(t, "TempC", r.TempC, c.wantC)
		wantInt(t, "TempF", r.TempF, c.wantF)
	}
	for _, v := range []string{"+", "-", "+-5", "--5"} {
		wantOnlyNull(t, alteredReply(t, indexTempF, v), indexTempF)
	}
}

func TestDecodeMultipleFailedFieldsEachReported(t *testing.T) {
	fields := strings.Split(capturedReply(t), ";")
	for _, i := range []int{indexBatteryType, indexTempC, indexAhYesterday, indexAh3d} {
		fields[i] = "?"
	}
	wantOnlyNull(t, strings.Join(fields, ";"), indexBatteryType, indexTempC, indexAhYesterday, indexAh3d)
}

func TestDecodeIgnoresIndex10(t *testing.T) {
	r := wantOnlyNull(t, alteredReply(t, 10, "not a number"))
	want, err := Decode(capturedReply(t))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i, v := range fieldsByIndex(r) {
		if *v != *fieldsByIndex(want)[i] {
			t.Errorf("field %d = %d, want %d", i, *v, *fieldsByIndex(want)[i])
		}
	}
}

// TestDecodeOutOfTableCodes pins how codes outside both tables decode:
// an unsigned number is kept with the name unknown, and a signed value,
// which the code fields' unsigned format can't carry, is null.
func TestDecodeOutOfTableCodes(t *testing.T) {
	for _, c := range []struct {
		index int
		value string
		want  int
	}{
		{indexChargeState, "008", 8},
		{indexChargeState, "255", 255},
		{indexBatteryType, "008", 8},
		{indexBatteryType, "255", 255},
	} {
		r := wantOnlyNull(t, alteredReply(t, c.index, c.value))
		wantInt(t, c.value, fieldsByIndex(r)[c.index], c.want)
	}
	if ChargeStateName(255) != "unknown" || BatteryTypeName(8) != "unknown" {
		t.Errorf("an unlisted code is not named unknown")
	}
	for _, i := range []int{indexChargeState, indexBatteryType} {
		wantOnlyNull(t, alteredReply(t, i, "-01"), i)
	}
}
