// ABOUTME: Tests holding one connection across readings against the real
// ABOUTME: controller on lola: dropped links, cut-off readings and recovery.

package ble

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// followReadBound is the context deadline of a reading in these tests, the
// default --timeout.
const followReadBound = 20 * time.Second

// newFollower opens the adapter and returns a Follower for the test
// controller, which ends the test with the link down, however the test ends.
func newFollower(t *testing.T) (*Follower, string) {
	t.Helper()
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	f := adapter.Follow(mac)
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("Follower.Close() error = %v", err)
		}
	})
	return f, mac
}

// followRead is one reading with the default timeout.
func followRead(f *Follower) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), followReadBound)
	defer cancel()
	started := time.Now()
	reply, err := f.Read(ctx)
	return reply, time.Since(started), err
}

// readWhole reads until a reading is whole, trying again after a failed one
// the way a loop of readings would. BlueZ sometimes aborts a connect with
// le-connection-abort-by-local, most often right after a drop, which
// protocol.md ("Time budget on lola") records as an ordinary failed reading.
// A dropped link is never what is being retried: the caller has seen it.
func readWhole(t *testing.T, f *Follower) string {
	t.Helper()
	const attempts = 4
	for attempt := 1; ; attempt++ {
		reply, took, err := followRead(f)
		if err == nil {
			t.Logf("reading %d: whole reply after %v", attempt, took.Round(time.Millisecond))
			return reply
		}
		t.Logf("reading %d: error after %v = %v", attempt, took.Round(time.Millisecond), err)
		if errors.Is(err, ErrLinkDropped) {
			t.Fatalf("reading %d: error = %v, want the link to stay up", attempt, err)
		}
		if attempt == attempts {
			t.Fatalf("no whole reply in %d readings, last error = %v", attempts, err)
		}
		time.Sleep(time.Second)
	}
}

// dropLink drops the link from outside, as the controller going away would.
func dropLink(t *testing.T, f *Follower, mac string) {
	t.Helper()
	sh(t, "bluetoothctl disconnect "+mac)
	if !waitLinkDown(t, f.adapter, mac, 10*time.Second) {
		t.Fatal("bluetoothctl disconnect left the link up")
	}
}

func TestFollowDetectsDroppedLink(t *testing.T) {
	requireHardware(t)
	f, mac := newFollower(t)

	requireWholeReading(t, readWhole(t, f))

	dropLink(t, f, mac)
	reply, took, err := followRead(f)
	t.Logf("reading after the drop = %q, %v after %v", reply, err, took.Round(time.Millisecond))
	if !errors.Is(err, ErrLinkDropped) {
		t.Fatalf("reading after the drop: error = %v, want ErrLinkDropped", err)
	}
	if !errors.Is(err, ErrRetryable) {
		t.Errorf("reading after the drop: error = %v, want ErrRetryable", err)
	}
	if reply != "" {
		t.Errorf("reading after the drop: reply = %q, want none", reply)
	}

	requireWholeReading(t, readWhole(t, f))
}

// cutOffReading takes a reading on f whose context ends a short time after
// the request is written, while its reply is still arriving, and returns its
// error. A held connection answers within a few hundred milliseconds, so the
// time to cut is looked for downwards: a reading that completes anyway is
// whole, and is followed by one cut sooner.
func cutOffReading(t *testing.T, f *Follower) error {
	t.Helper()
	for _, cut := range []time.Duration{200, 100, 60, 40, 25, 15, 10, 5} {
		cut *= time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), followReadBound)
		f.conn.afterWrite = func() { time.AfterFunc(cut, cancel) }
		reply, err := f.Read(ctx)
		cancel()
		if f.conn != nil {
			f.conn.afterWrite = nil
		}
		if err != nil {
			t.Logf("reading cut off %v after the request: %v", cut, err)
			return err
		}
		t.Logf("reading cut %v after the request completed anyway: %d bytes", cut, len(reply))
	}
	t.Fatal("no reading could be cut off before its reply completed")
	return nil
}

func TestFollowNoSpliceAfterCutOffReading(t *testing.T) {
	requireHardware(t)
	f, _ := newFollower(t)
	drops := &dropRecorder{}
	f.configure = func(c *Conn) { c.onDrop = drops.record }

	requireWholeReading(t, readWhole(t, f))
	held := f.conn

	err := cutOffReading(t, f)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("cut-off reading error = %v, want the context's error", err)
	}
	if errors.Is(err, ErrLinkDropped) {
		t.Errorf("cut-off reading error = %v, want a cut-off reply, not a dropped link", err)
	}

	// The tail of the cut-off reply must not reach the next one: it is read on
	// a connection of its own, made after the cut-off.
	reply := readWhole(t, f)
	requireWholeReading(t, reply)
	if f.conn == held {
		t.Error("the read after a cut-off reading used the connection the cut-off reply was arriving on")
	}
	for _, err := range drops.all() {
		t.Errorf("notification dropped: %v", err)
	}
}
