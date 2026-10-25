# Go Power! GP-PWM-30-SB Bluetooth protocol

How to read live status from a Go Power! **GP-PWM-30-SB** solar charge controller (catalog name GP-SB-PWM-30-BT, a Dometic brand, fitted at the factory to many Forest River trailers) over Bluetooth LE.

## Sources and confidence

- **Decoded from** the Go Power! Connect Android app, `com.gpelectric.gopowermonitor` version 1.7.23 (version code 78), decompiled with jadx 1.5.6. The relevant classes are `pmwbattery.newCode.PwmSbBatteryManager` (requests and reply handling), `pmwbattery.newCode.PwmResponseParser` (field decoding), `pmwbattery.PwmBatteryUtil` (code tables) and `gopowerble.nnppt_sc.Utils.BleConstant` (device names).
- **Verified** on 2026-10-25 against one controller, firmware field `8`, set to AGM. The decoded values matched the app's Status screen, taken a few minutes apart: battery 13.24 V (app 13.27 V), PV 22.88 V (app 22.80 V), current 0.66 A (app 0.60 A), 12 Ah today (app 12 Ah), AGM, 72°F.
- Anything marked **unverified** below comes from the app's code or variable names only.

## Discovery

The controller advertises:

- **Name:** `GP-PWM-30-SB`. The app also knows `GP-PWM-30-DB`, a dual-bank model, which this document does not cover.
- **Manufacturer data:** company `0x0639` (Shenzhen Minew Technologies), payload ASCII `Minew Tech`. The Bluetooth radio is a Minew BLE-to-serial module.
- **Service data** for UUID `0xFFE1`: the device's own 6-byte MAC address.

The advertisements carry **no readings**. You have to connect.

## Connection

- No pairing or bonding. A plain connection works.
- The controller appears to allow **one connection at a time**. While the phone app was connected, other connection attempts failed. That was observed once and is not confirmed in general.
- Relevant GATT layout:

| Service | Characteristic | Use |
|---|---|---|
| `0000fff0-0000-1000-8000-00805f9b34fb` | `0000fff2-…` | write requests |
| | `0000fff1-…` | notify, replies |

A `0000ffe1-…` service is also present. The app doesn't use it for the PWM controller.

## Status request

1. Enable notifications on `FFF1`.
2. Write the ASCII bytes `" "` (one space, `0x20`) to `FFF2`. Write-without-response works.
3. Concatenate the notification payloads on `FFF1`. The result is an ASCII string of decimal fields separated by `;`.

The app treats the reply as complete when it splits on `;` into **30, 31 or 32** fields (`SolarControllerDataStorage.d1ElementCount*`). The reply observed here had a trailing `;`, giving 32 fields with the last one empty.

No unlock is needed for the status request.

### Example reply

```
00655;200;13244;14475;00000;00003;00000;39026;00008;003;100;22879;000;00000;00019;00049;+22;+72;+22;00012;00015;00016;00004;00093;00132;00000;02306;02025;009;025;008;
```

### Fields

Indices are 0-based. Values are decimal and may have leading zeros or a `+`/`-` sign.

| Index | Meaning | Scale | Example | Confidence |
|---|---|---|---|---|
| 0 | Charge current | mA (÷1000 → A) | 655 → 0.66 A | verified |
| 2 | Battery voltage | mV (÷1000 → V) | 13244 → 13.24 V | verified |
| 5 | Operation flags | bit field, see below | 3 | partly verified |
| 8 | Firmware | — | 8 | unverified |
| 9 | Battery type | code, see below | 3 → AGM | verified |
| 10 | State of charge | % | 100 | unverified (the app's Status screen doesn't show it) |
| 11 | PV (solar) voltage | mV (÷1000 → V) | 22879 → 22.88 V | verified |
| 12 | Charge state | code, see below | 0 | verified (shown as "Bulk") |
| 14 | Serial | — | 19 | unverified |
| 16 | Temperature | °C | +22 | verified |
| 17 | Temperature | °F | +72 | verified |
| 19 | Amp-hours today | Ah | 12 | verified |
| 20 | Amp-hours yesterday | Ah | 15 | unverified |
| 21 | Amp-hours day before yesterday | Ah | 16 | unverified |
| 22 | Amp-hours, "three days before" | Ah | 4 | unverified (the name comes from the app) |
| 23 | Amp-hours, "seven days before" | Ah | 93 | unverified (the name comes from the app) |

Indices 1, 3, 4, 6, 7, 13, 15, 18 and 24–31 are read by the app only as bit fields it discards, or not at all. Their meaning is unknown. Index 3 (14475 in the example) looks like another millivolt value, perhaps a charge target, but that's a guess.

### Operation flags (index 5)

The app formats index 5 as a 16-bit binary number and looks at the two lowest bits:

| Low two bits | App's "charging" flag |
|---|---|
| `00`, `01` | not charging |
| `10`, `11` | charging |

In other words, bit 1 means charging. The example value 3 (`11`) came while the controller was charging at 0.66 A. The app also uses bit 0 in a way that looks like a temperature-unit preference. That's unverified.

### Charge state codes (index 12)

From `PwmBatteryUtil.getChargingMode`:

| Code | App's code name | App's Status screen |
|---|---|---|
| 0 | Boost | **Bulk** (observed) |
| 1 | Float | |
| 2 | Equalization | |
| 3 | Absorption | |
| 4 | Bulk Equalization | |
| 5 | Charge Current | |
| 6 | Charge Voltage | |
| 7 | No Charge | |

### Battery type codes (index 9)

From `PwmBatteryUtil.getBatteryType`: 0 Sealed, 1 Flooded, 2 LiFePO4, 3 AGM, 4 Custom.

## Other commands: do not send

The app has other commands for this controller in its `factoryReset` helper class: an unlock sequence, settings reads and writes (three settings pages), **factory reset, soft reset, history reset, force absorption, force float**, and a temperature-unit change. They change the controller's state and are deliberately not documented here. A monitoring tool needs only the status request.

## What doesn't work: the Modbus request

A project online (`bigthrilla/camper-ble-bridge`) polls this controller by writing a Modbus RTU "read holding registers" request to `FFF2`: `[addr, 0x03, 0x01, 0x00, 0x00, 0x21]` plus CRC-16, at slave address 1. The controller does answer, with about 3 KB of `;`-separated **hex** bytes. But the answer is a fixed dump:

- It begins with a header and a block of charge settings in millivolts. For example, `38 40` is 14,400 mV, the AGM absorption voltage.
- Then come dozens of 16-byte history records, then zeros.
- Repeated captures were byte-for-byte identical, and none of the live readings appear in them.

That project's parser reads one of the history records, and it takes "battery percent" from the record's `0x50` marker byte, so it always reports 80%. Use the status request above for live data.
