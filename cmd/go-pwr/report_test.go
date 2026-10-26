// ABOUTME: Tests that a received reply becomes a reading on stdout and its
// ABOUTME: diagnostics on stderr, using a real reply captured on lola.

package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// realReply returns the framed reply captured on lola as session-1-reply-01,
// reassembled from its notifications.
func realReply(t *testing.T) string {
	t.Helper()
	f, err := os.Open("../../status/testdata/lola-timing/session-1-reply-01.seq.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var b strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "#") {
			continue
		}
		_, payload, _ := strings.Cut(sc.Text(), " ")
		chunk, err := hex.DecodeString(payload)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(chunk)
	}
	return b.String()
}

// alteredFrame returns the real reply with the body field at index replaced.
// Altered replies exercise error paths and never stand in for a real reading
// being accepted.
func alteredFrame(t *testing.T, index int, value string) string {
	t.Helper()
	reply := realReply(t)
	fields := strings.Split(reply[1:len(reply)-2], ";")
	fields[index] = value
	return " " + strings.Join(fields, ";") + "\r\n"
}

// countingWriter counts the Write calls made to it.
type countingWriter struct {
	bytes.Buffer
	writes int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.Buffer.Write(p)
}

// WriteString counts too, because io.WriteString prefers it.
func (w *countingWriter) WriteString(s string) (int, error) {
	w.writes++
	return w.Buffer.WriteString(s)
}

var receivedAt = time.Date(2026, 10, 26, 9, 30, 0, 0, time.FixedZone("EDT", -4*3600))

func received(reply string) receivedReply {
	return receivedReply{
		reply:   reply,
		device:  "C3:00:00:58:51:40",
		at:      receivedAt,
		bootID:  "0a1b2c3d-0000-4000-8000-0123456789ab",
		uptimeS: 1234.5,
	}
}

// linesWithPrefix returns the lines of s that start with prefix.
func linesWithPrefix(s, prefix string) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestReportPrintsTheReadingInOneWrite(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var stdout countingWriter
			var stderr bytes.Buffer
			code := report(config{format: format}, received(realReply(t)), &stdout, &stderr)
			if code != 0 || stdout.writes != 1 {
				t.Errorf("code %d after %d writes to stdout, want 0 after 1; stderr %q", code, stdout.writes, stderr.String())
			}
			if !strings.HasSuffix(stdout.String(), "\n") || !strings.Contains(stdout.String(), "C3:00:00:58:51:40") {
				t.Errorf("stdout %q is not a complete reading for the device", stdout.String())
			}
			if format == "json" && !strings.Contains(stdout.String(), `"time":"2026-10-26T09:30:00-04:00"`) {
				t.Errorf("stdout %q: time is not RFC 3339 in the zone it carries", stdout.String())
			}
		})
	}
}

func TestReportFieldErrorKeepsTheReading(t *testing.T) {
	reply := alteredFrame(t, 9, "x") // battery type
	var stdout, stderr bytes.Buffer
	code := report(config{format: "json"}, received(reply), &stdout, &stderr)
	if code != 0 {
		t.Errorf("code %d, want 0: a field that failed to decode never suppresses the reading", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got["battery_type"] != nil {
		t.Errorf("stdout %q: want a JSON object with battery_type null (%v)", stdout.String(), err)
	}
	// The altered field is also too narrow, which draws a warning of its own.
	lines := linesWithPrefix(stderr.String(), "error: ")
	if len(lines) != 1 || strings.Count(lines[0], strconv.Quote(reply)) != 1 {
		t.Errorf("stderr %q: want one error line quoting the reply once", stderr.String())
	}
}

func TestReportGatingFailurePrintsNothing(t *testing.T) {
	reply := alteredFrame(t, 0, "x") // charge current
	var stdout, stderr bytes.Buffer
	code := report(config{format: "text"}, received(reply), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 {
		t.Errorf("code %d, stdout %q, want 1 and nothing", code, stdout.String())
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "error: ") || strings.Count(lines[0], strconv.Quote(reply)) != 1 {
		t.Errorf("stderr %q: want one error line quoting the reply once", stderr.String())
	}
}

func TestReportWarningGoesToStderr(t *testing.T) {
	reply := alteredFrame(t, 17, "+99") // °F far from °C
	var stdout, stderr bytes.Buffer
	code := report(config{format: "text"}, received(reply), &stdout, &stderr)
	if code != 0 || stdout.Len() == 0 {
		t.Errorf("code %d, stdout %q, want 0 and a reading", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "warning") {
		t.Errorf("stdout %q carries a warning", stdout.String())
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "warning: ") || strings.Count(lines[0], strconv.Quote(reply)) != 1 {
		t.Errorf("stderr %q: want one warning line quoting the reply once", stderr.String())
	}
}

func TestReportToSeparatesTextReadingsWithOneBlankLine(t *testing.T) {
	var stdout, stderr bytes.Buffer
	out := newRecordWriter(config{format: "text"}, &stdout)
	for range 3 {
		if code := reportTo(out, received(realReply(t)), &stderr); code != 0 {
			t.Fatalf("code %d; stderr %q", code, stderr.String())
		}
	}
	blocks := strings.Split(stdout.String(), "\n\n")
	if len(blocks) != 3 || strings.HasPrefix(stdout.String(), "\n") || strings.HasSuffix(stdout.String(), "\n\n") {
		t.Errorf("stdout %q, want three blocks with exactly one blank line between them", stdout.String())
	}
}

func TestReportToWritesOneJSONLinePerReading(t *testing.T) {
	var stdout, stderr bytes.Buffer
	out := newRecordWriter(config{format: "json"}, &stdout)
	for range 2 {
		reportTo(out, received(realReply(t)), &stderr)
	}
	if strings.Count(stdout.String(), "\n") != 2 || strings.Contains(stdout.String(), "\n\n") {
		t.Errorf("stdout %q, want two lines and no blank line", stdout.String())
	}
}
