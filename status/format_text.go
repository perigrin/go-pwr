// ABOUTME: Formats a Record as the text block of decision 0001: one labelled
// ABOUTME: line per quantity, with ? for a field that failed to decode.

package status

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// unknownField is what the text block shows for a field that failed to
// decode.
const unknownField = "?"

// labelWidth is the width of the label column: the longest label,
// "temperature", plus the gap to the values.
const labelWidth = 17

// valueWidth is the width of a voltage or current with its unit, plus the
// gap before the code label that may follow it.
const valueWidth = 10

// Text returns the record as decision 0001's text block: one line per
// quantity, each newline-terminated. A field that failed to decode shows
// as "?".
func (rec Record) Text() string {
	r := rec.Reading
	var b strings.Builder
	line := func(label, value string) {
		fmt.Fprintf(&b, "%-*s%s\n", labelWidth, label, value)
	}
	withCode := func(measure string, code *int, label func(int) string) string {
		return fmt.Sprintf("%-*s%s", valueWidth, measure, codeLabelOrUnknown(code, label))
	}
	line("time", rec.Time.Format(time.RFC3339))
	line("device", rec.Device)
	line("battery", withCode(milli(r.BatteryMV)+" V", r.BatteryType, BatteryTypeLabel))
	line("solar", milli(r.SolarMV)+" V")
	line("charge", withCode(milli(r.ChargeMA)+" A", r.ChargeState, ChargeStateLabel))
	line("temperature", intOrUnknown(r.TempC)+"°C / "+intOrUnknown(r.TempF)+"°F")
	line("amp-hours", fmt.Sprintf("today %s  yesterday %s  2d %s  3d %s  7d %s",
		intOrUnknown(r.AhToday), intOrUnknown(r.AhYesterday), intOrUnknown(r.Ah2d),
		intOrUnknown(r.Ah3d), intOrUnknown(r.Ah7d)))
	return b.String()
}

// TextWriter writes text blocks to a stream, one blank line between
// consecutive readings, as continuous modes need.
type TextWriter struct {
	w       io.Writer
	written bool
}

// NewTextWriter returns a TextWriter that writes to w.
func NewTextWriter(w io.Writer) *TextWriter {
	return &TextWriter{w: w}
}

// Write writes the record's text block, preceded by a blank line when it
// isn't the first. Each block goes out in a single Write call.
func (tw *TextWriter) Write(rec Record) error {
	block := rec.Text()
	if tw.written {
		block = "\n" + block
	}
	if _, err := io.WriteString(tw.w, block); err != nil {
		return fmt.Errorf("write reading: %w", err)
	}
	tw.written = true
	return nil
}

// milli formats a milli-unit count as units to two decimals, rounded half
// up, the same rounding as the JSON line.
func milli(n int) string {
	h := wholeHundredths(n)
	return fmt.Sprintf("%d.%02d", h/100, h%100)
}

// intOrUnknown formats n, or "?" when it failed to decode.
func intOrUnknown(n *int) string {
	if n == nil {
		return unknownField
	}
	return strconv.Itoa(*n)
}

// codeLabelOrUnknown gives the text label for a code, or "?" when the code
// failed to decode.
func codeLabelOrUnknown(code *int, label func(int) string) string {
	if code == nil {
		return unknownField
	}
	return label(*code)
}
