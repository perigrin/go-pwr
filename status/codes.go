// ABOUTME: Names and text labels for the controller's charge-state and
// ABOUTME: battery-type codes, from decision 0001's Code names tables.

package status

import "fmt"

// codeName is one row of a code table: the JSON name and the text label.
type codeName struct {
	name, label string
}

// chargeStates follows decision 0001, Code names. Only code 0 (bulk) has
// been observed; the rest follow the Android app and are provisional.
var chargeStates = map[int]codeName{
	0: {"bulk", "Bulk"},
	1: {"float", "Float"},
	2: {"equalization", "Equalization"},
	3: {"absorption", "Absorption"},
	4: {"bulk_equalization", "Bulk Equalization"},
	5: {"charge_current", "Charge Current"},
	6: {"charge_voltage", "Charge Voltage"},
	7: {"no_charge", "No Charge"},
}

// batteryTypes follows decision 0001, Code names. Only code 3 (agm) has
// been observed; the rest follow the Android app and are provisional.
var batteryTypes = map[int]codeName{
	0: {"sealed", "Sealed"},
	1: {"flooded", "Flooded"},
	2: {"lifepo4", "LiFePO4"},
	3: {"agm", "AGM"},
	4: {"custom", "Custom"},
}

// ChargeStateName returns the JSON name for a charge state code, or
// "unknown" for an unlisted one.
func ChargeStateName(code int) string { return lookupName(chargeStates, code) }

// ChargeStateLabel returns the text label for a charge state code, or
// "unknown (N)" for an unlisted one.
func ChargeStateLabel(code int) string { return lookupLabel(chargeStates, code) }

// BatteryTypeName returns the JSON name for a battery type code, or
// "unknown" for an unlisted one.
func BatteryTypeName(code int) string { return lookupName(batteryTypes, code) }

// BatteryTypeLabel returns the text label for a battery type code, or
// "unknown (N)" for an unlisted one.
func BatteryTypeLabel(code int) string { return lookupLabel(batteryTypes, code) }

func lookupName(table map[int]codeName, code int) string {
	if c, ok := table[code]; ok {
		return c.name
	}
	return "unknown"
}

func lookupLabel(table map[int]codeName, code int) string {
	if c, ok := table[code]; ok {
		return c.label
	}
	return fmt.Sprintf("unknown (%d)", code)
}
