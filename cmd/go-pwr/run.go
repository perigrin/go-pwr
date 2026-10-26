// ABOUTME: Runs go-pwr's one-shot mode: opens the Bluetooth adapter, reads the
// ABOUTME: controller once and prints the reading on stdout.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/perigrin/go-pwr/ble"
	"github.com/perigrin/go-pwr/internal/boot"
	"github.com/perigrin/go-pwr/status"
)

// connectAttempts is how many times one reading tries to connect. About 7% of
// connects on lola are aborted by the local controller, most often right
// after a link dropped, and BlueZ then accepts the next try. The attempts
// share the reading's --timeout.
const connectAttempts = 3

// connectPause is the wait between connect attempts.
const connectPause = time.Second

// run reads the controller and returns the exit status: 0 only if a reading
// was printed, 1 otherwise. SIGINT and SIGTERM end a reading in progress,
// and the link is closed on the way out.
func run(cfg config, stdout, stderr io.Writer) int {
	if cfg.poll > 0 || cfg.follow > 0 {
		fmt.Fprintln(stderr, "go-pwr: --poll and --follow are not implemented")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return oneShot(ctx, cfg, stdout, stderr)
}

// oneShot opens the adapter, then scans, connects, requests, decodes and
// prints one reading, all within cfg.timeout.
func oneShot(ctx context.Context, cfg config, stdout, stderr io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "error: internal error: %v\n", r)
			code = 1
		}
	}()
	adapter, err := ble.Open()
	if err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	found, err := adapter.Scan(ctx, cfg.mac)
	if err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	controller, err := selectController(found)
	if err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	conn, err := connect(ctx, adapter, controller.Address)
	if err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	defer closeConn(conn, stderr)
	reply, err := conn.RequestStatus(ctx)
	if err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	rx := receivedReply{reply: reply, device: controller.Address, at: time.Now()}
	if rx.bootID, err = boot.ID(); err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	if rx.uptimeS, err = boot.Uptime(); err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	return report(cfg, rx, stdout, stderr)
}

// connect opens the link to the controller, trying again after a failed
// attempt while the reading's budget lasts. A controller BlueZ doesn't know
// won't appear by trying again, and neither will anything once ctx is done.
func connect(ctx context.Context, adapter *ble.Adapter, address string) (*ble.Conn, error) {
	var err error
	for attempt := 1; ; attempt++ {
		var conn *ble.Conn
		if conn, err = adapter.Connect(ctx, address); err == nil {
			return conn, nil
		}
		if attempt == connectAttempts || ctx.Err() != nil || errors.Is(err, ble.ErrNotFound) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(connectPause):
		}
	}
}

// closeConn closes the link on every way out of a reading. A failure, or a
// panic, in Close is reported and never crashes the program.
func closeConn(conn *ble.Conn, stderr io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "error: close: internal error: %v\n", r)
		}
	}()
	if err := conn.Close(); err != nil {
		io.WriteString(stderr, status.ErrorLine(fmt.Errorf("close: %w", err)))
	}
}

// receivedReply is a whole reply from the controller, with where and when it
// was received.
type receivedReply struct {
	reply   string
	device  string
	at      time.Time // local time, so the output carries the local offset
	bootID  string
	uptimeS float64
}

// report decodes a received reply, prints the reading on stdout in the
// format cfg asks for and its diagnostics on stderr, and returns the exit
// status: 0 if a reading was printed, 1 if not. Every stderr line about the
// reply quotes it once.
func report(cfg config, rx receivedReply, stdout, stderr io.Writer) int {
	reading, err := status.Decode(rx.reply)
	if err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	rec := status.Record{Time: rx.at, BootID: rx.bootID, UptimeS: rx.uptimeS, Device: rx.device, Reading: reading}
	if cfg.format == "json" {
		err = status.WriteJSONLine(stdout, rec)
	} else {
		err = status.NewTextWriter(stdout).Write(rec)
	}
	if err != nil {
		io.WriteString(stderr, status.ErrorLine(err))
		return 1
	}
	for _, fe := range reading.FieldErrors {
		io.WriteString(stderr, status.ErrorLine(&status.ReplyError{Reply: reading.Raw, Err: fe}))
	}
	for _, w := range reading.Warnings {
		io.WriteString(stderr, status.WarningLine(w, reading.Raw))
	}
	return 0
}
