// ABOUTME: Tests that a read discards leading notifications that cannot start
// ABOUTME: a reply, such as the tail of a reply an earlier run left behind.

package status

import (
	"strings"
	"testing"
)

// staleTail is a real 20-byte notification from the middle of another real
// reply: the tail a killed run's request leaves for the next run.
func staleTail(t *testing.T) []byte {
	t.Helper()
	return lolaSequence(t, 2)[4]
}

// A reply's body has no space in it: a space is only ever the first byte,
// which is what lets a read tell a reply's start from a stale fragment.
func TestEveryCapturedReplyHasASpaceOnlyAtItsStart(t *testing.T) {
	for n := 1; n <= 21; n++ {
		reply := lolaReply(t, n)
		if !strings.HasPrefix(reply, " ") || strings.Contains(reply[1:], " ") {
			t.Errorf("reply %d: want exactly one space, at the start: %q", n, reply)
		}
	}
	if strings.Contains(capturedReply(t), " ") {
		t.Error("the afternoon capture's body has a space in it")
	}
}

func TestAssembleDiscardsStaleLeadingChunk(t *testing.T) {
	chunks := append([][]byte{staleTail(t)}, lolaSequence(t, 1)...)
	a := NewAssembler()
	tok := a.Begin(0)
	reply, whole := addAll(t, a, tok, 1, chunks)
	if !whole || reply != lolaReply(t, 1) {
		t.Errorf("whole=%v reply=%q, want the real reply alone", whole, reply)
	}
	if got := a.Discarded(); got != 20 {
		t.Errorf("Discarded = %d, want 20", got)
	}
}

func TestAssembleDiscardsSeveralStaleChunks(t *testing.T) {
	old := lolaSequence(t, 2)
	chunks := append([][]byte{old[6], old[7], old[8]}, lolaSequence(t, 1)...)
	a := NewAssembler()
	reply, whole := addAll(t, a, a.Begin(0), 1, chunks)
	if !whole || reply != lolaReply(t, 1) {
		t.Errorf("whole=%v reply=%q, want the real reply alone", whole, reply)
	}
}

// A reply whose real first chunk is missing is not rescued by what follows
// it: every later chunk lacks a leading space, so nothing is accepted.
func TestAssembleFirstChunkMissingNeverWhole(t *testing.T) {
	a := NewAssembler()
	reply, whole := addAll(t, a, a.Begin(0), 1, lolaSequence(t, 1)[1:])
	if whole || reply != "" {
		t.Errorf("whole=%v reply=%q, want nothing accepted", whole, reply)
	}
	if got := a.Discarded(); got != 169-20 {
		t.Errorf("Discarded = %d, want %d", got, 169-20)
	}
}

// Once a chunk starting with a space has begun the reply, a later chunk
// without one is part of it, as before.
func TestAssembleMidReplyChunkWithoutSpaceStaysInReply(t *testing.T) {
	a := NewAssembler()
	chunks := lolaSequence(t, 1)
	reply, whole := addAll(t, a, a.Begin(0), 1, chunks)
	if !whole || reply != lolaReply(t, 1) || a.Discarded() != 0 {
		t.Errorf("whole=%v discarded=%d reply=%q", whole, a.Discarded(), reply)
	}
}

// A read that fails after discarding says so, since the stale tail is part of
// why it failed.
func TestAssembleFailureAfterDiscardSaysSo(t *testing.T) {
	a := NewAssembler()
	tok := a.Begin(0)
	chunks := append([][]byte{staleTail(t)}, splitEvery(alteredOver32(t), 20)...)
	var err error
	for i, c := range chunks {
		if _, _, err = a.Add(tok, uint64(i+1), c); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("never failed")
	}
	if !strings.Contains(err.Error(), "discarded 20 bytes") {
		t.Errorf("error %q does not say 20 bytes were discarded", err)
	}
	// A space inside the body would restart the reply at it, and then the
	// discarded bytes are the current reply's, so the error says only where
	// they were, not whose.
	if !strings.Contains(err.Error(), "before the reply's last space") || strings.Contains(err.Error(), "earlier reply") {
		t.Errorf("error %q, want it to say the bytes came before the reply's last space without naming whose they were", err)
	}
}

// A stale tail that ends inside a notification shifts the real reply off the
// controller's usual 20-byte cuts: the notification that carries the reply's
// first byte also carries the tail's last bytes, so it does not begin with a
// space. Seen on lola after a SIGKILL: 198 bytes, a 29-byte tail and a whole
// reply, and no reply accepted. The stream is the last 29 bytes of one real
// reply followed by another real reply, cut every 20 bytes.
func TestAssembleStaleTailEndingMidNotification(t *testing.T) {
	old := lolaReply(t, 2)
	tail := old[len(old)-29:]
	stream := tail + lolaReply(t, 1)
	a := NewAssembler()
	reply, whole := addAll(t, a, a.Begin(0), 1, splitEvery(stream, 20))
	if !whole || reply != lolaReply(t, 1) {
		t.Errorf("whole=%v reply=%q, want the real reply alone", whole, reply)
	}
	if got := a.Discarded(); got != 29 {
		t.Errorf("Discarded = %d, want 29", got)
	}
}

// A stale head that began a reply, whose later bytes were lost, followed by a
// real reply: a second space inside the buffer means the first was not this
// read's reply, so the read restarts at it and counts what it threw away
// (decision 12: a whole reply is one space, 166 bytes, CRLF). Seen as a
// 174-byte "whole" reply that only Decode rejected. Each stream is a prefix
// of one real reply followed by another real reply, cut every 20 bytes, so
// both are altered data.
func TestAssembleSecondSpaceRestartsTheReply(t *testing.T) {
	old := lolaReply(t, 2)
	for _, tc := range []struct {
		name string
		head int // bytes of the stale reply's start that precede the real one
	}{
		{"altered-stale-head-5-bytes-inside-first-notification", 5},
		{"altered-stale-head-20-bytes-ends-at-notification-edge", 20},
		{"altered-stale-head-45-bytes-spans-notifications", 45},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := old[:tc.head] + lolaReply(t, 1)
			a := NewAssembler()
			reply, whole := addAll(t, a, a.Begin(0), 1, splitEvery(stream, 20))
			if !whole || reply != lolaReply(t, 1) || len(reply) != 169 {
				t.Errorf("whole=%v len=%d reply=%q, want the real 169-byte reply alone", whole, len(reply), reply)
			}
			if got := a.Discarded(); got != tc.head {
				t.Errorf("Discarded = %d, want %d", got, tc.head)
			}
		})
	}
}
