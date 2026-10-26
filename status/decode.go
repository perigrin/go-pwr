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
	indexChargeMA    = 0
	indexBatteryMV   = 2
	indexBatteryType = 9
	indexSolarMV     = 11
	indexChargeState = 12
	indexTempC       = 16
	indexTempF       = 17
	indexAhToday     = 19
	indexAhYesterday = 20
	indexAh2d        = 21
	indexAh3d        = 22
	indexAh7d        = 23
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

// Reading is one decoded status reply. The three gating fields are always
// set. Every other decoded field is nil when it failed to decode, and its
// error is in FieldErrors; a failed field never suppresses the reading.
type Reading struct {
	ChargeMA  int // charge current in mA
	BatteryMV int // battery voltage in mV
	SolarMV   int // PV (solar) voltage in mV

	BatteryType *int // battery type code (see codes.go)
	ChargeState *int // charge state code (see codes.go)
	TempC       *int // temperature in °C
	TempF       *int // temperature in °F

	// Amp-hours, read as decimal. Provisional: they may turn out to be
	// hexadecimal (docs/reference/protocol.md, Sources and confidence).
	AhToday     *int
	AhYesterday *int
	Ah2d        *int
	Ah3d        *int
	Ah7d        *int

	// Raw is the untouched reply.
	Raw string

	// FieldErrors holds one error for each non-gating field that failed
	// to decode, for stderr.
	FieldErrors []error

	// Warnings holds one error for each plausibility check the reading
	// failed, for stderr. A warning never changes or suppresses the
	// reading. Each is identifiable with errors.Is or errors.As, such as
	// ErrTempMismatch.
	Warnings []error
}

// FieldError reports a non-gating field that failed to decode.
type FieldError struct {
	Name  string // what the field holds, such as "battery type"
	Index int    // the field's index in the reply
	Err   error
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("decode %s (field %d): %v", e.Name, e.Index, e.Err)
}

func (e *FieldError) Unwrap() error { return e.Err }

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
	r.Raw = reply
	optional := []struct {
		dst   **int
		name  string
		index int
		parse func(string) (int, error)
	}{
		{&r.BatteryType, "battery type", indexBatteryType, parseUnsigned},
		{&r.ChargeState, "charge state", indexChargeState, parseUnsigned},
		{&r.TempC, "temperature °C", indexTempC, parseSigned},
		{&r.TempF, "temperature °F", indexTempF, parseSigned},
		{&r.AhToday, "amp-hours today", indexAhToday, parseUnsigned},
		{&r.AhYesterday, "amp-hours yesterday", indexAhYesterday, parseUnsigned},
		{&r.Ah2d, "amp-hours 2 days ago", indexAh2d, parseUnsigned},
		{&r.Ah3d, "amp-hours 3 days ago", indexAh3d, parseUnsigned},
		{&r.Ah7d, "amp-hours 7 days ago", indexAh7d, parseUnsigned},
	}
	for _, f := range optional {
		v, err := optionalField(fields, f.index, f.parse)
		if err != nil {
			r.FieldErrors = append(r.FieldErrors, &FieldError{Name: f.name, Index: f.index, Err: err})
			continue
		}
		*f.dst = &v
	}
	if w := checkTemps(r.TempC, r.TempF); w != nil {
		r.Warnings = append(r.Warnings, w)
	}
	return r, nil
}

// optionalField decodes fields[index] with parse.
func optionalField(fields []string, index int, parse func(string) (int, error)) (int, error) {
	if index >= len(fields) {
		return 0, fmt.Errorf("reply has only %d fields", len(fields))
	}
	return parse(fields[index])
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

// parseSigned decodes a signed decimal field: an optional '+' or '-'
// followed by one to maxFieldDigits ASCII digits.
func parseSigned(s string) (int, error) {
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		v, err := parseUnsigned(s[1:])
		if s[0] == '-' {
			v = -v
		}
		return v, err
	}
	return parseUnsigned(s)
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
