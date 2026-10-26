// ABOUTME: Buffers the notifications of the current status read, guarded so
// ABOUTME: a late fragment of one read never lands in the next.

package ble

import (
	"errors"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/perigrin/go-pwr/status"
)

// Why a notification was not added to the read.
var (
	// errBeforeRequest: the notification's key is at or below the request's
	// boundary, so it cannot be the reply to it. It is dropped; the read is
	// unaffected.
	errBeforeRequest = errors.New("notification predates the request")
	// errHoldFull: too many notifications arrived before the request was
	// bounded. The read fails, since a lost notification could leave a gap.
	errHoldFull = errors.New("too many notifications before the request was bounded")
	// errBoundTwice: bound was called twice for one read.
	errBoundTwice = errors.New("read already has its boundary")
)

// maxHeld caps the notifications kept between begin and bound. A reply is
// nine notifications.
const maxHeld = 64

type heldNotification struct {
	key   uint64
	chunk []byte
}

// readBuffer holds the notifications of the current read. Notifications
// arrive on the consumer's goroutine while the reader begins the next read
// and writes its request on another, so everything is under one mutex.
//
// A read is begun before its request is written, and bounded after, once the
// write's reply has a sequence number (the write call's ResponseSequence).
// That number, on the same connection as the notifications, puts the request
// in the notifications' key order: a notification with a key above it was
// sent after the write was acknowledged, one at or below it was not. When a
// notification with key k is handled:
//
//   - before the first begin, or between begin and bound: it is held, since
//     it cannot yet be told apart from a late fragment of the previous read;
//   - at bound(b): each held notification with k <= b is dropped, the rest
//     are added in order, and bound returns the reply so far;
//   - after bound(b): k <= b is dropped, anything above b is added.
//
// A drop is reported to dropped and never fails the read, except that more
// than maxHeld held notifications fail it with errHoldFull. A held notification
// that fails the assembler's own rules (keys not increasing) fails the read
// at bound. begin discards whatever the previous read held or assembled.
type readBuffer struct {
	mu       sync.Mutex
	asm      *status.Assembler
	tok      status.Token
	dropped  func(error)
	bounded  bool
	boundary uint64
	held     []heldNotification
	failed   error // set when the read can no longer be trusted
}

// newReadBuffer returns a buffer with no read begun. dropped, which may be
// nil, hears of every notification that is dropped. It is called with the
// buffer locked and must not call back into it.
func newReadBuffer(dropped func(error)) *readBuffer {
	return &readBuffer{asm: status.NewAssembler(), dropped: dropped}
}

// begin starts a fresh read, abandoning the one before it, before its
// request is written.
func (b *readBuffer) begin() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tok = b.asm.Begin(0)
	b.bounded = false
	b.boundary = 0
	b.held = nil
	b.failed = nil
}

// bound sets the boundary of the current read to the sequence of the
// request's write reply, and returns the reply so far, whether it is whole
// and any error from adding held notifications.
func (b *readBuffer) bound(seq dbus.Sequence) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return "", false, b.failed
	}
	if b.bounded {
		return "", false, errBoundTwice
	}
	b.bounded = true
	b.boundary = uint64(seq)
	b.tok = b.asm.Begin(b.boundary)
	held := b.held
	b.held = nil
	var reply string
	var whole bool
	for _, n := range held {
		var err error
		reply, whole, err = b.addBounded(n.key, n.chunk)
		if err != nil {
			return reply, false, err
		}
	}
	return reply, whole, nil
}

// add handles one notification as described on readBuffer. It returns the
// reply so far and whether it is whole; a dropped or held notification
// returns "", false, nil.
func (b *readBuffer) add(key uint64, chunk []byte) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return "", false, b.failed
	}
	if !b.bounded {
		if len(b.held) >= maxHeld {
			// Losing a held notification could leave a gap that still
			// assembles, so the whole read fails.
			b.failed = fmt.Errorf("%w: key %d", errHoldFull, key)
			b.held = nil
			b.drop(b.failed)
			return "", false, b.failed
		}
		b.held = append(b.held, heldNotification{key, append([]byte(nil), chunk...)})
		return "", false, nil
	}
	return b.addBounded(key, chunk)
}

// addBounded adds a notification once the boundary is known. The caller
// holds the mutex.
func (b *readBuffer) addBounded(key uint64, chunk []byte) (string, bool, error) {
	if key <= b.boundary {
		b.drop(fmt.Errorf("%w: key %d, boundary %d", errBeforeRequest, key, b.boundary))
		return "", false, nil
	}
	return b.asm.Add(b.tok, key, chunk)
}

func (b *readBuffer) drop(err error) {
	if b.dropped != nil {
		b.dropped(err)
	}
}
