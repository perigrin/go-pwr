// ABOUTME: Formats a Record, a reading plus the metadata that does not come
// ABOUTME: from the reply, as one newline-terminated JSON line.

package status

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Record is a reading with the metadata that doesn't come from the reply.
// The command supplies the metadata, so status needs no Bluetooth, D-Bus
// or boot-identity code.
type Record struct {
	Time    time.Time // when the reply was received
	BootID  string    // the kernel's identifier for the current boot
	UptimeS float64   // seconds since boot on the monotonic clock
	Device  string    // the controller's address
	Reading Reading
}

// jsonLine holds the keys of decision 0001's json example, in its order.
type jsonLine struct {
	Time     string  `json:"time"`
	BootID   string  `json:"boot_id"`
	UptimeS  float64 `json:"uptime_s"`
	Device   string  `json:"device"`
	BatteryV float64 `json:"battery_v"`
	SolarV   float64 `json:"solar_v"`
	ChargeA  float64 `json:"charge_a"`

	ChargeState     *string `json:"charge_state"`
	ChargeStateCode *int    `json:"charge_state_code"`
	BatteryType     *string `json:"battery_type"`
	BatteryTypeCode *int    `json:"battery_type_code"`
	TempC           *int    `json:"temp_c"`
	TempF           *int    `json:"temp_f"`
	AhToday         *int    `json:"ah_today"`
	AhYesterday     *int    `json:"ah_yesterday"`
	Ah2d            *int    `json:"ah_2d"`
	Ah3d            *int    `json:"ah_3d"`
	Ah7d            *int    `json:"ah_7d"`

	Raw string `json:"raw"`
}

// JSONLine returns the record as one JSON object followed by a single
// newline. Encoding escapes every control character in the raw reply, so
// the object can't span lines.
func (rec Record) JSONLine() ([]byte, error) {
	line, err := json.Marshal(jsonLine{
		Time:            rec.Time.Format(time.RFC3339),
		BootID:          rec.BootID,
		UptimeS:         rec.UptimeS,
		Device:          rec.Device,
		BatteryV:        hundredths(rec.Reading.BatteryMV),
		SolarV:          hundredths(rec.Reading.SolarMV),
		ChargeA:         hundredths(rec.Reading.ChargeMA),
		ChargeState:     codeNameOrNil(rec.Reading.ChargeState, ChargeStateName),
		ChargeStateCode: rec.Reading.ChargeState,
		BatteryType:     codeNameOrNil(rec.Reading.BatteryType, BatteryTypeName),
		BatteryTypeCode: rec.Reading.BatteryType,
		TempC:           rec.Reading.TempC,
		TempF:           rec.Reading.TempF,
		AhToday:         rec.Reading.AhToday,
		AhYesterday:     rec.Reading.AhYesterday,
		Ah2d:            rec.Reading.Ah2d,
		Ah3d:            rec.Reading.Ah3d,
		Ah7d:            rec.Reading.Ah7d,
		Raw:             rec.Reading.Raw,
	})
	if err != nil {
		return nil, fmt.Errorf("encode reading: %w", err)
	}
	return append(line, '\n'), nil
}

// WriteJSONLine writes the record's JSON line to w in a single Write call,
// so concurrent writers to one stream can't interleave inside a line.
func WriteJSONLine(w io.Writer, rec Record) error {
	line, err := rec.JSONLine()
	if err != nil {
		return err
	}
	if _, err := w.Write(line); err != nil {
		return fmt.Errorf("write reading: %w", err)
	}
	return nil
}

// hundredths converts a milli-unit count to units, rounding half up to two
// decimals. The rounding is integer arithmetic, so no float error enters
// it. Dividing the whole hundredths by 100 gives the float64 nearest the
// two-decimal value, which encoding/json prints in its shortest form: no
// artefacts, and no trailing zeros (13200 mV is 13.2).
func hundredths(milli int) float64 {
	return float64(wholeHundredths(milli)) / 100
}

// wholeHundredths converts a milli-unit count to a whole number of
// hundredths of a unit, rounding half up.
func wholeHundredths(milli int) int {
	return (milli + 5) / 10
}

// codeNameOrNil names a code with name, or gives nil when the code failed
// to decode, so a failed field is never reported as a name.
func codeNameOrNil(code *int, name func(int) string) *string {
	if code == nil {
		return nil
	}
	n := name(*code)
	return &n
}
