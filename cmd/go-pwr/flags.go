// ABOUTME: Command-line parsing for go-pwr: the config it produces and the
// ABOUTME: flag value types for --poll and --follow.

package main

import (
	"flag"
	"io"
	"time"
)

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
		d, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*m.d = d
	}
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
	fs.DurationVar(&cfg.timeout, "timeout", cfg.timeout, "budget for one reading")
	fs.Var(modeValue{&cfg.poll, 30 * time.Second}, "poll", "read every `DUR`, connecting each time")
	fs.Var(modeValue{&cfg.follow, 5 * time.Second}, "follow", "hold one connection and read every `DUR`")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return cfg, nil
}
