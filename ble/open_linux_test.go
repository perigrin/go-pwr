// ABOUTME: Tests that opening the adapter again in the middle of a run fails
// ABOUTME: as a retryable error, where the first open fails as a fatal one.

package ble

import (
	"context"
	"errors"
	"testing"
)

// A cancelled context makes the power-state read fail, with no need to break
// anything on the machine.
func TestOpenFailureIsFatalAtStartupButReopenIsRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := OpenContext(ctx)
	if !errors.Is(err, ErrFatalAtStartup) || errors.Is(err, ErrRetryable) {
		t.Errorf("OpenContext error = %v, want ErrFatalAtStartup and not ErrRetryable", err)
	}
	_, err = ReopenContext(ctx)
	if !errors.Is(err, ErrRetryable) || errors.Is(err, ErrFatalAtStartup) {
		t.Errorf("ReopenContext error = %v, want ErrRetryable and not ErrFatalAtStartup", err)
	}
}
