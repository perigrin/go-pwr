// ABOUTME: Tests how the end-to-end tests count failed readings on stderr:
// ABOUTME: the lines of readings that were not printed, never of ones that were.

package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// printedFieldError matches the error line of a field that failed to decode
// on a reading that was printed anyway: any field but the three key ones
// (charge current, battery voltage, PV voltage), whose failure fails the
// reading.
var printedFieldError = regexp.MustCompile(`^error: decode (battery type|charge state|temperature °[CF]|amp-hours[^:(]*) \(field \d+\)`)

// failedReadings counts the stderr lines that are a reading failing: error
// lines other than a printed reading's field errors. That includes a reply
// that was rejected, which quotes the reply. A warning is on a reading that
// was printed. A signal that ends a reading draws no line at all.
func failedReadings(stderr string) int {
	n := 0
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		if strings.HasPrefix(line, "error: ") && !printedFieldError.MatchString(line) {
			n++
		}
	}
	return n
}

// stderrOf runs a reply through report and returns what it wrote on stderr.
func stderrOf(t *testing.T, reply string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	report(config{format: "json"}, received(reply), &stdout, &stderr)
	return stderr.String()
}

func TestFailedReadingsCountsOnlyReadingsThatFailed(t *testing.T) {
	real := realReply(t)
	for _, tc := range []struct {
		name  string
		lines string
		want  int
	}{
		{"printed reading", stderrOf(t, real), 0},
		{"printed reading with a field that failed to decode", stderrOf(t, alteredFrame(t, 9, "x")), 0},
		{"printed reading with a warning", stderrOf(t, alteredFrame(t, 17, "+99")), 0},
		{"key field out of bounds", stderrOf(t, alteredFrame(t, 0, "x")), 1},
		{"reply not whole", stderrOf(t, real[:40]), 1},
		{"error with no reply", "error: scan for C3:00:00:58:51:40: controller not found\n", 1},
		{"two failures among printed readings", stderrOf(t, alteredFrame(t, 0, "x")) + stderrOf(t, alteredFrame(t, 9, "x")) + "error: connect: boom\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := failedReadings(tc.lines); got != tc.want {
				t.Errorf("failedReadings(%q) = %d, want %d", tc.lines, got, tc.want)
			}
		})
	}
}
