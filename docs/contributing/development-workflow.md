---
stability: 1
covers: ["docs/decisions/0001-go-pwr-design.md", "go.mod"]
---

## Setup

Install Go 1.27 with gvm (`gvm install go1.27.3`, then `gvm use go1.27.3`) and git, then clone the repository. The `toolchain go1.27.3` line in `go.mod` is what selects the toolchain: with `GOTOOLCHAIN=auto`, the default, any shell builds with Go 1.27.3. Run `go build ./cmd/go-pwr` and `go test ./...` to check the build. Neither needs Bluetooth hardware.

## Hardware tests

The integration and end-to-end tests talk to a real Go Power! GP-PWM-30-SB controller. They are skipped unless `GO_PWR_TEST_MAC` is set to the controller's address:

```
GO_PWR_TEST_MAC=C3:00:00:58:51:40 go test ./...
```

Run them on a Linux machine in Bluetooth range of the controller, with BlueZ running. The controller appears to accept only one connection at a time, so first close the Go Power! Connect app and stop any `go-pwr` process that owns the controller (on lola, the poller feeding the shutdown script).

lola, the Raspberry Pi Zero W in range of the controller, has no Go toolchain. Hardware tests are cross-compiled on the Mac with `go test -c`, copied to `lola:go-pwr-test/` with `scp`, and run over `ssh lola` from the Mac repo root:

```
GOOS=linux GOARCH=arm GOARM=6 go test -c -trimpath -buildvcs=false -o ble.test ./ble
ssh lola mkdir -p go-pwr-test
scp ble.test lola:go-pwr-test/
ssh lola "cd go-pwr-test && env GO_PWR_TEST_MAC=C3:00:00:58:51:40 ./ble.test -test.v"
```

Tests that need root run with `ssh lola sudo -n …`. lola takes one ssh session at a time, so run one hardware test binary at a time.

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
