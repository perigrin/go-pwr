// ABOUTME: Tests that go-pwr on an operating system without a Bluetooth layer
// ABOUTME: exits 1 with an "unsupported platform" error and nothing on stdout.

//go:build !linux

package main

import (
	"strings"
	"testing"
)

func TestUnsupportedPlatformExit1(t *testing.T) {
	r := runBinary(t, "--mac", "C3:00:00:58:51:40")
	if r.status != 1 || r.stdout != "" || !strings.Contains(r.stderr, "unsupported platform") {
		t.Errorf("status %d, stdout %q, stderr %q; want status 1, empty stdout, stderr naming unsupported platform",
			r.status, r.stdout, r.stderr)
	}
}
