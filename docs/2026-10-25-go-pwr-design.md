# go-pwr design

`go-pwr` reads live status from a Go Power! **GP-PWM-30-SB** solar charge controller (catalog name GP-SB-PWM-30-BT) over Bluetooth LE and prints it, either for a person or as JSON for other tools. It is the first of a set of small Unix-style tools for **lola**, a Raspberry Pi Zero W in a travel trailer. Other tools (a shell script that shuts lola down safely on low battery, a web dashboard) consume its output. Those are out of scope here.

## Goals

- Read the controller's live status: battery voltage, solar (PV) voltage, charge current, charge state, state of charge, temperature, amp-hour history.
- Print one reading, or a continuous stream of readings, as human-readable text or JSON lines.
- Never change the controller's state.
- Be usable as a Go library later, without restructuring.

## Non-goals

- Changing controller settings, or sending any command other than the status request.
- The safe-shutdown script, the web dashboard, systemd units, packaging.
- Other devices (Victron shunt, Lippert OneControl, Starlink, Coleman thermostat).
- Decoding every field of the reply. Undecoded fields are kept in `raw`.

## The protocol

The full protocol, its sources and how confident we are in each field are in [protocol.md](protocol.md). In short:

- Connect without pairing. The controller appears to allow one connection at a time.
- Enable notifications on `FFF1`, write a single space to `FFF2` (service `FFF0`), and concatenate the notifications.
- The reply is ASCII decimal fields separated by `;`, complete at 30–32 fields. `go-pwr` decodes indices 0 (charge mA), 2 (battery mV), 9 (battery type), 10 (state of charge %), 11 (PV mV), 12 (charge state), 16/17 (°C/°F) and 19–23 (amp-hour history).
- Charge state code 0 is reported as `bulk`, which is what the app shows. The other codes use the app's code names, lower-cased. An unknown code is reported as `unknown`, with its number.
- **`go-pwr` has no code path that sends anything other than the status request.** The app's reset, force-charge and settings commands are never sent.

## Command-line interface

```
go-pwr [--mac ADDR] [--format text|json] [--timeout DUR] [--poll[=DUR] | --follow[=DUR]]
```

| Flag | Default | Meaning |
|---|---|---|
| `--mac` | none: scan for a device advertising the name `GP-PWM-30-SB` | Controller address. Set it to skip the scan and to avoid picking up a neighbor's controller. |
| `--format` | `text` | `text` for people, `json` for one JSON object per line |
| `--timeout` | `20s` | Limit on connecting and receiving one complete reply |
| `--poll[=DUR]` | off; bare flag means `30s` | Read every DUR, connecting and disconnecting each time, which leaves the controller free for the phone app in between |
| `--follow[=DUR]` | off; bare flag means `5s` | Hold one connection and read every DUR. The phone app is locked out while this runs. |

- Interval values attach with `=` (`--poll=10s`). `--poll 10s` is rejected as a usage error, because Go's flag parser would otherwise treat `10s` as a positional argument.
- `--poll` and `--follow` together is a usage error.
- Without `--poll` or `--follow`: take one reading, print it, exit.
- Durations use Go syntax (`500ms`, `10s`, `2m`).

### Behavior and errors

- **stdout carries only successful readings.** Errors go to stderr as one line each, so a consumer never has to filter them out.
- **One-shot:** exit status `0` on success, `1` if the reading fails (no device found, connect failure, timeout, incomplete or unparseable reply).
- **Continuous modes:** a failed reading writes its error to stderr and the loop continues at the next interval. In `--follow` mode, a dropped connection is re-established at the next interval.
- **Usage errors:** exit status `2`, with usage text on stderr.
- **SIGINT and SIGTERM:** finish or abandon the current reading, disconnect, exit `0`. This matters under systemd.
- A reply is **complete** when it splits into 30–32 fields. A reply that doesn't before `--timeout` is an error ("incomplete reply"). It is never partially parsed.

## Output formats

### text (default)

