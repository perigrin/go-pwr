// ABOUTME: End-to-end tests of one-shot startup failures on lola, run as root
// ABOUTME: because they stop BlueZ, unbind hci0 or deny a user access to it.

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// requireRoot skips the test unless it runs as root, as
// `ssh lola "cd go-pwr-test && sudo -n env GO_PWR_TEST_MAC=… ./gopwr.test …"`.
func requireRoot(t *testing.T) string {
	t.Helper()
	mac := requireHardware(t)
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	return mac
}

// restoreLola puts back everything these tests change. Its commands are
// joined with `;`, never `&&`, so a step that fails still lets the rest run.
const restoreLola = `systemctl unmask --runtime bluetooth.service; ` +
	`systemctl start bluetooth; ` +
	`rfkill unblock bluetooth; ` +
	`test -e /sys/bus/serial/drivers/hci_uart_bcm/serial0-0 || echo serial0-0 | tee /sys/bus/serial/drivers/hci_uart_bcm/bind; ` +
	`rm -f /etc/dbus-1/system.d/zz-go-pwr-test-deny.conf; ` +
	`dbus-send --system --type=method_call --dest=org.freedesktop.DBus / org.freedesktop.DBus.ReloadConfig; ` +
	`bluetoothctl power on`

// waitPowered powers the adapter on and waits up to 30 seconds for BlueZ to
// report it powered.
func waitPowered(t *testing.T) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(sh(t, "bluetoothctl show"), "Powered: yes") {
			return true
		}
		sh(t, "bluetoothctl power on")
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// restoreOnCleanup registers the restore before a test changes any state, so
// it runs even when the test fails, then waits for a powered adapter.
func restoreOnCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		sh(t, restoreLola)
		if !waitPowered(t) {
			t.Errorf("adapter not powered 30s after restoring lola; run the restore command in docs/contributing/development-workflow.md")
		}
	})
}

// stopBlueZ masks bluetooth.service for this boot, so D-Bus activation can
// not restart it, then stops it.
func stopBlueZ(t *testing.T) {
	t.Helper()
	sh(t, "systemctl mask --runtime bluetooth.service && systemctl stop bluetooth")
}

// unbindAdapter removes hci0 by unbinding lola's Bluetooth UART driver, and
// waits for BlueZ to drop the adapter.
func unbindAdapter(t *testing.T) {
	t.Helper()
	sh(t, "echo serial0-0 | tee /sys/bus/serial/drivers/hci_uart_bcm/unbind")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/sys/class/bluetooth/hci0"); os.IsNotExist(err) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("hci0 still present 15s after unbinding it")
}

// wantStartupError checks the startup-failure contract: exit 1, nothing on
// stdout, and one stderr line that names the cause.
func wantStartupError(t *testing.T, r result, cause string) {
	t.Helper()
	if r.status != 1 || r.stdout != "" {
		t.Errorf("status %d, stdout %q, want 1 and nothing", r.status, r.stdout)
	}
	if strings.Count(r.stderr, "\n") != 1 || !strings.HasSuffix(r.stderr, "\n") || !strings.Contains(r.stderr, cause) {
		t.Errorf("stderr %q, want one line naming %q", r.stderr, cause)
	}
}

func TestOneShotStartupNoAdapterExit1(t *testing.T) {
	mac := requireRoot(t)
	restoreOnCleanup(t)
	unbindAdapter(t)

	r, _ := runGoPwr(t, "--mac", mac)
	wantStartupError(t, r, "no bluetooth adapter")
}

const (
	denyDropIn   = "/etc/dbus-1/system.d/zz-go-pwr-test-deny.conf"
	nobodyDir    = "/tmp/go-pwr-test-nobody"
	reloadBusCmd = "dbus-send --system --type=method_call --dest=org.freedesktop.DBus / org.freedesktop.DBus.ReloadConfig"
)

// runAsNobody runs the cross-built go-pwr as the user nobody, who the deny
// drop-in locks out of BlueZ. The drop-in, and the copy of the binary that
// nobody can read, are removed when the test ends.
func runAsNobody(t *testing.T, args ...string) result {
	t.Helper()
	t.Cleanup(func() {
		sh(t, "rm -f "+denyDropIn+"; "+reloadBusCmd+"; rm -rf "+nobodyDir)
	})

	policy := `<busconfig><policy user="nobody"><deny send_destination="org.bluez"/></policy></busconfig>`
	if err := os.WriteFile(denyDropIn, []byte(policy+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-c", reloadBusCmd).CombinedOutput(); err != nil {
		t.Fatalf("reload the bus: %v: %s", err, out)
	}

	binary, err := os.ReadFile(crossBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(nobodyDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nobodyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := nobodyDir + "/go-pwr"
	if err := os.WriteFile(child, binary, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := commandInGroup("runuser", append([]string{"-u", "nobody", "--", "env", "HOME=/tmp", child}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(60 * time.Second):
		// runuser starts go-pwr as a child; kill the group, or go-pwr goes on
		// polling after the deny drop-in is gone.
		killGroup(cmd)
		t.Fatalf("go-pwr as nobody still running after 60 s\nstdout:\n%sstderr:\n%s", &stdout, &stderr)
	}
	var exit *exec.ExitError
	status := 0
	switch {
	case errors.As(err, &exit):
		status = exit.ExitCode()
	case err != nil:
		t.Fatalf("running go-pwr as nobody: %v", err)
	}
	t.Logf("go-pwr as nobody: status %d\nstdout:\n%sstderr:\n%s", status, &stdout, &stderr)
	return result{status, stdout.String(), stderr.String()}
}

func TestOneShotStartupAccessDeniedExit1(t *testing.T) {
	mac := requireRoot(t)
	r := runAsNobody(t, "--mac", mac)
	wantStartupError(t, r, "access to BlueZ denied")
}

func TestOneShotStartupErrorsExit1(t *testing.T) {
	mac := requireRoot(t)
	restoreOnCleanup(t)

	for _, tc := range []struct {
		name   string
		cause  func(*testing.T)
		want   string
		revert string
	}{
		{"BlueZ stopped", stopBlueZ, "BlueZ not running", "systemctl unmask --runtime bluetooth.service; systemctl start bluetooth"},
		{"adapter powered off", func(t *testing.T) { sh(t, "bluetoothctl power off") }, "powered off or blocked", "bluetoothctl power on"},
		{"adapter blocked", func(t *testing.T) { sh(t, "rfkill block bluetooth"); time.Sleep(time.Second) }, "powered off or blocked", "rfkill unblock bluetooth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cause(t)
			r, _ := runGoPwr(t, "--mac", mac)
			wantStartupError(t, r, tc.want)
			sh(t, tc.revert)
			if !waitPowered(t) {
				t.Fatalf("adapter not powered 30s after reverting %s", tc.name)
			}
		})
	}
}
