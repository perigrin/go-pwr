// ABOUTME: Serialises stops of a scan so a timeout and a callback cannot race
// ABOUTME: each other, and recovers a panic raised inside the scan call.

package ble

import "sync"

// libraryNotScanning is the text of the bluetooth library's errNotScanning,
// which it does not export. StopScan returns it when no scan is running.
const libraryNotScanning = "bluetooth: there is no scan in progress"

// scanGuard lets one scan be stopped from several goroutines, such as the
// timeout and the scan callback, without two stops reaching the library at
// once. The library's StopScan reads, closes and clears a channel without a
// lock, so two overlapping calls can close it twice.
type scanGuard struct {
	mu      sync.Mutex
	stop    func() error
	stopped bool
}

// newScanGuard returns a guard that stops the scan with stop.
func newScanGuard(stop func() error) *scanGuard {
	return &scanGuard{stop: stop}
}

// Stop stops the scan once. Later calls, and calls after End, do nothing and
// return nil. A stop that finds no scan running is benign: it returns nil but
// leaves the guard open, because the scan may not have started yet, so the
// caller can try again.
func (g *scanGuard) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return nil
	}
	err := g.stop()
	if err != nil && err.Error() == libraryNotScanning {
		return nil
	}
	g.stopped = true
	return err
}

// End records that the scan has returned on its own, so no later Stop
// reaches the library.
func (g *scanGuard) End() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
}

// Stopped reports whether the scan has been stopped or has ended.
func (g *scanGuard) Stopped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopped
}
