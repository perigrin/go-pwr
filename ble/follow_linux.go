// ABOUTME: Holds one connection to the controller across readings on Linux,
// ABOUTME: for --follow; it connects when it has no link and reads on it.

package ble

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// errLinkDown is the cause that ends a reading when the link is seen down.
var errLinkDown = errors.New("link down")

// linkCheckBound is the longest the check of the link after a failed reading
// may take. It runs after the reading's own context may be done.
const linkCheckBound = 2 * time.Second

// Follower holds one connection to the controller across readings.
type Follower struct {
	adapter *Adapter
	mac     string

	// mu serialises Read and Close, and guards conn.
	mu   sync.Mutex
	conn *Conn

	// configure, when set, runs on each Conn the Follower makes, before its
	// first request. Tests set it before the first Read.
	configure func(*Conn)
}

// Follow returns a Follower for the controller at mac. It connects on the
// first Read.
func (a *Adapter) Follow(mac string) *Follower {
	return &Follower{adapter: a, mac: mac}
}

// Read takes one reading on the held connection, making the connection first
// if there is none.
//
// BlueZ tells nothing of a link that the other end dropped except through the
// Connected property, so Read reads it before it uses a held connection, and
// watches it while the reply arrives. A link seen down gives an error that
// matches ErrLinkDropped, as soon as it is seen, and not after the reading's
// timeout as an incomplete reply. The dropped connection is closed, so the
// next Read connects again. Every error matches ErrRetryable.
func (f *Follower) Read(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn == nil {
		conn, err := f.establish(ctx)
		if err != nil {
			return "", err
		}
		f.conn = conn
	} else if up, err := connected(ctx, f.conn.object); err != nil && !isGone(err) {
		return "", f.abandoned(fmt.Errorf("check the link: %w", err))
	} else if err != nil || !up {
		return "", f.dropped(nil)
	}

	watched, cancel := context.WithCancelCause(ctx)
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		f.watch(watched, cancel)
	}()
	reply, err := f.conn.RequestStatus(watched)
	down := context.Cause(watched) == errLinkDown
	cancel(nil)
	<-watching
	if err == nil {
		return reply, nil
	}
	if down || f.linkDown() {
		return "", f.dropped(err)
	}
	return "", f.abandoned(err)
}

// establish scans for the controller and connects to it.
func (f *Follower) establish(ctx context.Context) (*Conn, error) {
	if _, err := f.adapter.Scan(ctx, f.mac); err != nil {
		return nil, err
	}
	conn, err := f.adapter.Connect(ctx, f.mac)
	if err != nil {
		return nil, err
	}
	if f.configure != nil {
		f.configure(conn)
	}
	return conn, nil
}

// watch ends the reading with errLinkDown as soon as the link is seen down.
func (f *Follower) watch(ctx context.Context, cancel context.CancelCauseFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
		if up, err := connected(ctx, f.conn.object); !up && (err == nil || isGone(err)) {
			cancel(errLinkDown)
			return
		}
	}
}

// linkDown reports whether BlueZ says the held link is down now.
func (f *Follower) linkDown() bool {
	ctx, cancel := context.WithTimeout(context.Background(), linkCheckBound)
	defer cancel()
	up, err := connected(ctx, f.conn.object)
	return !up && (err == nil || isGone(err))
}

// abandoned closes the connection of a reading that failed, and returns the
// reading's error, joined with the close's own error if that failed. The next
// Read connects again, so nothing of this reading's reply that is still
// arriving can reach it. This is how a Follower prevents a splice (see the
// package documentation).
func (f *Follower) abandoned(cause error) error {
	conn := f.conn
	f.conn = nil
	err := fmt.Errorf("follow read: %w", cause)
	if closeErr := conn.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if errors.Is(err, ErrRetryable) {
		return err
	}
	return retryableError(err)
}

// dropped closes the connection whose link went down and returns the error
// for it, joined with the close's own error if that failed. cause is the
// failure that showed it, if any.
func (f *Follower) dropped(cause error) error {
	conn := f.conn
	f.conn = nil
	err := fmt.Errorf("follow read: the link to %s dropped", f.mac)
	if cause != nil {
		err = fmt.Errorf("%w: %w", err, cause)
	}
	if closeErr := conn.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	return retryableError(classify(nil, ErrLinkDropped, "", err))
}

// Close ends the held connection, if there is one.
func (f *Follower) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn == nil {
		return nil
	}
	conn := f.conn
	f.conn = nil
	return conn.Close()
}
