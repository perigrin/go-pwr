// ABOUTME: Tests the field-width backstop: the width table comes only from
// ABOUTME: the real captures in testdata, with the temperature fields variable.

package status

import (
	"encoding/hex"
	"errors"
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

// widthWarnings returns the field width warnings on r.
func widthWarnings(r Reading) []*FieldWidthWarning {
	var ws []*FieldWidthWarning
	for _, w := range r.Warnings {
		if errors.Is(w, ErrFieldWidth) {
			var fw *FieldWidthWarning
			if errors.As(w, &fw) {
				ws = append(ws, fw)
			}
		}
	}
	return ws
}

func TestDecodeWidthMismatchWarns(t *testing.T) {
	r, err := Decode(alteredReply(t, 2, "1324"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	ws := widthWarnings(r)
	if len(ws) != 1 || ws[0].Index != 2 || ws[0].Got != 4 || ws[0].Want != 5 {
		t.Fatalf("warnings = %v, want one for field 2 (4 bytes, want 5)", r.Warnings)
	}
	if r.BatteryMV != 1324 {
		t.Errorf("BatteryMV = %d, want the reading returned as it decoded, 1324", r.BatteryMV)
	}
}

// copyFile copies src to dst, creating dst's directory.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWidthsIgnoreAlteredFiles copies the real captures into a temporary
// directory and adds altered ones, one body-only and one lola-style, with
// every field a different width. The derived table must not change.
func TestWidthsIgnoreAlteredFiles(t *testing.T) {
	root := t.TempDir()
	for _, f := range realCaptureFiles(t, "testdata") {
		rel, err := filepath.Rel("testdata", f)
		if err != nil {
			t.Fatal(err)
		}
		copyFile(t, f, filepath.Join(root, rel))
	}
	want := deriveWidths(t, root)

	wide := strings.ReplaceAll(strings.TrimSpace(capturedReply(t)), ";", "0;")
	for _, name := range []string{"altered-reply-wide.txt", "reply-derived-from-altered.txt", "reply-altered-wide.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(wide), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seq := "# trace: altered\n1 " + hex.EncodeToString([]byte(" "+wide+"\r\n")) + "\n"
	for _, name := range []string{"altered-session-1-reply-99.seq.txt", "session-1-reply-99-altered.seq.txt", "session-1-reply-99-derived.seq.txt"} {
		if err := os.WriteFile(filepath.Join(root, "lola-timing", name), []byte(seq), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := deriveWidths(t, root); got != want {
		t.Errorf("widths with altered files added = %v, want unchanged %v", got, want)
	}
	if want != fieldWidths {
		t.Errorf("copied captures derive %v, want fieldWidths %v", want, fieldWidths)
	}
}

// TestWidthsNoWarningOnRealCaptures decodes every real capture, the body-only
// afternoon reply and the framed lola replies, and expects no warning.
func TestWidthsNoWarningOnRealCaptures(t *testing.T) {
	files := realCaptureFiles(t, "testdata")
	if len(files) != 22 {
		t.Fatalf("%d real captures, want 22", len(files))
	}
	for _, f := range files {
		r, err := Decode(readCapture(t, f))
		if err != nil {
			t.Fatalf("%s: Decode: %v", f, err)
		}
		if len(r.Warnings) != 0 {
			t.Errorf("%s: warnings %v, want none", f, r.Warnings)
		}
	}
}

// TestWidthsTemperaturesVariable gives the three temperature fields widths
// no capture shows. They raise no width warning.
func TestWidthsTemperaturesVariable(t *testing.T) {
	for _, c := range []struct{ c, f, other string }{
		{"-05", "+23", "-05"},
		{"+38", "+100", "+38"},
		{"-40", "-40", "-40"},
		{"5", "+9", "5"},
		{"+0022", "+00072", "+000022"},
	} {
		fields := strings.Split(capturedReply(t), ";")
		fields[indexTempC], fields[indexTempF], fields[indexTempOther] = c.c, c.f, c.other
		r, err := Decode(strings.Join(fields, ";"))
		if err != nil {
			t.Fatalf("Decode %v: %v", c, err)
		}
		if ws := widthWarnings(r); len(ws) != 0 {
			t.Errorf("temperatures %v: width warnings %v, want none", c, ws)
		}
	}
}
