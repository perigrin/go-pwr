// ABOUTME: Connects with a bounded number of attempts, for a reading or a
// ABOUTME: Follower, since the local controller aborts some connects.

package ble

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// connectAttempts is how many times ConnectWithRetry tries to connect. About
// 7% of connects on lola are aborted by the local controller, most often
// right after a link dropped, and BlueZ then accepts the next try. The
// attempts share the caller's context.
const connectAttempts = 3

// connectPause is the wait between connect attempts.
const connectPause = time.Second

// ConnectWithRetry opens the link to the controller as Connect does, trying
// again after a failed attempt while ctx lasts, up to three attempts. A
// controller BlueZ doesn't know (ErrNotFound) won't appear by trying again,
// and neither will anything once ctx is done.
func (a *Adapter) ConnectWithRetry(ctx context.Context, mac string) (*Conn, error) {
	return retryConnect(ctx, connectPause, func() (*Conn, error) { return a.Connect(ctx, mac) })
}

// retryConnect calls attempt until it succeeds, attempts run out, ctx is done
// or attempt fails with ErrNotFound, pausing between attempts.
func retryConnect(ctx context.Context, pause time.Duration, attempt func() (*Conn, error)) (*Conn, error) {
	var err error
	for n := 1; ; n++ {
		var conn *Conn
		if conn, err = attempt(); err == nil {
			return conn, nil
		}
		if n == connectAttempts || ctx.Err() != nil || errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if err := waitToRetry(ctx, pause); err != nil {
			return nil, err
		}
	}
}

// waitToRetry pauses between connect attempts. When ctx ends in the pause,
// the error says the reading was interrupted, which is what the person needs
// to know, not the attempt that failed before it.
func waitToRetry(ctx context.Context, pause time.Duration) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("connect: interrupted while waiting to try again: %w", ctx.Err())
	case <-time.After(pause):
		return nil
	}
}
