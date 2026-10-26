// ABOUTME: Tests that run the built go-pwr binary and check its exit status
// ABOUTME: and output streams for usage errors and missing-address errors.

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	builtPath string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if builtPath != "" {
		os.RemoveAll(filepath.Dir(builtPath))
	}
	os.Exit(code)
}

// binary builds go-pwr once per test run and returns its path.
func binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := builtDir()
		if err != nil {
			buildErr = err
			return
		}
		builtPath = filepath.Join(dir, "go-pwr")
		if out, err := exec.Command("go", "build", "-o", builtPath, ".").CombinedOutput(); err != nil {
			buildErr = errors.New("go build: " + err.Error() + "\n" + string(out))
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return builtPath
}

// result is one run of the binary.
type result struct {
	status         int
	stdout, stderr string
}

func runBinary(t *testing.T, args ...string) result {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	status := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		status = exit.ExitCode()
	case err != nil:
		t.Fatalf("running go-pwr %q: %v", args, err)
	}
	return result{status, stdout.String(), stderr.String()}
}

// wantUsageError checks the usage-error contract: exit 2, nothing on stdout,
// something on stderr.
func wantUsageError(t *testing.T, args ...string) {
	t.Helper()
	r := runBinary(t, args...)
	if r.status != 2 || r.stdout != "" || r.stderr == "" {
		t.Errorf("go-pwr %q: status %d, stdout %q, stderr %q; want status 2, empty stdout, non-empty stderr",
			args, r.status, r.stdout, r.stderr)
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{"--poll", "10s"},
		{"--poll", "--follow"},
		{"--poll=true", "--follow=true"},
		{"--timeout=0s"},
		{"--timeout=-1s"},
		{"--timeout=soon"},
		{"--poll=0s"},
		{"--follow=-5s"},
		{"--mac", "nope"},
		{"--format", "xml"},
		{"--no-such-flag"},
	} {
		t.Run(joinArgs(args), func(t *testing.T) { wantUsageError(t, args...) })
	}
}
