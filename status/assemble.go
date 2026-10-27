// ABOUTME: Assembles a controller's notifications into one whole framed reply,
// ABOUTME: pure logic with no Bluetooth, driven by a per-read token and keys.

package status

import (
	"bytes"
	"errors"
	"fmt"
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
	// ErrKeyBeforeBoundary: a notification's key was not above the request
	// boundary, so it can belong to an earlier read.
	ErrKeyBeforeBoundary = errors.New("notification key is not after the request")
	// ErrStaleToken: a notification was added under an earlier read's
	// token. It is dropped, and the current read is unaffected.
	ErrStaleToken = errors.New("notification belongs to an earlier read")
	// ErrTrailingBytes: bytes arrived after the reply's CRLF.
	ErrTrailingBytes = errors.New("bytes after the reply's CRLF")
	// ErrNotWhole: the reply ended with CRLF but its body is not 32 fields
	// with the last empty.
	ErrNotWhole = errors.New("reply body is not 32 fields ending in an empty one")
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
	mu       sync.Mutex
	tok      Token
	buf      strings.Builder
	boundary uint64 // keys at or below it predate this read's request
	last     uint64 // key of the latest notification added
	err      error  // set once the read has failed; the read is then closed
	// discarded counts the bytes of leading notifications thrown away
	// because the reply had not started (see Add).
	discarded int
	done      bool
}

// NewAssembler returns an Assembler with no read begun.
func NewAssembler() *Assembler {
	return &Assembler{}
}

// Begin starts a fresh read and returns its token. Any earlier read is
// abandoned, and its buffered bytes are discarded. boundary is the key
// ordering position of the read's request: only notifications with a key
// above it can be a reply to it, so keys at or below it fail the read.
func (a *Assembler) Begin(boundary uint64) Token {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tok++
	a.buf.Reset()
	a.boundary = boundary
	a.last = boundary
	a.err = nil
	a.discarded = 0
	a.done = false
	return a.tok
}

// Add adds one notification to the read named by tok. key is the
// notification's position in the controller's order; within a read the keys
// must be strictly increasing and all above the boundary given to Begin.
//
// It returns the reply so far and whether it is now whole. Any error other
// than ErrStaleToken fails the read, and every later Add under its token
// returns the same error. A notification under an earlier token is dropped
// with ErrStaleToken and changes nothing. A zero-length notification adds
// nothing but still takes its place in the key order.
func (a *Assembler) Add(tok Token, key uint64, chunk []byte) (string, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if tok != a.tok {
		return "", false, ErrStaleToken
	}
	if a.err != nil {
		return a.buf.String(), false, a.err
	}
	if a.done {
		return a.fail(ErrTrailingBytes)
	}
	if key <= a.boundary {
		return a.fail(ErrKeyBeforeBoundary)
	}
	if key <= a.last {
		return a.fail(ErrKeyOrder)
	}
	a.last = key
	// A reply starts with a space, and a space occurs nowhere else in it: all
	// 21 replies captured on lola have exactly one, as their first byte, and
	// so does no byte of the afternoon capture's body (TestEveryCapturedReply
	// HasASpaceOnlyAtItsStart). So a space in a notification is where a reply
	// starts, and the bytes before it cannot be part of this read's reply.
	// Until the reply has started they are the tail of an earlier reply, such
	// as one a killed run's request left in the controller. After it has
	// started, a second space means what was taken for the start was the head
	// of such a reply whose later bytes were lost, so the read restarts at the
	// last space, and what it had buffered is discarded too: a reply with a
	// second space is not whole (decision 12), and failing the read instead
	// would lose a reading that is there. Discarded bytes are counted. The cut
	// is at the space and not at the notification: a tail that ends inside a
	// notification leaves that notification beginning with the tail and
	// carrying the reply's first bytes after it. A reply whose first
	// notification is lost is not rescued by this: none of its later ones has
	// a space either, so it never completes.
	if start := replyStart(a.buf.String(), chunk); start >= 0 {
		a.discarded += a.buf.Len() + start
		a.buf.Reset()
		chunk = chunk[start:]
	} else if a.buf.Len() == 0 {
		a.discarded += len(chunk)
		chunk = nil
	}
	a.buf.Write(chunk)
	reply := a.buf.String()
	if strings.Count(reply, ";") >= replyFields {
		return a.fail(ErrTooManyFields)
	}
	end := strings.Index(reply, "\r\n")
	switch {
	case end < 0:
		return reply, false, nil
	case end+2 != len(reply):
		return a.fail(ErrTrailingBytes)
	case !wholeBody(reply[1:end]):
		return a.fail(ErrNotWhole)
	}
	a.done = true
	return reply, true, nil
}

// replyStart returns the offset in chunk of the last space before the end of
// the reply buffered so far plus chunk, or -1 if there is none. Once a reply
// has started, its end is the first CRLF: a space after it is not a start but
// the bytes after a reply that is already complete, which fail the read
// (ErrTrailingBytes). Before one has started, a CRLF is the end of an earlier
// reply's tail and limits nothing.
func replyStart(buffered string, chunk []byte) int {
	region := chunk
	if end := strings.Index(buffered+string(chunk), "\r\n"); buffered != "" && end >= 0 {
		region = chunk[:max(0, min(len(chunk), end+2-len(buffered)))]
	}
	return bytes.LastIndexByte(region, ' ')
}

// wholeBody reports whether body, a reply without its framing, is
// replyFields fields with the last one empty: it ends in ';' and has one
// fewer ';' than fields.
func wholeBody(body string) bool {
	return strings.HasSuffix(body, ";") && strings.Count(body, ";") == replyFields-1
}

// fail closes the read with err, carrying the bytes received so far.
func (a *Assembler) fail(err error) (string, bool, error) {
	reply := a.buf.String()
	if a.discarded > 0 {
		err = fmt.Errorf("%w (discarded %d bytes of an earlier reply's tail before it)", err, a.discarded)
	}
	a.err = &ReplyError{Reply: reply, Err: err}
	return reply, false, a.err
}

// Discarded is how many bytes of the current read were discarded as the tail
// of an earlier reply.
func (a *Assembler) Discarded() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.discarded
}
