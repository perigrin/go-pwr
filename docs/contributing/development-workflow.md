---
stability: 1
covers: ["docs/decisions/0001-go-pwr-design.md", "go.mod"]
---

## Setup

Install Go 1.27 with gvm (`gvm install go1.27.3`, then `gvm use go1.27.3`) and git, then clone the repository. The `toolchain go1.27.3` line in `go.mod` is what selects the toolchain: with `GOTOOLCHAIN=auto`, the default, any shell builds with Go 1.27.3. Run `go build ./cmd/go-pwr` and `go test ./...` to check the build. Neither needs Bluetooth hardware.

## Hardware tests

Linux tests and hardware tests run on **lola**, the Raspberry Pi Zero W (ARMv6, BlueZ) in Bluetooth range of the controller. lola has no Go toolchain, so each test binary is cross-compiled on the Mac, copied to `lola:go-pwr-test/`, and run there over `ssh lola` from the Mac repo root:

```
GOOS=linux GOARCH=arm GOARM=6 go test -c -trimpath -buildvcs=false -o ble.test ./ble
ssh lola mkdir -p go-pwr-test
scp ble.test lola:go-pwr-test/
ssh lola "cd go-pwr-test && env GO_PWR_TEST_MAC=C3:00:00:58:51:40 ./ble.test -test.count=1 -test.v"
```

The same pattern, `GOOS=linux GOARCH=arm GOARM=6 go test -c -trimpath -buildvcs=false -o <pkg>.test ./<pkg>` then `./<pkg>.test -test.count=1` on lola, works for every package. `-trimpath -buildvcs=false` makes the binary reproducible, so a binary on lola can be checked against a fresh cross-compile by SHA-256.

- The end-to-end tests (`gopwr.test`, from `./cmd/go-pwr`) run the cross-built `go-pwr` binary that sits beside them, so copy both to lola together, and again after every change, so neither is stale:

  ```
  d=$(mktemp -d) && GOOS=linux GOARCH=arm GOARM=6 go test -c -trimpath -buildvcs=false -o "$d/gopwr.test" ./cmd/go-pwr && GOOS=linux GOARCH=arm GOARM=6 go build -trimpath -buildvcs=false -o "$d/go-pwr" ./cmd/go-pwr && ssh lola mkdir -p go-pwr-test && scp "$d/gopwr.test" "$d/go-pwr" lola:go-pwr-test/
  ssh lola "cd go-pwr-test && env GO_PWR_TEST_MAC=C3:00:00:58:51:40 ./gopwr.test -test.count=1 -test.run '^TestOneShot' -test.v"
  ```

  The ones that stop BlueZ, unbind hci0 or deny `nobody` access need `sudo -n` as above. Without `--mac`, `TestOneShotWithoutMac` needs the test controller to be the only GP-PWM-30-SB in range.
- Tests that talk to the controller are skipped unless `GO_PWR_TEST_MAC` is set, on lola, to the controller's address. Tests that only need Linux, such as `internal/boot`, run without it.
- Tests that need root run with `ssh lola "cd go-pwr-test && sudo -n env GO_PWR_TEST_MAC=… ./ble.test …"`. `env` keeps `GO_PWR_TEST_MAC` through `sudo`, and `-n` means sudo never prompts. The `ble` tests that need root block the adapter with `rfkill`, power it off, unbind hci0 from `hci_uart_bcm`, mask and stop BlueZ, and install a deny drop-in for `nobody` in `/etc/dbus-1/system.d/`. Each restores what it changed in a `;`-joined `t.Cleanup`, so a failed step doesn't stop the rest.
- The btmon tests (`TestBtmon…` in `cmd/go-pwr`) run go-pwr under `btmon` and check every ATT write in the trace. Their prerequisites are `btmon` (BlueZ) on `PATH` and root or `CAP_NET_RAW` to capture, so they run with `sudo -n`; `TestBtmonPrerequisites` checks both, and every btmon test **fails**, never skips, when `GO_PWR_TEST_MAC` is unset or either is missing. Each live capture first clears BlueZ's GATT cache with `bluetoothctl remove` and rescans, so the trace holds discovery and the checker's handle map comes from it. The test refuses, and fails, if `bluetoothctl info` shows the controller Paired, Bonded or Trusted, since removing it would lose its keys:

  ```
  ssh lola "cd go-pwr-test && sudo -n env GO_PWR_TEST_MAC=C3:00:00:58:51:40 ./gopwr.test -test.count=1 -test.run '^TestBtmon' -test.v"
  ```

  The checker itself (`internal/btmon`) is pure parsing and its tests run on the Mac against the real lola trace and altered copies of it: `go test ./internal/btmon`.
- lola takes one ssh session at a time, so run one test binary at a time, and one ssh command at a time. lola refused ssh sessions with `resources` errors when nine hit it at once.
- The controller appears to accept only one connection at a time, so first close the Go Power! Connect app and stop any `go-pwr` process that owns the controller (on lola, the poller feeding the shutdown script).
- Every test that changes lola's state restores it, even when it fails. `TestUptimeIgnoresWallClockStep` steps the wall clock with `systemd-timesyncd` stopped; if it is killed before its cleanup runs, restore the clock with `ssh lola 'sudo -n systemctl start systemd-timesyncd; sudo -n timedatectl set-ntp true'`.

A test killed before its cleanup runs (a `-test.timeout` panic, a dropped ssh session, SIGKILL) can leave lola with BlueZ masked, the adapter blocked or unbound, or the deny drop-in installed. Restore it by hand:

```
ssh lola 'sudo -n systemctl unmask --runtime bluetooth.service; sudo -n rfkill unblock bluetooth; test -e /sys/bus/serial/drivers/hci_uart_bcm/serial0-0 || echo serial0-0 | sudo -n tee /sys/bus/serial/drivers/hci_uart_bcm/bind; sudo -n rm -f /etc/dbus-1/system.d/zz-go-pwr-test-deny.conf; sudo -n systemctl reload dbus; sudo -n systemctl start bluetooth; sudo -n systemctl start systemd-timesyncd; bluetoothctl power on'
```

Then check that lola is as it was: bluetooth active and not masked, `/sys/class/bluetooth/hci0` present, `bluetoothctl show` says `Powered: yes`, `/usr/sbin/rfkill list bluetooth` blocks nothing (plain `rfkill` is not on the ssh user's PATH), and `/etc/dbus-1/system.d/zz-go-pwr-test-deny.conf` is gone.

## Race testing

`go test -race` does not run on lola's target: `GOOS=linux GOARCH=arm GOARM=6 go test -race` exits 2, because the race detector does not support linux/arm. So race testing runs on the Mac, on the platform-neutral code that holds the concurrency. For the `ble` package that is the scan guard (`ble/scanguard.go`):

```
go test -race -count=1 -run TestScanGuard ./ble
```

(perigrin's decision, owner decision 10.) The hardware side, the library's own `StopScan` and `Scan` against real BlueZ, is covered by the tests that run on lola without `-race`. The library's `StopScan` and `Scan` share the unlocked `scanCancelChan` field, which the guard serialises only on go-pwr's side, so no tool here proves the library itself race-free.

## Branching

Create feature branches from `main`. Merge back to `main` through a pull request.

## Review

All changes need a pull request, and the tests must pass. Don't use `--no-verify`.

## Deploy

Cross-compile for a Raspberry Pi Zero W (ARMv6) and copy the binary over:

```
GOOS=linux GOARCH=arm GOARM=6 go build -o go-pwr ./cmd/go-pwr
scp go-pwr <host>:
```
