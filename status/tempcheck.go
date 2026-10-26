// ABOUTME: Checks that the reported °C and °F describe the same temperature,
// ABOUTME: allowing for each being rounded or truncated on its own.

package status

import (
	"errors"
	"fmt"
)

// tempToleranceFifths is how far the reported °F may sit from
// 9/5·(reported °C) + 32, in fifths of a °F. 13 fifths is 2.6 °F.
//
// The controller reports each temperature as a whole number, and which way
// it rounds or truncates is not known. TestTempToleranceCoversRoundingAndTruncation
// sweeps every true temperature from −40 to +60 °C in 0.01 °C steps, with °C
// and °F each independently half-up, half-even, half-away, truncated toward
// zero or floored. The worst gap is 2.6 °F, and the tolerance is exactly
// that. Cases, all within the sweep:
//
//   - 2.6 °F: true −38.99 °C (−38.18 °F). °C truncated toward zero gives −38
//     (expected −36.4 °F); °F floored gives −39. This is the worst case.
//   - 2.0 °F: true −0.99 °C (30.2 °F). °C truncated to 0 (expected 32 °F);
//     °F truncated to 30. The truncation-toward-zero case seen near freezing.
//   - 1.4 °F: the worst gap when both fields are rounded (any of the three
//     rounding modes), against 2.6 °F when °C truncates toward zero and °F
//     floors, which err in opposite directions below zero.
//   - 0.4 °F: the real capture, +22 °C / +72 °F against an expected 71.6 °F.
//
// 13 fifths also covers any rounding mode outside the sweep: a whole-degree
// °C is off by less than 1 °C (1.8 °F) and a whole-degree °F by less than
// 1 °F, so the gap is under 2.8 °F, and since 5·°F − (9·°C + 160) is a whole
// number, the gap in fifths is at most 13.
//
// 5·°F − (9·°C + 160) is always a whole number, and the gap in fifths is
// compared as integers, so there is no float rounding at the boundary.
// Because the tolerance is a whole number of fifths, "at the tolerance" is
// a gap of exactly 13/5 °F, which is reachable for some °C values (22 °C
// with 69 °F, 3 °C with 40 °F); it gives no warning, and any larger gap does.
const tempToleranceFifths = 13

// ErrTempMismatch is matched by errors.Is for every *TempMismatchWarning.
var ErrTempMismatch = errors.New("temperature °C and °F disagree")

// TempMismatchWarning is a Reading warning: the reported °C and °F differ by
// more than rounding or truncation explains. It never changes the Reading.
type TempMismatchWarning struct {
	TempC int
	TempF int
}

func (w *TempMismatchWarning) Error() string {
	return fmt.Sprintf("%v: %d °C and %d °F differ by more than %.1f °F",
		ErrTempMismatch, w.TempC, w.TempF, float64(tempToleranceFifths)/5)
}

func (w *TempMismatchWarning) Unwrap() error { return ErrTempMismatch }

// tempGapFifths is |°F − (9/5·°C + 32)| in fifths of a °F.
func tempGapFifths(c, f int) int {
	g := 5*f - (9*c + 160)
	if g < 0 {
		g = -g
	}
	return g
}

// checkTemps returns a warning when both temperatures decoded and disagree
// beyond the tolerance, and nil otherwise, including when either is nil.
func checkTemps(c, f *int) error {
	if c == nil || f == nil {
		return nil
	}
	if tempGapFifths(*c, *f) > tempToleranceFifths {
		return &TempMismatchWarning{TempC: *c, TempF: *f}
	}
	return nil
}
