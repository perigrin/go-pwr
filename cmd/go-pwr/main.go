// ABOUTME: Entry point for go-pwr: parses the command line, rejects usage
// ABOUTME: errors with exit status 2, and reaches the run step.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr))
}

// realMain returns the exit status: 0 for success, 1 for a runtime failure
// and 2 for a usage error.
func realMain(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	// Not a usage error: the command line is valid, it just can't run.
	if cfg.mac == "" && cfg.machineMode() {
		fmt.Fprintln(stderr, "go-pwr: --mac is required with --format json, --poll or --follow")
		return 1
	}
	return run(cfg, stdout, stderr)
}

// run is a placeholder until the one-shot reading is implemented. It makes no
// Bluetooth call.
func run(cfg config, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "go-pwr: reading is not implemented")
	return 1
}
