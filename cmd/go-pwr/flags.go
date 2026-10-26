// ABOUTME: Command-line parsing for go-pwr: the config it produces and the
// ABOUTME: flag value types for --poll and --follow.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"time"
)

const usageText = `usage: go-pwr [--mac ADDR] [--format text|json] [--timeout DUR] [--poll[=DUR] | --follow[=DUR]]`

// config is a parsed command line. A zero poll or follow means that mode is off.
type config struct {
	mac     string
	format  string
	timeout time.Duration
	poll    time.Duration
	follow  time.Duration
}

// modeValue is a flag.Value for --poll and --follow. A bare flag, or "true",
// selects def; "false" turns the mode off; anything else is a duration.
type modeValue struct {
	d   *time.Duration
	def time.Duration
}

func (m modeValue) String() string {
	if m.d == nil || *m.d == 0 {
		return "false"
	}
	return m.d.String()
}

func (m modeValue) Set(s string) error {
	switch s {
	case "true":
		*m.d = m.def
	case "false":
		*m.d = 0
	default:
		d, err := parsePositive(s)
		if err != nil {
			return err
		}
		*m.d = d
	}
	return nil
}

// parsePositive parses a Go duration and rejects zero and negative values.
func parsePositive(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return d, nil
}

// timeoutValue is the flag.Value for --timeout.
type timeoutValue struct{ d *time.Duration }

func (t timeoutValue) String() string {
	if t.d == nil {
		return ""
	}
	return t.d.String()
}

func (t timeoutValue) Set(s string) error {
	d, err := parsePositive(s)
	if err != nil {
		return err
	}
	*t.d = d
	return nil
}

// IsBoolFlag lets a bare --poll or --follow stand for Set("true").
func (m modeValue) IsBoolFlag() bool { return true }

// parseArgs parses a command line. The flag package writes problems and the
// usage text to stderr.
func parseArgs(args []string, stderr io.Writer) (config, error) {
	cfg := config{format: "text", timeout: 20 * time.Second}
	fs := flag.NewFlagSet("go-pwr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.mac, "mac", "", "controller `address`")
	fs.StringVar(&cfg.format, "format", cfg.format, "output format, text or json")
	fs.Var(timeoutValue{&cfg.timeout}, "timeout", "budget for one reading, default 20s")
	fs.Var(modeValue{&cfg.poll, 30 * time.Second}, "poll", "read every `DUR`, connecting each time")
	fs.Var(modeValue{&cfg.follow, 5 * time.Second}, "follow", "hold one connection and read every `DUR`")
	fs.Usage = func() { fmt.Fprintln(stderr, usageText) }
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if err := cfg.validate(fs.NArg()); err != nil {
		fmt.Fprintf(stderr, "go-pwr: %v\n", err)
		fs.Usage()
		return config{}, err
	}
	return cfg, nil
}

// validate checks what the flag package can't: the flags together.
func (c config) validate(positional int) error {
	if positional > 0 {
		return errors.New("unexpected argument; interval values attach with =, as in --poll=10s")
	}
	if c.poll > 0 && c.follow > 0 {
		return errors.New("--poll and --follow can't both be on")
	}
	if c.mac != "" {
		if _, err := net.ParseMAC(c.mac); err != nil {
			return fmt.Errorf("invalid --mac %q", c.mac)
		}
	}
	if c.format != "text" && c.format != "json" {
		return fmt.Errorf("invalid --format %q: want text or json", c.format)
	}
	return nil
}
