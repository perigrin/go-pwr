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
	Time     string      `json:"time"`
	BootID   string      `json:"boot_id"`
	UptimeS  float64     `json:"uptime_s"`
	Device   string      `json:"device"`
	BatteryV json.Number `json:"battery_v"`
	SolarV   json.Number `json:"solar_v"`
	ChargeA  json.Number `json:"charge_a"`

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

// hundredths converts a milli-unit count to units with two decimals,
// rounding half up. It works in integers, so no float rounding error can
// reach the text.
func hundredths(milli int) json.Number {
	h := (milli + 5) / 10
	return json.Number(fmt.Sprintf("%d.%02d", h/100, h%100))
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
