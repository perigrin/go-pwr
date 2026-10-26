// ABOUTME: Runs go-pwr's --follow mode: one reading every interval, start to
// ABOUTME: start, over one held connection, until SIGINT or SIGTERM.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/perigrin/go-pwr/ble"
	"github.com/perigrin/go-pwr/internal/boot"
)

// followMode reads the controller every cfg.follow over one held connection
// until ctx is done, which is not an error: it returns 0, after the link is
// closed. Startup and the failed readings that follow it are handled as in
// pollMode. The reading that a signal ends is cancelled before the link is
// closed, since Close waits for a reading in flight.
//
// A failed reading drops the Follower and the adapter, and the next reading
// makes new ones: the Follower has already closed its connection, and the
// adapter's D-Bus object can't recover from BlueZ or the bus restarting.
func followMode(ctx context.Context, cfg config, stdout, stderr io.Writer) int {
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
	var follower *ble.Follower
	defer func() {
		if follower != nil {
			closeFollower(follower, stderr)
		}
	}()
	out := newRecordWriter(cfg, stdout)
	pollLoop(ctx, cfg.follow, func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
		if !protect(stderr, func() bool {
			if adapter == nil {
				var err error
				if adapter, err = ble.ReopenContext(ctx); err != nil {
					failed(ctx, stderr, err)
					return false
				}
			}
			if follower == nil {
				follower = adapter.Follow(cfg.mac)
			}
			return followOnce(ctx, cfg, follower, out, stderr)
		}) {
			if follower != nil {
				closeFollower(follower, stderr)
			}
			follower, adapter = nil, nil
		}
	})
	return 0
}

// followOnce takes one reading on the held connection, prints it, and says
// whether a reading was printed. It reports every failure on stderr itself.
func followOnce(ctx context.Context, cfg config, follower *ble.Follower, out recordWriter, stderr io.Writer) bool {
	bootID, err := boot.ID()
	if err != nil {
		failed(ctx, stderr, err)
		return false
	}
	reply, err := follower.Read(ctx)
	if err != nil {
		failed(ctx, stderr, err)
		return false
	}
	rx, err := receive(reply, cfg.mac, bootID, boot.Uptime)
	if err != nil {
		failed(ctx, stderr, err)
		return false
	}
	return reportTo(out, rx, stderr) == 0
}

// closeFollower closes the held link. A failure, or a panic, in Close is
// reported and never crashes the program.
func closeFollower(follower *ble.Follower, stderr io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			printPanic(stderr, "close", r)
		}
	}()
	if err := follower.Close(); err != nil {
		printError(stderr, fmt.Errorf("close: %w", err))
	}
}
