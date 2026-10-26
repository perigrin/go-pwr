// ABOUTME: Tests the field-width backstop: the width table comes only from
// ABOUTME: the real captures in testdata, with the temperature fields variable.

package status

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realCaptureFiles returns the real captures under root: the body-only
// replies (root/reply-*.txt) and the lola notification sequences
// (root/lola-timing/session-*-reply-*.seq.txt). A file whose name says it was
// altered or derived is never one, so an altered copy added to testdata
// cannot move the width table.
func realCaptureFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, pattern := range []string{"reply-*.txt", "lola-timing/session-*-reply-*.seq.txt"} {
		m, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		for _, f := range m {
			name := filepath.Base(f)
			if strings.Contains(name, "altered") || strings.Contains(name, "derived") {
				continue
			}
			files = append(files, f)
		}
	}
	return files
}

// readCapture returns the reply in a capture file as one string: a .seq.txt
// file is its notifications joined in order, anything else is the file as is.
func readCapture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if !strings.HasSuffix(name, ".seq.txt") {
		return string(b)
	}
	var reply strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, payload, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("%s: malformed line %q", name, line)
		}
		raw, err := hex.DecodeString(payload)
		if err != nil {
			t.Fatalf("%s: line %q: %v", name, line, err)
		}
		reply.Write(raw)
	}
	return reply.String()
}

// deriveWidths measures every field of every real capture under root. A
// field keeps its width when all captures agree, and is variableWidth when
// they differ or when it is a temperature.
func deriveWidths(t *testing.T, root string) [replyFields]int {
	t.Helper()
	files := realCaptureFiles(t, root)
	if len(files) == 0 {
		t.Fatalf("no real captures under %s", root)
	}
	var widths [replyFields]int
	for i, f := range files {
		fields := strings.Split(replyBody(readCapture(t, f)), ";")
		if len(fields) != replyFields {
			t.Fatalf("%s: %d fields, want %d", f, len(fields), replyFields)
		}
		for j, field := range fields {
			switch {
			case i == 0:
				widths[j] = len(field)
			case widths[j] != len(field):
				widths[j] = variableWidth
			}
		}
	}
	for _, j := range []int{indexTempC, indexTempF, indexTempOther} {
		widths[j] = variableWidth
	}
	return widths
}

func TestWidthsFromCapturesOnly(t *testing.T) {
	got := deriveWidths(t, "testdata")
	if got != fieldWidths {
		t.Errorf("fieldWidths = %v\nderived from the real captures = %v", fieldWidths, got)
	}
	for _, i := range []int{16, 17, 18} {
		if fieldWidths[i] != variableWidth {
			t.Errorf("field %d width = %d, want variable", i, fieldWidths[i])
		}
	}
	if fieldWidths[0] != 5 || fieldWidths[1] != 3 || fieldWidths[31] != 0 {
		t.Errorf("fieldWidths = %v, want widths 5, 3 ... 0 for fields 0, 1, 31", fieldWidths)
	}
}
