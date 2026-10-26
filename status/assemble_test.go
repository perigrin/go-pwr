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

func TestAssembleCutOffNeverComplete(t *testing.T) {
	chunks := lolaSequence(t, 1)
	var prefix strings.Builder
	for k := 1; k <= 8; k++ {
		prefix.Write(chunks[k-1])
		a := NewAssembler()
		tok := a.Begin(0)
		reply, whole := addAll(t, a, tok, 1, chunks[:k])
		if whole {
			t.Errorf("%d of 9 chunks reported whole", k)
		}
		if reply != prefix.String() {
			t.Errorf("%d chunks: reply = %q, want the prefix %q", k, reply, prefix.String())
		}
	}
	// The 8-chunk prefix is the case worth guarding: it already splits into
	// 30 fields, but it has no CRLF.
	if n := len(strings.Split(prefix.String(), ";")); n != 30 {
		t.Errorf("8-chunk prefix splits into %d fields, want 30", n)
	}
}

func TestAssembleLateOldTokenNotificationDropped(t *testing.T) {
	old := lolaSequence(t, 1)
	a := NewAssembler()
	first := a.Begin(0)
	addAll(t, a, first, 1, old[:5])
	second := a.Begin(5)
	want := lolaSequence(t, 2)
	var reply string
	var whole bool
	for i, c := range want {
		if i == 4 {
			// The old read's 6th notification arrives in the middle of
			// the new read.
			_, _, err := a.Add(first, 6, old[5])
			if !errors.Is(err, ErrStaleToken) {
				t.Fatalf("late old-token Add: err = %v, want ErrStaleToken", err)
			}
		}
		var err error
		reply, whole, err = a.Add(second, uint64(6+i), c)
		if err != nil {
			t.Fatalf("chunk %d: %v", i+1, err)
		}
	}
	if !whole || reply != lolaReply(t, 2) {
		t.Errorf("whole=%v reply=%q, want exactly reply-02 %q", whole, reply, lolaReply(t, 2))
	}
}

func TestAssembleRejectsKeysBeforeRequestBoundary(t *testing.T) {
	old := lolaSequence(t, 1)
	next := lolaSequence(t, 2)
	const boundary = 100
	spliced32 := 0
	for k := 1; k <= 8; k++ {
		// Tail of the earlier read, then head of this one, with keys at or
		// below the request boundary.
		var splice [][]byte
		splice = append(splice, old[k:]...)
		splice = append(splice, next[:k]...)
		var joined strings.Builder
		for _, c := range splice {
			joined.Write(c)
		}
		if strings.Count(joined.String(), ";") == replyFields-1 {
			spliced32++
		}
		a := NewAssembler()
		tok := a.Begin(boundary)
		for i, c := range splice {
			key := uint64(boundary - len(splice) + 1 + i)
			_, whole, err := a.Add(tok, key, c)
			if whole {
				t.Fatalf("k=%d: splice reported whole at chunk %d", k, i+1)
			}
			if i == 0 && !errors.Is(err, ErrKeyBeforeBoundary) {
				t.Fatalf("k=%d: first splice chunk: err = %v, want ErrKeyBeforeBoundary", k, err)
			}
		}
	}
	if spliced32 == 0 {
		t.Error("no splice adds up to 32 fields, so the test shows nothing")
	}
	// A key equal to the boundary is before the request too.
	a := NewAssembler()
	tok := a.Begin(boundary)
	if _, _, err := a.Add(tok, boundary, next[0]); !errors.Is(err, ErrKeyBeforeBoundary) {
		t.Errorf("key == boundary: err = %v, want ErrKeyBeforeBoundary", err)
	}
}

func TestAssembleDuplicateKeyFails(t *testing.T) {
	chunks := lolaSequence(t, 1)
	a := NewAssembler()
	tok := a.Begin(0)
	addAll(t, a, tok, 1, chunks[:3])
	_, whole, err := a.Add(tok, 3, chunks[3])
	if !errors.Is(err, ErrKeyOrder) || whole {
		t.Errorf("repeated key: whole=%v err=%v, want ErrKeyOrder", whole, err)
	}
}

func TestAssembleEmptyNotification(t *testing.T) {
	chunks := lolaSequence(t, 1)
	a := NewAssembler()
	tok := a.Begin(0)
	var key uint64
	next := func() uint64 { key++; return key }
	for _, empty := range [][]byte{nil, {}} {
		reply, whole, err := a.Add(tok, next(), empty)
		if whole || err != nil || reply != "" {
			t.Fatalf("empty first: reply=%q whole=%v err=%v", reply, whole, err)
		}
	}
	var reply string
	var whole bool
	for i, c := range chunks {
		var err error
		reply, whole, err = a.Add(tok, next(), c)
		if err != nil {
			t.Fatal(err)
		}
		if i == 3 {
			if r, w, err := a.Add(tok, next(), nil); w || err != nil || r != reply {
				t.Fatalf("empty mid-read: reply=%q whole=%v err=%v", r, w, err)
			}
		}
	}
	if !whole || reply != lolaReply(t, 1) {
		t.Errorf("whole=%v reply=%q, want reply-01", whole, reply)
	}
}

