// ABOUTME: Tests the charge-state and battery-type code tables against the
// ABOUTME: Code names tables in decision 0001.

package status

import (
	"strconv"
	"testing"
)

func TestCodeNames(t *testing.T) {
	chargeStates := []struct {
		code        int
		name, label string
	}{
		{0, "bulk", "Bulk"},
		{1, "float", "Float"},
		{2, "equalization", "Equalization"},
		{3, "absorption", "Absorption"},
		{4, "bulk_equalization", "Bulk Equalization"},
		{5, "charge_current", "Charge Current"},
		{6, "charge_voltage", "Charge Voltage"},
		{7, "no_charge", "No Charge"},
		{8, "unknown", "unknown (8)"},
	}
	for _, c := range chargeStates {
		r, err := Decode(alteredReply(t, indexChargeState, "00"+strconv.Itoa(c.code)))
		if err != nil {
			t.Fatalf("Decode with charge state %d: %v", c.code, err)
		}
		wantInt(t, "ChargeState", r.ChargeState, c.code)
		if got := ChargeStateName(c.code); got != c.name {
			t.Errorf("ChargeStateName(%d) = %q, want %q", c.code, got, c.name)
		}
		if got := ChargeStateLabel(c.code); got != c.label {
			t.Errorf("ChargeStateLabel(%d) = %q, want %q", c.code, got, c.label)
		}
	}
	batteryTypes := []struct {
		code        int
		name, label string
	}{
		{0, "sealed", "Sealed"},
		{1, "flooded", "Flooded"},
		{2, "lifepo4", "LiFePO4"},
		{3, "agm", "AGM"},
		{4, "custom", "Custom"},
		{5, "unknown", "unknown (5)"},
	}
	for _, c := range batteryTypes {
		r, err := Decode(alteredReply(t, indexBatteryType, "00"+strconv.Itoa(c.code)))
		if err != nil {
			t.Fatalf("Decode with battery type %d: %v", c.code, err)
		}
		wantInt(t, "BatteryType", r.BatteryType, c.code)
		if got := BatteryTypeName(c.code); got != c.name {
			t.Errorf("BatteryTypeName(%d) = %q, want %q", c.code, got, c.name)
		}
		if got := BatteryTypeLabel(c.code); got != c.label {
			t.Errorf("BatteryTypeLabel(%d) = %q, want %q", c.code, got, c.label)
		}
	}
}
