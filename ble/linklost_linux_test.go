// ABOUTME: Tests that a link found down at any step of opening the status link
// ABOUTME: is closed and connected again, with the retry harness and no device.

package ble

import (
	"context"
	"errors"
	"testing"
	"time"
)

// openAttempt returns an attempt like ConnectWithRetry's, with the opening of
// the status link and the check of the link as the harness's own: it opens
// with the errors in openErrs in turn, then succeeds, and the link is up when
// up says so. closes counts the closes of a lost link.
func openAttempt(openErrs []error, up func() (bool, error)) (attempt func() (*Conn, error), opens, closes *int) {
	opens, closes = new(int), new(int)
	return func() (*Conn, error) {
		*opens++
		if *opens > len(openErrs) {
			return &Conn{}, nil
		}
		lost := linkLost("C3:00:00:58:51:40", openErrs[*opens-1], up, func() error { *closes++; return nil })
		if lost != nil {
			return nil, lost
		}
		return &Conn{}, nil
	}, opens, closes
}

var (
	errServicesNotResolved = errors.New("services not resolved")
	errGattGone            = errors.New("GATT objects gone")
)

// A link that goes down at any step of opening the status link is closed and
// connected again, whichever error that step gave.
func TestLinkLostAtAnyStepClosesThenRetries(t *testing.T) {
	for _, openErr := range []error{
		errors.New("turn on notifications of /org/bluez/hci0/dev_C3/service000c/char000d: org.bluez.Error.NotConnected"),
		errServicesNotResolved,
		errGattGone,
	} {
		t.Run(openErr.Error(), func(t *testing.T) {
			attempt, opens, closes := openAttempt([]error{openErr}, func() (bool, error) { return false, nil })
			conn, err := retryConnect(context.Background(), time.Millisecond, attempt)
			if err != nil || conn == nil || *opens != 2 || *closes != 1 {
				t.Errorf("conn %v, err %v after %d opens and %d closes, want a conn on the second open after one close", conn, err, *opens, *closes)
			}
		})
	}
}

// An error opening the status link with the link still up is not a lost
// link: nothing is closed, and the error is left for the request to report.
func TestLinkStillUpIsNotLost(t *testing.T) {
	attempt, opens, closes := openAttempt([]error{errServicesNotResolved}, func() (bool, error) { return true, nil })
	if _, err := retryConnect(context.Background(), time.Millisecond, attempt); err != nil || *opens != 1 || *closes != 0 {
		t.Errorf("err %v after %d opens and %d closes, want the first conn and no close", err, *opens, *closes)
	}
}

// A check of the link that fails says nothing of the link, so it is not
// taken as lost.
func TestLinkCheckFailureIsNotLost(t *testing.T) {
	attempt, _, closes := openAttempt([]error{errServicesNotResolved}, func() (bool, error) { return false, errors.New("bus gone") })
	if _, err := retryConnect(context.Background(), time.Millisecond, attempt); err != nil || *closes != 0 {
		t.Errorf("err %v and %d closes, want no error and no close", err, *closes)
	}
}

// A lost link that won't close is reported with the loss, and still retried.
func TestLinkLostReportsFailedClose(t *testing.T) {
	closeErr := errors.New("still up")
	err := linkLost("C3:00:00:58:51:40", errGattGone, func() (bool, error) { return false, nil }, func() error { return closeErr })
	if !errors.Is(err, errGattGone) || !errors.Is(err, closeErr) || !errors.Is(err, ErrRetryable) {
		t.Errorf("err %v, want the open error, the close error and ErrRetryable", err)
	}
}

func TestLinkLostNoError(t *testing.T) {
	if err := linkLost("C3:00:00:58:51:40", nil, func() (bool, error) { return false, nil }, func() error { return nil }); err != nil {
		t.Errorf("linkLost with no open error = %v, want nil", err)
	}
}
