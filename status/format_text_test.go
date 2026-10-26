// ABOUTME: Tests formatting a Record as the text block, using the real capture
// ABOUTME: in testdata and altered copies of it for the failed-field cases.

package status

import (
	"bytes"
	"strings"
	"testing"
)

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

// nonGatingIndexes are the reply fields whose failure shows as ? instead of
// suppressing the reading.
var nonGatingIndexes = []int{
	indexBatteryType, indexChargeState, indexTempC, indexTempF,
	indexAhToday, indexAhYesterday, indexAh2d, indexAh3d, indexAh7d,
}

// withFailedFields returns the captured reply with the given fields
// replaced by something that doesn't decode.
func withFailedFields(t *testing.T, indexes ...int) string {
	t.Helper()
	fields := strings.Split(capturedReply(t), ";")
	for _, i := range indexes {
		fields[i] = "x"
	}
	return strings.Join(fields, ";")
}

func TestTextNullShowsQuestionMark(t *testing.T) {
	reply := withFailedFields(t, indexBatteryType, indexTempF, indexAh2d)
	want := `time             2026-10-25T15:42:10-04:00
device           C3:00:00:58:51:40
battery          13.24 V   ?
solar            22.88 V
charge           0.66 A    Bulk
temperature      22°C / ?°F
amp-hours        today 12  yesterday 15  2d ?  3d 4  7d 93
`
	if got := testRecord(t, reply).Text(); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}
}

func TestTextAllNonGatingNull(t *testing.T) {
	reply := withFailedFields(t, nonGatingIndexes...)
	want := `time             2026-10-25T15:42:10-04:00
device           C3:00:00:58:51:40
battery          13.24 V   ?
solar            22.88 V
charge           0.66 A    ?
temperature      ?°C / ?°F
amp-hours        today ?  yesterday ?  2d ?  3d ?  7d ?
`
	if got := testRecord(t, reply).Text(); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}
}

func TestTextUnknownCodeAndSignedTemps(t *testing.T) {
	cases := []struct {
		name     string
		c, f     string
		wantTemp string
	}{
		{"negative", "-05", "+23", "-5°C / 23°F"},
		{"three digits", "+38", "100", "38°C / 100°F"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fields := strings.Split(capturedReply(t), ";")
			fields[indexTempC], fields[indexTempF] = c.c, c.f
			got := testRecord(t, strings.Join(fields, ";")).Text()
			if !strings.Contains(got, "temperature      "+c.wantTemp+"\n") {
				t.Errorf("Text() =\n%s\nwant temperature %q", got, c.wantTemp)
			}
		})
	}
	fields := strings.Split(capturedReply(t), ";")
	fields[indexBatteryType], fields[indexChargeState] = "8", "8"
	got := testRecord(t, strings.Join(fields, ";")).Text()
	for _, want := range []string{"13.24 V   unknown (8)\n", "0.66 A    unknown (8)\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("Text() =\n%s\nwant it to contain %q", got, want)
		}
	}
}

func TestTextSeparatorBetweenReadings(t *testing.T) {
	var buf bytes.Buffer
	tw := NewTextWriter(&buf)
	rec := testRecord(t, capturedReply(t))
	for range 3 {
		if err := tw.Write(rec); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	want := decisionTextBlock + "\n" + decisionTextBlock + "\n" + decisionTextBlock
	if got := buf.String(); got != want {
		t.Errorf("three readings =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(buf.String(), "\n\n\n") {
		t.Error("more than one blank line between readings")
	}
}

func TestTextWriterOneWritePerBlock(t *testing.T) {
	var cw countingWriter
	tw := NewTextWriter(&cw)
	rec := testRecord(t, capturedReply(t))
	for range 2 {
		if err := tw.Write(rec); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if len(cw.writes) != 2 {
		t.Errorf("%d Write calls for 2 readings, want 2", len(cw.writes))
	}
}
