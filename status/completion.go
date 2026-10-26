// ABOUTME: The give-up timing for a read whose reply never completes, set
// ABOUTME: from the lola capture; pure, with times passed in, never read.

package status

import "time"

// A complete reply needs no timer: its CRLF marks it whole (see Assembler).
// The timing below only decides when to give up on a reply that is cut off
// or silent. Each limit is the longest value seen in the 21 replies captured
// on lola (protocol.md, "Completion timing") times TimingMargin, rounded up
// to a whole number of seconds or half seconds.
const (
	// TimingMargin is the least factor by which a limit must exceed the
	// longest measured value. The capture is one night, one adapter and one
	// controller, so the margin is wide: a slower link must not turn a good
	// reply into a failure, while a dead one still fails within seconds.
	TimingMargin = 10

	// FirstNotificationTimeout is how long after the request the first
	// notification may take. Measured longest: 96 ms (x10 = 960 ms).
	FirstNotificationTimeout = 2 * time.Second

	// GapTimeout is how long a reply may go quiet between two notifications.
	// Measured longest: 88 ms (x10 = 880 ms).
	GapTimeout = 1 * time.Second

	// TotalTimeout is how long after the request a reply may take to
	// complete. Measured longest: 202 ms (x10 = 2.02 s).
	TotalTimeout = 3 * time.Second
)
