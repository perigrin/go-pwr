// ABOUTME: Formats a Record, a reading plus the metadata that does not come
// ABOUTME: from the reply, as one newline-terminated JSON line.

package status

import (
	"encoding/json"
	"fmt"
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
	Raw      string      `json:"raw"`
}

// JSONLine returns the record as one JSON object followed by a single
// newline. Encoding escapes every control character in the raw reply, so
// the object can't span lines.
func (rec Record) JSONLine() ([]byte, error) {
	line, err := json.Marshal(jsonLine{
		Time:     rec.Time.Format(time.RFC3339),
		BootID:   rec.BootID,
		UptimeS:  rec.UptimeS,
		Device:   rec.Device,
		BatteryV: hundredths(rec.Reading.BatteryMV),
		SolarV:   hundredths(rec.Reading.SolarMV),
		ChargeA:  hundredths(rec.Reading.ChargeMA),
		Raw:      rec.Reading.Raw,
	})
	if err != nil {
		return nil, fmt.Errorf("encode reading: %w", err)
	}
	return append(line, '\n'), nil
}

// hundredths converts a milli-unit count to units with two decimals,
// rounding half up. It works in integers, so no float rounding error can
// reach the text.
func hundredths(milli int) json.Number {
	h := (milli + 5) / 10
	return json.Number(fmt.Sprintf("%d.%02d", h/100, h%100))
}
