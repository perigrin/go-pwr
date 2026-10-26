// ABOUTME: Tests the poll loop's timing with real time and short intervals:
// ABOUTME: start to start, no overlap, no make-up, and prompt shutdown.

package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// runLoop runs pollLoop with reading for about total, then cancels it, and
// returns when each reading started and ended.
func runLoop(t *testing.T, interval, total time.Duration, reading func(context.Context)) (starts, ends []time.Time) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		pollLoop(ctx, interval, func(ctx context.Context) {
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			reading(ctx)
			mu.Lock()
			ends = append(ends, time.Now())
			mu.Unlock()
		})
	}()
	time.Sleep(total)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollLoop still running 5s after its context was cancelled")
	}
	mu.Lock()
	defer mu.Unlock()
	return starts, ends
}

func sleepFor(d time.Duration) func(context.Context) {
	return func(ctx context.Context) {
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
	}
}

func TestPollLoopStartsEveryIntervalStartToStart(t *testing.T) {
	const interval = 200 * time.Millisecond
	starts, _ := runLoop(t, interval, 1050*time.Millisecond, sleepFor(60*time.Millisecond))
	if len(starts) != 6 {
		t.Fatalf("%d readings in 1.05 s at %v, want 6 (at 0, 200, ... 1000 ms)", len(starts), interval)
	}
	for i := 1; i < len(starts); i++ {
		// Start to start, so a reading's own 60 ms doesn't stretch the interval.
		if d := starts[i].Sub(starts[i-1]); d < interval-5*time.Millisecond || d > interval+60*time.Millisecond {
			t.Errorf("reading %d started %v after the one before, want about %v", i, d, interval)
		}
	}
}

func TestPollLoopLongReadingDelaysTheNextWithoutMakeUp(t *testing.T) {
	const interval = 100 * time.Millisecond
	const long = 350 * time.Millisecond
	starts, ends := runLoop(t, interval, 1000*time.Millisecond, sleepFor(long))
	if len(starts) < 2 {
		t.Fatalf("%d readings, want at least 2", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Before(ends[i-1]) {
			t.Errorf("reading %d started before reading %d ended: overlap", i, i-1)
		}
		// Making up the missed intervals would start readings back to back,
		// well under the reading's own length.
		if d := starts[i].Sub(starts[i-1]); d < long-5*time.Millisecond {
			t.Errorf("reading %d started %v after the previous, less than a reading takes (%v)", i, d, long)
		}
	}
	if len(starts) > 3 {
		t.Errorf("%d readings of %v in 1 s, want at most 3: the missed intervals were made up", len(starts), long)
	}
}

func TestPollLoopStopsWaitingWhenCancelled(t *testing.T) {
	start := time.Now()
	starts, _ := runLoop(t, time.Hour, 50*time.Millisecond, sleepFor(0))
	if len(starts) != 1 {
		t.Errorf("%d readings, want 1: the first one starts at once", len(starts))
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("took %v to stop, want well under the hour-long interval", took)
	}
}

func TestPollLoopStartsNothingOnceCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pollLoop(ctx, time.Millisecond, func(context.Context) { t.Error("a reading started on a cancelled context") })
}

func TestPollLoopReadingSeesCancellation(t *testing.T) {
	starts, ends := runLoop(t, time.Hour, 50*time.Millisecond, sleepFor(time.Hour))
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("%d starts, %d ends, want 1 and 1: the reading must end when the context is cancelled", len(starts), len(ends))
	}
}

// A loop that kept a schedule, next += interval, would start the readings it
// missed back to back once a long one ended. This is the case that tells the
// two apart: after one long reading, the loop waits a whole interval again.
func TestPollLoopDoesNotMakeUpMissedIntervals(t *testing.T) {
	const interval = 100 * time.Millisecond
	var mu sync.Mutex
	calls := 0
	reading := func(ctx context.Context) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			sleepFor(350 * time.Millisecond)(ctx)
		} else {
			sleepFor(10 * time.Millisecond)(ctx)
		}
	}
	starts, _ := runLoop(t, interval, 700*time.Millisecond, reading)
	if len(starts) < 4 {
		t.Fatalf("%d readings, want at least 4", len(starts))
	}
	// The second reading started when the long one ended. The third is a
	// whole interval later, not right behind it as a make-up would be.
	if d := starts[2].Sub(starts[1]); d < interval-10*time.Millisecond || d > interval+60*time.Millisecond {
		t.Errorf("third reading started %v after the second, want about %v: missed intervals were made up", d, interval)
	}
}
