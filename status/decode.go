// ABOUTME: Decodes a controller reply, ASCII fields separated by ';', into a
// ABOUTME: Reading, failing it when a gating field is missing or implausible.

package status

import (
	"fmt"
	"strconv"
	"strings"
)

// Indices of the reply fields go-pwr decodes (docs/reference/protocol.md,
// Fields).
const (
	indexChargeMA  = 0
	indexBatteryMV = 2
	indexSolarMV   = 11
)

// Plausibility bounds for the three gating fields, inclusive. They come
// from what the field format can carry, never from the range seen in
// captures; docs/reference/protocol.md, Plausibility bounds, gives the
// reasons in full. The manual's ratings (35 VDC input, 37.5 A input
// current) are tolerances, not limits on what the controller reports, so
// they are not used: a cold-morning array above 35 V must not suppress the
// battery reading.
const (
	// minChargeMA is zero: the field is unsigned, and no current flows at
	// night.
	minChargeMA = 0
	// maxChargeMA is the largest value five unsigned digits can carry.
	maxChargeMA = 99999
	// minBatteryMV is zero: a real reading must never be rejected however
	// low (decision 0001, Behavior and errors).
	minBatteryMV = 0
	// maxBatteryMV is the largest value five unsigned digits can carry.
	maxBatteryMV = 99999
	// minSolarMV is zero: the field is unsigned, and the array gives
	// nothing at night.
	minSolarMV = 0
	// maxSolarMV is the largest value five unsigned digits can carry.
	maxSolarMV = 99999
)

// Reading is one decoded status reply.
type Reading struct {
	ChargeMA  int // charge current in mA
	BatteryMV int // battery voltage in mV
	SolarMV   int // PV (solar) voltage in mV
}

// Decode turns a reply into a Reading. It fails when a gating field (charge
// current, battery voltage, PV voltage) is missing, doesn't decode, or lies
// outside its plausibility bounds.
func Decode(reply string) (Reading, error) {
	fields := strings.Split(reply, ";")
	var r Reading
	var err error
	if r.ChargeMA, err = gatingField(reply, fields, "charge current", indexChargeMA, minChargeMA, maxChargeMA); err != nil {
		return Reading{}, err
	}
	if r.BatteryMV, err = gatingField(reply, fields, "battery voltage", indexBatteryMV, minBatteryMV, maxBatteryMV); err != nil {
		return Reading{}, err
	}
	if r.SolarMV, err = gatingField(reply, fields, "PV voltage", indexSolarMV, minSolarMV, maxSolarMV); err != nil {
		return Reading{}, err
	}
	return r, nil
}

// maxFieldDigits is the width of the reply's numeric fields. A longer
// value isn't one the controller sent in that field.
const maxFieldDigits = 5

// parseUnsigned decodes an unsigned decimal field: one to maxFieldDigits
// ASCII digits, with no sign.
func parseUnsigned(s string) (int, error) {
	if len(s) == 0 || len(s) > maxFieldDigits {
		return 0, fmt.Errorf("%q is not 1 to %d digits", s, maxFieldDigits)
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is not an unsigned decimal", s)
		}
	}
	return strconv.Atoi(s)
}

// gatingField decodes fields[index] and checks it lies within [min, max].
func gatingField(reply string, fields []string, name string, index, min, max int) (int, error) {
	if index >= len(fields) {
		return 0, fmt.Errorf("decode %s (field %d) in reply %q: reply has only %d fields", name, index, reply, len(fields))
	}
	v, err := parseUnsigned(fields[index])
	if err != nil {
		return 0, fmt.Errorf("decode %s (field %d) in reply %q: %w", name, index, reply, err)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("decode %s (field %d) in reply %q: %d outside plausible range %d to %d", name, index, reply, v, min, max)
	}
	return v, nil
}
