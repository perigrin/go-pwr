// ABOUTME: Tests the status request against the real controller through BlueZ
// ABOUTME: on lola, with GO_PWR_TEST_MAC set; the only write is the 0x20 request.

package ble

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/status"
)

// wholeFramedReply is the length of a reply as the controller sends it: a
// leading space, the 166-byte body and CRLF.
const wholeFramedReply = 169

// connectController scans for the controller and connects to it, and closes
// the link when the test ends, even if the test fails. It leaves the
// controller disconnected, with notifications off, however the test ends.
func connectController(t *testing.T) *Conn {
	t.Helper()
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := adapter.Connect(ctx, mac)
	if err != nil {
		t.Fatalf("Connect(%s) error = %v", mac, err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return conn
}

// requireWholeReading checks that reply is a whole framed reply that decodes,
// passes the gating bounds and carries no warning.
func requireWholeReading(t *testing.T, reply string) status.Reading {
	t.Helper()
	if len(reply) != wholeFramedReply || reply[0] != ' ' || !strings.HasSuffix(reply, "\r\n") {
		t.Fatalf("reply = %q (%d bytes), want %d bytes framed by a space and CRLF", reply, len(reply), wholeFramedReply)
	}
	reading, err := status.Decode(reply)
	if err != nil {
		t.Fatalf("Decode(%q) error = %v", reply, err)
	}
	if reading.Raw != reply {
		t.Errorf("Raw = %q, want the whole reply %q", reading.Raw, reply)
	}
	for _, w := range reading.Warnings {
		t.Errorf("reading carries a warning: %v", w)
	}
	for _, e := range reading.FieldErrors {
		t.Errorf("reading carries a field error: %v", e)
	}
	return reading
}

func TestRequestStatusReturnsWholeReply(t *testing.T) {
	conn := connectController(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	reply, err := conn.RequestStatus(ctx)
	t.Logf("RequestStatus() = %q, %v after %v", reply, err, time.Since(started).Round(time.Millisecond))
	if err != nil {
		t.Fatalf("RequestStatus() error = %v", err)
	}
	reading := requireWholeReading(t, reply)
	t.Logf("battery %d mV, solar %d mV, charge %d mA", reading.BatteryMV, reading.SolarMV, reading.ChargeMA)
}

// dropRecorder collects the notifications a Conn did not use, to prove a run
// raised none. Set conn.onDrop to its record before the first request.
type dropRecorder struct {
	mu    sync.Mutex
	drops []error
}

func (d *dropRecorder) record(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.drops = append(d.drops, err)
}

func (d *dropRecorder) all() []error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]error(nil), d.drops...)
}

func TestRequestStatusRepeatedOnOneConnection(t *testing.T) {
	conn := connectController(t)
	drops := &dropRecorder{}
	conn.onDrop = drops.record
	const requests = 50
	var slowest time.Duration
	for i := 0; i < requests; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		started := time.Now()
		reply, err := conn.RequestStatus(ctx)
		took := time.Since(started)
		cancel()
		if err != nil {
			t.Fatalf("request %d: RequestStatus() error = %v after %v", i, err, took)
		}
		requireWholeReading(t, reply)
		if t.Failed() {
			t.Fatalf("request %d: reply was not a whole, clean reading", i)
		}
		if i > 0 && took > slowest {
			slowest = took
		}
	}
	t.Logf("%d requests on one connection, each whole; slowest after the first %v", requests, slowest.Round(time.Millisecond))
	for _, err := range drops.all() {
		t.Errorf("notification dropped: %v", err)
	}
}

// TestRequestStatusSettle measures the settle question (protocol.md, "Settle
// delay"): how many of a number of fresh connections return a whole reply to
// a request sent as soon as Connect returns. The request goes out after the
// link's setup, which this package cannot skip, so the gap between Connect
// and the write is measured too. It also records whether BlueZ ever delivered
// a notification of the reply before the write's method return, which would
// show as a dropped notification.
func TestRequestStatusSettle(t *testing.T) {
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	scanFor(t, adapter, mac)
	const connections = 20
	whole, failed := 0, 0
	var shortest, longest time.Duration
	for i := 0; i < connections; i++ {
		var conn *Conn
		var connectedAt, wroteAt time.Time
		for attempt := 0; conn == nil; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			c, err := adapter.Connect(ctx, mac)
			cancel()
			if err != nil {
				t.Logf("connection %2d: connect failed, repeated: %v", i, err)
				if attempt >= 5 {
					t.Fatalf("connect failed %d times in a row", attempt+1)
				}
				scanFor(t, adapter, mac)
				continue
			}
			conn, connectedAt = c, time.Now()
		}
		drops := &dropRecorder{}
		conn.onDrop = drops.record
		conn.afterWrite = func() { wroteAt = time.Now() }
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		reply, err := conn.RequestStatus(ctx)
		cancel()
		gap := wroteAt.Sub(connectedAt)
		if i == 0 || gap < shortest {
			shortest = gap
		}
		if gap > longest {
			longest = gap
		}
		if err == nil {
			_, decodeErr := status.Decode(reply)
			switch {
			case len(reply) != wholeFramedReply || decodeErr != nil:
				failed++
				t.Errorf("connection %2d: reply %q does not decode whole: %v", i, reply, decodeErr)
			default:
				whole++
			}
		} else {
			failed++
			t.Errorf("connection %2d: RequestStatus() error = %v", i, err)
		}
		t.Logf("connection %2d: Connect returned %v before the write, error %v, dropped %v", i, gap.Round(time.Millisecond), err, drops.all())
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("connection %2d: Close() error = %v", i, closeErr)
		}
	}
	t.Logf("settle: %d of %d fresh connections returned a whole reply to a request sent %v to %v after Connect returned", whole, connections, shortest.Round(time.Millisecond), longest.Round(time.Millisecond))
}

func TestRequestStatusAfterCancelledRequest(t *testing.T) {
	conn := connectController(t)
	drops := &dropRecorder{}
	conn.onDrop = drops.record

	// Cancel the first request as soon as its write has returned, while its
	// reply is still arriving.
	ctx, cancel := context.WithCancel(context.Background())
	conn.afterWrite = cancel
	_, err := conn.RequestStatus(ctx)
	cancel()
	conn.afterWrite = nil
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled RequestStatus() error = %v, want context.Canceled", err)
	}

	next, nextCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer nextCancel()
	reply, err := conn.RequestStatus(next)
	if err != nil {
		t.Fatalf("RequestStatus() after the cancelled one: error = %v", err)
	}
	requireWholeReading(t, reply)
	for _, err := range drops.all() {
		t.Errorf("notification dropped: %v", err)
	}
}

func TestRequestStatusWithoutConnectionErrors(t *testing.T) {
	var written int
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var none *Conn
	if _, err := none.RequestStatus(ctx); !errors.Is(err, ErrNotConnected) {
		t.Errorf("RequestStatus() on a nil Conn: error = %v, want ErrNotConnected", err)
	}
	zero := &Conn{afterWrite: func() { written++ }}
	if _, err := zero.RequestStatus(ctx); !errors.Is(err, ErrNotConnected) {
		t.Errorf("RequestStatus() on a Conn never connected: error = %v, want ErrNotConnected", err)
	}

	conn := connectController(t)
	conn.afterWrite = func() { written++ }
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := conn.RequestStatus(ctx); !errors.Is(err, ErrNotConnected) {
		t.Errorf("RequestStatus() after Close: error = %v, want ErrNotConnected", err)
	}
	if written != 0 {
		t.Errorf("%d requests were written, want none", written)
	}
}
