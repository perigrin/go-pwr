// ABOUTME: Tests the bounded connect retry that readings and Followers share,
// ABOUTME: with the attempt as a function so no device is needed.

package ble

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errAborted = errors.New("connection aborted by local host")

// attempts returns an attempt function that fails with errs in turn and then
// succeeds, and a count of its calls.
func attempts(errs ...error) (func() (*Conn, error), *int) {
	calls := 0
	return func() (*Conn, error) {
		calls++
		if calls <= len(errs) {
			return nil, errs[calls-1]
		}
		return &Conn{}, nil
	}, &calls
}

func TestRetryConnectTriesAgainAfterAnAbortedConnect(t *testing.T) {
	attempt, calls := attempts(errAborted, errAborted)
	conn, err := retryConnect(context.Background(), time.Millisecond, attempt)
	if err != nil || conn == nil || *calls != 3 {
		t.Errorf("conn %v, err %v after %d attempts, want a conn on the third", conn, err, *calls)
	}
}

func TestRetryConnectGivesUpAfterThreeAttempts(t *testing.T) {
	attempt, calls := attempts(errAborted, errAborted, errAborted, errAborted)
	_, err := retryConnect(context.Background(), time.Millisecond, attempt)
	if !errors.Is(err, errAborted) || *calls != 3 {
		t.Errorf("err %v after %d attempts, want the last error after 3", err, *calls)
	}
}

func TestRetryConnectDoesNotRetryAnUnknownController(t *testing.T) {
	attempt, calls := attempts(ErrNotFound)
	_, err := retryConnect(context.Background(), time.Millisecond, attempt)
	if !errors.Is(err, ErrNotFound) || *calls != 1 {
		t.Errorf("err %v after %d attempts, want ErrNotFound after 1", err, *calls)
	}
}

func TestRetryConnectStopsWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := retryConnect(ctx, time.Millisecond, func() (*Conn, error) {
		calls++
		cancel()
		return nil, errAborted
	})
	if err == nil || calls != 1 {
		t.Errorf("err %v after %d attempts, want an error after 1", err, calls)
	}
}

func TestWaitToRetryReportsTheInterruption(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitToRetry(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("waitToRetry on a cancelled context = %v, want context.Canceled", err)
	}
	if err := waitToRetry(context.Background(), time.Millisecond); err != nil {
		t.Errorf("waitToRetry = %v, want nil after the pause", err)
	}
}
