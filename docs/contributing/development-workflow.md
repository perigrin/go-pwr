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

- Tests that talk to the controller are skipped unless `GO_PWR_TEST_MAC` is set, on lola, to the controller's address. Tests that only need Linux, such as `internal/boot`, run without it.
- Tests that need root run with `ssh lola "cd go-pwr-test && sudo -n ./<pkg>.test …"`.
- lola takes one ssh session at a time, so run one test binary at a time.
- The controller appears to accept only one connection at a time, so first close the Go Power! Connect app and stop any `go-pwr` process that owns the controller (on lola, the poller feeding the shutdown script).
- Every test that changes lola's state restores it, even when it fails. `TestUptimeIgnoresWallClockStep` steps the wall clock with `systemd-timesyncd` stopped; if it is killed before its cleanup runs, restore the clock with `ssh lola 'sudo -n systemctl start systemd-timesyncd; sudo -n timedatectl set-ntp true'`.

## Branching

Create feature branches from `main`. Merge back to `main` through a pull request.

## Review

All changes need a pull request, and the tests must pass. Don't use `--no-verify`.

## Deploy

Cross-compile for a Raspberry Pi Zero W (ARMv6) and copy the binary over:

```
GOOS=linux GOARCH=arm GOARM=6 go build ./cmd/go-pwr
scp go-pwr <host>:
```
