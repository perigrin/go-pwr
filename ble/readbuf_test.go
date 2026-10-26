// ABOUTME: Tests that the read buffer keeps each read's notifications to its
// ABOUTME: own token while notifications arrive concurrently with a new read.

package ble

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/perigrin/go-pwr/status"
)

// TestReadBufferGuard adds fragments under an old read's token from another
// goroutine while a new read begins, and checks that none of them reaches the
// new read: it assembles exactly the real reply it is fed.
func TestReadBufferGuard(t *testing.T) {
	chunks := lolaChunks(t, 1)
	var want strings.Builder
	for _, c := range chunks {
		want.Write(c)
	}

	for iteration := range 200 {
		b := newReadBuffer()
		old := b.begin(0)

		var begun atomic.Bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := uint64(1); key < 500; key++ {
				after := begun.Load() // read before the add, so the add began after begin returned
				_, _, err := b.addTo(old, key, []byte("late fragment of read N;"))
				if after && !errors.Is(err, status.ErrStaleToken) {
					t.Errorf("iteration %d: add under the old token after the new read began returned %v, want ErrStaleToken", iteration, err)
					return
				}
			}
		}()

		const boundary = 1_000_000
		b.begin(boundary)
		begun.Store(true)

		var reply string
		var whole bool
		for i, c := range chunks {
			var err error
			reply, whole, err = b.add(boundary+1+uint64(i), c)
			if err != nil {
				t.Fatalf("iteration %d: new read failed: %v", iteration, err)
			}
		}
		wg.Wait()
		if !whole || reply != want.String() {
			t.Fatalf("iteration %d: new read = %q (whole %v), want %q", iteration, reply, whole, want.String())
		}
	}
}
