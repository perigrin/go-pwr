// ABOUTME: A backstop that warns when a reply field's width differs from the
// ABOUTME: width every real capture shows, which a misplaced chunk can cause.

package status

import (
	"errors"
	"fmt"
)

// indexTempOther is the third temperature-like field, +22 beside the °C and
// °F fields in the real captures. Its meaning is unknown, but it is signed
// like they are.
const indexTempOther = 18

// variableWidth marks a field whose width is not checked.
const variableWidth = -1

// fieldWidths is the width in bytes of each field of a reply body, measured
// over every real capture in testdata (the afternoon reply and the 21 lola
// replies; TestWidthsFromCapturesOnly derives it again from them). Altered
// or derived files never count. Indices 16 to 18 are temperatures and are
// variableWidth, since a value of 100 °F or more, or a negative one, may
// change their width. The last field is empty after the trailing ';'.
//
// A misplaced chunk often moves a field boundary and so changes a width, but
// not always: this is a statistical backstop, and in-order delivery is the
// guard (docs/reference/protocol.md, Reordered notifications).
var fieldWidths = [replyFields]int{
	5, 3, 5, 5, 5, 5, 5, 5, 5, 3, // 0-9
	3, 5, 3, 5, 5, 5, variableWidth, variableWidth, variableWidth, 5, // 10-19
	5, 5, 5, 5, 5, 5, 5, 5, 3, 3, // 20-29
	3, 0, // 30-31
}

// ErrFieldWidth is matched by errors.Is for every *FieldWidthWarning.
var ErrFieldWidth = errors.New("reply field has an unexpected width")

// FieldWidthWarning is a Reading warning: a field is not the width every
// real capture shows. It never changes the Reading.
type FieldWidthWarning struct {
	Index int // the field's index in the reply
	Got   int // its width in bytes
	Want  int // the width the captures show
}

func (w *FieldWidthWarning) Error() string {
	return fmt.Sprintf("%v: field %d is %d bytes wide, the captures show %d",
		ErrFieldWidth, w.Index, w.Got, w.Want)
}

func (w *FieldWidthWarning) Unwrap() error { return ErrFieldWidth }

// checkWidths returns one warning for each field whose width differs from
// fieldWidths. A reply with fewer fields than a whole one is checked as far
// as it goes: whether it is whole is the Assembler's rule.
func checkWidths(fields []string) []error {
	var warnings []error
	for i, f := range fields {
		if i >= len(fieldWidths) {
			break
		}
		if want := fieldWidths[i]; want != variableWidth && len(f) != want {
			warnings = append(warnings, &FieldWidthWarning{Index: i, Got: len(f), Want: want})
		}
	}
	return warnings
}
