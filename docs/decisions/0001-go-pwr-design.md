# go-pwr design

`go-pwr` reads live status from a Go Power! **GP-PWM-30-SB** solar charge controller (catalog name GP-SB-PWM-30-BT) over Bluetooth LE and prints it, either for a person or as JSON for other tools. It is the first of a set of small Unix-style tools for **lola**, a Raspberry Pi Zero W in a travel trailer. Other tools consume its output: a shell script that shuts lola down safely on low battery, and a web dashboard. Those tools are out of scope here, but this decision states what they may rely on.

## Goals

- Read the controller's live status: battery voltage, solar (PV) voltage, charge current, charge state, battery type, temperature, amp-hour history.
- Print one reading, or a continuous stream of readings, as human-readable text or JSON lines.
- Never change the controller's state.
- Make the decoding usable from other Go programs: the code that turns a reply into a reading, and a reading into output, is importable and needs no Bluetooth.

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
- Index 10 is **not** decoded. It was guessed to be state of charge, but it read 100 while the battery was bulk-charging at 13.24 V, and nothing supports that meaning.
- **Evidence is thin.** Charge current, battery voltage, PV voltage, battery type, charge state 0 and °F were each seen once, in one reply, matching the iPhone app. °C was consistent with °F, though the app didn't show it. The amp-hour history, the other charge-state codes, and whether the amp-hour fields are decimal rest on the Android code alone. The reference labels every field.

### What go-pwr writes

- To `FFF2`, the only thing `go-pwr` ever writes is the single byte `0x20`. `FFF2` feeds a serial command parser, so nothing else may reach it.
- Its only other writes are the standard descriptor writes that turn `FFF1` notifications on and off.
- Connection-level traffic that BlueZ itself generates is outside `go-pwr`'s control and not covered here.

## Command-line interface

```
go-pwr [--mac ADDR] [--format text|json] [--timeout DUR] [--poll[=DUR] | --follow[=DUR]]
```

| Flag | Default | Meaning |
|---|---|---|
| `--mac` | none | Controller address. **Required** with `--format json`, `--poll` or `--follow`. |
| `--format` | `text` | `text` for people, `json` for one JSON object per line |
| `--timeout` | `20s` | Budget for one reading: finding the controller, connecting when needed, and the reply |
| `--poll[=DUR]` | off; bare flag or `=true` means `30s` | Read every DUR, connecting and disconnecting each time |
| `--follow[=DUR]` | off; bare flag or `=true` means `5s` | Hold one connection and read every DUR |

- Durations use Go syntax (`500ms`, `10s`, `2m`). Interval values attach with `=`. `go-pwr` takes no positional arguments, so `--poll 10s` is a usage error.
- `--poll=false` and `--follow=false` mean off. Setting both to on is a usage error. A zero, negative or unparseable duration, for any flag, is a usage error.
- Without `--poll` or `--follow`: take one reading, print it, exit.

### Finding the controller

- **Machine use requires `--mac`.** Many trailers have this controller, and a campground puts several in range. A script that read a neighbour's battery could shut lola down, or miss lola's own low battery. So JSON output and both continuous modes refuse to run without `--mac`.
- Without `--mac`, a one-shot text reading is for a person finding the address to use. If exactly one `GP-PWM-30-SB` is seen during the scan, it is read and its address reported. If more than one is seen, `go-pwr` prints nothing on stdout. It lists every address found, with its signal strength, on stderr, and exits 1, so a person never pins a neighbour's controller by accident.

### Timing

- The interval runs from the start of one reading to the start of the next. Readings never overlap. If one runs long, the next starts when it finishes, and missed intervals are not made up.

### Behavior and errors

- **stdout carries only successful readings.** Errors go to stderr, one line each. When the error concerns a reply that was received, the line includes that reply verbatim, so rejected replies remain evidence.
- **A reading is printed only if it is the whole reply and plausible.**
  - *Whole* means the reply as observed: 32 fields, the last empty after a trailing `;`, with the notifications assembled in the order the controller sent them. A cut-off reply is never printed, even though it can already split into 30 fields.
  - *Plausible* means its decoded values are within what the hardware can physically report, and agree with each other. For example, °F must equal °C × 9/5 + 32 to within 1°F. The bounds must never reject a real reading, however low: a flat battery at night is exactly the reading the shutdown script needs.
