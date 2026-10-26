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

// Plausibility bounds for the three gating fields, inclusive. Each comes
// from what the GP-PWM-30-SB can report, never from the range seen in
// captures; docs/reference/protocol.md, Plausibility bounds, gives the
// reasons in full.
const (
	// minChargeMA is zero: the field is unsigned, and no current flows at
	// night.
	minChargeMA = 0
	// maxChargeMA is the controller's rated maximum continuous DC input
	// current, 37.5 A (GP-PWM-30-SB User Manual, 1.2 System Voltage and
	// Current). A PWM controller passes the array current to the battery,
	// so it can't charge with more.
	maxChargeMA = 37500
	// minBatteryMV is zero: a real reading must never be rejected however
	// low, and the controller can report with its battery disconnected
	// while the array powers it.
	minBatteryMV = 0
	// maxBatteryMV is 35 VDC, the highest voltage any terminal of the
	// controller is rated for (User Manual, 1.2 and 1.6 Specifications,
	// Maximum Solar DC Input Voltage). A 12 V nominal battery above it is
	// not a voltage this controller can measure.
	maxBatteryMV = 35000
	// minSolarMV is zero: the field is unsigned, and the array gives
	// nothing at night.
	minSolarMV = 0
	// maxSolarMV is the controller's rated maximum solar DC input
	// voltage, 35 VDC (User Manual, 1.2 and 1.6).
	maxSolarMV = 35000
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

// gatingField decodes fields[index] and checks it lies within [min, max].
func gatingField(reply string, fields []string, name string, index, min, max int) (int, error) {
	v, err := strconv.Atoi(fields[index])
	if err != nil {
		return 0, fmt.Errorf("decode %s (field %d) in reply %q: %w", name, index, reply, err)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("decode %s (field %d) in reply %q: %d outside plausible range %d to %d", name, index, reply, v, min, max)
	}
	return v, nil
}
