// ABOUTME: Tests assembling notifications into a whole reply, using the real
// ABOUTME: framed replies captured on lola and altered or re-chunked copies.

package status

import (
	"testing"
)

// addAll adds chunks to the read under tok with keys first, first+1, ...
// and returns the reply and whether it was whole after the last one. It
// fails the test if any chunk before the last reports whole, or errors.
func addAll(t *testing.T, a *Assembler, tok Token, first uint64, chunks [][]byte) (string, bool) {
	t.Helper()
	var reply string
	var whole bool
	for i, c := range chunks {
		var err error
		reply, whole, err = a.Add(tok, first+uint64(i), c)
		if err != nil {
			t.Fatalf("Add chunk %d of %d: %v", i+1, len(chunks), err)
		}
		if whole && i < len(chunks)-1 {
			t.Fatalf("whole after chunk %d of %d", i+1, len(chunks))
		}
	}
	return reply, whole
}

func TestAssembleCapturedChunksInOrder(t *testing.T) {
	chunks := lolaSequence(t, 1)
	if len(chunks) != 9 {
		t.Fatalf("reply-01 has %d notifications, want 9", len(chunks))
	}
	a := NewAssembler()
	tok := a.Begin(0)
	reply, whole := addAll(t, a, tok, 1, chunks)
	if !whole {
		t.Fatal("not whole after the 9th chunk")
	}
	if want := lolaReply(t, 1); reply != want {
		t.Errorf("reply = %q, want %q", reply, want)
	}
}

func TestAssembleTokenStartsFreshRead(t *testing.T) {
	a := NewAssembler()
	first := a.Begin(0)
	addAll(t, a, first, 1, lolaSequence(t, 1)[:5])
	second := a.Begin(5)
	reply, whole := addAll(t, a, second, 6, lolaSequence(t, 2))
	if !whole {
		t.Fatal("second read not whole")
	}
	if want := lolaReply(t, 2); reply != want {
		t.Errorf("reply = %q, want exactly reply-02 %q", reply, want)
	}
}
