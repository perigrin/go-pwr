// ABOUTME: Tests assembling notifications into a whole reply, using the real
// ABOUTME: framed replies captured on lola and altered or re-chunked copies.

package status

import (
	"errors"
	"strings"
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

func TestAssembleRequiresIncreasingKeys(t *testing.T) {
	chunks := lolaSequence(t, 1)
	a := NewAssembler()
	tok := a.Begin(0)
	for i, key := range []uint64{1, 2, 3} {
		if _, _, err := a.Add(tok, key, chunks[i]); err != nil {
			t.Fatalf("Add key %d: %v", key, err)
		}
	}
	_, whole, err := a.Add(tok, 2, chunks[3])
	if !errors.Is(err, ErrKeyOrder) {
		t.Fatalf("Add with a decreasing key: err = %v, want ErrKeyOrder", err)
	}
	if whole {
		t.Error("whole after a decreasing key")
	}
	// The read has failed: later chunks with good keys don't revive it.
	for i := 4; i < len(chunks); i++ {
		if _, whole, err := a.Add(tok, uint64(10+i), chunks[i]); err == nil || whole {
			t.Fatalf("Add after a failed read: whole=%v err=%v, want an error", whole, err)
		}
	}
}

// alteredOver32 is the real reply session-1-reply-01 with "008;" glued on
// after the trailing ';', which makes its body 33 fields.
func alteredOver32(t *testing.T) string {
	t.Helper()
	reply := lolaReply(t, 1)
	return strings.TrimSuffix(reply, "\r\n") + "008;\r\n"
}

func TestAssembleOver32FieldsFailsAtOnce(t *testing.T) {
	reply := alteredOver32(t)
	if n := strings.Count(reply, ";"); n != replyFields {
		t.Fatalf("altered reply has %d semicolons, want %d", n, replyFields)
	}
	chunks := splitEvery(reply, 20)
	a := NewAssembler()
	tok := a.Begin(0)
	// The 33rd field's separator is the 32nd ';', in the chunk holding "008;".
	failAt := -1
	semicolons := 0
	for i, c := range chunks {
		semicolons += strings.Count(string(c), ";")
		if semicolons > replyFields-1 {
			failAt = i
			break
		}
	}
	for i, c := range chunks {
		_, whole, err := a.Add(tok, uint64(i+1), c)
		if whole {
			t.Fatalf("whole at chunk %d", i+1)
		}
		switch {
		case i < failAt && err != nil:
			t.Fatalf("chunk %d failed early: %v", i+1, err)
		case i == failAt:
			if !errors.Is(err, ErrTooManyFields) {
				t.Fatalf("chunk %d: err = %v, want ErrTooManyFields", i+1, err)
			}
			return
		}
	}
	t.Fatal("never failed")
}
