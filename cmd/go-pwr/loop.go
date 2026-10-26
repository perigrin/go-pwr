// ABOUTME: Runs go-pwr's --poll mode: one reading every interval, start to
// ABOUTME: start, each on a fresh connection, until SIGINT or SIGTERM.

package main

import (
	"context"
	"io"
	"time"

	"github.com/perigrin/go-pwr/ble"
)

// pollLoop calls reading every interval, counted from the start of one call
// to the start of the next, until ctx is done. The first call is at once.
// Calls never overlap: one that runs longer than the interval is followed by
// the next as soon as it returns, and the intervals it missed are not made
// up. Both timings use the monotonic clock, so a step of the wall clock
// can't shorten or stretch an interval.
func pollLoop(ctx context.Context, interval time.Duration, reading func(context.Context)) {
	for ctx.Err() == nil {
		start := time.Now()
		reading(ctx)
		wait := interval - time.Since(start)
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

// pollMode reads the controller every cfg.poll until ctx is done, which is
// not an error: it returns 0. Only the conditions at startup that need a
// person or a supervisor, such as no adapter, BlueZ denied or not running,
// or the adapter powered off or blocked, end the loop before it starts, with
// 1. After that a failed reading writes its error on stderr and the loop
// goes on.
//
// The adapter is opened again after every failed reading. ble.Adapter keeps
// its D-Bus object from the first connection, which can't recover from BlueZ
// or the bus restarting, and a fresh one costs a property read. A reading's
// --timeout covers the power-state read in opening the adapter, so a masked
// BlueZ, which takes about 15 s to fail it, can't hold up the loop for longer
// than a reading may take. The library's own calls, such as Enable and the
// ones a scan makes, are not bounded by it.
func pollMode(ctx context.Context, cfg config, stdout, stderr io.Writer) int {
	startup, cancel := context.WithTimeout(ctx, cfg.timeout)
	adapter, err := ble.OpenContext(startup)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		printError(stderr, err)
		return 1
	}
	out := newRecordWriter(cfg, stdout)
	pollLoop(ctx, cfg.poll, func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
		if adapter == nil {
			// A failed open is an ordinary failed reading, not a startup error.
			if !protect(stderr, func() bool {
				var err error
				if adapter, err = ble.ReopenContext(ctx); err != nil {
					failed(ctx, stderr, err)
					return false
				}
				return true
			}) {
				adapter = nil
				return
			}
		}
		if !readOnce(ctx, cfg, adapter, out, stderr) {
			adapter = nil
		}
	})
	return 0
}
