// ABOUTME: Tests that a Follower's link watch stops when a reading panics,
// ABOUTME: with a stand-in D-Bus object since no device panics on demand.

package ble

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// upObject is a D-Bus object whose Connected property is always true. It
// stands in for the device because a panic inside a status request can't be
// produced with the real one.
type upObject struct {
	dbus.BusObject
	reads *atomic.Int32 // how many times Connected was read
}

func (o upObject) CallWithContext(context.Context, string, dbus.Flags, ...interface{}) *dbus.Call {
	o.reads.Add(1)
	return &dbus.Call{Body: []interface{}{dbus.MakeVariant(true)}}
}

// A reading that panics after the link watch started must not leave the
// watch running: once the caller has recovered and closed the Follower, the
// watch would read a connection that is gone, and go on doing so.
func TestFollowReadPanicStopsTheLinkWatch(t *testing.T) {
	// A Conn with an Adapter that has no D-Bus connection panics in
	// RequestStatus when it opens the status link.
	var reads atomic.Int32
	conn := &Conn{adapter: &Adapter{}, object: upObject{reads: &reads}, closing: make(chan struct{})}
	f := &Follower{adapter: &Adapter{}, mac: "C3:00:00:58:51:40", conn: conn}

	func() {
		defer func() {
			if recover() == nil {
				t.Error("Read did not panic, so the test does not exercise the panic path")
			}
		}()
		f.Read(context.Background())
	}()

	// What closing the Follower does to the connection of a failed reading.
	f.mu.Lock()
	f.conn = nil
	f.mu.Unlock()
	// A watch left running reads the Connected property every poll.
	after := reads.Load()
	time.Sleep(4 * pollInterval)
	if now := reads.Load(); now != after {
		t.Errorf("Connected was read %d more times after the panic: the link watch is still running", now-after)
	}
}
