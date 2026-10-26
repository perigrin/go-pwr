// ABOUTME: Tests that the read buffer keeps each read to its own notifications,
// ABOUTME: using the request boundary taken from the write's reply sequence.

package ble

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// realReply returns the real chunks and whole text of captured reply n.
func realReply(t *testing.T, n int) ([][]byte, string) {
	t.Helper()
	chunks := lolaChunks(t, n)
	var want strings.Builder
	for _, c := range chunks {
		want.Write(c)
	}
	return chunks, want.String()
}

// dropCounter returns a buffer whose dropped notifications are counted.
func dropCounter() (*readBuffer, *[]error) {
	var mu sync.Mutex
	var drops []error
	b := newReadBuffer(func(err error) {
		mu.Lock()
		defer mu.Unlock()
		drops = append(drops, err)
	})
	return b, &drops
}

// feed adds chunks with keys first, first+1, ... and returns the last result.
func feed(t *testing.T, b *readBuffer, first uint64, chunks [][]byte) (string, bool) {
	t.Helper()
	var reply string
	var whole bool
	for i, c := range chunks {
		var err error
		reply, whole, err = b.add(first+uint64(i), c)
		if err != nil {
			t.Fatalf("add key %d: %v", first+uint64(i), err)
		}
	}
	return reply, whole
}

// TestReadBufferGuard: a late fragment of read N, handled after read N+1
// has begun and been bounded, is dropped and reported, and read N+1 still
// assembles its real reply whole.
func TestReadBufferGuard(t *testing.T) {
	chunks, want := realReply(t, 1)
	b, drops := dropCounter()

	b.begin()
	if _, _, err := b.bound(100); err != nil {
		t.Fatal(err)
	}
	feed(t, b, 101, chunks[:4]) // read N, partly received

	b.begin() // read N+1
	if _, _, err := b.bound(200); err != nil {
		t.Fatal(err)
	}
	late, _ := realReply(t, 2)
	feed(t, b, 150, late[4:6]) // late fragments of read N: keys below the new request
	reply, whole := feed(t, b, 201, chunks)
	if !whole || reply != want {
		t.Fatalf("read N+1 = %q (whole %v), want %q", reply, whole, want)
	}
	if len(*drops) != 2 || !errors.Is((*drops)[0], errBeforeRequest) {
		t.Errorf("dropped = %v, want two errBeforeRequest", *drops)
	}
}

// TestReadBufferHoldsNotificationsUntilBound: notifications handled between
// begin and bound are neither lost nor misassigned. Once bound sets the
// boundary, those at or below it are dropped and those above it are
// assembled in order.
func TestReadBufferHoldsNotificationsUntilBound(t *testing.T) {
	chunks, want := realReply(t, 1)
	late, _ := realReply(t, 2)
	b, drops := dropCounter()

	b.begin()
	feed(t, b, 50, late[3:5]) // late fragments of an earlier read
	reply, whole := feed(t, b, 101, chunks[:5])
	if reply != "" || whole {
		t.Fatalf("an unbounded read assembled %q (whole %v)", reply, whole)
	}
	if _, whole, err := b.bound(100); whole || err != nil {
		t.Fatalf("bound after partial reply: whole %v err %v", whole, err)
	}
	if len(*drops) != 2 {
		t.Errorf("dropped %d held notifications, want 2: %v", len(*drops), *drops)
	}
	reply, whole = feed(t, b, 106, chunks[5:])
	if !whole || reply != want {
		t.Fatalf("read = %q (whole %v), want %q", reply, whole, want)
	}
}

// TestReadBufferBoundReturnsHeldReply: when the whole reply was held before
// bound, bound returns it whole.
func TestReadBufferBoundReturnsHeldReply(t *testing.T) {
	chunks, want := realReply(t, 3)
	b, _ := dropCounter()
	b.begin()
	feed(t, b, 101, chunks)
	reply, whole, err := b.bound(100)
	if err != nil || !whole || reply != want {
		t.Fatalf("bound = %q whole %v err %v, want whole %q", reply, whole, err, want)
	}
}

// TestReadBufferRacingBound: adds on the consumer's goroutine race the
// reader's bound. Whichever comes first, the outcome is the same: the late
// fragments are dropped and the real reply comes out whole.
func TestReadBufferRacingBound(t *testing.T) {
	chunks, want := realReply(t, 1)
	late, _ := realReply(t, 2)
	for i := range 200 {
		b, drops := dropCounter()
		b.begin()

		var mu sync.Mutex
		var final string
		var wholeSeen bool
		note := func(reply string, whole bool) {
			mu.Lock()
			defer mu.Unlock()
			if whole {
				final, wholeSeen = reply, true
			}
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k, c := range late[:3] {
				if _, _, err := b.add(10+uint64(k), c); err != nil {
					t.Errorf("iteration %d: late add: %v", i, err)
				}
			}
			for k, c := range chunks {
				reply, whole, err := b.add(101+uint64(k), c)
				if err != nil {
					t.Errorf("iteration %d: add: %v", i, err)
					return
				}
				note(reply, whole)
			}
		}()
		reply, whole, err := b.bound(100)
		if err != nil {
			t.Fatal(err)
		}
		note(reply, whole)
		wg.Wait()
		if !wholeSeen || final != want {
			t.Fatalf("iteration %d: whole %v reply %q, want %q", i, wholeSeen, final, want)
		}
		if len(*drops) != 3 {
			t.Fatalf("iteration %d: dropped %d, want 3: %v", i, len(*drops), *drops)
		}
	}
}

// TestReadBufferBoundTwice: a read has one request, so a second bound fails.
func TestReadBufferBoundTwice(t *testing.T) {
	b, _ := dropCounter()
	b.begin()
	if _, _, err := b.bound(5); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.bound(6); !errors.Is(err, errBoundTwice) {
		t.Errorf("second bound returned %v, want errBoundTwice", err)
	}
}
