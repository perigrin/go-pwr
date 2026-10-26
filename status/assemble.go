// ABOUTME: Assembles a controller's notifications into one whole framed reply,
// ABOUTME: pure logic with no Bluetooth, driven by a per-read token and keys.

package status

import (
	"errors"
	"strings"
	"sync"
)

// replyFields is how many fields the body of a whole reply has, the last
// one empty after a trailing ';' (decision 0001, Behavior and errors).
const replyFields = 32

// Why a read fails. Each is wrapped in a *ReplyError that carries the bytes
// received so far.
var (
	// ErrKeyOrder: a notification's key was not above the one before it.
	ErrKeyOrder = errors.New("notification keys are not strictly increasing")
	// ErrTooManyFields: the reply has more fields than a whole one, so it
	// can never become whole.
	ErrTooManyFields = errors.New("reply has more than 32 fields")
)

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
	last uint64 // key of the latest notification added
	err  error  // set once the read has failed; the read is then closed
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
	a.last = boundary
	a.err = nil
	a.done = false
	return a.tok
}

// Add adds one notification to the read named by tok. It returns the
// reply so far and whether it is now whole.
func (a *Assembler) Add(tok Token, key uint64, chunk []byte) (string, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.buf.String(), false, a.err
	}
	if key <= a.last {
		return a.fail(ErrKeyOrder)
	}
	a.last = key
	a.buf.Write(chunk)
	reply := a.buf.String()
	if strings.Count(reply, ";") >= replyFields {
		return a.fail(ErrTooManyFields)
	}
	if !strings.HasSuffix(reply, "\r\n") {
		return reply, false, nil
	}
	a.done = true
	return reply, true, nil
}

// fail closes the read with err, carrying the bytes received so far.
func (a *Assembler) fail(err error) (string, bool, error) {
	reply := a.buf.String()
	a.err = &ReplyError{Reply: reply, Err: err}
	return reply, false, a.err
}
