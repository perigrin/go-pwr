// ABOUTME: End-to-end tests of --follow on lola, run as root, for startup
// ABOUTME: failures and for failures in the middle of a run that it survives.

package main

import (
	"testing"
	"time"
)

// followThroughFailure runs throughFailure with --follow.
func followThroughFailure(t *testing.T, mac string, breakIt, fix func(*testing.T)) {
	t.Helper()
	throughFailure(t, mac, "--follow=8s", breakIt, fix)
}

func TestFollowContinuesAfterAdapterUnbound(t *testing.T) {
	mac := requireRoot(t)
	followThroughFailure(t, mac, unbindAdapter, rebindAdapter)
}

func TestFollowContinuesAfterBlueZRestart(t *testing.T) {
	mac := requireRoot(t)
	followThroughFailure(t, mac,
		stopBlueZ,
		func(t *testing.T) {
			sh(t, "systemctl unmask --runtime bluetooth.service; systemctl start bluetooth")
			if !waitPowered(t) {
				t.Fatal("adapter not powered 30 s after restarting BlueZ")
			}
		})
}

func TestFollowContinuesAfterAdapterBlocked(t *testing.T) {
	mac := requireRoot(t)
	followThroughFailure(t, mac,
		func(t *testing.T) { sh(t, "rfkill block bluetooth") },
		func(t *testing.T) {
			sh(t, "rfkill unblock bluetooth")
			if !waitPowered(t) {
				t.Fatal("adapter not powered 30 s after unblocking it")
			}
		})
}

// wantFollowStartupError starts --follow and checks the startup-failure
// contract: it exits 1 on its own, within limit, with nothing on stdout and
// one stderr line that names the cause.
func wantFollowStartupError(t *testing.T, mac, cause string, limit time.Duration) {
	t.Helper()
	r := startGoPwr(t, "--mac", mac, "--follow=10s", "--format", "json")
	got := r.finish(60 * time.Second)
	wantStartupError(t, got, cause)
	if took := time.Since(r.began); took > limit {
		t.Errorf("took %v to exit, want within %v", took, limit)
	}
}

func TestFollowStartupErrorsExit1(t *testing.T) {
	mac := requireRoot(t)
	restoreOnCleanup(t)

	for _, tc := range []struct {
		name   string
		cause  func(*testing.T)
		want   string
		limit  time.Duration
		revert func(*testing.T)
	}{
		{"no adapter", unbindAdapter, "no bluetooth adapter", 5 * time.Second, rebindAdapter},
		{"BlueZ stopped", stopBlueZ, "BlueZ not running", 25 * time.Second, func(t *testing.T) {
			sh(t, "systemctl unmask --runtime bluetooth.service; systemctl start bluetooth")
		}},
		{"adapter blocked", func(t *testing.T) { sh(t, "rfkill block bluetooth"); time.Sleep(time.Second) }, "powered off or blocked", 5 * time.Second, func(t *testing.T) { sh(t, "rfkill unblock bluetooth") }},
		{"adapter powered off", func(t *testing.T) { sh(t, "bluetoothctl power off") }, "powered off or blocked", 5 * time.Second, func(t *testing.T) { sh(t, "bluetoothctl power on") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cause(t)
			wantFollowStartupError(t, mac, tc.want, tc.limit)
			tc.revert(t)
			if !waitPowered(t) {
				t.Fatalf("adapter not powered 30s after reverting %s", tc.name)
			}
		})
	}
}
