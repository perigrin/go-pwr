// ABOUTME: Tests the scan guard that serialises StopScan and recovers a panic
// ABOUTME: from the scan call; platform-neutral, so it runs under -race on the Mac.

package ble

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
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
