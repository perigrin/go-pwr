// ABOUTME: Tests the stderr error and warning lines: one line each, with the
// ABOUTME: received reply quoted as a Go string so it can't break or forge a line.

package status

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// unquoteReply returns the quoted reply at the end of line and the reply it
// unquotes to. It fails the test when line has no such part.
func unquoteReply(t *testing.T, line string) string {
	t.Helper()
	const marker = ` reply "`
	i := strings.LastIndex(line, marker)
	if i < 0 {
		t.Fatalf("line %q has no quoted reply", line)
	}
	quoted := strings.TrimSuffix(line[i+len(" reply "):], "\n")
	got, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("reply part %q does not unquote: %v", quoted, err)
	}
	return got
}

// wantOneLine checks that line is a single newline-terminated line holding
// no control byte, so a terminal or a log reader sees exactly one line.
func wantOneLine(t *testing.T, line string) {
	t.Helper()
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("line %q is not newline-terminated", line)
	}
	for i := 0; i < len(line)-1; i++ {
		if c := line[i]; c < 0x20 || c == 0x7f {
			t.Errorf("line %q holds raw control byte %#x at %d", line, c, i)
			return
		}
	}
}

// cutAfterChunks returns the first n 20-byte notifications of reply, as the
// app's 20-byte chunking would have delivered them.
func cutAfterChunks(reply string, n int) string {
	return reply[:20*n]
}

func TestErrorLineQuotesReply(t *testing.T) {
	cut := cutAfterChunks(capturedReply(t), 8)
	altered := alteredReply(t, indexChargeMA, "0\n\"6")
	_, decodeErr := Decode(altered)
	if decodeErr == nil {
		t.Fatal("Decode of the altered reply succeeded, want an error")
	}
	cases := []struct {
		name  string
		err   error
		reply string
	}{
		{"cut after 8 chunks", &ReplyError{Reply: cut, Err: errors.New("reply is not whole")}, cut},
		{"newline and quote", decodeErr, altered},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := ErrorLine(c.err)
			wantOneLine(t, line)
			if got := unquoteReply(t, line); got != c.reply {
				t.Errorf("reply part unquotes to %q, want %q", got, c.reply)
			}
			if n := strings.Count(line, strconv.Quote(c.reply)); n != 1 {
				t.Errorf("line %q holds the quoted reply %d times, want once", line, n)
			}
		})
	}
}

func TestErrorLineRealFraming(t *testing.T) {
	// Real replies on lola start with the 0x20 echo and end in "\r\n".
	reply := " " + capturedReply(t) + "\r\n"
	line := ErrorLine(&ReplyError{Reply: reply, Err: errors.New("framing")})
	wantOneLine(t, line)
	if got := unquoteReply(t, line); got != reply {
		t.Errorf("reply part unquotes to %q, want %q", got, reply)
	}
}

func TestErrorLineControlBytes(t *testing.T) {
	replies := map[string]string{
		"carriage return": "00655;\r200",
		"NUL":             "00655;\x00;200",
		"ANSI escape":     "\x1b[2Jcleared",
		"non-UTF-8":       "00655;\xff\xfe;200",
		"DEL":             "00655;\x7f",
	}
	for name, reply := range replies {
		t.Run(name, func(t *testing.T) {
			line := ErrorLine(&ReplyError{Reply: reply, Err: errors.New("bad reply")})
			wantOneLine(t, line)
			if got := unquoteReply(t, line); got != reply {
				t.Errorf("reply part unquotes to %q, want %q", got, reply)
			}
		})
	}
}

func TestErrorLineMessageControlBytes(t *testing.T) {
	line := ErrorLine(errors.New("adapter said\nerror: forged\x1b[2J"))
	wantOneLine(t, line)
	if n := strings.Count(line, "\n"); n != 1 {
		t.Errorf("line %q spans %d lines", line, n)
	}
}

func TestErrorLineCannotForgeLine(t *testing.T) {
	forged := []string{
		"\n00655;200;13244;14475;00000;00003",
		"\n{\"time\":\"2026-10-25T15:42:10-04:00\",\"battery_v\":13.24}",
		"\ntime             2026-10-25T15:42:10-04:00\nbattery          13.24 V   AGM",
		"\nerror: everything is fine",
	}
	for _, reply := range forged {
		line := ErrorLine(&ReplyError{Reply: reply, Err: errors.New("bad reply")})
		wantOneLine(t, line)
		if n := strings.Count(line, "\n"); n != 1 {
			t.Errorf("line %q spans %d lines", line, n)
		}
		if got := unquoteReply(t, line); got != reply {
			t.Errorf("reply part unquotes to %q, want %q", got, reply)
		}
	}
}

func TestErrorLineEmptyReply(t *testing.T) {
	line := ErrorLine(&ReplyError{Err: errors.New("timed out waiting for notifications")})
	wantOneLine(t, line)
	if !strings.Contains(line, "no reply received") {
		t.Errorf("line %q does not say nothing arrived", line)
	}
	if strings.Contains(line, `reply "`) {
		t.Errorf("line %q quotes a reply that never arrived", line)
	}
}

func TestErrorLineWithoutReply(t *testing.T) {
	line := ErrorLine(errors.New("bluetooth adapter is powered off"))
	wantOneLine(t, line)
	if strings.Contains(line, "no reply received") || strings.Contains(line, `reply "`) {
		t.Errorf("line %q mentions a reply, but the error is not about one", line)
	}
}

func TestErrorLineWrappedReplyError(t *testing.T) {
	_, err := Decode(alteredReply(t, indexBatteryMV, "x"))
	wrapped := fmt.Errorf("read controller: %w", err)
	line := ErrorLine(wrapped)
	wantOneLine(t, line)
	if got := unquoteReply(t, line); got != alteredReply(t, indexBatteryMV, "x") {
		t.Errorf("reply part unquotes to %q", got)
	}
}

func TestWarningLineQuotesReply(t *testing.T) {
	fields := strings.Split(capturedReply(t), ";")
	fields[indexTempF] = "+99"
	reply := " " + strings.Join(fields, ";") + "\r\n"
	r, err := Decode(reply)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("got %d warnings, want 1", len(r.Warnings))
	}
	line := WarningLine(r.Warnings[0], reply)
	wantOneLine(t, line)
	if !strings.HasPrefix(line, "warning: ") {
		t.Errorf("line %q does not start with warning:", line)
	}
	if !strings.Contains(line, ErrTempMismatch.Error()) {
		t.Errorf("line %q does not say the temperatures disagree", line)
	}
	if got := unquoteReply(t, line); got != reply {
		t.Errorf("reply part unquotes to %q, want %q", got, reply)
	}
}

func TestFieldErrorLineQuotesReply(t *testing.T) {
	reply := alteredReply(t, indexBatteryType, "x")
	r, err := Decode(reply)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(r.FieldErrors) != 1 {
		t.Fatalf("got %d field errors, want 1", len(r.FieldErrors))
	}
	line := ErrorLine(&ReplyError{Reply: r.Raw, Err: r.FieldErrors[0]})
	wantOneLine(t, line)
	if !strings.Contains(line, "battery type") {
		t.Errorf("line %q does not name the field", line)
	}
	if got := unquoteReply(t, line); got != reply {
		t.Errorf("reply part unquotes to %q, want %q", got, reply)
	}
}
