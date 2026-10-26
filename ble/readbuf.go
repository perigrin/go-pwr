// ABOUTME: Buffers the notifications of the current status read, guarded so
// ABOUTME: a late fragment of one read never lands in the next.

package ble

import (
	"sync"

	"github.com/perigrin/go-pwr/status"
)

// readBuffer holds the notifications of the current read. Notifications
// arrive on the consumer's goroutine while the reader begins the next read on
// another, so every read takes a fresh token and the status.Assembler drops
// anything added under an earlier one. The mutex makes "the current token"
// and the add that uses it one step, so a notification handled before begin
// returns belongs to the read that was current when it was handled. A late
// fragment that is handled after begin is told apart by its key instead: it
// predates the request, which begin takes as its boundary.
type readBuffer struct {
	mu  sync.Mutex
	asm *status.Assembler
	tok status.Token
}

func newReadBuffer() *readBuffer {
	return &readBuffer{asm: status.NewAssembler()}
}

// begin starts a fresh read, abandoning the one before it. boundary is the
// key of the request: notifications with a key at or below it can't be its
// reply.
func (b *readBuffer) begin(boundary uint64) status.Token {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tok = b.asm.Begin(boundary)
	return b.tok
}

// add adds a notification to the current read.
func (b *readBuffer) add(key uint64, chunk []byte) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.asm.Add(b.tok, key, chunk)
}

// addTo adds a notification to the read named by tok. If that read is no
// longer the current one the notification is dropped with
// status.ErrStaleToken.
func (b *readBuffer) addTo(tok status.Token, key uint64, chunk []byte) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.asm.Add(tok, key, chunk)
}
