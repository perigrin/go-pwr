// ABOUTME: Tests the scan guard that serialises StopScan and recovers a panic
// ABOUTME: from the scan call; platform-neutral, so it runs under -race on the Mac.

package ble

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingScanner stands in for the library's StopScan, which is not safe to
// call from two goroutines at once. It records how many calls reach it.
type countingScanner struct {
	calls atomic.Int32
	err   error
}

func (s *countingScanner) stop() error {
	s.calls.Add(1)
	return s.err
}

func TestScanGuardConcurrentStops(t *testing.T) {
	scanner := &countingScanner{}
	guard := newScanGuard(scanner.stop)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := guard.Stop(); err != nil {
				t.Errorf("Stop() = %v, want nil", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := scanner.calls.Load(); got != 1 {
		t.Errorf("%d stops reached the scanner, want 1", got)
	}
	if !guard.Stopped() {
		t.Error("Stopped() = false after a stop, want true")
	}
}

func TestScanGuardStopAfterEnd(t *testing.T) {
	scanner := &countingScanner{}
	guard := newScanGuard(scanner.stop)
	guard.End()

	if err := guard.Stop(); err != nil {
		t.Errorf("Stop() after End() = %v, want nil", err)
	}
	if got := scanner.calls.Load(); got != 0 {
		t.Errorf("%d stops reached the scanner after the scan ended, want 0", got)
	}
}

func TestScanGuardNotScanningIsBenign(t *testing.T) {
	scanner := &countingScanner{err: errors.New(libraryNotScanning)}
	guard := newScanGuard(scanner.stop)

	if err := guard.Stop(); err != nil {
		t.Errorf("Stop() = %v, want nil for a scan that is not running", err)
	}
	if guard.Stopped() {
		t.Error("Stopped() = true after a stop that found no scan, want false so the stop can be retried")
	}
	scanner.err = nil
	if err := guard.Stop(); err != nil {
		t.Errorf("second Stop() = %v, want nil", err)
	}
	if got := scanner.calls.Load(); got != 2 {
		t.Errorf("%d stops reached the scanner, want 2 (the first found no scan)", got)
	}
}

func TestScanGuardOtherErrorsAreReturned(t *testing.T) {
	cause := errors.New("bus closed")
	scanner := &countingScanner{err: cause}
	guard := newScanGuard(scanner.stop)

	if err := guard.Stop(); !errors.Is(err, cause) {
		t.Errorf("Stop() = %v, want %v", err, cause)
	}
}

// fakeLibrary behaves like the bluetooth library's Scan on the one point that
// matters here: Scan marks itself running, and refuses to start while it is
// marked, until StopScan clears the mark. A hand-built fake is allowed because
// the library's panic can't be produced on the Mac (owner decision 11); the
// real library is tested on lola.
type fakeLibrary struct {
	running        bool
	panicNextScan  bool
	stopScanCalls  int
	stopDiscCalls  int
	callOrder      []string
	discoveringNow bool
}

var errFakeScanning = errors.New("bluetooth: a scan is already in progress")

func (l *fakeLibrary) scan(callback func(string)) error {
	if l.running {
		return errFakeScanning
	}
	l.running = true
	l.discoveringNow = true
	if l.panicNextScan {
		// The library asserts props["UUIDs"].Value().([]string) without a
		// comma-ok while evaluating the callback's argument, outside the
		// callback's body.
		var uuids any
		callback(fmt.Sprint(uuids.([]string)))
	}
	l.running = false
	l.discoveringNow = false
	return nil
}

func (l *fakeLibrary) stopScan() error {
	l.stopScanCalls++
	l.callOrder = append(l.callOrder, "StopScan")
	if !l.running {
		return errors.New(libraryNotScanning)
	}
	l.running = false
	return nil
}

func (l *fakeLibrary) stopDiscovery() error {
	l.stopDiscCalls++
	l.callOrder = append(l.callOrder, "StopDiscovery")
	l.discoveringNow = false
	return nil
}

func TestScanRecoversPanicInScanCall(t *testing.T) {
	library := &fakeLibrary{panicNextScan: true}
	callback := func(string) { t.Error("callback body ran, want the panic before it") }

	guard := newScanGuard(library.stopScan)
	err := recoverScan(func() error { return library.scan(callback) }, guard, library.stopDiscovery)
	if err == nil {
		t.Fatal("recoverScan() = nil after a panic, want a scan error")
	}
	t.Logf("recoverScan() after a panic = %v", err)
	if library.stopScanCalls != 1 || library.stopDiscCalls != 1 {
		t.Errorf("reset hooks: StopScan %d times, StopDiscovery %d times, want 1 and 1", library.stopScanCalls, library.stopDiscCalls)
	}
	if got, want := fmt.Sprint(library.callOrder), "[StopScan StopDiscovery]"; got != want {
		t.Errorf("reset order = %s, want %s", got, want)
	}
	if library.running || library.discoveringNow {
		t.Errorf("after the panic the library is running=%v discovering=%v, want both false", library.running, library.discoveringNow)
	}

	library.panicNextScan = false
	guard = newScanGuard(library.stopScan)
	if err := recoverScan(func() error { return library.scan(callback) }, guard, library.stopDiscovery); err != nil {
		t.Errorf("second recoverScan() = %v, want nil", err)
	}
}

func TestRecoverScanPassesErrorsThrough(t *testing.T) {
	cause := errors.New("adapter not powered")
	library := &fakeLibrary{}
	guard := newScanGuard(library.stopScan)
	err := recoverScan(func() error { return cause }, guard, library.stopDiscovery)
	if !errors.Is(err, cause) {
		t.Errorf("recoverScan() = %v, want %v", err, cause)
	}
	if library.stopDiscCalls != 0 || library.stopScanCalls != 0 {
		t.Errorf("reset hooks ran without a panic: StopScan %d, StopDiscovery %d", library.stopScanCalls, library.stopDiscCalls)
	}
	if !guard.Stopped() {
		t.Error("guard not ended after the scan returned")
	}
}

func TestStopWhenDoneRetriesUntilTheScanStarts(t *testing.T) {
	var calls atomic.Int32
	guard := newScanGuard(func() error {
		// The library's Scan has not set up its state for the first calls.
		if calls.Add(1) < 4 {
			return errors.New(libraryNotScanning)
		}
		return nil
	})
	done := make(chan struct{})
	close(done)
	finished := make(chan struct{})
	go func() {
		stopWhenDone(done, make(chan struct{}), guard, time.Millisecond)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("stopWhenDone did not return after the stop took")
	}
	if got := calls.Load(); got != 4 {
		t.Errorf("%d stops reached the scanner, want 4 (three before the scan started, then one that took)", got)
	}
}

func TestStopWhenDoneGivesUpWhenTheScanEnds(t *testing.T) {
	scanner := &countingScanner{}
	guard := newScanGuard(scanner.stop)
	ended := make(chan struct{})
	close(ended)
	stopWhenDone(make(chan struct{}), ended, guard, time.Millisecond)
	if got := scanner.calls.Load(); got != 0 {
		t.Errorf("%d stops reached the scanner after the scan ended on its own, want 0", got)
	}
}
