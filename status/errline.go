// ABOUTME: Formats the stderr error and warning lines. A received reply goes
// ABOUTME: in as a quoted Go string, so its bytes can't break or forge a line.

package status

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ReplyError is an error about a reply that was received, or about one that
// never arrived (an empty Reply). Its message does not include the reply:
// ErrorLine and WarningLine add the reply once, quoted, so the quoting is
// in one place.
type ReplyError struct {
	Reply string // the reply as received; empty when nothing arrived
	Err   error
}

func (e *ReplyError) Error() string { return e.Err.Error() }

func (e *ReplyError) Unwrap() error { return e.Err }

// ErrorLine returns err as one newline-terminated line for stderr. When err
// is, or wraps, a *ReplyError, the line ends with the reply as a quoted Go
// string, or says that no reply was received when there was none. Control
// bytes in the message are escaped, so the line is always one line.
func ErrorLine(err error) string {
	var re *ReplyError
	if !errors.As(err, &re) {
		return line("error: ", err.Error(), "")
	}
	return line("error: ", err.Error(), replyPart(re.Reply))
}

// WarningLine returns warn as one newline-terminated line for stderr,
// ending with the reply the warning concerns as a quoted Go string.
func WarningLine(warn error, reply string) string {
	return line("warning: ", warn.Error(), replyPart(reply))
}

// replyPart describes reply for the end of a line: its quoted form, or a
// statement that nothing arrived.
func replyPart(reply string) string {
	if reply == "" {
		return "no reply received"
	}
	return "reply " + strconv.Quote(reply)
}

// line joins prefix, the escaped message and the optional reply part into
// one newline-terminated line.
func line(prefix, msg, reply string) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(escapeUnprintable(msg))
	if reply != "" {
		b.WriteString(": ")
		b.WriteString(reply)
	}
	b.WriteByte('\n')
	return b.String()
}

// escapeUnprintable replaces every rune that strconv.IsPrint rejects, and
// every invalid UTF-8 byte, with its Go escape.
func escapeUnprintable(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 || !strconv.IsPrint(r) {
			q := strconv.Quote(s[i : i+size])
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
