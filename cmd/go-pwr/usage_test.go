// ABOUTME: Tests that run the built go-pwr binary and check its exit status
// ABOUTME: and output streams for usage errors and missing-address errors.

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// wantMacRequired checks the missing --mac contract: exit 1, nothing on
// stdout, an error naming --mac on stderr.
func wantMacRequired(t *testing.T, args ...string) {
	t.Helper()
	r := runBinary(t, args...)
	if r.status != 1 || r.stdout != "" || !strings.Contains(r.stderr, "--mac") {
		t.Errorf("go-pwr %q: status %d, stdout %q, stderr %q; want status 1, empty stdout, stderr naming --mac",
			args, r.status, r.stdout, r.stderr)
	}
}

func TestMacRequiredForMachineModes(t *testing.T) {
	for _, args := range [][]string{
		{"--format", "json"},
		{"--format=json"},
		{"--poll"},
		{"--poll=10s"},
		{"--follow"},
		{"--follow=2s"},
	} {
		t.Run(joinArgs(args), func(t *testing.T) { wantMacRequired(t, args...) })
	}
}

func TestInvalidMacBeatsMissingMac(t *testing.T) {
	for _, args := range [][]string{
		{"--format", "json", "--mac", "nope"},
		{"--poll", "--mac", "nope"},
		{"--follow", "--mac", "nope"},
	} {
		t.Run(joinArgs(args), func(t *testing.T) { wantUsageError(t, args...) })
	}
}

func TestUsageErrorsPrecedeMacRequired(t *testing.T) {
	for _, args := range [][]string{
		{"--poll", "10s"},
		{"--format", "xml"},
		{"--format=json", "--timeout=0s"},
		{"--poll", "--follow"},
		{"--follow=soon"},
	} {
		t.Run(joinArgs(args), func(t *testing.T) { wantUsageError(t, args...) })
	}
}

func TestMacRejectsNonBluetoothForms(t *testing.T) {
	for _, mac := range []string{
		"C3:00:00:58:51",          // five octets
		"C3:00:00:58:51:40:41",    // seven octets
		"C3-00-00-58-51-40",       // dashes
		"C300.0058.5140",          // dots
		"C3:00:00:58:51:40:00:01", // EUI-64, which net.ParseMAC accepts
		"00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:01", // 20 bytes, likewise
		"C3:00:00:58:51:4G",   // non-hex digit
		"C:0:0:58:51:40",      // single-digit octets
		" C3:00:00:58:51:40",  // leading space
		"C3:00:00:58:51:40\n", // trailing newline
		"",                    // empty value
	} {
		t.Run(mac, func(t *testing.T) { wantUsageError(t, "--mac="+mac) })
	}
	r := runBinary(t, "--mac=c3:00:00:58:51:40")
	if r.status == 2 {
		t.Errorf("lowercase address rejected: stderr %q", r.stderr)
	}
}

func TestModeValueNotBoolShorthand(t *testing.T) {
	for _, args := range [][]string{
		{"--poll=1"},
		{"--poll=t"},
		{"--poll=T"},
		{"--poll=TRUE"},
		{"--follow=0"},
		{"--follow=f"},
		{"--poll=10"},
		{"--follow=5"},
		{"--poll="},
		{"--follow="},
	} {
		t.Run(joinArgs(args), func(t *testing.T) { wantUsageError(t, args...) })
	}
}

func TestFormatRejectsCaseAndEmpty(t *testing.T) {
	for _, args := range [][]string{
		{"--format", "JSON"},
		{"--format", "Text"},
		{"--format="},
		{"--format", "text,json"},
		{"--format", " text"},
	} {
		t.Run(joinArgs(args), func(t *testing.T) { wantUsageError(t, args...) })
	}
}

func TestPositionalAfterFlagsExit2(t *testing.T) {
	mac := "--mac=C3:00:00:58:51:40"
	for _, args := range [][]string{
		{mac, "extra"},
		{mac, "--format=json", "extra"},
		{mac, "--poll=10s", "extra"},
		{mac, "--", "extra"},
		{mac, "--format=json", "--", "--poll"},
		{"extra", mac},
	} {
		t.Run(joinArgs(args), func(t *testing.T) { wantUsageError(t, args...) })
	}
}

func TestMacRequiredBeforeBluetooth(t *testing.T) {
	for _, args := range [][]string{
		{"--format", "json"},
		{"--poll"},
		{"--follow"},
	} {
		t.Run(joinArgs(args), func(t *testing.T) {
			wantMacRequired(t, args...)
			r := runBinary(t, args...)
			if strings.Contains(strings.ToLower(r.stderr), "unsupported platform") {
				t.Errorf("go-pwr %q reached Bluetooth before checking --mac: stderr %q", args, r.stderr)
			}
		})
	}
}
