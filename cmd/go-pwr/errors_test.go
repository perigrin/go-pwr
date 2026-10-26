// ABOUTME: Tests that a startup error names its class in the one stderr line
// ABOUTME: even when the D-Bus message under it does not.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/ble"
	"github.com/perigrin/go-pwr/status"
)

// dbusLike matches a class by Unwrap but has a message that doesn't name it,
// as the D-Bus errors under ble's classes do.
type dbusLike struct{ class error }

func (e dbusLike) Error() string { return "Method \"Get\" doesn't exist\n" }
func (e dbusLike) Unwrap() error { return e.class }

func TestPrintErrorNamesTheClass(t *testing.T) {
	for _, class := range []error{ble.ErrNoAdapter, ble.ErrAccessDenied, ble.ErrBlueZNotRunning, ble.ErrAdapterUnavailable} {
		t.Run(class.Error(), func(t *testing.T) {
			var stderr bytes.Buffer
			printError(&stderr, dbusLike{class})
			if strings.Count(stderr.String(), "\n") != 1 || !strings.Contains(stderr.String(), class.Error()) {
				t.Errorf("stderr %q, want one line naming %q", stderr.String(), class)
			}
		})
	}
}

func TestPrintErrorDoesNotRepeatTheClass(t *testing.T) {
	var stderr bytes.Buffer
	printError(&stderr, errors.Join(ble.ErrBlueZNotRunning, errors.New("x")))
	if strings.Count(stderr.String(), ble.ErrBlueZNotRunning.Error()) != 1 {
		t.Errorf("stderr %q names the class more than once", stderr.String())
	}
}

func TestPanicLineIsOneEscapedLine(t *testing.T) {
	var stderr bytes.Buffer
	printPanic(&stderr, "close", "boom\nerror: forged\x1b[31m")
	got := stderr.String()
	if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") || strings.Contains(got, "\x1b") {
		t.Errorf("stderr %q, want one line with control bytes escaped", got)
	}
	if !strings.Contains(got, "internal error") || !strings.Contains(got, "close") {
		t.Errorf("stderr %q, want it to say what failed", got)
	}
}

func TestReceiveQuotesTheReplyWhenUptimeFails(t *testing.T) {
	failing := func() (float64, error) { return 0, errors.New("no uptime") }
	_, err := receive("reply\r\n", "C3:00:00:58:51:40", "boot", failing)
	if err == nil {
		t.Fatal("receive: want an error")
	}
	if line := status.ErrorLine(err); !strings.Contains(line, `reply "reply\r\n"`) {
		t.Errorf("error line %q does not quote the received reply", line)
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

func TestFailedSaysNothingWhenASignalEndedTheReading(t *testing.T) {
	signalled, stop := context.WithCancel(context.Background())
	stop() // what SIGINT and SIGTERM do
	expired, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-expired.Done()

	for _, tc := range []struct {
		name      string
		ctx       context.Context
		err       error
		wantLines int
	}{
		{"signal", signalled, fmt.Errorf("scan: %w", context.Canceled), 0},
		{"timeout", expired, fmt.Errorf("scan: %w", context.DeadlineExceeded), 1},
		{"other error after a signal", signalled, errors.New("write the request: broken"), 1},
		{"no signal", context.Background(), fmt.Errorf("scan: %w", context.Canceled), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			failed(tc.ctx, &stderr, tc.err)
			if got := strings.Count(stderr.String(), "\n"); got != tc.wantLines {
				t.Errorf("stderr %q has %d lines, want %d", stderr.String(), got, tc.wantLines)
			}
		})
	}
}

func TestProtectTurnsAPanicIntoAFailedStep(t *testing.T) {
	var stderr bytes.Buffer
	if protect(&stderr, func() bool { panic("boom\nforged") }) {
		t.Error("protect returned true for a step that panicked")
	}
	if strings.Count(stderr.String(), "\n") != 1 || !strings.Contains(stderr.String(), "internal error") {
		t.Errorf("stderr %q, want one escaped internal error line", stderr.String())
	}
	if !protect(&stderr, func() bool { return true }) {
		t.Error("protect returned false for a step that succeeded")
	}
}
