// ABOUTME: Tests the Bluetooth layer's BlueZ adapter handling on Linux, on
// ABOUTME: lola with GO_PWR_TEST_MAC set; tests that change state need root.

package ble

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// requireHardware skips the test unless it runs on a machine configured for
// hardware tests. Nothing in these tests connects to the controller.
func requireHardware(t *testing.T) {
	t.Helper()
	if os.Getenv("GO_PWR_TEST_MAC") == "" {
		t.Skip("GO_PWR_TEST_MAC not set")
	}
}

// requireRoot skips the test unless it runs as root. Tests that change lola's
// state run as `ssh lola "cd go-pwr-test && sudo -n env GO_PWR_TEST_MAC=… ./ble.test …"`.
func requireRoot(t *testing.T) {
	t.Helper()
	requireHardware(t)
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
}

// sh runs a shell script and returns its combined output.
func sh(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Logf("sh -c %q: %v: %s", script, err, out)
	}
	return string(out)
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

// restoreOnCleanup registers the restore before a test changes any state,
// so it runs even when the test fails, then waits for a powered adapter.
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

func TestOpenClassifiesStartupErrors(t *testing.T) {
	requireRoot(t)
	restoreOnCleanup(t)

	t.Run("blocked adapter", func(t *testing.T) {
		sh(t, "rfkill block bluetooth")
		time.Sleep(time.Second)
		_, err := Open()
		var dbusErr dbus.Error
		t.Logf("Open() error = %v (D-Bus error: %v, %#v)", err, errors.As(err, &dbusErr), dbusErr)
		if !errors.Is(err, ErrAdapterUnavailable) || !errors.Is(err, ErrFatalAtStartup) {
			t.Errorf("Open() error = %v, want ErrAdapterUnavailable and ErrFatalAtStartup", err)
		}
	})
	sh(t, "rfkill unblock bluetooth")
	waitPowered(t)

	t.Run("BlueZ stopped", func(t *testing.T) {
		stopBlueZ(t)
		_, err := Open()
		var dbusErr dbus.Error
		t.Logf("Open() error = %v (D-Bus error: %v, %#v)", err, errors.As(err, &dbusErr), dbusErr)
		if !errors.Is(err, ErrBlueZNotRunning) || !errors.Is(err, ErrFatalAtStartup) {
			t.Errorf("Open() error = %v, want ErrBlueZNotRunning and ErrFatalAtStartup", err)
		}
	})
}

func TestOpenAdapter(t *testing.T) {
	requireHardware(t)
	adapter, err := Open()
	if err != nil {
		t.Fatalf("Open() error = %v, want nil on a powered adapter", err)
	}
	if adapter == nil {
		t.Fatal("Open() returned a nil Adapter and no error")
	}
}

const (
	denyDropIn   = "/etc/dbus-1/system.d/zz-go-pwr-test-deny.conf"
	nobodyDir    = "/tmp/go-pwr-test-nobody"
	reloadBusCmd = "dbus-send --system --type=method_call --dest=org.freedesktop.DBus / org.freedesktop.DBus.ReloadConfig"
)