func TestAssembleRequiresTrailingSemicolon(t *testing.T) {
	// Real reply-01 with text appended after the final ';': the body is still
	// 32 fields, but the last one is not empty.
	reply := lolaReply(t, 1)
	altered := strings.TrimSuffix(reply, ";\r\n") + ";x\r\n"
	a := NewAssembler()
	tok := a.Begin(0)
	for i, c := range splitEvery(altered, 20) {
		_, whole, err := a.Add(tok, uint64(i+1), c)
		if whole {
			t.Fatalf("whole at chunk %d with a non-empty last field", i+1)
		}
		if err != nil {
			if !errors.Is(err, ErrNotWhole) {
				t.Errorf("err = %v, want ErrNotWhole", err)
			}
			return
		}
	}
	t.Error("a reply with a non-empty last field never failed")
}

func TestAssembleAnyChunkSize(t *testing.T) {
	// Chunks derived from real reply-01 at sizes the controller didn't use.
	want := lolaReply(t, 1)
	for _, size := range []int{1, 20, 23, 182, 244} {
		a := NewAssembler()
		tok := a.Begin(0)
		reply, whole := addAll(t, a, tok, 1, splitEvery(want, size))
		if !whole || reply != want {
			t.Errorf("size %d: whole=%v reply=%q, want %q", size, whole, reply, want)
		}
	}
}

func TestAssembleRequiresFraming(t *testing.T) {
	body := capturedReply(t) // the afternoon capture: body only, no framing
	a := NewAssembler()
	tok := a.Begin(0)
	for i, c := range splitEvery(body, 20) {
		_, whole, err := a.Add(tok, uint64(i+1), c)
		if whole {
			t.Fatal("an unframed body was reported whole")
		}
		if i == 0 && !errors.Is(err, ErrNotFramed) {
			t.Fatalf("unframed first chunk: err = %v, want ErrNotFramed", err)
		}
	}
	// A leading space and no CRLF is cut off, however many bytes arrive.
	a = NewAssembler()
	tok = a.Begin(0)
	reply, whole := addAll(t, a, tok, 1, splitEvery(" "+body, 20))
	if whole {
		t.Errorf("space and body without CRLF reported whole: %q", reply)
	}
}

func TestAssembleBytesAfterCRLFNotWhole(t *testing.T) {
	reply := lolaReply(t, 1)
	// The CRLF and the start of another reply arrive in one read.
	a := NewAssembler()
	tok := a.Begin(0)
	chunks := lolaSequence(t, 1)
	last := append(append([]byte{}, chunks[8]...), []byte(" 000")...)
	for i := 0; i < 8; i++ {
		if _, _, err := a.Add(tok, uint64(i+1), chunks[i]); err != nil {
			t.Fatal(err)
		}
	}
	got, whole, err := a.Add(tok, 9, last)
	if whole || !errors.Is(err, ErrTrailingBytes) {
		t.Errorf("bytes after CRLF: whole=%v err=%v, want ErrTrailingBytes", whole, err)
	}
	if got == reply {
		t.Error("the failed read returned the clean reply")
	}
	// Once whole, the read is closed: another notification is an error.
	a = NewAssembler()
	tok = a.Begin(0)
	addAll(t, a, tok, 1, chunks)
	if _, whole, err := a.Add(tok, 10, []byte(" ")); whole || !errors.Is(err, ErrTrailingBytes) {
		t.Errorf("Add after whole: whole=%v err=%v, want ErrTrailingBytes", whole, err)
	}
}

// TestAssembleEveryLolaReplyDecodes assembles all 21 real replies from their
// notifications and decodes each one.
func TestAssembleEveryLolaReplyDecodes(t *testing.T) {
	a := NewAssembler()
	var key uint64
	for n := 1; n <= 21; n++ {
		tok := a.Begin(key)
		var reply string
		var whole bool
		for _, c := range lolaSequence(t, n) {
			key++
			var err error
			if reply, whole, err = a.Add(tok, key, c); err != nil {
				t.Fatalf("reply-%02d: %v", n, err)
			}
		}
		if !whole || reply != lolaReply(t, n) {
			t.Fatalf("reply-%02d: whole=%v reply=%q", n, whole, reply)
		}
		r, err := Decode(reply)
		if err != nil {
			t.Fatalf("reply-%02d: Decode: %v", n, err)
		}
		if r.Raw != reply || len(r.FieldErrors) != 0 {
			t.Errorf("reply-%02d: Raw matches=%v FieldErrors=%v", n, r.Raw == reply, r.FieldErrors)
		}
	}
}

// TestAssembleConcurrentBeginAndAdd has notifications arrive on another
// goroutine while reads begin, for the race detector. Every outcome is
// either a clean add or a stale-token drop.
func TestAssembleConcurrentBeginAndAdd(t *testing.T) {
	a := NewAssembler()
	chunks := lolaSequence(t, 1)
	done := make(chan error)
	toks := make(chan Token)
	go func() {
		var key uint64
		for tok := range toks {
			for _, c := range chunks {
				key++
				if _, _, err := a.Add(tok, key, c); err != nil && !errors.Is(err, ErrStaleToken) {
					done <- err
					return
				}
			}
		}
		done <- nil
	}()
	var key uint64
	for i := 0; i < 50; i++ {
		toks <- a.Begin(key)
		key += uint64(len(chunks))
	}
	close(toks)
	if err := <-done; err != nil {
		t.Errorf("Add: %v", err)
	}
}
