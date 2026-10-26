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

// Reading is one decoded status reply.
type Reading struct {
	ChargeMA  int // charge current in mA
	BatteryMV int // battery voltage in mV
	SolarMV   int // PV (solar) voltage in mV
}

// Decode turns a reply into a Reading.
func Decode(reply string) (Reading, error) {
	fields := strings.Split(reply, ";")
	var r Reading
	var err error
	if r.ChargeMA, err = strconv.Atoi(fields[indexChargeMA]); err != nil {
		return Reading{}, fmt.Errorf("decode charge current: %w", err)
	}
	if r.BatteryMV, err = strconv.Atoi(fields[indexBatteryMV]); err != nil {
		return Reading{}, fmt.Errorf("decode battery voltage: %w", err)
	}
	if r.SolarMV, err = strconv.Atoi(fields[indexSolarMV]); err != nil {
		return Reading{}, fmt.Errorf("decode PV voltage: %w", err)
	}
	return r, nil
}
