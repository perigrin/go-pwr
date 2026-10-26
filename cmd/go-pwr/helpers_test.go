// ABOUTME: Small helpers shared by go-pwr's binary tests: a temporary build
// ABOUTME: directory and a readable subtest name for an argument list.

package main

import (
	"os"
	"strings"
)

// builtDir makes the directory the test binary is built into. It lives for
// the whole test run and is removed by TestMain.
func builtDir() (string, error) {
	return os.MkdirTemp("", "go-pwr-test-")
}

func joinArgs(args []string) string {
	return strings.Join(args, " ")
}
