# go-pwr design

`go-pwr` reads live status from a Go Power! **GP-PWM-30-SB** solar charge controller (catalog name GP-SB-PWM-30-BT) over Bluetooth LE and prints it, either for a person or as JSON for other tools. It is the first of a set of small Unix-style tools for **lola**, a Raspberry Pi Zero W in a travel trailer. Other tools consume its output: a shell script that shuts lola down safely on low battery, and a web dashboard. Those tools are out of scope here, but this decision states what they may rely on.

## Goals

- Read the controller's live status: battery voltage, solar (PV) voltage, charge current, charge state, battery type, temperature, amp-hour history.
- Print one reading, or a continuous stream of readings, as human-readable text or JSON lines.
- Never change the controller's state.
- Be usable as a Go library later, without restructuring.

## Non-goals

- Changing controller settings, or sending any command other than the status request.
- The safe-shutdown script, the web dashboard, systemd units, packaging.
- Arbitrating access between several consumers (see **Sharing the controller**).
- Other devices (Victron shunt, Lippert OneControl, Starlink, Coleman thermostat).
- Decoding every field of the reply. Undecoded fields are kept in `raw`.

## The protocol

The protocol, its sources, and how much evidence stands behind each field are in [the protocol reference](../reference/protocol.md). In short:

- Connect, enable notifications on `FFF1`, write a single space to `FFF2` (service `FFF0`), and assemble the notifications into one reply.
- The reply is ASCII decimal fields separated by `;`. `go-pwr` decodes indices 0 (charge mA), 2 (battery mV), 9 (battery type), 11 (PV mV), 12 (charge state), 16/17 (°C/°F) and 19–23 (amp-hour history).
- Index 10 is **not** decoded. It was guessed to be state of charge, but it read 100 while the battery was bulk-charging at 13.24 V, and there is no evidence for that meaning.
- Every decoded field has been observed in a single reply from one controller. The reference says so field by field.
- **`go-pwr` writes exactly one thing to `FFF2`: the single byte `0x20`.** Apart from the standard descriptor write that turns on `FFF1` notifications, it writes nothing else to the controller. The app's reset, force-charge, unlock and settings commands are never sent.

## Command-line interface

```
go-pwr [--mac ADDR] [--format text|json] [--timeout DUR] [--poll[=DUR] | --follow[=DUR]]
```

| Flag | Default | Meaning |
|---|---|---|
| `--mac` | none | Controller address. **Required** with `--format json`, `--poll` or `--follow`. |
| `--format` | `text` | `text` for people, `json` for one JSON object per line |
| `--timeout` | `20s` | Budget for one reading: finding the controller, connecting when needed, and the reply |
| `--poll[=DUR]` | off; bare flag means `30s` | Read every DUR, connecting and disconnecting each time |
| `--follow[=DUR]` | off; bare flag means `5s` | Hold one connection and read every DUR |

- Interval values attach with `=` (`--poll=10s`). `go-pwr` takes no positional arguments, so `--poll 10s` is a usage error.
- `--poll=false` and `--follow=false` mean off. Setting both `--poll` and `--follow` to on is a usage error. A zero or negative interval is a usage error.
- Without `--poll` or `--follow`: take one reading, print it, exit.

### Finding the controller

- **Machine use requires `--mac`.** Many trailers have this controller, and a campground puts several in range. A shutdown script that read a neighbour's battery could shut lola down, or miss lola's own low battery. So JSON output and both continuous modes refuse to run without `--mac`.
- Without `--mac`, a one-shot text reading matches the advertised name `GP-PWM-30-SB` and reports the controller it used. This is for a person at a terminal, finding the address to use.

### Timing

- The interval runs from the start of one reading to the start of the next. Readings never overlap. If one runs long, the next starts when it finishes, and missed intervals are not made up.

### Behavior and errors

- **stdout carries only successful readings.** Errors go to stderr, one line each.
- **A reading is printed only if it is complete and plausible.** It must be the whole reply, in order. Its decoded values must be physically possible and agree with each other: for example, the °C and °F fields must describe the same temperature. Anything else is an error. A partial or implausible reply is never printed.
- **One-shot:** exit status `0` if a reading was printed, `1` otherwise. That includes being interrupted by SIGINT or SIGTERM before a reading was printed.
- **Continuous modes:** a failed reading writes its error to stderr, and the loop continues at the next interval. In `--follow` mode, a lost connection is re-established at the next interval. SIGINT or SIGTERM ends the loop with exit status `0`.
- **Usage errors:** exit status `2`, with usage text on stderr.
- **Staleness is the consumer's job.** A continuous mode prints nothing while the controller is unreachable, so a consumer must judge freshness from each reading's `time` rather than wait for a line.
- On exit, `go-pwr` disconnects from the controller.