- **One-shot:** exit status `0` if a reading was printed, `1` otherwise. That includes being interrupted by SIGINT or SIGTERM before a reading was printed.
- **Continuous modes:** a reading that fails writes its error to stderr, and the loop continues at the next interval. In `--follow` mode, a lost connection is re-established at the next interval. An error that can't clear without someone stepping in (no Bluetooth adapter, or access to BlueZ denied) ends the loop with exit status `1`. SIGINT or SIGTERM ends it with exit status `0`.
- **Usage errors:** exit status `2`, with usage text on stderr.
- **Disconnecting:** `go-pwr` disconnects after every reading in one-shot and `--poll` mode, failed readings included, and on every exit it controls (normal, error, SIGINT, SIGTERM). It can't do so if it is killed outright or crashes, and BlueZ may then keep the link up. Each run must therefore work even if a previous one left a link behind.

### Staleness

A continuous mode prints nothing while the controller is unreachable. That happens when it is out of range, when it is powered down, and **whenever the phone app is connected to it**. A consumer must decide what silence means, and silence is not the same as a low battery. lola has no real-time clock, so its wall clock can step at boot or when it syncs over the network. A consumer should measure silence on its own monotonic clock, not by comparing `time` with the current time.

### Sharing the controller

The controller appears to accept **one connection at a time**. `go-pwr` doesn't arbitrate between consumers. So:

- On lola, **one `go-pwr` process owns the controller.** The intended setup is a single `go-pwr --poll --format json --mac …` writing to a log, which the shutdown script and the dashboard both read.
- `--poll` leaves the controller free for the phone app between readings. `--follow` locks the phone app, and any other consumer, out for as long as it runs.
- Hardware tests on lola need the owner process stopped while they run.

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

- Each reading is written as **one complete, newline-terminated line**, flushed when the reading completes, so a reader of a live log never sees part of a line.
- **Every key shown is always present and never null.** Keys may be added in later versions. Existing keys are never renamed or removed, and never change meaning, within a major version.
- `time` is when the reply was received, in RFC 3339 with the local offset. See **Staleness** for why it isn't a freshness signal.
- `device` is the controller's address.
- `raw` is the untouched reply, so undecoded fields can be worked out later from logs.
- Voltages and currents are given to two decimal places, rounded half-up from the controller's mV and mA values (655 mA is 0.66 A).
- `ah_yesterday`, `ah_2d`, `ah_3d` and `ah_7d` follow the Android app's labels and haven't been checked against a controller's real history (see **Open questions**).

### Code names

The JSON strings are part of the interface that scripts depend on, so they are fixed here:

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

Only codes 0 (charge state) and 3 (battery type) have been observed. The rest follow the Android app. An unknown code is never an error, and the numeric code is always in `charge_state_code` and `battery_type_code`.

## Shape of the code

- Go, with Bluetooth through `tinygo.org/x/bluetooth`, which talks to BlueZ over D-Bus without cgo.
- The command lives under `cmd/`. The decoding and formatting are packages other programs can import.
- Turning notifications into a reply, a reply into a reading, and a reading into output is pure logic with no Bluetooth. The Bluetooth layer hands that logic the notifications in the order the controller sent them.
- On a platform without BlueZ, the Bluetooth layer still builds and returns an "unsupported platform" error, so the whole module builds and its non-hardware tests run on the Mac.

## Testing

No mocks and no fake Bluetooth device.

- **Unit tests** cover reply assembly, decoding and formatting. They use replies and notification sequences captured from the real controller. Error cases are made by cutting or altering real captures, never invented from scratch. Unit tests and usage-error tests run on any machine.
- **Hardware tests** (Bluetooth, and the command end to end in every mode) run on lola against the real controller, with the owner process stopped. They are skipped unless `GO_PWR_TEST_MAC` is set. They include a check of every byte written to the controller.
- More captures are needed before implementation relies on them: **low battery at night** (the shutdown case), no PV, Float, cold, amp-hours today counting past 9 (`00010` means decimal, `0000A` means hex), and the notification sizes and timing of a reply on lola.

## Prerequisites

- On the target: BlueZ running, the Bluetooth adapter powered and unblocked, and access to BlueZ on the system D-Bus.

## Open questions

- Whether the amp-hour fields are decimal: logging `raw` until amp-hours today passes 9 will settle it.
- Indices 20–23 (amp-hours for earlier days): what period each one covers.
- Index 10: what it actually is.
- Whether the controller really allows only one connection. **Sharing the controller** and **Staleness** depend on this.
