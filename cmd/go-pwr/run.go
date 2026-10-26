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
	"strings"
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

// run reads the controller and returns the exit status. A one-shot returns 0
// only if a reading was printed, 1 otherwise. --poll reads until SIGINT or
// SIGTERM, and returns 0 then, or 1 if it can't start. SIGINT and SIGTERM end
// a reading in progress, and the link is closed on the way out.
func run(cfg config, stdout, stderr io.Writer) int {
	if cfg.follow > 0 {
		fmt.Fprintln(stderr, "go-pwr: --follow is not implemented")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.poll > 0 {
		return pollMode(ctx, cfg, stdout, stderr)
	}
	return oneShot(ctx, cfg, stdout, stderr)
}

// oneShot opens the adapter, then scans, connects, requests, decodes and
// prints one reading, all within cfg.timeout.
func oneShot(ctx context.Context, cfg config, stdout, stderr io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			printPanic(stderr, "", r)
			code = 1
		}
	}()
	adapter, err := ble.Open()
	if err != nil {
		printError(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	if !readOnce(ctx, cfg, adapter, newRecordWriter(cfg, stdout), stderr) {
		return 1
	}
	return 0
}

// readOnce scans, connects, requests, decodes and prints one reading on an
// open adapter, within ctx, and says whether a reading was printed. It
// reports every failure on stderr itself, and a panic in it counts as a
// failed reading, so a loop of readings goes on. The link is closed on every
// way out.
func readOnce(ctx context.Context, cfg config, adapter *ble.Adapter, out recordWriter, stderr io.Writer) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			printPanic(stderr, "", r)
			ok = false
		}
	}()
	// The boot ID is read before the scan, so that a failure to read it can't
	// cost a reply that was already received.
	bootID, err := boot.ID()
	if err != nil {
		printError(stderr, err)
		return false
	}
	found, err := adapter.Scan(ctx, cfg.mac)
	if err != nil {
		printError(stderr, err)
		return false
	}
	controller, err := selectController(found)
	if err != nil {
		printError(stderr, err)
		return false
	}
	conn, err := connect(ctx, adapter, controller.Address)
	if err != nil {
		printError(stderr, err)
		return false
	}
	defer closeConn(conn, stderr)
	reply, err := conn.RequestStatus(ctx)
	if err != nil {
		printError(stderr, err)
		return false
	}
	rx, err := receive(reply, controller.Address, bootID, boot.Uptime)
	if err != nil {
		printError(stderr, err)
		return false
	}
	return reportTo(out, rx, stderr) == 0
}

// receive stamps a reply that has just been received with the local time and
// the uptime taken together. An error from uptime quotes the reply, so the
// reply that arrived is kept as evidence.
func receive(reply, device, bootID string, uptime func() (float64, error)) (receivedReply, error) {
	rx := receivedReply{reply: reply, device: device, at: time.Now(), bootID: bootID}
	var err error
	if rx.uptimeS, err = uptime(); err != nil {
		return rx, &status.ReplyError{Reply: reply, Err: err}
	}
	return rx, nil
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
		if err := waitToRetry(ctx, connectPause); err != nil {
			return nil, err
		}
	}
}

// waitToRetry pauses between connect attempts. When ctx ends in the pause,
// the error says the reading was interrupted, which is what the person needs
// to know, not the attempt that failed before it.
func waitToRetry(ctx context.Context, pause time.Duration) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("connect: interrupted while waiting to try again: %w", ctx.Err())
	case <-time.After(pause):
		return nil
	}
}

// closeConn closes the link on every way out of a reading. A failure, or a
// panic, in Close is reported and never crashes the program.
func closeConn(conn *ble.Conn, stderr io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			printPanic(stderr, "close", r)
		}
	}()
	if err := conn.Close(); err != nil {
		printError(stderr, fmt.Errorf("close: %w", err))
	}
}

// printPanic writes a recovered panic to stderr as one error line, escaped
// like any other, since its text can be anything. what names the step that
// panicked, or is empty.
func printPanic(stderr io.Writer, what string, r any) {
	if what != "" {
		what += ": "
	}
	printError(stderr, fmt.Errorf("%sinternal error: %v", what, r))
}

// printError writes err to stderr as one error line.
func printError(stderr io.Writer, err error) {
	io.WriteString(stderr, status.ErrorLine(nameClass(err)))
}

// nameClass puts the name of err's BlueZ error class in front of its message
// when the message doesn't have it. The message comes from D-Bus, such as
// `Method "Get" … doesn't exist` for an adapter that is gone, which doesn't
// say what is wrong.
func nameClass(err error) error {
	for _, class := range []error{ble.ErrNoAdapter, ble.ErrAccessDenied, ble.ErrBlueZNotRunning, ble.ErrAdapterUnavailable} {
		if errors.Is(err, class) && !strings.Contains(err.Error(), class.Error()) {
			return fmt.Errorf("%w: %w", class, err)
		}
	}
	return err
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

// recordWriter writes readings to stdout in one format. A text writer
// remembers that it has written, so that it separates the readings of a
// continuous mode with a blank line.
type recordWriter interface {
	Write(status.Record) error
}

// jsonWriter writes each reading as one JSON line.
type jsonWriter struct{ w io.Writer }

func (j jsonWriter) Write(rec status.Record) error { return status.WriteJSONLine(j.w, rec) }

// newRecordWriter returns the writer for the format cfg asks for.
func newRecordWriter(cfg config, stdout io.Writer) recordWriter {
	if cfg.format == "json" {
		return jsonWriter{stdout}
	}
	return status.NewTextWriter(stdout)
}

// report decodes a received reply and prints it, as reportTo does, for a
// single reading.
func report(cfg config, rx receivedReply, stdout, stderr io.Writer) int {
	return reportTo(newRecordWriter(cfg, stdout), rx, stderr)
}

// reportTo decodes a received reply, prints the reading with out and its
// diagnostics on stderr, and returns the exit status: 0 if a reading was
// printed, 1 if not. Every stderr line about the reply quotes it once.
func reportTo(out recordWriter, rx receivedReply, stderr io.Writer) int {
	reading, err := status.Decode(rx.reply)
	if err != nil {
		printError(stderr, err)
		return 1
	}
	rec := status.Record{Time: rx.at, BootID: rx.bootID, UptimeS: rx.uptimeS, Device: rx.device, Reading: reading}
	if err = out.Write(rec); err != nil {
		printError(stderr, err)
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