### Sharing the controller

The controller appears to accept **one connection at a time**: lola could not connect while the phone app was connected. `go-pwr` doesn't arbitrate between consumers. So:

- On lola, **one `go-pwr` process owns the controller.** The intended setup is a single `go-pwr --poll --format json --mac …` writing to a log, which the shutdown script and the dashboard both read.
- `--poll` leaves the controller free for the phone app between readings. `--follow` locks the phone app, and any other consumer, out for as long as it runs.

## Output formats

### text (default)

One block per reading, with a blank line between readings in continuous modes:

```
time             2026-10-25T15:42:10-04:00
device           C3:00:00:58:51:40
battery          13.24 V   AGM
solar            22.88 V
charge           0.66 A    Bulk
temperature      22°C / 72°F
amp-hours        today 12  yesterday 15  2d 16  3d 4  7d 93
```

### json

One object per line, with units in the key names:

```json
{"time":"2026-10-25T15:42:10-04:00","device":"C3:00:00:58:51:40","battery_v":13.24,"solar_v":22.88,"charge_a":0.66,"charge_state":"bulk","charge_state_code":0,"battery_type":"agm","battery_type_code":3,"temp_c":22,"temp_f":72,"ah_today":12,"ah_yesterday":15,"ah_2d":16,"ah_3d":4,"ah_7d":93,"raw":"00655;200;13244;…"}
```

- `time` is when the reply was received, in RFC 3339 with the local offset.
- `device` is the controller's address.
- `raw` is the untouched reply, so undecoded fields can be worked out later from logs.
- Voltages and currents are given to two decimal places, rounded half-up from the controller's mV and mA values (655 mA is 0.66 A).
- `ah_3d` and `ah_7d` are named after the app's own labels, which haven't been confirmed (see **Open questions**).

### Code names

The JSON strings are part of the interface scripts depend on, so they are fixed here:

| Charge state code | JSON `charge_state` | Text label |
|---|---|---|
| 0 | `bulk` | Bulk |
| 1 | `float` | Float |
| 2 | `equalization` | Equalization |
| 3 | `absorption` | Absorption |
| 4 | `bulk_equalization` | Bulk Equalization |
| 5 | `charge_current` | Charge Current |
| 6 | `charge_voltage` | Charge Voltage |
| 7 | `no_charge` | No Charge |
| other | `unknown` | unknown (N) |

| Battery type code | JSON `battery_type` | Text label |
|---|---|---|
| 0 | `sealed` | Sealed |
| 1 | `flooded` | Flooded |
| 2 | `lifepo4` | LiFePO4 |
| 3 | `agm` | AGM |
| 4 | `custom` | Custom |
| other | `unknown` | unknown (N) |

An unknown code is never an error. The numeric code is always in `charge_state_code` and `battery_type_code`.

## Code structure

Module `github.com/perigrin/go-pwr`:

```
cmd/go-pwr/   the command: flags, modes, signals, exit codes
status/       turn a reply into a Reading (pure, no Bluetooth)
format/       Reading → text or JSON (pure)
ble/          the only package that touches Bluetooth (Linux/BlueZ)
```

- The library packages sit at the module root, so other Go programs can import them. The poll/follow loop stays in `cmd/` until a library user needs it.
- `ble` uses `tinygo.org/x/bluetooth`, which talks to BlueZ over D-Bus without cgo. It builds only for Linux.
- Deciding when a reply is complete, its assembly, ordering and timing, is pure logic, kept out of `ble` so that tests can drive it.

## Testing

No mocks and no fake Bluetooth device.

- **Unit tests** (`status`, `format` and the reply-assembly logic) use replies and notification sequences captured from the real controller. Error cases are made from real captures by cutting or altering them, never invented from scratch. They run anywhere `go test ./...` runs.
- **Hardware tests** (`ble`, and the command end to end in every mode) run on lola against the real controller. They are skipped unless `GO_PWR_TEST_MAC` is set. Usage-error tests need no hardware and always run.
- Before implementation leans on them, more captures are needed: night (no PV), Float, cold, and the notification sizes and timing of a reply on lola.

## Build and deploy

- Cross-compile on the Mac for lola (Linux, ARMv6) and copy the binary over. `go install github.com/perigrin/go-pwr/cmd/go-pwr@latest` builds it natively.
- Prerequisites on the target: BlueZ running, the Bluetooth adapter powered and unblocked, and access to BlueZ on the system D-Bus.

## Open questions

- Indices 20–23 (amp-hours for earlier days): check against the app's History screen.
- Index 10: what it actually is.
- Whether the controller really allows only one connection. **Sharing the controller** depends on this.
