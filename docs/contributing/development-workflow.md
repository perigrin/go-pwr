---
stability: 1
covers: []
---

## Setup

Install Go 1.24 or later and git, then clone the repository. Run `go build ./cmd/go-pwr` and `go test ./...` to check the build. Neither needs Bluetooth hardware.

## Hardware tests

The integration and end-to-end tests talk to a real Go Power! GP-PWM-30-SB controller. They are skipped unless `GO_PWR_TEST_MAC` is set to the controller's address:

```
GO_PWR_TEST_MAC=C3:00:00:58:51:40 go test ./...
```

Run them on a Linux machine in Bluetooth range of the controller, with BlueZ running. The controller appears to accept only one connection at a time, so first close the Go Power! Connect app and stop any `go-pwr` process that owns the controller (on lola, the poller feeding the shutdown script).

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