// TestOpenAccessDenied makes the bus deny user nobody access to BlueZ, then
// runs this binary as nobody to see what Open returns. The child run is the
// same test with GO_PWR_ACCESS_DENIED_CHILD set.
func TestOpenAccessDenied(t *testing.T) {
	requireHardware(t)
	if os.Getenv("GO_PWR_ACCESS_DENIED_CHILD") == "1" {
		_, err := Open()
		var dbusErr dbus.Error
		t.Logf("Open() error = %v (D-Bus error: %v, %#v)", err, errors.As(err, &dbusErr), dbusErr)
		if !errors.Is(err, ErrAccessDenied) || !errors.Is(err, ErrFatalAtStartup) {
			t.Errorf("Open() error = %v, want ErrAccessDenied and ErrFatalAtStartup", err)
		}
		if errors.Is(err, ErrBlueZNotRunning) {
			t.Errorf("Open() error = %v, access denied must not be told as not running", err)
		}
		return
	}
	requireRoot(t)
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

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(nobodyDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nobodyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := nobodyDir + "/ble.test"
	if err := os.WriteFile(child, binary, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("runuser", "-u", "nobody", "--", "env", "HOME=/tmp",
		"GO_PWR_ACCESS_DENIED_CHILD=1", "GO_PWR_TEST_MAC="+os.Getenv("GO_PWR_TEST_MAC"),
		child, "-test.run", "^TestOpenAccessDenied$", "-test.v").CombinedOutput()
	t.Logf("child output:\n%s", out)
	if err != nil {
		t.Errorf("child run as nobody: %v", err)
	}
	if !strings.Contains(string(out), "--- PASS: TestOpenAccessDenied (") {
		t.Errorf("child run as nobody did not pass")
	}
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

func TestOpenNoAdapter(t *testing.T) {
	requireRoot(t)
	restoreOnCleanup(t)
	unbindAdapter(t)

	_, err := Open()
	var dbusErr dbus.Error
	t.Logf("Open() error = %v (D-Bus error: %v, %#v)", err, errors.As(err, &dbusErr), dbusErr)
	if !errors.Is(err, ErrNoAdapter) || !errors.Is(err, ErrFatalAtStartup) {
		t.Errorf("Open() error = %v, want ErrNoAdapter and ErrFatalAtStartup", err)
	}
}

func TestAdapterUnboundAfterOpenIsRetryable(t *testing.T) {
	requireRoot(t)
	restoreOnCleanup(t)
	adapter, err := Open()
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	unbindAdapter(t)

	err = adapter.Check()
	t.Logf("Check() with hci0 unbound = %v", err)
	if !errors.Is(err, ErrRetryable) || !errors.Is(err, ErrNoAdapter) {
		t.Errorf("Check() error = %v, want ErrRetryable and ErrNoAdapter", err)
	}
	if errors.Is(err, ErrFatalAtStartup) {
		t.Errorf("Check() error = %v, an adapter lost after Open must not be fatal at startup", err)
	}

	sh(t, "echo serial0-0 | tee /sys/bus/serial/drivers/hci_uart_bcm/bind")
	if !waitPowered(t) {
		t.Fatal("adapter not powered 30s after rebinding hci0")
	}
	if err := adapter.Check(); err != nil {
		t.Errorf("Check() after rebinding hci0 = %v, want nil", err)
	}
}

// rfkillBlocked reports whether rfkill soft-blocks Bluetooth.
func rfkillBlocked(t *testing.T) bool {
	t.Helper()
	return strings.Contains(sh(t, "rfkill list bluetooth"), "Soft blocked: yes")
}

func TestOpenAdapterPoweredOff(t *testing.T) {
	requireRoot(t)
	restoreOnCleanup(t)
	sh(t, "bluetoothctl power off")
	if rfkillBlocked(t) {
		t.Fatal("adapter is blocked, want it powered off only")
	}

	_, err := Open()
	t.Logf("Open() with the adapter powered off = %v", err)
	if !errors.Is(err, ErrAdapterUnavailable) || !errors.Is(err, ErrFatalAtStartup) {
		t.Errorf("Open() error = %v, want ErrAdapterUnavailable and ErrFatalAtStartup", err)
	}
}

func TestOpenDoesNotPowerAdapter(t *testing.T) {
	requireRoot(t)
	restoreOnCleanup(t)

	sh(t, "bluetoothctl power off")
	if _, err := Open(); err == nil {
		t.Error("Open() with the adapter powered off succeeded, want an error")
	}
	if strings.Contains(sh(t, "bluetoothctl show"), "Powered: yes") {
		t.Error("adapter is powered after a failed Open, want it still off")
	}

	sh(t, "rfkill block bluetooth")
	if _, err := Open(); err == nil {
		t.Error("Open() with the adapter blocked succeeded, want an error")
	}
	if !rfkillBlocked(t) {
		t.Error("adapter is unblocked after a failed Open, want it still blocked")
	}
	if strings.Contains(sh(t, "bluetoothctl show"), "Powered: yes") {
		t.Error("adapter is powered after a failed Open, want it still off")
	}
}

func TestAdapterLossAfterOpenIsRetryable(t *testing.T) {
	requireRoot(t)
	restoreOnCleanup(t)
	adapter, err := Open()
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	retryable := func(t *testing.T, class error) {
		t.Helper()
		err := adapter.Check()
		t.Logf("Check() = %v", err)
		if !errors.Is(err, ErrRetryable) || !errors.Is(err, class) {
			t.Errorf("Check() error = %v, want ErrRetryable and %v", err, class)
		}
		if errors.Is(err, ErrFatalAtStartup) {
			t.Errorf("Check() error = %v, must not be fatal at startup", err)
		}
	}

	sh(t, "rfkill block bluetooth")
	time.Sleep(time.Second)
	retryable(t, ErrAdapterUnavailable)
	sh(t, "rfkill unblock bluetooth")
	if !waitPowered(t) {
		t.Fatal("adapter not powered 30s after unblocking it")
	}
	if err := adapter.Check(); err != nil {
		t.Errorf("Check() after unblocking = %v, want nil", err)
	}

	stopBlueZ(t)
	retryable(t, ErrBlueZNotRunning)
	sh(t, "systemctl unmask --runtime bluetooth.service; systemctl start bluetooth")
	if !waitPowered(t) {
		t.Fatal("adapter not powered 30s after restarting BlueZ")
	}
	if err := adapter.Check(); err != nil {
		t.Errorf("Check() after restarting BlueZ = %v, want nil", err)
	}
}

func TestOpenErrorClassesAreTyped(t *testing.T) {
	requireRoot(t)
	restoreOnCleanup(t)
	classes := []error{ErrNoAdapter, ErrAccessDenied, ErrBlueZNotRunning, ErrAdapterUnavailable}

	for _, tc := range []struct {
		name   string
		cause  func(t *testing.T)
		want   error
		revert string
	}{
		{"powered off", func(t *testing.T) { sh(t, "bluetoothctl power off") }, ErrAdapterUnavailable, "bluetoothctl power on"},
		{"blocked", func(t *testing.T) { sh(t, "rfkill block bluetooth"); time.Sleep(time.Second) }, ErrAdapterUnavailable, "rfkill unblock bluetooth"},
		{"BlueZ stopped", stopBlueZ, ErrBlueZNotRunning, "systemctl unmask --runtime bluetooth.service; systemctl start bluetooth"},
		{"adapter unbound", unbindAdapter, ErrNoAdapter, "echo serial0-0 | tee /sys/bus/serial/drivers/hci_uart_bcm/bind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cause(t)
			_, err := Open()
			wrapped := fmt.Errorf("start up: %w", err)
			for _, class := range classes {
				if got, want := errors.Is(wrapped, class), class == tc.want; got != want {
					t.Errorf("errors.Is(%q, %v) = %v, want %v", wrapped, class, got, want)
				}
			}
			if !errors.Is(wrapped, ErrFatalAtStartup) {
				t.Errorf("errors.Is(%q, ErrFatalAtStartup) = false, want true", wrapped)
			}
			var dbusErr dbus.Error
			if tc.want != ErrAdapterUnavailable && !errors.As(wrapped, &dbusErr) {
				t.Errorf("errors.As(%q, dbus.Error) = false, want the D-Bus error", wrapped)
			}
			sh(t, tc.revert)
			if !waitPowered(t) {
				t.Fatalf("adapter not powered 30s after reverting %s", tc.name)
			}
		})
	}
}
