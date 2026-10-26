// ABOUTME: Tests that a startup error names its class in the one stderr line
// ABOUTME: even when the D-Bus message under it does not.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/perigrin/go-pwr/ble"
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
