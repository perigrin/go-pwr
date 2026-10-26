// ABOUTME: Tests that an abandoned connect reports a failed cleanup apart from
// ABOUTME: the cancellation, so a caller can tell a link may still be up.

package ble

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

// stubDevice answers the calls disconnect makes, the way BlueZ would when it
// drops the link and when it won't. BlueZ not dropping a link can't be
// produced on demand.
type stubDevice struct {
	dbus.BusObject
	disconnectErr error
}

func (d stubDevice) CallWithContext(_ context.Context, method string, _ dbus.Flags, _ ...any) *dbus.Call {
	switch method {
	case deviceInterface + ".Disconnect":
		return &dbus.Call{Err: d.disconnectErr}
	case "org.freedesktop.DBus.Properties.Get":
		return &dbus.Call{Body: []any{dbus.MakeVariant(false)}}
	}
	return &dbus.Call{Err: errors.New("unexpected call " + method)}
}

func TestAbandonedMarksAFailedCleanup(t *testing.T) {
	adapter := &Adapter{}
	cancelled := errors.New("cancelled")

	err := adapter.abandoned(stubDevice{disconnectErr: errors.New("refused")}, "C3:00:00:58:51:40", cancelled)
	for _, want := range []error{cancelled, ErrCleanupFailed, ErrRetryable} {
		if !errors.Is(err, want) {
			t.Errorf("abandoned with a failed cleanup = %v, want it to match %v", err, want)
		}
	}

	err = adapter.abandoned(stubDevice{}, "C3:00:00:58:51:40", cancelled)
	if !errors.Is(err, cancelled) || errors.Is(err, ErrCleanupFailed) {
		t.Errorf("abandoned with a clean cleanup = %v, want the cancellation alone", err)
	}
}
