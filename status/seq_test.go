// ABOUTME: Test helpers that read the real notification sequences captured on
// ABOUTME: lola (testdata/lola-timing/*.seq.txt) and chunk them.

package status

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
)

// lolaSequence returns the notification payloads of the real framed reply
// captured on lola as session-1-reply-NN (n is 1 to 21), in the order the
// controller sent them. A line is "<microseconds> <hex payload>"; the first
// line is a "# trace:" header.
func lolaSequence(t *testing.T, n int) [][]byte {
	t.Helper()
	name := fmt.Sprintf("testdata/lola-timing/session-1-reply-%02d.seq.txt", n)
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	var chunks [][]byte
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		_, payload, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("%s: malformed line %q", name, line)
		}
		b, err := hex.DecodeString(payload)
		if err != nil {
			t.Fatalf("%s: line %q: %v", name, line, err)
		}
		chunks = append(chunks, b)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return chunks
}

// lolaReply returns the real framed reply session-1-reply-NN as one string.
func lolaReply(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	for _, c := range lolaSequence(t, n) {
		b.Write(c)
	}
	return b.String()
}

// splitEvery cuts s into chunks of size bytes, the last possibly shorter.
// Chunks made this way are derived from a real reply, not captured.
func splitEvery(s string, size int) [][]byte {
	var chunks [][]byte
	for len(s) > 0 {
		n := min(size, len(s))
		chunks = append(chunks, []byte(s[:n]))
		s = s[n:]
	}
	return chunks
}
