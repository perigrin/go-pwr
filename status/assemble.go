// ABOUTME: Assembles a controller's notifications into one whole framed reply,
// ABOUTME: pure logic with no Bluetooth, driven by a per-read token and keys.

package status

import (
	"strings"
	"sync"
)

// replyFields is how many fields the body of a whole reply has, the last
// one empty after a trailing ';' (decision 0001, Behavior and errors).
const replyFields = 32

// Token names one read. Notifications added under an earlier token never
// become part of a later read.
type Token uint64

// Assembler turns the notifications of one read at a time into a whole
// reply. A whole reply starts with a space, ends with CRLF, and has a body
// of replyFields fields between them; the CRLF marks it complete. The
// Assembler is safe for concurrent use, because notifications arrive on
// another goroutine than the one that begins the read.
type Assembler struct {
	mu   sync.Mutex
	tok  Token
	buf  strings.Builder
	done bool
}

// NewAssembler returns an Assembler with no read begun.
func NewAssembler() *Assembler {
	return &Assembler{}
}

// Begin starts a fresh read and returns its token. Any earlier read is
// abandoned, and its buffered bytes are discarded.
func (a *Assembler) Begin(boundary uint64) Token {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tok++
	a.buf.Reset()
	a.done = false
	return a.tok
}

// Add adds one notification to the read named by tok. It returns the
// reply so far and whether it is now whole.
func (a *Assembler) Add(tok Token, key uint64, chunk []byte) (string, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.buf.Write(chunk)
	reply := a.buf.String()
	if !strings.HasSuffix(reply, "\r\n") {
		return reply, false, nil
	}
	a.done = true
	return reply, true, nil
}