One block per reading, with a blank line between readings in continuous modes:

```
time             2026-10-25T15:42:10-04:00
battery          13.24 V   AGM
solar            22.88 V
charge           0.66 A    bulk
state of charge  100%
temperature      22°C / 72°F
amp-hours        today 12  yesterday 15  2d 16  3d 4  7d 93
```

### json

One object per line, with units in the key names:

```json
{"time":"2026-10-25T15:42:10-04:00","device":"C3:00:00:58:51:40","battery_v":13.24,"solar_v":22.88,"charge_a":0.66,"soc_pct":100,"charge_state":"bulk","charge_state_code":0,"battery_type":"agm","battery_type_code":3,"temp_c":22,"temp_f":72,"ah_today":12,"ah_yesterday":15,"ah_2d":16,"ah_3d":4,"ah_7d":93,"raw":"00655;200;13244;…"}
```

- `time` is when the reply was received, in RFC 3339 with the local offset.
- `device` is the controller's address.
- `raw` is the untouched reply, so undecoded fields can be worked out later from logs.
- Voltages and currents are rounded to two decimal places.

## Code structure

Module `github.com/perigrin/go-pwr`:

```
cmd/go-pwr/main.go   flags, mode selection, poll/follow loop, signals, exit codes
status/              parse a reply into a Reading (pure, no Bluetooth)
format/              Reading → text or JSON (pure)
ble/                 the only package that touches Bluetooth
docs/
LICENSE              Artistic License 2.0
```

- **`status`**: `Parse(raw string) (Reading, error)`. Errors on a field count outside 30–32, or on a non-numeric value in a decoded field.
- **`format`**: `Text(w io.Writer, r Reading) error` and `JSON(w io.Writer, r Reading) error`.
- **`ble`**: uses `tinygo.org/x/bluetooth` (pure Go over BlueZ D-Bus on Linux, no cgo). `Connect(ctx, mac string) (*Client, error)`, where an empty `mac` means scan by name. Then `(*Client).Read(ctx) (string, error)`, which subscribes to `FFF1`, writes `" "` to `FFF2`, and collects notifications until the reply is complete or the context expires. And `(*Client).Close() error`.
- The library packages sit at the module root so other Go programs can import them. The poll/follow loop stays in `cmd/` until a library user needs it.

## Testing

No mocks and no fake Bluetooth device.

- **Unit tests (`status`, `format`)** use the real reply above, plus real-shaped edge cases: 30- and 31-field replies, a short reply, a non-numeric decoded field, negative temperatures, an unknown charge-state code. These run anywhere with `go test ./...`.
- **Integration tests (`ble`)** run against the real controller. They connect, read, and check that the reply parses and is plausible (battery 10–16 V, PV 0–30 V, charge 0–30 A). They are skipped unless `GO_PWR_TEST_MAC` is set.
- **End-to-end tests** build the binary and run it against the real controller: one-shot in both formats, `--poll=…` and `--follow=…` for a few readings each, SIGTERM handling, and usage errors (`--poll 10s`, `--poll` with `--follow`). The usage-error cases don't need hardware and always run. The hardware cases need `GO_PWR_TEST_MAC`.
- The hardware tests run on lola, which is in Bluetooth range of the controller.

## Build and deploy

- Cross-compile on the Mac: `GOOS=linux GOARCH=arm GOARM=6 go build ./cmd/go-pwr`, then copy the binary to lola.
- `go install github.com/perigrin/go-pwr/cmd/go-pwr@latest` for anyone building natively.
- BlueZ must be running on the target (it is on Raspberry Pi OS). Reading the controller needs no special privileges beyond access to the system D-Bus BlueZ API, which the default `bluetooth` group policy allows. This should be confirmed on lola during implementation.

## Open questions

- Indices 22 and 23 (the 3-day and 7-day amp-hour figures) need checking against the app's History screen.
- Whether the controller really allows only one connection, or whether it was something about that particular attempt. `--follow`'s documented lock-out depends on this.
