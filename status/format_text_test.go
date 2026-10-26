// ABOUTME: Tests formatting a Record as the text block, using the real capture
// ABOUTME: in testdata and altered copies of it for the failed-field cases.

package status

import "testing"

// decisionTextBlock is the text example in decision 0001 (Output formats).
const decisionTextBlock = `time             2026-10-25T15:42:10-04:00
device           C3:00:00:58:51:40
battery          13.24 V   AGM
solar            22.88 V
charge           0.66 A    Bulk
temperature      22°C / 72°F
amp-hours        today 12  yesterday 15  2d 16  3d 4  7d 93
`

func TestTextCapturedReply(t *testing.T) {
	got := testRecord(t, capturedReply(t)).Text()
	if got != decisionTextBlock {
		t.Errorf("Text() =\n%s\nwant\n%s", got, decisionTextBlock)
	}
}
