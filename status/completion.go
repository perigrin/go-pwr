// ABOUTME: The give-up timing for a read whose reply never completes, set
// ABOUTME: from the lola capture; pure, with times passed in, never read.

package status

import (
	"errors"
	"time"
)

// A complete reply needs no timer: its CRLF marks it whole (see Assembler).
// The timing below only decides when to give up on a reply that is cut off
// or silent. Each limit is at least TimingMargin times the longest value seen
// in the 21 replies captured on lola (protocol.md, "Completion timing"); each
// constant says what it was set to and why.
const (
	// TimingMargin is the least factor by which a limit must exceed the
	// longest measured value. The capture is one night, one adapter and one
	// controller, so the margin is wide: a slower link must not turn a good
	// reply into a failure, while a dead one still fails within seconds.
	TimingMargin = 10

	// FirstNotificationTimeout is how long after the request the first
	// notification may take. Measured longest: 96 ms. Set to 2 s, about 21x,
	// to match the Go Power! app's own 2000 ms give-up (protocol.md, Status
	// request), the longest wait the controller is known to be used with.
	FirstNotificationTimeout = 2 * time.Second

	// GapTimeout is how long a reply may go quiet between two notifications.
	// Measured longest: 88 ms. Set to 1 s, about 11x, the smallest whole
	// second at or above TimingMargin times the longest gap.
	GapTimeout = 1 * time.Second

	// TotalTimeout is how long after the request a reply may take to
	// complete. Measured longest: 202 ms. Set to 3 s, about 15x: the smallest
	// whole second at or above TimingMargin times the longest reply, and long
	// enough to hold the full FirstNotificationTimeout plus a GapTimeout.
	TotalTimeout = 3 * time.Second
)

// Why a read gives up. Each is wrapped in a *ReplyError that carries the
// bytes received so far, so the error line quotes them.
var (
	// ErrNoReply: nothing arrived within FirstNotificationTimeout of the
	// request, or within the read's limit.
	ErrNoReply = errors.New("no reply to the request")
	// ErrIncompleteReply: a reply started but did not complete in time.
	ErrIncompleteReply = errors.New("incomplete reply")
)

// Completion decides when one read gives up. It holds no clock: the caller
// passes the time of the request, of each notification, and of each check,
// so it can be driven by real time or by a captured timestamp. It is not
// safe for concurrent use.
type Completion struct {
	request time.Time
	limit   time.Time // no deadline is later than this; zero means none
	lastAt  time.Time // when the latest notification arrived; zero if none
}

// NewCompletion starts the timing for a read whose request was written at
// request. limit, if not zero, is the end of the read's budget (from
// --timeout): the read never waits past it.
func NewCompletion(request, limit time.Time) *Completion {
	return &Completion{request: request, limit: limit}
}

// Notified records that a notification arrived at at.
func (c *Completion) Notified(at time.Time) {
	c.lastAt = at
}

// Deadline returns when the read gives up unless another notification
// arrives first: FirstNotificationTimeout after the request while nothing
// has arrived, then GapTimeout after the latest notification, and never
// TotalTimeout after the request or past the limit.
func (c *Completion) Deadline() time.Time {
	d := c.request.Add(FirstNotificationTimeout)
	if !c.lastAt.IsZero() {
		d = c.lastAt.Add(GapTimeout)
		if total := c.request.Add(TotalTimeout); total.Before(d) {
			d = total
		}
	}
	if !c.limit.IsZero() && c.limit.Before(d) {
		d = c.limit
	}
	return d
}

// Check returns nil while the read may keep waiting at now. At or after the
// Deadline it returns a *ReplyError carrying reply, the bytes that arrived:
// ErrNoReply if there are none, ErrIncompleteReply if there are some.
func (c *Completion) Check(now time.Time, reply string) error {
	if now.Before(c.Deadline()) {
		return nil
	}
	if reply == "" {
		return &ReplyError{Err: ErrNoReply}
	}
	return &ReplyError{Reply: reply, Err: ErrIncompleteReply}
}
